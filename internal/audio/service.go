package audio

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	framepkg "github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Service is the single retail audio owner. Simulation code submits events to
// this service; the presentation edge calls Drain once for each rendered
// frame. Queue arbitration, alias identity, decoded samples, music state, and
// their shared CRT stream stay together here [03 §8.2–§8.4] [I4] [I6].
//
// The service deliberately does not know about Session, visibility, units, or
// a camera. Those are supplied as typed callbacks or viewport values at the
// actual boundaries that use them.
type Service struct {
	Queue    *Queue
	Registry *Registry
	Cache    *SampleCache
	Music    *Controller

	viewport          Viewport
	frame             uint32
	fs                vfs.FSOps
	playbackInstalled bool
	musicConfigured   bool
	musicFSBound      bool
	musicTracks       []string
	musicStartPending bool
	lastEventTick     uint32
	hasEventTick      bool
	streamPath        string
	streamVolume      int
	streamDeadlines   []uint32
	streamPlaying     bool
	voiceCache        *SampleCache
}

// NewService constructs the queue, registry/cache, and music controller with
// the retail presentation defaults. Missing media remains silent.
func NewService(fs vfs.FSOps) *Service {
	a := &Service{fs: fs}
	a.Queue = NewQueue()
	a.Queue.Seed(1)
	a.Queue.Configure(10, 5, true, true)
	a.Registry = NewRegistry(fs)
	a.Cache = a.Registry.Cache()
	a.Music = NewMusicController()
	a.installPlayback()
	return a
}

// Init ensures a service created as a zero value is fully initialized. It is
// intentionally idempotent so strict and fixture constructors can share the
// same audio boundary without replacing queued events.
func (a *Service) Init(fs vfs.FSOps) {
	if a == nil {
		return
	}
	if fs != nil {
		a.fs = fs
	}
	if a.Queue == nil {
		a.Queue = NewQueue()
		a.Queue.Seed(1)
		a.Queue.Configure(10, 5, true, true)
	}
	if a.Registry == nil {
		a.Registry = NewRegistry(a.fs)
	} else if a.fs != nil {
		a.Registry.SetFS(a.fs)
	}
	if a.Cache == nil {
		a.Cache = a.Registry.Cache()
	} else if a.Registry.Cache() != a.Cache {
		a.Registry.SetCache(a.Cache)
	}
	if a.Music == nil {
		a.Music = NewMusicController()
	}
	if a.voiceCache == nil {
		a.voiceCache = NewCache(a.fs)
	} else if a.fs != nil {
		a.voiceCache.SetFS(a.fs)
	}
	a.installPlayback()
}

// ResetBattleCues starts a new battle's cue lifetime while retaining media,
// settings and playback hooks. Call only at a new session binding, before
// installing its resolver and private random stream; Init remains idempotent.
// The host policy is documented in DESIGN_PRESENTATION_CLIENT §5.
func (a *Service) ResetBattleCues() {
	if a == nil {
		return
	}
	a.Queue.resetBattle()
	a.frame, a.lastEventTick, a.hasEventTick = 0, 0, false
}

func (a *Service) installPlayback() {
	if a == nil || a.Queue == nil || a.playbackInstalled {
		return
	}
	a.Queue.OnPlay(func(alias string, _ Slot, _ pool.Handle) {
		if a == nil || alias == "" {
			return
		}
		// A unit voice line is the loader's mode 1 [R-AUD-01 §1]: it is read
		// from the VFS under the `sounds` prefix on every play and never
		// consults the alias registry, whose 255 entries belong to the
		// mode-0 aliases (sound.tdf/allsound registrations, weapon and
		// feature sounds). Registering each resolved variant here consumed
		// those entries — the stock corpus authors 219 distinct voice
		// variants against 41 authored aliases and 62 weapon sounds — and
		// once the registry filled, registration returned the null identity
		// and every later cue, voice or weapon, was silent.
		sample := a.loadVoiceLine(alias)
		if sample == nil {
			return
		}
		// Mode-1 voices use the same base attenuation as interface cues
		// [03 R-AUD-01 §1]; FX gain is applied once by the backend.
		if output := GlobalOutput(); output != nil {
			_ = output.PlaySample(sample, VolumeFromCentibel(VolInView), 0)
		}
	})
	a.Queue.OnSpeech(func(string) {})
	a.playbackInstalled = true
}

