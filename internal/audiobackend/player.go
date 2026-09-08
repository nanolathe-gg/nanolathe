package audiobackend

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"
	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

// Backend is the PCM output over Ebitengine audio [03 §8.2][03 §8.3].
// Device construction remains lazy until the presentation edge admits a sample.
type Backend struct {
	sampleRate   int
	master       bool
	effects      float64
	soundMode    retailaudio.SoundMode
	mu           sync.Mutex
	ctx          *audio.Context
	players      []voice
	streams      []voice
	createPlayer func(io.Reader) (outputPlayer, error)
	lastPump     time.Time
}

// outputPlayer is the device boundary; gain stays outside immutable PCM.
type outputPlayer interface {
	Play()
	IsPlaying() bool
	SetVolume(float64)
	PauseAndStopReading()
}

type voice struct {
	player   outputPlayer
	baseGain float64
}

func (b *Backend) newPlayer(source io.Reader) (outputPlayer, error) {
	if b.createPlayer != nil {
		return b.createPlayer(source)
	}
	b.ensureContext()
	if b.ctx == nil {
		return nil, nil
	}
	return b.ctx.NewPlayerF32(source)
}

// Capabilities describes the concrete presentation device surface.
// Capabilities is what the opened device can do: whether there is a device at
// all, and whether it is stereo. Sound Mode independently selects positional
// placement [03 R-AUD-01 §1][03 R-AUD-01 §2].
type Capabilities struct {
	Device bool
	Stereo bool
}

// New opens a backend at the default 44100 Hz output rate.
func New() *Backend { return NewWithRate(44100) }

// NewWithRate opens a backend at an explicit output rate; a non-positive rate
// means 44100 Hz. Master output starts enabled at full effects volume.
func NewWithRate(rate int) *Backend {
	if rate <= 0 {
		rate = 44100
	}
	return &Backend{sampleRate: rate, master: true, effects: 1, soundMode: retailaudio.SoundModeMono}
}

// Capabilities reports what this backend offers. A nil backend reports none.
func (b *Backend) Capabilities() Capabilities {
	if b == nil {
		return Capabilities{}
	}
	return Capabilities{Device: true, Stereo: true}
}

// SetSoundMode installs the player's selected spatial policy. Device format
// capability is deliberately not this decision: the default is Mono even on
// this stereo backend [03 R-AUD-01 §1][03 R-AUD-01 §2].
func (b *Backend) SetSoundMode(mode retailaudio.SoundMode) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.soundMode = mode
	b.mu.Unlock()
}

// SoundMode reports the selected spatial policy for the client viewport
// builder. Outputs without this optional reader fall back to Mono.
func (b *Backend) SoundMode() retailaudio.SoundMode {
	if b == nil {
		return retailaudio.SoundModeMono
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.soundMode
}

// ConfigureOutput applies the retained presentation configuration when this
// device is installed and whenever options change. It keeps the device-local
// live-gain owner: changing FX still updates already playing voices and MODE
// Off still stops them [03 R-AUD-01 §2].
func (b *Backend) ConfigureOutput(config retailaudio.OutputConfig) {
	if b == nil {
		return
	}
	b.SetMasterEnabled(config.MasterEnabled)
	b.SetEffectsVolume(config.EffectsVolume)
	b.SetSoundMode(config.SoundMode)
}

// SetMasterEnabled controls ordinary cues. MODE Off stops the voice table;
// streamed narration has its separate lifetime [03 R-AUD-01 §1][03 R-AUD-02 §1].
func (b *Backend) SetMasterEnabled(enabled bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.master = enabled
	if !enabled {
		for _, v := range b.players {
			release(v.player)
		}
		b.players = nil
	}
}

// SetEffectsVolume applies the application-local wave-output gain to both
// current and future cues and narration [03 R-AUD-01 §2]. Zero gain mutes
// current buffers without restarting them when the level rises again.
func (b *Backend) SetEffectsVolume(volume float64) {
	if b == nil {
		return
	}
	volume, _ = clampPlayback(volume, 0)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.effects = volume
	for _, v := range b.players {
		b.applyGain(v)
	}
	for _, v := range b.streams {
		b.applyGain(v)
	}
}

func (b *Backend) applyGain(v voice) {
	gain, _ := clampPlayback(v.baseGain*b.effects, 0)
	v.player.SetVolume(gain)
}

// CanPlay reports whether an ordinary cue submitted now would be audible.
func (b *Backend) CanPlay() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.master && b.effects > 0
}

// SampleRate is the device output rate samples are converted to.
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
// released immediately: it only needs to reach player.Play(), which is what
// triggers the device open on the caller's behalf; nothing about it is meant
// to be audible or to occupy a voice slot for any length of time.
func (b *Backend) WarmUp() {
	if b == nil || !b.CanPlay() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
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
	release(player)
}

