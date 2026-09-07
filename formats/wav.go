package formats

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nanolathe/nanolathe/vfs"
)

// WAV describes PCM metadata for canonical RIFF/WAVE and supported legacy
// sound containers. The sample bytes remain in the VFS and are intentionally
// not copied here; the viewer can stream the original file or normalize it
// for a browser audio element.
type WAV struct {
	Container     string
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	DataOffset    uint32
	DataSize      uint32
}

// LoadWAV reads the first fmt/data records using retail's unpadded chunk
// stride and declared RIFF span [fmt wav][02 R-MALF-01 §10]. Metadata retains
// authored format/alignment words; playback policy belongs to internal/audio.
func LoadWAV(data []byte) (*WAV, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav: missing RIFF/WAVE header")
	}
	result := &WAV{Container: "RIFF"}
	var haveFormat, haveData bool
	span := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	for offset := uint64(12); offset < span; {
		if offset+8 > uint64(len(data)) {
			return nil, fmt.Errorf("wav: chunk header is truncated")
		}
		chunkID := string(data[offset : offset+4])
		chunkSize := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payload := offset + 8
		// Checked slices are a host-safety policy for malformed containers.
		if chunkSize > uint64(len(data))-payload {
			return nil, fmt.Errorf("wav: %s chunk is truncated", chunkID)
		}
		switch {
		case chunkID == "fmt " && !haveFormat:
			if int32(chunkSize) < 16 {
				return nil, fmt.Errorf("wav: fmt chunk is too small")
			}
			result.AudioFormat = binary.LittleEndian.Uint16(data[payload:])
			result.Channels = binary.LittleEndian.Uint16(data[payload+2:])
			result.SampleRate = binary.LittleEndian.Uint32(data[payload+4:])
			result.ByteRate = binary.LittleEndian.Uint32(data[payload+8:])
			result.BlockAlign = binary.LittleEndian.Uint16(data[payload+12:])
			result.BitsPerSample = binary.LittleEndian.Uint16(data[payload+14:])
			haveFormat = true
		case chunkID == "data" && !haveData:
			if int32(chunkSize) <= 0 || payload > uint64(^uint32(0)) {
				return nil, fmt.Errorf("wav: invalid data chunk size")
			}
			result.DataOffset = uint32(payload)
			result.DataSize = uint32(chunkSize)
			haveData = true
		}
		if haveFormat && haveData {
			return result, nil
		}
		offset = payload + chunkSize
	}
	return nil, fmt.Errorf("wav: missing fmt or data chunk")
}

// LoadAudio is the canonical WAV-family classifier and lossless payload
// locator [fmt wav]. Unrecognized fixed signatures select raw PCM, including
// a damaged DIGI signature. Recognized but truncated headers return an error.
func LoadAudio(data []byte) (*WAV, error) {
	if len(data) >= 36 && string(data[:4]) == "DIGI" &&
		string(data[8:12]) == "HSHD" && string(data[32:36]) == "SDAT" {
		return loadDIGI(data)
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return LoadWAV(data)
	}
	if len(data) == 0 || uint64(len(data)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("wav: raw audio is empty or too large")
	}
	return &WAV{Container: "raw", AudioFormat: 1, Channels: 1, SampleRate: 11025, ByteRate: 11025, BlockAlign: 1, BitsPerSample: 8, DataSize: uint32(len(data))}, nil
}

func loadDIGI(data []byte) (*WAV, error) {
	if len(data) < 40 || uint64(len(data)-40) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("wav: DIGI sample is truncated or too large")
	}
	// Fixed positions, independent of all advertised chunk sizes [fmt wav].
	rate := binary.LittleEndian.Uint32(data[22:26])
	if rate == 11000 {
		rate = 11025
	}
	return &WAV{Container: "DIGI", AudioFormat: 1, Channels: 1, SampleRate: rate, ByteRate: rate, BlockAlign: 1, BitsPerSample: 8, DataOffset: 40, DataSize: uint32(len(data) - 40)}, nil
}

// EncodeWAV returns a canonical PCM RIFF/WAVE wrapper around the source
// samples described by w. It is used for browser playback of legacy files.
func EncodeWAV(data []byte, w *WAV) ([]byte, error) {
	if w == nil {
		return nil, fmt.Errorf("wav: missing audio metadata")
	}
	if w.AudioFormat != 1 || w.Channels == 0 || w.SampleRate == 0 || w.BitsPerSample == 0 || w.BitsPerSample%8 != 0 {
		return nil, fmt.Errorf("wav: only non-empty PCM metadata can be encoded")
	}
	expectedBlockAlign := uint64(w.Channels) * uint64(w.BitsPerSample) / 8
	if expectedBlockAlign > uint64(^uint16(0)) || uint64(w.BlockAlign) != expectedBlockAlign {
		return nil, fmt.Errorf("wav: block alignment does not match PCM metadata")
	}
	expectedByteRate := uint64(w.SampleRate) * expectedBlockAlign
	if expectedByteRate > uint64(^uint32(0)) || uint64(w.ByteRate) != expectedByteRate {
		return nil, fmt.Errorf("wav: byte rate does not match PCM metadata")
	}
	if uint64(w.DataOffset) > uint64(len(data)) || uint64(w.DataSize) > uint64(len(data))-uint64(w.DataOffset) {
		return nil, fmt.Errorf("wav: audio data is outside source")
	}
	pcm := data[w.DataOffset : uint64(w.DataOffset)+uint64(w.DataSize)]
	if uint64(len(pcm))%expectedBlockAlign != 0 {
		return nil, fmt.Errorf("wav: PCM data is not aligned to a sample")
	}
	if uint64(len(pcm)) > uint64(^uint32(0))-36 {
		return nil, fmt.Errorf("wav: encoded audio is too large")
	}
	out := make([]byte, 44+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], w.AudioFormat)
	binary.LittleEndian.PutUint16(out[22:24], w.Channels)
	binary.LittleEndian.PutUint32(out[24:28], w.SampleRate)
	binary.LittleEndian.PutUint32(out[28:32], w.ByteRate)
	binary.LittleEndian.PutUint16(out[32:34], w.BlockAlign)
	binary.LittleEndian.PutUint16(out[34:36], w.BitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out, nil
}

// Duration returns the playing time implied by the sample count and rate.
func (w *WAV) Duration() time.Duration {
	if w == nil || w.ByteRate == 0 {
		return 0
	}
	return time.Duration(uint64(w.DataSize) * uint64(time.Second) / uint64(w.ByteRate))
}

// LoadWAVFile reads and decodes a WAVE file from the VFS.
func LoadWAVFile(fs *vfs.FS, name string) (*WAV, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadAudio(data)
}
