package audiobackend

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"
	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

// Backend is the PCM output over Ebitengine audio [03 §8.2][03 §8.3].
// Device construction remains lazy until the presentation edge admits a sample.
type Backend struct {
	sampleRate int
	voiceLimit int
	master     bool
	effects    float64
	soundMode  retailaudio.SoundMode
	mu         sync.Mutex
	ctx        *audio.Context
	players    []voice
	// transients retains mode-1 sample ownership independently of the ordinary
	// voice table. It is a separate eight-entry loader admission limit, not a
	// contribution to the mixer's tracked voice count [03 R-AUD-01 §1].
	transients []voice
	// untracked retains host gain/cleanup ownership only, outside the retail
	// admission count and stop-all table [03 R-AUD-01 §1 step 7].
	untracked []voice
	streams   []voice
	// statics retains the four reusable device instances for each registered
	// sample identity. It is presentation-only session ownership [03 R-AUD-01 §1].
	statics      []staticSample
	createPlayer func(io.Reader) (outputPlayer, error)
	lastPump     time.Time
}

// outputPlayer is the device boundary; gain stays outside immutable PCM.
type outputPlayer interface {
	Play()
	IsPlaying() bool
	Position() time.Duration
	Rewind() error
	SetVolume(float64)
	PauseAndStopReading()
}

type voice struct {
	player   outputPlayer
	baseGain float64
	loop     bool
}

const staticInstances = 4

type staticSample struct {
	sample    *retailaudio.Sample
	instances [staticInstances]staticInstance
}

type staticInstance struct {
	player outputPlayer
	reader *panReader
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
	return &Backend{sampleRate: rate, master: true, effects: 1, soundMode: retailaudio.SoundModeMono, voiceLimit: defaultVoiceLimit}
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
	b.mu.Lock()
	b.voiceLimit = config.MixingBuffers
	if b.voiceLimit <= 0 {
		// Preserve the existing settings host recovery, not a retail clamp.
		b.voiceLimit = defaultVoiceLimit
	}
	b.mu.Unlock()
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
		b.stopVoicesLocked()
	}
}

// StopVoices is the ordinary stop-all boundary used by MODE Off and shell
// transitions. Narration streams keep their independent lifetime.
func (b *Backend) StopVoices() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopVoicesLocked()
}

