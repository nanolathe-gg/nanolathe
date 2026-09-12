package audiobackend

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

// Music bypasses the wave voice table, effects gain, and narration stop-all
// boundary, as CD audio does [03 R-AUD-01 §2][03 R-AUD-01 §4].
func (b *Backend) NewMusicPlayer(source io.ReadSeeker, extension string) (retailaudio.MusicPlayer, error) {
	var decoded io.ReadCloser
	var err error
	switch strings.ToLower(extension) {
	case ".mp3":
		decoded, err = decodeMP3(source, b.sampleRate)
	case ".wav":
		// Host resource limit for an optional file, not a retail media limit.
		const maxWAV = 1 << 24
		var data []byte
		data, err = io.ReadAll(io.LimitReader(source, maxWAV+1))
		if err == nil && len(data) > maxWAV {
			err = errors.New("music WAV exceeds 16 MiB")
		}
		if err == nil {
			var sample *retailaudio.Sample
			sample, err = retailaudio.Decode("music", data)
			if err == nil {
				decoded = io.NopCloser(bytes.NewReader(retailaudio.ConvertSample(sample, 1, 0, b.sampleRate)))
			}
		}
	default:
		err = fmt.Errorf("unsupported music format %q", extension)
	}
	if err != nil {
		return nil, err
	}
	reader := &musicReader{ReadCloser: decoded}
	b.mu.Lock()
	defer b.mu.Unlock()
	player, err := b.newPlayer(reader)
	if err != nil || player == nil {
		_ = reader.Close()
		if err == nil {
			err = errors.New("music output unavailable")
		}
		return nil, err
	}
	m := &musicPlayer{player: player, reader: reader}
	kept := b.music[:0]
	for _, prior := range b.music {
		if !prior.isClosed() {
			kept = append(kept, prior)
		}
	}
	b.music = append(kept, m)
	return m, nil
}

// Reading EOF is not completion until the device drains its buffered audio.
// Decode errors remain distinguishable from successful media notifications.
type musicReader struct {
	io.ReadCloser
	mu  sync.Mutex
	eof bool
	err error
}

func (r *musicReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		r.mu.Lock()
		if err == io.EOF {
			r.eof = true
		} else {
			r.err = err
			// Optional media failures belong to the presentation diagnostic
			// sink. Forwarding them poisons Ebitengine's shared audio context.
			err = io.EOF
		}
		r.mu.Unlock()
	}
	return n, err
}

type musicPlayer struct {
	mu             sync.Mutex
	player         outputPlayer
	reader         *musicReader
	paused, closed bool
}

func (p *musicPlayer) Play() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.paused = false
		p.player.Play()
	}
}

func (p *musicPlayer) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.paused = true
		p.player.PauseAndStopReading()
	}
}

func (p *musicPlayer) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && !p.paused && p.player.IsPlaying()
}

func (p *musicPlayer) SetVolume(v float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		v, _ = clampPlayback(v, 0)
		p.player.SetVolume(v)
	}
}

func (p *musicPlayer) PositionMillis() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0
	}
	return int(p.player.Position() / time.Millisecond)
}

func (p *musicPlayer) Completed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.paused || p.player.IsPlaying() {
		return false
	}
	p.reader.mu.Lock()
	defer p.reader.mu.Unlock()
	return p.reader.eof && p.reader.err == nil
}

func (p *musicPlayer) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if device, ok := p.player.(interface{ Err() error }); ok {
		if err := device.Err(); err != nil {
			return err
		}
	}
	p.reader.mu.Lock()
	defer p.reader.mu.Unlock()
	return p.reader.err
}

func (p *musicPlayer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *musicPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	// Wait for any device read before disposing the decoder.
	p.player.PauseAndStopReading()
	return p.reader.Close()
}
