package audiobackend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

func authoredMoviePCM(channels uint16, rate uint32, values ...int16) *retailaudio.Sample {
	data := make([]byte, len(values)*2)
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(value))
	}
	return &retailaudio.Sample{
		AudioFormat: 1, Channels: channels, SampleRate: rate,
		ByteRate: rate * uint32(channels) * 2, BlockAlign: channels * 2,
		BitsPerSample: 16, Data: data,
	}
}

// Host PCM conversion preserves channel order and signed extrema, including
// reads that split a float or stereo frame. It is not retail mixer arithmetic.
func TestMoviePCMConversion(t *testing.T) {
	for _, channels := range []uint16{1, 2} {
		sample := authoredMoviePCM(channels, 22050, -32768, 32767, -16384, 16384, 0, -1)
		var want []byte
		for _, value := range []float32{-1, 32767.0 / 32768, -0.5, 0.5, 0, -1.0 / 32768} {
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], math.Float32bits(value))
			want = append(want, encoded[:]...)
			if channels == 1 {
				want = append(want, encoded[:]...)
			}
		}
		for _, size := range []int{1, 3, 7, 8, 11, 64} {
			pcm := &moviePCMReader{data: sample.Data, frameBytes: int(channels) * 2}
			if got := readMovieChunks(t, pcm, size); !bytes.Equal(got, want) {
				t.Fatalf("PCM channels=%d size=%d: conversion = %x, want %x", channels, size, got, want)
			}
			r, err := newMovieReader(sample, 22050)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.Read(nil); n != 0 || err != nil {
				t.Fatalf("empty read = %d, %v", n, err)
			}
			got := readMovieChunks(t, r, size)
			if !bytes.Equal(got, want) {
				t.Fatalf("channels=%d size=%d: conversion = %x, want %x", channels, size, got, want)
			}
			if n, err := r.Read(make([]byte, 8)); n != 0 || err != io.EOF {
				t.Fatalf("repeat EOF = %d, %v", n, err)
			}
			_ = r.Close()
			if r.stream != nil || r.pending != nil {
				t.Fatal("closed reader retains source or pending bytes")
			}
		}
	}
}

func TestMovieEmptyAndConcurrentClose(t *testing.T) {
	for _, rate := range []int{22050, 44100} {
		r, err := newMovieReader(authoredMoviePCM(1, 22050), rate)
		if err != nil {
			t.Fatal(err)
		}
		if got := readMovieChunks(t, r, 1); len(got) != 0 {
			t.Fatal("empty source produced PCM")
		}
		_ = r.Close()
	}
	r, err := newMovieReader(authoredMoviePCM(1, 22050, make([]int16, 2205)...), 44100)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := io.Copy(io.Discard, r)
		done <- err
	}()
	<-started
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader shutdown deadlocked")
	}
}

func readMovieChunks(t *testing.T, r io.Reader, size int) []byte {
	t.Helper()
	var result []byte
	buffer := make([]byte, size)
	for {
		n, err := r.Read(buffer)
		result = append(result, buffer[:n]...)
		if err == io.EOF {
			return result
		}
		if err != nil || n == 0 {
			t.Fatalf("read = %d, %v", n, err)
		}
	}
}

