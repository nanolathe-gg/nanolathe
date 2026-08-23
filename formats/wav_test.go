package formats

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
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
