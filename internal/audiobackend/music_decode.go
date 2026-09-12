package audiobackend

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

// decodeMP3 adapts copied soundtrack files to the desktop PCM device. This is
// host codec policy; retail music itself uses CD audio [03 §8.4]. Ebitengine's
// decoder yields little-endian stereo F32 without taking ownership of source.
func decodeMP3(source io.ReadSeeker, sampleRate int) (io.ReadCloser, error) {
	if sampleRate <= 0 {
		return nil, errors.New("invalid music sample rate")
	}
	decoded, err := mp3.DecodeF32(source)
	if err != nil {
		return nil, fmt.Errorf("decode MP3: %w", err)
	}
	// Derive duration from decoded EOF: the MP3 frame index can include frames
	// that produce no PCM, so its declared length can add trailing silence.
	stream := audio.ResampleF32(decoded, 0, decoded.SampleRate(), sampleRate)
	return &mp3Reader{stream: stream}, nil
}

type mp3Reader struct {
	mu      sync.Mutex
	stream  io.Reader
	block   [4096 * 8]byte
	pending []byte
	err     error
}

func (r *mp3Reader) Read(p []byte) (int, error) {
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
		// The float decoder reads whole samples. A bounded frame-aligned block
		// also permits callers to request arbitrary byte counts, including one.
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

func (r *mp3Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// The pure Go decoder has no Close operation or native resources. Drop
	// its buffers and source reference; the caller still owns the open source.
	r.stream = nil
	r.pending = nil
	return nil
}