func TestMovieResamplingDurationAndChannelIdentity(t *testing.T) {
	values := make([]int16, 2205*2)
	for i := 0; i < len(values); i += 2 {
		values[i], values[i+1] = 8192, -16384
	}
	for _, rate := range []int{11025, 44100, 48000} {
		r, err := newMovieReader(authoredMoviePCM(2, 22050, values...), rate)
		if err != nil {
			t.Fatal(err)
		}
		got := readMovieChunks(t, r, 13)
		// One tenth of a second remains one tenth at the device rate, rounded
		// to a complete output frame by Ebitengine's host resampler.
		if frames := len(got) / 8; frames != rate/10 {
			t.Fatalf("rate=%d frames=%d, want %d", rate, frames, rate/10)
		}
		middle := len(got) / 16 * 8
		left := math.Float32frombits(binary.LittleEndian.Uint32(got[middle:]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(got[middle+4:]))
		if left <= 0 || right != -2*left {
			t.Fatalf("rate=%d: channel relation = %v, %v", rate, left, right)
		}
		_ = r.Close()
	}
}

func TestMovieMetadataRejectedBeforeDevice(t *testing.T) {
	b := New()
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		t.Fatal("invalid PCM reached device")
		return nil, nil
	}
	for _, mutate := range []func(*retailaudio.Sample){
		func(s *retailaudio.Sample) { s.AudioFormat = 3 },
		func(s *retailaudio.Sample) { s.Channels = 3 },
		func(s *retailaudio.Sample) { s.BitsPerSample = 8 },
		func(s *retailaudio.Sample) { s.SampleRate = 0 },
		func(s *retailaudio.Sample) { s.BlockAlign = 1 },
		func(s *retailaudio.Sample) { s.ByteRate++ },
		func(s *retailaudio.Sample) { s.Data = s.Data[:3] },
	} {
		sample := authoredMoviePCM(2, 22050, 1, 2)
		mutate(sample)
		if _, err := b.NewMoviePlayer(sample); err == nil {
			t.Fatal("accepted invalid PCM metadata")
		}
	}
	if _, err := b.NewMoviePlayer(nil); err == nil {
		t.Fatal("accepted nil PCM")
	}
}

func TestMoviePlayerPauseResumeAndBackendOwnership(t *testing.T) {
	b := NewWithRate(22050)
	defer b.Close()
	device := &observedPlayer{}
	var source *musicReader
	b.createPlayer = func(r io.Reader) (outputPlayer, error) {
		source = r.(*musicReader)
		return device, nil
	}
	movie, err := b.NewMoviePlayer(authoredMoviePCM(2, 22050, -32768, 32767, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if movie.IsPlaying() || device.volume != 1 || len(b.players) != 0 {
		t.Fatal("movie starts playing, lacks full volume, or consumes a wave voice")
	}
	movie.Play()
	device.position = 1500 * time.Millisecond
	b.SetEffectsVolume(0)
	b.SetMasterEnabled(false)
	b.StopStream()
	if !movie.IsPlaying() || device.volume != 1 {
		t.Fatal("wave controls affected movie playback")
	}
	movie.Pause()
	if movie.IsPlaying() || movie.Completed() || movie.PositionMillis() != 1500 {
		t.Fatal("pause lost position or notified completion")
	}
	movie.Play()
	if !movie.IsPlaying() || movie.PositionMillis() != 1500 {
		t.Fatal("resume reset playback position")
	}
	if _, err := io.ReadAll(source); err != nil {
		t.Fatal(err)
	}
	if movie.Completed() {
		t.Fatal("EOF completed before device drain")
	}
	device.playing = false
	if !movie.Completed() || movie.Err() != nil {
		t.Fatal("drained PCM did not complete successfully")
	}
	b.Close()
	movie.Play()
	if movie.IsPlaying() || len(b.music) != 0 || source.ReadCloser.(*movieReader).stream != nil {
		t.Fatal("backend close failed to release movie")
	}
	if err := movie.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMovieDeviceFailureReleasesSource(t *testing.T) {
	b := New()
	var reader *movieReader
	failure := errors.New("authored device failure")
	b.createPlayer = func(r io.Reader) (outputPlayer, error) {
		reader = r.(*musicReader).ReadCloser.(*movieReader)
		return nil, failure
	}
	_, err := b.NewMoviePlayer(authoredMoviePCM(1, 22050, 1))
	if !errors.Is(err, failure) || reader.stream != nil || len(b.music) != 0 {
		t.Fatal("failed device retains PCM or loses the error")
	}
}

func TestMovieDiagnosticUsesPortableProvenance(t *testing.T) {
	sample := authoredMoviePCM(1, 22050, 1)
	sample.Alias = "Data/2.zrb"
	sample.Provenance.SourcePath = "/authored/install/archive.hpi"
	failure := errors.New("authored failure")
	for _, path := range []string{"", "movies/intro.zrb"} {
		sample.Provenance.LogicalPath = path
		if path == "" {
			path = sample.Alias
		}
		err := moviePCMError(sample, failure)
		want := "nanolathe: movie playback failed: logical path " + path + ", providers searched [archive.hpi], expected signed-16 mono or stereo PCM: authored failure"
		if err.Error() != want || !errors.Is(err, failure) {
			t.Fatalf("diagnostic = %v, want %s with wrapped cause", err, want)
		}
	}
}