// loadVoiceLine resolves one unit voice variant through the sample cache's
// canonical `sounds` candidates without touching the alias registry
// [R-AUD-01 §1 mode 1]. Retail re-decodes the file on every play; the retained
// cache here is the documented presentation divergence of [03 §8.2]. A
// missing file stays silent, as it does in retail.
func (a *Service) loadVoiceLine(alias string) *Sample {
	if a == nil || strings.TrimSpace(alias) == "" {
		return nil
	}
	a.Init(nil)
	if a.voiceCache == nil {
		return nil
	}
	// Mode-0 alias names may map to a different authored path with the same
	// spelling. The mode-1 filename cache must preserve its own identity
	// [03 R-AUD-01 §1].
	sample, err := a.voiceCache.Load(alias)
	if err != nil {
		return nil
	}
	return sample
}

// BindCatalog registers authored aliases in catalog order and installs the
// unit resolver used by category queue resolution. The resolver is supplied by
// the session because the audio package must not own unit state.
func (a *Service) BindCatalog(cat *content.Catalog, resolver func(pool.Handle) (*Category, string, bool)) {
	if a == nil {
		return
	}
	a.Init(nil)
	if cat != nil && a.Registry != nil {
		for _, alias := range cat.AliasOrder {
			if alias != nil {
				a.Registry.RegisterPath(alias.Alias, alias.Sound)
			}
		}
	}
	a.Queue.SetResolver(resolver)
}

// BindCRT connects queue and music variant draws to the session's shared CRT
// stream. Silent resolves still consume the same presentation draw order.
func (a *Service) BindCRT(crt *rng.CRT) {
	if a == nil {
		return
	}
	a.Init(nil)
	a.Queue.SetCRTRandom(crt)
	a.Music.SetCRTRandom(crt)
}

// Emit inserts one category cue using the supplied presentation frame.
func (a *Service) Emit(frame uint32, slot Slot, unit pool.Handle, text string) bool {
	if a == nil || a.Queue == nil || slot == 0 {
		return false
	}
	a.Queue.SetNow(frame)
	return a.Queue.InsertAt(frame, slot, unit, text)
}

// DrainEvents resolves queued cues and committed positional events at the
// presentation edge. A committed tick is consumed once; repeated rendered
// frames must not replay its events [03 §8.3] [I6].
//
// The drain runs once per rendered frame, but the value it arbitrates against
// is the global tick counter, not a private presentation counter: the queue's
// thirty-frame window and the per-slot next-allowed frames are expressed in
// that one counter [03 §8.3], and [R-AUD-01 §3] names it "the global tick
// counter" where the honk/sing alias divides it by thirty. Producers stamp
// their inserts with the same tick (Service.Emit), so a second clock here put
// the next-allowed frames in a domain the insert test could never satisfy —
// with a rendered frame ahead of the tick, `tick < nextAllowed` held forever
// and every slot fell silent after its first audible resolve.
//
// The rendered-frame count was a leading parameter here until CL-4, discarded
// on the first line of the body. Keeping it in the signature invited exactly
// the reading the paragraph above rules out — that the drain arbitrates
// against a presentation clock — and one test existed to pass two different
// values for it on one tick.
func (a *Service) DrainEvents(committedTick uint32, events []framepkg.EventView) {
	if a == nil {
		return
	}
	if a.Queue == nil || a.Music == nil {
		a.Init(nil)
	}
	a.Queue.Drain(committedTick)
	if events != nil && (!a.hasEventTick || committedTick != a.lastEventTick) {
		for _, ev := range events {
			if ev.Kind != framepkg.EventKindAudio || ev.Sound == "" {
				continue
			}
			if ev.AudioAudible {
				if ev.AudioPositional {
					a.playAdmittedPositional(ev.Sound, [3]numeric.Fixed{ev.X, ev.Y, ev.Z})
				} else {
					// Trigger celebration is a published by-name cue [08 R-TRIG-01 §8].
					a.PlayUICue(ev.Sound)
				}
			}
		}
		a.lastEventTick = committedTick
		a.hasEventTick = true
	}
	a.frame = committedTick
}

// Frame returns the queue clock as of the last drain — the committed global
// tick counter [03 §8.3].
func (a *Service) Frame() uint32 {
	if a == nil {
		return 0
	}
	return a.frame
}