// PlaySample converts one decoded sample to the device rate at the given
// pan and submits it with separate base and FX gains. It is a no-op while playback is off.
func (b *Backend) PlaySample(sample *retailaudio.Sample, volume, pan float64) error {
	if b == nil || sample == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.master || !(b.effects > 0) {
		return nil
	}
	_, pan = clampPlayback(volume, pan)
	data := retailaudio.ConvertSample(sample, 1, pan, b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	player, err := b.newPlayer(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	if player == nil {
		return nil
	}
	v := voice{player: player, baseGain: volume}
	b.applyGain(v)
	b.admitVoiceLocked(v)
	player.Play()
	return nil
}

// PlayRegisteredSample plays a mode-0 alias from the sample-owned canonical
// PCM cache. The pan reader is independent per voice, so simultaneous plays
// cannot alter each other's placement or timeline.
func (b *Backend) PlayRegisteredSample(sample *retailaudio.Sample, volume, pan float64) error {
	if b == nil || sample == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.master || !(b.effects > 0) {
		return nil
	}
	_, pan = clampPlayback(volume, pan)
	data := sample.RegisteredPCM(b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	player, err := b.newPlayer(newPanReader(data, pan))
	if err != nil || player == nil {
		return nil
	}
	v := voice{player: player, baseGain: volume}
	b.applyGain(v)
	b.admitVoiceLocked(v)
	player.Play()
	return nil
}

func (b *Backend) admitVoiceLocked(v voice) {
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
		release(b.players[0].player)
		b.players = append(b.players[:0], b.players[1:]...)
	}
	b.players = append(b.players, v)
}

// voiceLimit is the device's `MixingBuffers` default [R-AUD-01 §2].
const voiceLimit = 8

// release frees one voice slot: the player stops producing sound at once and
// stops reading its source, and the caller then drops its reference.
//
// This is the Ebitengine 2.10 idiom, not a change of behaviour. Player.Close
// is deprecated as of 2.10 in favour of Player.PauseAndStopReading, which
// removes the player from the audio context's playing set and blocks until any
// read in flight finishes, so nothing further is mixed and the source is safe
// to drop. Release of the player itself is then automatic: a Player is
// collected once it is unreferenced *and* no longer playing, which is exactly
// the state PauseAndStopReading leaves it in. Our sources are in-memory
// readers over converted PCM, so there is no handle left over for the caller
// to close.
func release(player outputPlayer) {
	if player == nil {
		return
	}
	player.PauseAndStopReading()
}

// reapLocked drops every voice whose buffer has stopped, mirroring the
// application pump's media keepalive walk [R-AUD-02 §2]. The caller holds
// b.mu.
func (b *Backend) reapLocked() {
	b.players = reapVoices(b.players)
}

func reapVoices(voices []voice) []voice {
	live := voices[:0]
	for _, v := range voices {
		if !v.player.IsPlaying() {
			release(v.player)
			continue
		}
		live = append(live, v)
	}
	clear(voices[len(live):])
	return live
}

// Pump releases finished output buffers from the application pump, including
// its last silent batch. This wall-clock keepalive is independent of pause,
// simulation ticks and subsequent cue submissions [03 R-AUD-02 §2].
func (b *Backend) Pump(now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.lastPump.IsZero() && now.Sub(b.lastPump) < 99*time.Millisecond {
		return
	}
	b.lastPump = now
	b.reapLocked()
	b.streams = reapVoices(b.streams)
}

// PlayStream is the optional non-looping stream boundary. The sample is
// already decoded by internal/audio; keeping a separate player list lets a
// narration stop leave ordinary cue voices untouched [03 R-AUD-02 §1].
func (b *Backend) PlayStream(sample *retailaudio.Sample, volume float64) error {
	if b == nil || sample == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// The stream opener has no ordinary MODE/FX play gate; wave-output gain
	// still applies to its output [03 R-AUD-02 §1][03 R-AUD-01 §2].
	data := retailaudio.ConvertSample(sample, 1, 0, b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	player, err := b.newPlayer(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	if player == nil {
		return nil
	}
	v := voice{player: player, baseGain: volume}
	b.applyGain(v)
	b.streams = append(b.streams, v)
	player.Play()
	return nil
}

// StopStream releases every streaming player, ending music and speech.
func (b *Backend) StopStream() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range b.streams {
		release(v.player)
	}
	b.streams = nil
}

// clampPlayback preserves the concrete backend's presentation boundary before
// output: effects scaling is narrowed to 0..1 and pan to -1..1.
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

// PlayAlias loads an alias through the sample cache and plays it. A missing
// alias is silent, not an error [03 §8.2].
func (b *Backend) PlayAlias(alias string, cache *retailaudio.SampleCache, volume, pan float64) error {
	if b == nil || alias == "" || cache == nil {
		return nil
	}
	sample, err := cache.Load(alias)
	if err != nil || sample == nil {
		return nil
	}
	return b.PlayRegisteredSample(sample, volume, pan)
}

// Close stops and releases every player this backend opened.
func (b *Backend) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range b.players {
		release(v.player)
	}
	for _, v := range b.streams {
		release(v.player)
	}
	b.players = nil
	b.streams = nil
}
