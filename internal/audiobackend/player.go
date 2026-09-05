// Package audiobackend owns the desktop PCM device boundary.
//
// Keeping the Ebitengine audio import here lets authoritative packages import
// internal/audio without initializing a graphical platform backend [I6].
package audiobackend

import (
	"bytes"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"
	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

// Backend is the PCM output over Ebitengine audio [03 §8.2][03 §8.3].
// Device construction remains lazy until the presentation edge admits a sample.
type Backend struct {
	sampleRate int
	master     bool
	effects    float64
	mu         sync.Mutex
	ctx        *audio.Context
	players    []*audio.Player
	streams    []*audio.Player
}

// Capabilities describes the concrete presentation device surface.
type Capabilities struct {
	Device bool
	Stereo bool
}

func New() *Backend { return NewWithRate(44100) }

func NewWithRate(rate int) *Backend {
	if rate <= 0 {
		rate = 44100
	}
	return &Backend{sampleRate: rate, master: true, effects: 1}
}

func (b *Backend) Capabilities() Capabilities {
	if b == nil {
		return Capabilities{}
	}
	return Capabilities{Device: true, Stereo: true}
}

func (b *Backend) StereoCapable() bool { return b != nil }

func (b *Backend) SetMasterEnabled(enabled bool) {
	if b != nil {
		b.master = enabled
	}
}

func (b *Backend) SetEffectsVolume(volume float64) {
	if b == nil {
		return
	}
	if volume < 0 {
		volume = 0
	}
	if volume > 1 {
		volume = 1
	}
	b.effects = volume
}

func (b *Backend) CanPlay() bool { return b != nil && b.master && b.effects > 0 }

func (b *Backend) SampleRate() int {
	if b == nil || b.sampleRate == 0 {
		return 44100
	}
	return b.sampleRate
}

func (b *Backend) ensureContext() {
	if b == nil || b.ctx != nil {
		return
	}
	if current := audio.CurrentContext(); current != nil {
		b.ctx = current
		return
	}
	func() {
		defer func() {
			if recover() != nil {
				b.ctx = audio.CurrentContext()
			}
		}()
		b.ctx = audio.NewContext(b.sampleRate)
	}()
}

// warmUpSample is a single silent frame used only to trigger the host
// device's bring-up (see WarmUp). It is never queued as a retail cue and
// carries no alias identity a lookup could resolve.
var warmUpSample = &retailaudio.Sample{
	Alias:         "__warmup",
	Container:     "raw",
	AudioFormat:   1,
	Channels:      1,
	SampleRate:    11025,
	ByteRate:      11025,
	BlockAlign:    1,
	BitsPerSample: 8,
	Data:          []byte{0x80},
}

// WarmUp starts the host device's bring-up as early as the platform boundary
// can, instead of paying that cost as a side effect of the first real cue.
//
// This is platform work, not a retail contract: retail has no equivalent
// step, and nothing here cites the retail spec. It exists because ebiten's
// audio.Context.IsReady() only turns true once the underlying device
// finishes an asynchronous open — on this host that measured anywhere from
// tens of milliseconds to low seconds on a cold process, while the Play()
// call that starts it returns in well under a millisecond. Without a warm-up
// that wait lands on whichever cue happens to play first, which is what a
// play-test felt as "the first click takes a while to play" (WU-19-224).
//
// The probe itself is a single silent frame, played at zero volume and
// closed immediately: it only needs to reach player.Play(), which is what
// triggers the device open on the caller's behalf; nothing about it is meant
// to be audible or to occupy a voice slot for any length of time.
func (b *Backend) WarmUp() {
	if b == nil || !b.CanPlay() {
		return
	}
	b.ensureContext()
	if b.ctx == nil {
		return
	}
	data := retailaudio.ConvertSample(warmUpSample, 0, 0, b.sampleRate)
	if len(data) == 0 {
		return
	}
	player, err := b.ctx.NewPlayerF32(bytes.NewReader(data))
	if err != nil {
		return
	}
	player.Play()
	_ = player.Close()
}

func (b *Backend) PlaySample(sample *retailaudio.Sample, volume, pan float64) error {
	if b == nil || sample == nil || !b.CanPlay() {
		return nil
	}
	volume *= b.effects
	volume, pan = clampPlayback(volume, pan)
	b.ensureContext()
	if b.ctx == nil {
		return nil
	}
	data := retailaudio.ConvertSample(sample, volume, pan, b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	player, err := b.ctx.NewPlayerF32(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b.mu.Lock()
	b.reapLocked()
	// The voice limit is compared against the voices that are still playing:
	// retail's reaper frees every finished buffer from the application pump,
	// so the mixer's steal only ever evicts a live voice [R-AUD-01 §1 step 2]
	// [R-AUD-02 §2]. Without the reap the list saturated at eight voices ever
	// started, and from the ninth cue on every play closed a sound that had
	// only just begun.
	for len(b.players) >= voiceLimit {
		// Oldest start first: the steal picks the smallest sequence number
		// among the non-looping voices [R-AUD-01 §1 step 2]. Appends keep the
		// slice in start order, so that voice is the front one.
		if oldest := b.players[0]; oldest != nil {
			_ = oldest.Close()
		}
		b.players = append(b.players[:0], b.players[1:]...)
	}
	b.players = append(b.players, player)
	b.mu.Unlock()
	player.Play()
	return nil
}

// voiceLimit is the device's `MixingBuffers` default [R-AUD-01 §2].
const voiceLimit = 8

// reapLocked drops every voice whose buffer has stopped, mirroring the
// application pump's media keepalive walk [R-AUD-02 §2]. The caller holds
// b.mu.
func (b *Backend) reapLocked() {
	live := b.players[:0]
	for _, player := range b.players {
		if player == nil {
			continue
		}
		if !player.IsPlaying() {
			_ = player.Close()
			continue
		}
		live = append(live, player)
	}
	for i := len(live); i < len(b.players); i++ {
		b.players[i] = nil
	}
	b.players = live
}

// PlayStream is the optional non-looping stream boundary. The sample is
// already decoded by internal/audio; keeping a separate player list lets a
// narration stop leave ordinary cue voices untouched [03 R-AUD-02 §1].
func (b *Backend) PlayStream(sample *retailaudio.Sample, volume float64) error {
	if b == nil || sample == nil || !b.CanPlay() {
		return nil
	}
	volume *= b.effects
	volume, _ = clampPlayback(volume, 0)
	b.ensureContext()
	if b.ctx == nil {
		return nil
	}
	data := retailaudio.ConvertSample(sample, volume, 0, b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	player, err := b.ctx.NewPlayerF32(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b.mu.Lock()
	b.streams = append(b.streams, player)
	b.mu.Unlock()
	player.Play()
	return nil
}

func (b *Backend) StopStream() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, player := range b.streams {
		if player != nil {
			_ = player.Close()
		}
	}
	b.streams = nil
}

// clampPlayback preserves the concrete backend's presentation boundary before
// PCM conversion: effects scaling is narrowed to 0..1 and pan to -1..1.
func clampPlayback(volume, pan float64) (float64, float64) {
	if volume < 0 {
		volume = 0
	} else if volume > 1 {
		volume = 1
	}
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	return volume, pan
}

func (b *Backend) PlayAlias(alias string, cache *retailaudio.SampleCache, volume, pan float64) error {
	if b == nil || alias == "" || cache == nil {
		return nil
	}
	sample, err := cache.Load(alias)
	if err != nil || sample == nil {
		return nil
	}
	return b.PlaySample(sample, volume, pan)
}

func (b *Backend) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, player := range b.players {
		if player != nil {
			_ = player.Close()
		}
	}
	for _, player := range b.streams {
		if player != nil {
			_ = player.Close()
		}
	}
	b.players = nil
	b.streams = nil
}
