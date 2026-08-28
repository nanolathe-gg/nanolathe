package audio

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	framepkg "github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
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
	lastEventTick     uint32
	hasEventTick      bool
}

// NewService constructs the queue, registry/cache, and music controller with
// the retail presentation defaults. Missing media remains silent.
func NewService(fs vfs.FSOps) *Service {
	a := &Service{fs: fs}
	a.Queue = NewQueue()
	a.Queue.Seed(1)
	a.Queue.Configure(10, 10, true, true)
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
		a.Queue.Configure(10, 10, true, true)
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
	a.installPlayback()
}

func (a *Service) installPlayback() {
	if a == nil || a.Queue == nil || a.playbackInstalled {
		return
	}
	a.Queue.OnPlay(func(alias string, _ Slot, _ pool.Handle) {
		if a == nil || alias == "" || a.Registry == nil {
			return
		}
		id := a.Registry.Lookup(alias)
		if id == MissingAlias {
			id = a.Registry.Register(alias)
		}
		sample, err := a.Registry.Load(id)
		if err != nil || sample == nil {
			return
		}
		if backend := GlobalBackend(); backend != nil {
			_ = backend.PlaySample(sample, 1.0, 0)
		}
	})
	a.Queue.OnSpeech(func(string) {})
	a.playbackInstalled = true
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
func (a *Service) DrainEvents(frame, committedTick uint32, events []framepkg.EventView) {
	if a == nil {
		return
	}
	if a.Queue == nil || a.Music == nil {
		a.Init(nil)
	}
	a.Queue.Drain(frame)
	if events != nil && (!a.hasEventTick || committedTick != a.lastEventTick) {
		for _, ev := range events {
			if ev.Kind != framepkg.EventKindAudio || !ev.AudioPositional || ev.Sound == "" {
				continue
			}
			if ev.AudioAudible {
				a.playAdmittedPositional(ev.Sound, [3]numeric.Fixed{ev.X, ev.Y, ev.Z})
			}
		}
		a.lastEventTick = committedTick
		a.hasEventTick = true
	}
	a.frame = frame
	a.Music.TickFrame(frame, a.Music.IsPlaying())
}

// Frame returns the most recently drained presentation frame.
func (a *Service) Frame() uint32 {
	if a == nil {
		return 0
	}
	return a.frame
}

func (a *Service) SetViewport(v Viewport) {
	if a != nil {
		a.viewport = v
	}
}

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
	if v.StereoCapable {
		pan = ComputePan(pos, v)
		volume = VolInView
	} else {
		volume = Attenuate(pos, v)
	}
	sample, _ := a.Load(alias)
	if backend := GlobalBackend(); backend != nil && sample != nil {
		_ = backend.PlaySample(sample, VolumeFromAttenuation(volume), PanFloat(pan, v))
	}
	return pan, volume, true
}

// PlayUICue resolves and plays an unpositioned authored alias at unity.
func (a *Service) PlayUICue(alias string) bool {
	if a == nil || strings.TrimSpace(alias) == "" {
		return false
	}
	sample, err := a.Load(alias)
	if err != nil || sample == nil {
		return false
	}
	if backend := GlobalBackend(); backend != nil {
		_ = backend.PlaySample(sample, 1.0, 0)
	}
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
	n := ProbeMusicTracks(a.fs)
	a.Music.Configure(SelectMusicMode(n, hasBriefing), 0)
	a.Music.SetNumTracks(n)
	if n > 0 {
		_ = a.Music.Open(n)
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
			if backend := GlobalBackend(); backend != nil {
				_ = backend.PlaySample(sample, 1.0, 0)
			}
			return true
		}
	}
	return a.Music != nil && a.Music.NumTracks() > 0 && a.Music.Play(1)
}

// Close stops presentation music when the battle view leaves. Queue and
// decoded aliases remain service-owned for the session lifetime.
func (a *Service) Close() {
	if a == nil || a.Music == nil {
		return
	}
	a.Music.Stop()
	a.Music.Close()
}
