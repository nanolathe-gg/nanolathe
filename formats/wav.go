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

func LoadWAV(data []byte) (*WAV, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav: missing RIFF/WAVE header")
	}
	result := &WAV{Container: "RIFF"}
	var haveFormat, haveData bool
	for offset := uint64(12); offset+8 <= uint64(len(data)); {
		chunkID := string(data[offset : offset+4])
		chunkSize := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payload := offset + 8
		if chunkSize > uint64(len(data))-payload {
			return nil, fmt.Errorf("wav: %s chunk is truncated", chunkID)
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("wav: fmt chunk is too small")
			}
			result.AudioFormat = binary.LittleEndian.Uint16(data[payload:])
			result.Channels = binary.LittleEndian.Uint16(data[payload+2:])
			result.SampleRate = binary.LittleEndian.Uint32(data[payload+4:])
			result.ByteRate = binary.LittleEndian.Uint32(data[payload+8:])
			result.BlockAlign = binary.LittleEndian.Uint16(data[payload+12:])
			result.BitsPerSample = binary.LittleEndian.Uint16(data[payload+14:])
			haveFormat = true
		case "data":
			if chunkSize > uint64(^uint32(0)) || payload > uint64(^uint32(0)) {
				return nil, fmt.Errorf("wav: data chunk is too large")
			}
			result.DataOffset = uint32(payload)
			result.DataSize = uint32(chunkSize)
			haveData = true
		}
		if chunkSize&1 != 0 {
			chunkSize++
		}
		offset = payload + chunkSize
	}
	if !haveFormat || !haveData {
		return nil, fmt.Errorf("wav: missing fmt or data chunk")
	}
	if result.AudioFormat == 0 || result.Channels == 0 || result.SampleRate == 0 || result.BlockAlign == 0 {
		return nil, fmt.Errorf("wav: invalid audio format")
	}
	return result, nil
}

// LoadAudio accepts the RIFF/WAVE files plus the two legacy raw containers
// present in the retail sound directory: unsigned 8-bit mono PCM stored with
// a .WAV extension, and Humongous-style DIGI/HSHD/SDAT chunks used by a small
// set of stock sounds. The viewer can normalize both legacy forms to RIFF.
func LoadAudio(data []byte) (*WAV, error) {
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return LoadWAV(data)
	}
	if len(data) >= 16 && string(data[:4]) == "DIGI" {
		return loadDIGI(data)
	}
	if len(data) == 0 || uint64(len(data)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("wav: raw audio is empty or too large")
	}
	return &WAV{Container: "raw", AudioFormat: 1, Channels: 1, SampleRate: 11025, ByteRate: 11025, BlockAlign: 1, BitsPerSample: 8, DataSize: uint32(len(data))}, nil
}

func loadDIGI(data []byte) (*WAV, error) {
	if len(data) < 32 || string(data[8:12]) != "HSHD" {
		return nil, fmt.Errorf("wav: invalid DIGI/HSHD header")
	}
	hshdSize := uint64(binary.BigEndian.Uint32(data[12:16]))
	if hshdSize < 8 || hshdSize > uint64(len(data))-8 {
		return nil, fmt.Errorf("wav: DIGI HSHD chunk is outside file")
	}
	sdatOffset := 8 + hshdSize
	if sdatOffset+8 > uint64(len(data)) || string(data[sdatOffset:sdatOffset+4]) != "SDAT" {
		return nil, fmt.Errorf("wav: DIGI SDAT chunk is missing")
	}
	sdatSize := uint64(binary.BigEndian.Uint32(data[sdatOffset+4 : sdatOffset+8]))
	if sdatSize < 8 || sdatSize-8 > uint64(len(data))-(sdatOffset+8) {
		return nil, fmt.Errorf("wav: DIGI SDAT chunk is outside file")
	}
	dataOffset := sdatOffset + 8
	if dataOffset > uint64(^uint32(0)) || sdatSize-8 > uint64(^uint32(0)) {
		return nil, fmt.Errorf("wav: DIGI audio data is too large")
	}
	return &WAV{Container: "DIGI", AudioFormat: 1, Channels: 1, SampleRate: 11025, ByteRate: 11025, BlockAlign: 1, BitsPerSample: 8, DataOffset: uint32(dataOffset), DataSize: uint32(sdatSize - 8)}, nil
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

func (w *WAV) Duration() time.Duration {
	if w == nil || w.ByteRate == 0 {
		return 0
	}
	return time.Duration(uint64(w.DataSize) * uint64(time.Second) / uint64(w.ByteRate))
}

func LoadWAVFile(fs *vfs.FS, name string) (*WAV, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadAudio(data)
}
