package formats

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestWAVDecodesPCMMetadataAndDuration(t *testing.T) {
	data := make([]byte, 48)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 40)
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 11025)
	binary.LittleEndian.PutUint32(data[28:], 11025)
	binary.LittleEndian.PutUint16(data[32:], 1)
	binary.LittleEndian.PutUint16(data[34:], 8)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:], 4)
	wav, err := LoadWAV(data)
	if err != nil {
		t.Fatal(err)
	}
	if wav.AudioFormat != 1 || wav.Channels != 1 || wav.SampleRate != 11025 || wav.DataSize != 4 || wav.Duration().Nanoseconds() == 0 {
		t.Fatalf("WAV metadata = %+v", wav)
	}
}

func TestLoadAudioNormalizesRetailLegacyContainers(t *testing.T) {
	raw := []byte{0x80, 0x90, 0x70}
	audio, err := LoadAudio(raw)
	if err != nil {
		t.Fatal(err)
	}
	if audio.Container != "raw" || audio.DataOffset != 0 || audio.DataSize != uint32(len(raw)) || audio.SampleRate != 11025 {
		t.Fatalf("raw audio metadata = %+v", audio)
	}
	encoded, err := EncodeWAV(raw, audio)
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded[44:]; string(got) != string(raw) {
		t.Fatalf("raw samples = %#v, want %#v", got, raw)
	}

	digi := make([]byte, 43)
	copy(digi[0:4], "DIGI")
	copy(digi[8:12], "HSHD")
	binary.BigEndian.PutUint32(digi[12:16], 24)
	binary.LittleEndian.PutUint32(digi[22:26], 11000)
	copy(digi[32:36], "SDAT")
	binary.BigEndian.PutUint32(digi[36:40], 11)
	copy(digi[40:], raw)
	audio, err = LoadAudio(digi)
	if err != nil {
		t.Fatal(err)
	}
	if audio.Container != "DIGI" || audio.DataOffset != 40 || audio.DataSize != 3 {
		t.Fatalf("DIGI audio metadata = %+v", audio)
	}
	encoded, err = EncodeWAV(digi, audio)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := LoadWAV(encoded)
	if err != nil || decoded.DataSize != 3 || string(encoded[44:]) != string(raw) {
		t.Fatalf("normalized DIGI audio = %+v, %v", decoded, err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "legacy.wav"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	fileAudio, err := LoadWAVFile(fs, "legacy.wav")
	if err != nil || fileAudio.Container != "raw" {
		t.Fatalf("file-based legacy audio = %+v, %v", fileAudio, err)
	}

	bad := *audio
	bad.ByteRate = 0
	if _, err := EncodeWAV(raw, &bad); err == nil {
		t.Fatal("invalid PCM metadata was encoded")
	}
}

func authoredWAVChunk(name string, payload []byte) []byte {
	chunk := append([]byte(name), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	return append(chunk, payload...)
}

func authoredRIFF(chunks ...[]byte) []byte {
	data := make([]byte, 12)
	copy(data, "RIFF")
	copy(data[8:], "WAVE")
	for _, chunk := range chunks {
		data = append(data, chunk...)
	}
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	return data
}

func TestWAVFirstChunksAndDeclaredSpan(t *testing.T) {
	fmtBody := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtBody, 1)
	binary.LittleEndian.PutUint16(fmtBody[2:], 1)
	binary.LittleEndian.PutUint32(fmtBody[4:], 11025)
	binary.LittleEndian.PutUint16(fmtBody[14:], 8)
	firstFmt := authoredWAVChunk("fmt ", fmtBody)
	binary.LittleEndian.PutUint32(fmtBody[4:], 22050)
	secondFmt := authoredWAVChunk("fmt ", fmtBody)
	firstData := authoredWAVChunk("data", []byte{1, 2, 3})
	secondData := authoredWAVChunk("data", []byte{9})
	for _, chunks := range [][][]byte{
		{authoredWAVChunk("JUNK", []byte{8, 7, 6}), firstFmt, secondFmt, firstData, secondData},
		{firstData, secondData, firstFmt, secondFmt},
	} {
		data := authoredRIFF(chunks...)
		w, err := LoadWAV(data)
		if err != nil {
			t.Fatal(err)
		}
		if w.SampleRate != 11025 || w.DataSize != 3 || string(data[w.DataOffset:uint64(w.DataOffset)+uint64(w.DataSize)]) != string([]byte{1, 2, 3}) {
			t.Fatalf("first chunk selection = %+v", w)
		}
	}
	data := authoredRIFF(firstFmt)
	data = append(data, firstData...)
	if _, err := LoadWAV(data); err == nil {
		t.Fatal("accepted data chunk beyond declared RIFF span")
	}
	data = authoredRIFF(firstFmt, firstData)
	data = append(data, []byte("unparsed outside span")...)
	if _, err := LoadWAV(data); err != nil {
		t.Fatal("trailing bytes changed selected records", err)
	}
	for _, payload := range [][]byte{nil, {1, 2}} {
		data = authoredRIFF(firstFmt, authoredWAVChunk("data", payload))
		binary.LittleEndian.PutUint32(data[len(data)-len(payload)-4:], 10)
		if _, err := LoadWAV(data); err == nil {
			t.Fatal("accepted short data read")
		}
	}
}

func TestWAVFixedSignatureFallbackAndEmptyData(t *testing.T) {
	data := make([]byte, 40)
	copy(data, "DIGI")
	copy(data[8:], "HSHD")
	// A missing fixed SDAT marker selects raw, even with a DIGI prefix.
	w, err := LoadAudio(data)
	if err != nil || w.Container != "raw" || w.DataOffset != 0 {
		t.Fatalf("fallback = %+v, %v", w, err)
	}
	copy(data[32:], "SDAT")
	if _, err := LoadAudio(data[:36]); err == nil {
		t.Fatal("accepted truncated recognized DIGI")
	}
	fmtBody := make([]byte, 16)
	if _, err := LoadWAV(authoredRIFF(authoredWAVChunk("fmt ", fmtBody), authoredWAVChunk("data", nil))); err == nil {
		t.Fatal("accepted zero-length RIFF data")
	}
}
