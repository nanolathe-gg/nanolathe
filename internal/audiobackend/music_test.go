package audiobackend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

func authoredMusicWAV() []byte {
	data := make([]byte, 44+128)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 11025)
	binary.LittleEndian.PutUint32(data[28:], 11025)
	binary.LittleEndian.PutUint16(data[32:], 1)
	binary.LittleEndian.PutUint16(data[34:], 8)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 128)
	for i := 44; i < len(data); i++ {
		data[i] = 192
	}
	return data
}

// CD volume and ownership are independent of wave effects/narration
// [03 R-AUD-01 §2][03 R-AUD-01 §4]. EOF is only a completion after drain.
func TestMusicIndependentGainLifetimeAndDrain(t *testing.T) {
	b := New()
	defer b.Close()
	var observed []*observedPlayer
	b.createPlayer = func(source io.Reader) (outputPlayer, error) {
		data, err := io.ReadAll(source)
		if err != nil {
			return nil, err
		}
		p := &observedPlayer{data: data}
		observed = append(observed, p)
		return p, nil
	}
	music, err := b.NewMusicPlayer(bytes.NewReader(authoredMusicWAV()), ".wav")
	if err != nil {
		t.Fatal(err)
	}
	music.SetVolume(0.5)
	music.Play()
	device := observed[0]
	if len(device.data) == 0 || device.firstOutput() == 0 {
		t.Fatal("music never reached PCM output")
	}
	if err := b.PlayStream(&retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{192}}, 1); err != nil {
		t.Fatal(err)
	}
	b.SetEffectsVolume(0)
	b.SetMasterEnabled(false)
	b.StopStream()
	if !music.IsPlaying() || device.volume != 0.5 {
		t.Fatal("wave controls muted/stopped CD audio")
	}
	if music.Completed() {
		t.Fatal("EOF notified before device drain")
	}
	music.Pause()
	if music.Completed() {
		t.Fatal("pause notified as completion")
	}
	music.Play()
	device.playing = false
	if !music.Completed() {
		t.Fatal("drained EOF did not notify completion")
	}
	if err := music.Close(); err != nil {
		t.Fatal(err)
	}
	if music.Completed() {
		t.Fatal("closed player notified completion")
	}
}

func TestMusicMalformedMediaDoesNotOpenDevice(t *testing.T) {
	b := New()
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		t.Fatal("invalid media reached device")
		return nil, nil
	}
	for _, ext := range []string{".mp3", ".wav", ".unknown"} {
		input := []byte("authored invalid file")
		if ext == ".wav" {
			input = []byte("RIFF1234WAVEfmt ")
		}
		if _, err := b.NewMusicPlayer(bytes.NewReader(input), ext); err == nil {
			t.Fatalf("accepted %s", ext)
		}
	}
}

type failedMusicReader struct{}

func (failedMusicReader) Read(p []byte) (int, error) {
	return copy(p, []byte{1, 2, 3, 4}), errors.New("damaged frame")
}
func (failedMusicReader) Close() error { return nil }

func TestMusicReadFailureIsNotADeviceErrorOrCompletion(t *testing.T) {
	r := &musicReader{ReadCloser: failedMusicReader{}}
	var data [8]byte
	if n, err := r.Read(data[:]); n != 4 || err != io.EOF {
		t.Fatalf("device read = %d, %v", n, err)
	}
	p := &musicPlayer{player: &observedPlayer{}, reader: r}
	if p.Err() == nil || p.Completed() {
		t.Fatal("lost failure or reported successful completion")
	}
}
