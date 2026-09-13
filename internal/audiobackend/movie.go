package audiobackend

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"
	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

// NewMoviePlayer adapts immutable signed-16 PCM to the desktop device. Movie
// codec and mixing are host policy [08 R-OOS-01 §4]. The returned player starts
// stopped at full volume, outside effects gain, voice limits and CD control.
func (b *Backend) NewMoviePlayer(sample *retailaudio.Sample) (retailaudio.MusicPlayer, error) {
	if b == nil {
		return nil, moviePCMError(sample, errors.New("movie output unavailable"))
	}
	reader, err := newMovieReader(sample, b.SampleRate())
	if err != nil {
		return nil, moviePCMError(sample, err)
	}
	player, err := b.newMusicPlayer(reader)
	if err != nil {
		return nil, moviePCMError(sample, err)
	}
	player.SetVolume(1)
	return player, nil
}

func moviePCMError(sample *retailaudio.Sample, err error) error {
	var path, provider string
	if sample != nil {
		path, provider = sample.Provenance.LogicalPath, sample.Provenance.ProviderID()
		if path == "" {
			path = sample.Alias
		}
	}
	return fmt.Errorf("nanolathe: movie playback failed: logical path %s, providers searched [%s], expected signed-16 mono or stereo PCM: %w", path, provider, err)
}

func newMovieReader(sample *retailaudio.Sample, rate int) (*movieReader, error) {
	if sample == nil || sample.AudioFormat != 1 || sample.BitsPerSample != 16 ||
		(sample.Channels != 1 && sample.Channels != 2) || sample.SampleRate == 0 || rate <= 0 {
		return nil, errors.New("invalid movie PCM format")
	}
	frameBytes := int(sample.Channels) * 2
	if int(sample.BlockAlign) != frameBytes ||
		uint64(sample.ByteRate) != uint64(sample.SampleRate)*uint64(frameBytes) ||
		len(sample.Data)%frameBytes != 0 {
		return nil, errors.New("invalid movie PCM frame alignment or byte rate")
	}
	source := &moviePCMReader{data: sample.Data, frameBytes: frameBytes}
	stream := audio.ResampleReaderF32(source, int64(len(sample.Data)/frameBytes)*8, int(sample.SampleRate), rate)
	return &movieReader{stream: stream}, nil
}

// moviePCMReader expands one source frame at a time. Its only conversion
// storage is one stereo F32 frame; no converted copy of the movie is retained.
type moviePCMReader struct {
	data       []byte
	frameBytes int
	frame      [8]byte
	pending    []byte
}

func (r *moviePCMReader) Read(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(r.pending) == 0 {
			if len(r.data) == 0 {
				return n, io.EOF
			}
			left := float32(int16(binary.LittleEndian.Uint16(r.data))) / 32768
			right := left
			if r.frameBytes == 4 {
				right = float32(int16(binary.LittleEndian.Uint16(r.data[2:]))) / 32768
			}
			binary.LittleEndian.PutUint32(r.frame[:4], math.Float32bits(left))
			binary.LittleEndian.PutUint32(r.frame[4:], math.Float32bits(right))
			r.data = r.data[r.frameBytes:]
			r.pending = r.frame[:]
		}
		copied := copy(p, r.pending)
		n += copied
		p = p[copied:]
		r.pending = r.pending[copied:]
	}
	return n, nil
}

// Ebitengine's resampler consumes whole frames. A bounded aligned block lets
// the device request any byte count without dropping a partial frame.
type movieReader struct {
	mu      sync.Mutex
	stream  io.Reader
	block   [4096 * 8]byte
	pending []byte
	err     error
}

func (r *movieReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stream == nil {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		n, err := r.stream.Read(r.block[:])
		r.err = err
		if n == 0 {
			return 0, err
		}
		r.pending = r.block[:n]
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *movieReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// The source is immutable memory, so no read waits for a producer. Dropping
	// the resampler releases its source PCM after any current read finishes.
	r.stream = nil
	r.pending = nil
	return nil
}
