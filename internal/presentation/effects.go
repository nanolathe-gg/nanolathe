package presentation

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// EffectCapacity is the presentation-side bound for active effect views. The
// authoritative fixed effect pool is 300 records [01 §6.1][03 §1]. This bound
// only limits the immutable hand-off; it never changes simulation admission.
const EffectCapacity = snapshot.MaxSnapshotEffects

// FrameTiming is optional authored GAF timing supplied by a read-only asset
// resolver. A missing duration is deliberately not replaced by a guessed
// one-tick value [03 §4.4][03 §5.5][I9].
type FrameTiming struct {
	Durations []int32
	Loop      bool
}

// TimingResolver resolves the authored sequence for an already admitted
// event. It is presentation-only: it must not retain mutable simulation
// pointers or call back into Session. Returning false leaves frame timing
// explicitly unknown.
type TimingResolver func(Event) (FrameTiming, bool)

type activeEffect struct {
	view       snapshot.EffectView
	timing     FrameTiming
	frame      int
	countdown  int32
	hasTiming  bool
	lastUpdate uint32
}

// EffectService is a bounded, deterministic presentation effect ring. Events
// are admitted in collector order, IDs are stable, and iteration is stable.
// It has no simulation references and therefore cannot alter authoritative
// state [I6].
type EffectService struct {
	max          int
	effects      []activeEffect
	nextID       uint32
	lastSequence uint64
	dropped      uint64
	resolver     TimingResolver
}

// NewEffectService returns an empty deterministic service. max <= 0 uses the
// researched fixed effect capacity.
func NewEffectService(max int) *EffectService {
	if max <= 0 {
		max = EffectCapacity
	}
	return &EffectService{max: max, nextID: 1}
}

// SetTimingResolver installs an optional read-only GAF timing lookup. The
// resolver is not called for events without an authored Graphic identity.
func (s *EffectService) SetTimingResolver(resolver TimingResolver) {
	if s == nil {
		return
	}
	s.resolver = resolver
}

// Dropped reports effects rejected during the most recent admission window.
// Events remain visible in Frame.Events; this counter is presentation
// diagnostics for the current immutable hand-off, rather than a sticky
// process-lifetime flag.
func (s *EffectService) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped
}

// Advance expires known records and admits the current ordered event window.
// The collector is reset only after snapshot publication, so each event is
// consumed once by the session boundary. A repeated nonzero Sequence is
// ignored defensively and cannot duplicate a visual record.
func (s *EffectService) Advance(tick uint32, events []Event) {
	if s == nil {
		return
	}
	// The snapshot flag is a per-publication diagnostic. Clear the previous
	// window before admitting this one so a single full-pool rejection does not
	// mark every later frame truncated forever.
	s.dropped = 0
	s.compactExpired(tick)
	for _, e := range events {
		if e.Sequence != 0 && e.Sequence <= s.lastSequence {
			continue
		}
		if e.Sequence != 0 {
			s.lastSequence = e.Sequence
		}
		if e.Kind == KindSmokeEnd {
			s.endSmoke(e)
			continue
		}
		if !effectKind(e.Kind) {
			continue
		}
		s.admit(tick, e)
	}
}

// Snapshot copies active records in stable admission order. Callers may
// mutate the result without changing the service.
func (s *EffectService) Snapshot() []snapshot.EffectView {
	if s == nil || len(s.effects) == 0 {
		return nil
	}
	out := make([]snapshot.EffectView, len(s.effects))
	for i := range s.effects {
		out[i] = s.effects[i].view
	}
	return out
}