// SetViewport records the presentation viewport positional cues are attenuated
// and panned against [03 §8.3].
func (a *Service) SetViewport(v Viewport) {
	if a != nil {
		a.viewport = v
	}
}

// Viewport is the viewport recorded by SetViewport.
func (a *Service) Viewport() Viewport {
	if a == nil {
		return Viewport{}
	}
	return a.viewport
}

// Load resolves an alias through the registry/cache. Missing aliases are
// returned as errors to the caller; playback callers intentionally discard
// that error to preserve retail silence [03 §8.2].
func (a *Service) Load(alias string) (*Sample, error) {
	if a == nil || strings.TrimSpace(alias) == "" {
		return nil, nil
	}
	a.Init(nil)
	if a.Registry != nil {
		id := a.Registry.Lookup(alias)
		if id == MissingAlias {
			id = a.Registry.Register(alias)
		}
		return a.Registry.Load(id)
	}
	if a.Cache != nil {
		return a.Cache.Load(alias)
	}
	return nil, nil
}

// PlayPositional applies the retail audience gate supplied by the caller,
// computes viewport placement, resolves the sample, and sends it to the
// presentation backend. The gate is deliberately external because visibility
// belongs to the world/session boundary, not audio [03 §3.1] [03 §8.3].
func (a *Service) PlayPositional(alias string, pos [3]numeric.Fixed, audible func([3]numeric.Fixed) bool) (Pan, int32, bool) {
	if a == nil || strings.TrimSpace(alias) == "" || audible == nil || !audible(pos) {
		return Pan{}, 0, false
	}
	return a.playAdmittedPositional(alias, pos)
}

func (a *Service) playAdmittedPositional(alias string, pos [3]numeric.Fixed) (Pan, int32, bool) {
	if a == nil || strings.TrimSpace(alias) == "" {
		return Pan{}, 0, false
	}
	v := a.Viewport()
	var pan Pan
	var volume int32
	gain := 1.0
	if v.SoundMode == SoundMode3D {
		pan = ComputePan(pos, v)
		volume = VolInView
		gain = DistanceGain(pan, v)
	} else {
		volume = Attenuate(pos, v)
	}
	sample, _ := a.Load(alias)
	if sample != nil {
		playRegistered(sample, VolumeFromAttenuation(volume)*gain, PanFloat(pan, v))
	}
	return pan, volume, true
}

// PlayUICue resolves an unpositioned authored alias at the ordinary cue
// attenuation [03 R-AUD-01 §1]. The backend applies the FX gain separately.
func (a *Service) PlayUICue(alias string) bool {
	if a == nil || strings.TrimSpace(alias) == "" {
		return false
	}
	sample, err := a.Load(alias)
	if err != nil || sample == nil {
		return false
	}
	playRegistered(sample, VolumeFromCentibel(VolInView), 0)
	return true
}

// PlayLoopingUICue resolves the by-name front-end loop. Only an output that
// explicitly exposes the looping registered seam receives it; a missing seam
// stays silent rather than changing this mode-0 request into a one-shot.
func (a *Service) PlayLoopingUICue(alias string) bool {
	if a == nil || strings.TrimSpace(alias) == "" {
		return false
	}
	sample, err := a.Load(alias)
	if err != nil || sample == nil {
		return false
	}
	output, ok := GlobalOutput().(LoopingRegisteredOutput)
	if !ok {
		return false
	}
	_ = output.PlayLoopingRegisteredSample(sample, VolumeFromCentibel(VolInView), 0)
	return true
}

// ConfigureMusic probes authored music media and selects the mission's
// briefing/sequential mode. Zero tracks leaves the controller idle.
func (a *Service) ConfigureMusic(hasBriefing bool) {
	if a == nil {
		return
	}
	a.Init(nil)
	if a.musicConfigured && (a.musicFSBound || a.fs == nil) {
		return
	}
	if a.fs == nil {
		return
	}
	a.musicTracks = MusicTracks(a.fs)
	a.Music.openTrack = a.openMusicTrack
	n := len(a.musicTracks)
	a.Music.Configure(SelectMusicMode(n, hasBriefing), 0)
	a.Music.SetNumTracks(n)
	if n > 0 {
		_ = a.Music.Open(n)
		if packagedMusicTracks(a.musicTracks) {
			// Default categories for the retail disc's audio layout
			// [03 R-AUD-01 §4 "Packaged MP3 media"].
			for track := 1; track <= n; track++ {
				category := uint8(0)
				if track <= 7 {
					category = 1
				}
				a.Music.trackCategory[track] = category
			}
		}
	}
	a.musicConfigured = true
	a.musicFSBound = true
}