func (b *Backend) stopVoicesLocked() {
	for _, v := range b.players {
		release(v.player)
	}
	b.players = nil

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
	for _, v := range b.untracked {
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
	// Mode-1 loads first reclaim finished transient references and ordinary
	// voices. A ninth live transient is dropped before it creates a device
	// player or reaches the ordinary mixer [03 R-AUD-01 §1].
	b.reapTransientAdmissionLocked()
	if len(b.transients) >= transientVoiceSlots {
		return nil
	}
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
	// Mode 1 reaches mixer capacity only after creation. A failed creation
	// above cannot steal; a post-steal rewind failure releases the new player
	// and leaves no mixer or transient reference [03 R-AUD-01 §1].
	if !b.makeVoiceCapacityLocked() {
		release(player)
		return nil
	}
	if err := player.Rewind(); err != nil {
		release(player)
		return nil
	}
	v := voice{player: player, baseGain: volume}
	b.applyGain(v)
	player.Play()
	b.trackVoiceLocked(v)
	b.transients = append(b.transients, v)
	return nil
}

// PlayRegisteredSample plays a mode-0 alias from the sample-owned canonical
// PCM cache. Each physical static instance owns its pan reader, so distinct
// instances cannot alter each other's placement or timeline.
func (b *Backend) PlayRegisteredSample(sample *retailaudio.Sample, volume, pan float64) error {
	if b == nil || sample == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.playRegistered(sample, volume, pan, false)
}

// PlayLoopingRegisteredSample is the by-name exclusive loop path. It shares
// static selection, gain and tracking with ordinary registered playback.
func (b *Backend) PlayLoopingRegisteredSample(sample *retailaudio.Sample, volume, pan float64) error {
	if b == nil || sample == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.playRegistered(sample, volume, pan, true)
}

// playRegistered runs under b.mu and keeps ordinary and looping registered
// playback on one selection, capacity and tracking path.
func (b *Backend) playRegistered(sample *retailaudio.Sample, volume, pan float64, loop bool) error {
	if !b.master || !(b.effects > 0) {
		return nil
	}
	if loop && b.hasTrackedLoopLocked() {
		return nil
	}
	_, pan = clampPlayback(volume, pan)
	data := sample.RegisteredPCM(b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	// Retail resolves global mixer capacity before it chooses a static-buffer
	// instance. Creation and reset failures therefore happen after a steal
	// [03 R-AUD-01 §1 steps 2,4,6].
	if !b.makeVoiceCapacityLocked() {
		return nil
	}
	group, ok := b.staticGroupLocked(sample)
	if !ok {
		reader := newPanReader(data, pan)
		player, err := b.newPlayer(reader)
		if err != nil || player == nil {
			return nil
		}
		b.statics = append(b.statics, staticSample{sample: sample})
		group = &b.statics[len(b.statics)-1]
		group.instances[0] = staticInstance{player: player, reader: reader}
		// TODO(T23): initial instance creation is lazy at this host boundary;
		// retail allocates it during alias registration.
	}
	instance, allBusy, err := b.selectStaticInstanceLocked(group, data, pan)
	if err != nil || instance == nil {
		return nil
	}
	if allBusy {
		// This first reset is intentionally ignored. The checked reset below is
		// the one that decides whether gain, play, and tracking may proceed.
		_ = instance.player.Rewind()
	}
	instance.reader.SetPan(pan)
	instance.reader.SetLoop(loop)
	if err := instance.player.Rewind(); err != nil {
		return nil
	}
	b.updateStaticGainLocked(instance.player, volume)
	v := voice{player: instance.player, baseGain: volume, loop: loop}
	b.applyGain(v)
	// TODO(T23): host SetVolume and Play have no failure result; proceed with
	// those calls and track after Play until the device boundary exposes one.
	instance.player.Play()
	b.trackVoiceLocked(v)
	return nil
}

func (b *Backend) hasTrackedLoopLocked() bool {
	for _, v := range b.players {
		if v.loop {
			return true
		}
	}
	return false
}

func (b *Backend) makeVoiceCapacityLocked() bool {
	limit := b.voiceLimit
	if limit <= 0 {
		limit = defaultVoiceLimit
	}
	for len(b.players) >= limit {
		// Oldest start first, excluding loops. The all-loop edge is unreachable
		// in retail because exclusive-loop admission permits only one, but a
		// host-configured limit can expose it; dropping is safer than choosing
		// an invented victim [03 R-AUD-01 §1].
		victim := -1
		for i, v := range b.players {
			if !v.loop {
				victim = i
				break
			}
		}
		if victim < 0 {
			return false
		}
		release(b.players[victim].player)
		copy(b.players[victim:], b.players[victim+1:])
		b.players[len(b.players)-1] = voice{}
		b.players = b.players[:len(b.players)-1]
	}
	return true
}

func (b *Backend) trackVoiceLocked(v voice) {
	// A full tracking table does not reject playback [03 R-AUD-01 §1 step 7].
	// Host-only references preserve the shared output gain without admitting
	// these players into the retail count or stop-all table.
	if len(b.players) < trackedVoiceSlots {
		b.players = append(b.players, v)
	} else {
		// Retail has no slot after the 32nd reference. Keep one host-only
		// ownership record per physical player so repeated static restarts do
		// not grow retention without bound; the 32 tracked entries above still
		// preserve duplicate mixer references [03 R-AUD-01 §1 step 7].
		for i := range b.untracked {
			if b.untracked[i].player == v.player {
				b.untracked[i].baseGain = v.baseGain
				return
			}
		}
		b.untracked = append(b.untracked, v)
	}
}

func (b *Backend) staticGroupLocked(sample *retailaudio.Sample) (*staticSample, bool) {
	for i := range b.statics {
		if b.statics[i].sample == sample {
			return &b.statics[i], true
		}
	}
	return nil, false
}

// selectStaticInstanceLocked follows the four-slot selection order. TODO(T23):
// the host's bool IsPlaying cannot report a failed status query, and Position
// estimates audible time rather than exposing the retail byte play cursor.
func (b *Backend) selectStaticInstanceLocked(group *staticSample, data []byte, pan float64) (*staticInstance, bool, error) {
	lastNull := -1
	selected := -1
	var furthest time.Duration
	for i := range group.instances {
		instance := &group.instances[i]
		if instance.player == nil {
			lastNull = i
			continue
		}
		if !instance.player.IsPlaying() {
			return instance, false, nil
		}
		cursor := instance.player.Position()
		if selected < 0 || cursor > furthest {
			selected, furthest = i, cursor
		}
	}
	if lastNull >= 0 {
		reader := newPanReader(data, pan)
		player, err := b.newPlayer(reader)
		if err != nil || player == nil {
			return nil, false, err
		}
		group.instances[lastNull] = staticInstance{player: player, reader: reader}
		return &group.instances[lastNull], false, nil
	}
	if selected < 0 {
		return nil, false, nil
	}
	return &group.instances[selected], true, nil
}

// updateStaticGainLocked gives a reused physical instance one current gain.
// Older tracked or host-only aliases must not overwrite it on a live FX update.
func (b *Backend) updateStaticGainLocked(player outputPlayer, gain float64) {
	for i := range b.players {
		if b.players[i].player == player {
			b.players[i].baseGain = gain
		}
	}
	for i := range b.untracked {
		if b.untracked[i].player == player {
			b.untracked[i].baseGain = gain
		}
	}
}

// Device defaults and tracking capacity [03 R-AUD-01 §1][03 R-AUD-01 §2].
const (
	defaultVoiceLimit   = 8
	trackedVoiceSlots   = 32
	transientVoiceSlots = 8
)

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
	b.untracked = reapVoices(b.untracked)
}

// reapTransientAdmissionLocked drops stopped mode-1 ownership references and
// then reaps ordinary voices. Only mode-1 loading and the paced presentation
// pump call this helper; registered-alias admission does not reap
// [03 R-AUD-01 §1][03 R-AUD-02 §2]. The caller holds b.mu.
func (b *Backend) reapTransientAdmissionLocked() {
	b.transients = dropStoppedVoices(b.transients)
	b.reapLocked()
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

// dropStoppedVoices releases no player: every transient is already retained
// by the ordinary tracked or host-only list. This avoids releasing a completed
// or stolen device player once for each ownership reference.
func dropStoppedVoices(voices []voice) []voice {
	live := voices[:0]
	for _, v := range voices {
		if !v.player.IsPlaying() {
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
	if !b.lastPump.IsZero() && now.Sub(b.lastPump) < 100*time.Millisecond {
		return
	}
	b.lastPump = now
	b.reapTransientAdmissionLocked()
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
	var released []outputPlayer
	releaseOnce := func(player outputPlayer) {
		if player == nil {
			return
		}
		for _, prior := range released {
			if prior == player {
				return
			}
		}
		release(player)
		released = append(released, player)
	}
	for _, v := range b.players {
		releaseOnce(v.player)
	}
	for _, v := range b.streams {
		releaseOnce(v.player)
	}
	for _, v := range b.untracked {
		releaseOnce(v.player)
	}
	for _, group := range b.statics {
		for _, instance := range group.instances {
			releaseOnce(instance.player)
		}
	}
	b.players = nil
	b.streams = nil
	b.untracked = nil
	b.transients = nil
	b.statics = nil
}