func (s *EffectService) admit(now uint32, e Event) {
	if len(s.effects) >= s.max {
		if s.dropped != ^uint64(0) {
			s.dropped++
		}
		return
	}
	id := e.ID
	if id == 0 {
		if s.nextID == 0 {
			if s.dropped != ^uint64(0) {
				s.dropped++
			}
			return
		}
		id = s.nextID
		s.nextID++
	} else if id >= s.nextID {
		s.nextID = id + 1
		if s.nextID == 0 {
			s.nextID = ^uint32(0)
		}
	}

	view := snapshot.EffectView{
		ID:         id,
		EventSeq:   e.Sequence,
		Source:     e.Source,
		Target:     e.Target,
		EffectID:   e.EffectID,
		Piece:      e.Piece,
		SFXType:    e.SFXType,
		SFXClass:   e.SFXClass,
		Mode:       e.Mode,
		StartTick:  e.Tick,
		Lifetime:   e.Lifetime,
		X:          e.X,
		Y:          e.Y,
		Z:          e.Z,
		TargetX:    e.TargetX,
		TargetY:    e.TargetY,
		TargetZ:    e.TargetZ,
		Kind:       e.Kind.String(),
		Graphic:    e.Graphic,
		PaletteRow: e.PaletteRow,
		Light:      e.Kind == KindLHTFlash,
		Shake:      e.Magnitude,
	}
	if e.ExpiryTick != 0 {
		view.ExpiryTick = e.ExpiryTick
	} else if e.Lifetime > 0 {
		view.ExpiryTick = e.Tick + uint32(e.Lifetime)
	}

	entry := activeEffect{view: view, lastUpdate: now}
	if s.resolver != nil && e.Graphic != "" {
		if timing, ok := s.resolver(e); ok && validTiming(timing) {
			entry.timing = FrameTiming{Durations: append([]int32(nil), timing.Durations...), Loop: timing.Loop}
			entry.hasTiming = true
			entry.countdown = entry.timing.Durations[0]
		}
	}
	s.effects = append(s.effects, entry)
}

func (s *EffectService) compactExpired(tick uint32) {
	write := 0
	for read := range s.effects {
		entry := s.effects[read]
		if entry.view.ExpiryTick != 0 && tick >= entry.view.ExpiryTick {
			continue
		}
		if entry.hasTiming {
			s.advanceTiming(&entry, tick-entry.lastUpdate)
			if !entry.timing.Loop && entry.frame >= len(entry.timing.Durations) {
				continue
			}
			entry.view.SeqA = int32(entry.frame)
		}
		entry.lastUpdate = tick
		s.effects[write] = entry
		write++
	}
	s.effects = s.effects[:write]
}

func (s *EffectService) endSmoke(e Event) {
	write := 0
	removed := false
	for _, entry := range s.effects {
		if !removed && entry.view.Kind == KindSmokeStart.String() && sameEndpoint(entry.view.Source, e.Source, entry.view.Target, e.Target) {
			removed = true
			continue
		}
		s.effects[write] = entry
		write++
	}
	s.effects = s.effects[:write]
}

func sameEndpoint(a, b, c, d pool.Handle) bool {
	return (a == b && (c == d || b == 0 || d == 0)) || (a == 0 && b == 0)
}

func (s *EffectService) advanceTiming(entry *activeEffect, delta uint32) {
	for delta > 0 && entry.hasTiming && entry.frame < len(entry.timing.Durations) {
		if entry.countdown > int32(delta) {
			entry.countdown -= int32(delta)
			return
		}
		delta -= uint32(maxPositive(entry.countdown))
		entry.frame++
		if entry.frame >= len(entry.timing.Durations) {
			if entry.timing.Loop {
				entry.frame = 0
			} else {
				return
			}
		}
		entry.countdown = entry.timing.Durations[entry.frame]
	}
}

func maxPositive(v int32) int32 {
	if v <= 0 {
		return 1
	}
	return v
}

func validTiming(t FrameTiming) bool {
	if len(t.Durations) == 0 {
		return false
	}
	for _, d := range t.Durations {
		if d <= 0 {
			return false
		}
	}
	return true
}

func effectKind(k Kind) bool {
	switch k {
	case KindCOBSFX, KindNanolathe, KindMuzzleFlash, KindSmokeStart, KindProjectileTrail,
		KindImpact, KindWaterImpact, KindExplosion, KindLHTFlash, KindCorpse:
		return true
	default:
		return false
	}
}