// PreloadBriefing resolves the established briefing alias without playing it.
// A missing alias remains a non-fatal silent presentation result.
func (a *Service) PreloadBriefing(glamourSound, brief, narration, missionHint string) error {
	if a == nil {
		return nil
	}
	alias := BriefingAlias(glamourSound, brief, narration, missionHint)
	if alias == "" {
		return nil
	}
	_, err := a.Load(alias)
	return err
}

// PlayBriefing resolves the established field precedence and plays the alias
// at unity volume, with CD/MCI track one as the established fallback.
func (a *Service) PlayBriefing(glamourSound, brief, narration, missionHint string) bool {
	if a == nil {
		return false
	}
	alias := BriefingAlias(glamourSound, brief, narration, missionHint)
	if alias != "" {
		if sample, err := a.Load(alias); err == nil && sample != nil {
			playRegistered(sample, 1.0, 0)
			return true
		}
	}
	return a.Music != nil && a.Music.NumTracks() > 0 && a.Music.Play(1)
}

// playRegistered selects the optional mode-0 presentation cache without
// changing ordinary-output compatibility for outputs that only implement
// Output. The looping extension intentionally does not take this fallback.
func playRegistered(sample *Sample, volume, pan float64) {
	if output := GlobalOutput(); output != nil {
		if registered, ok := output.(RegisteredOutput); ok {
			_ = registered.PlayRegisteredSample(sample, volume, pan)
			return
		}
		_ = output.PlaySample(sample, volume, pan)
	}
}

// StopVoices ends the ordinary tracked voice table when a presentation
// transition reaches its committed stop point. Streams remain separate.
func (a *Service) StopVoices() {
	if a == nil {
		return
	}
	if output, ok := GlobalOutput().(VoiceOutput); ok {
		output.StopVoices()
	}
}

// StartStream arms one authored narration/glamour timer on the existing
// semantic audio owner. Starts overwrite the current path/volume but retain
// every armed deadline; the earliest deadline admits that current path once
// [03 R-AUD-02 §1].
func (a *Service) StartStream(path string, volume int, delay, now uint32) {
	if a == nil || path == "" {
		return
	}
	a.streamPath, a.streamVolume = path, volume
	a.streamDeadlines = append(a.streamDeadlines, now+delay)
}

// TickStream admits a pending stream exactly once when the presentation clock
// reaches its due unit. Missing files and outputs without StreamOutput remain
// silent at this boundary.
func (a *Service) TickStream(now uint32) {
	if a == nil || len(a.streamDeadlines) == 0 {
		return
	}
	earlest := a.streamDeadlines[0]
	for _, due := range a.streamDeadlines[1:] {
		if due < earlest {
			earlest = due
		}
	}
	if now < earlest {
		return
	}
	a.streamDeadlines = nil
	output, ok := GlobalOutput().(StreamOutput)
	if a.streamPlaying {
		if ok {
			output.StopStream()
		}
		a.streamPlaying = false
	}
	if !ok {
		return
	}
	a.Init(nil)
	if a.Cache == nil {
		return
	}
	sample, err := a.Cache.LoadPath(a.streamPath)
	if err != nil || sample == nil {
		return
	}
	if err := output.PlayStream(sample, VolumeFromCentibel(int32(a.streamVolume))); err == nil {
		a.streamPlaying = true
	}
}

// StopStream cancels delayed narration and stops only the active stream
// player, leaving the ordinary 8-slot cue pool alone.
func (a *Service) StopStream() {
	if a == nil {
		return
	}
	a.streamDeadlines = nil
	if a.streamPlaying {
		if output, ok := GlobalOutput().(StreamOutput); ok {
			output.StopStream()
		}
	}
	a.streamPlaying = false
}

// Close stops presentation music when the battle view leaves. Queue and
// decoded aliases remain service-owned for the session lifetime.
func (a *Service) Close() {
	if a == nil {
		return
	}
	a.StopStream()
	a.musicStartPending = false
	if a.Music == nil {
		return
	}
	a.Music.Stop()
	a.Music.Close()
}
