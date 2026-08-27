package presentation

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// EffectCapacity is the fixed active-effect pool bound [01 §6.1][03 §1].
const EffectCapacity = snapshot.MaxSnapshotEffects

// FrameTiming is optional authored GAF timing supplied by a read-only asset
// resolver. Missing or malformed timing is not replaced by a cursor guess
// [03 §4.4][03 §5.5][I9].
type FrameTiming struct {
	Durations []int32
	Loop      bool
}

// TimingResolver resolves authored timing for an already admitted event.
type TimingResolver func(Event) (FrameTiming, bool)

// EffectPool is the sole mutable active-effect owner supplied by composition.
// Presentation only admits detached views into it and reads snapshots back;
// it never implements a second cursor or retirement clock [03 §1][I6].
type EffectPool interface {
	Len() int
	Update(uint32)
	AppendView(snapshot.EffectView) bool
	SnapshotViews() []snapshot.EffectView
	RemoveMatching(pool.Handle, pool.Handle, string)
}

// EffectService is an admission/snapshot adapter around the one canonical
// render.FixedEffectPool. It owns no second cursor, expiration, or retirement
// lifecycle; Advance updates that pool once, then appends this window's
// detached event views [03 §1][I6].
type EffectService struct {
	max          int
	owner        EffectPool
	pending      []snapshot.EffectView
	nextID       uint32
	lastSequence uint64
	dropped      uint64
	resolver     TimingResolver
}

// NewEffectService returns an adapter. max <= 0 uses the fixed pool capacity;
// a smaller value is retained only as a presentation admission bound for
// existing fixture callers.
func NewEffectService(max int) *EffectService {
	if max <= 0 {
		max = EffectCapacity
	}
	return &EffectService{max: max, nextID: 1}
}

// NewEffectServiceWithPool binds the non-advancing presentation adapter to
// the canonical fixed pool owned by composition.
func NewEffectServiceWithPool(max int, owner EffectPool) *EffectService {
	s := NewEffectService(max)
	s.owner = owner
	return s
}

func (s *EffectService) SetTimingResolver(resolver TimingResolver) {
	if s != nil {
		s.resolver = resolver
	}
}

func (s *EffectService) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped
}

// Advance updates the canonical pool once and admits the current ordered event
// window. Repeated nonzero sequences are ignored defensively.
func (s *EffectService) Advance(tick uint32, events []Event) {
	if s == nil {
		return
	}
	s.dropped = 0
	if s.owner != nil {
		s.owner.Update(tick)
	} else {
		s.pending = s.pending[:0]
	}
	for _, e := range events {
		if e.Sequence != 0 && e.Sequence <= s.lastSequence {
			continue
		}
		if e.Sequence != 0 {
			s.lastSequence = e.Sequence
		}
		if e.Kind == KindSmokeEnd {
			s.removePending(e.Source, e.Target)
			if s.owner != nil {
				s.owner.RemoveMatching(e.Source, e.Target, KindSmokeStart.String())
			}
			continue
		}
		if !effectKind(e.Kind) {
			continue
		}
		s.admit(tick, RoutedEvent(e))
	}
}

// Snapshot returns a detached copy of the canonical pool's stable order.
func (s *EffectService) Snapshot() []snapshot.EffectView {
	if s == nil {
		return nil
	}
	if s.owner != nil {
		return s.owner.SnapshotViews()
	}
	return cloneViews(s.pending)
}

func (s *EffectService) admit(now uint32, e Event) {
	if s.owner != nil && s.owner.Len() >= s.max {
		s.noteDrop()
		return
	}
	id := e.ID
	if id == 0 {
		if s.nextID == 0 {
			s.noteDrop()
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
		ID: id, EventSeq: e.Sequence, Source: e.Source, Target: e.Target,
		EffectID: e.EffectID, Piece: e.Piece, SFXType: e.SFXType,
		SFXClass: e.SFXClass, Mode: e.Mode, StartTick: e.Tick,
		Lifetime: e.Lifetime, X: e.X, Y: e.Y, Z: e.Z,
		TargetX: e.TargetX, TargetY: e.TargetY, TargetZ: e.TargetZ,
		Kind: e.Kind.String(), Graphic: e.Graphic, PaletteRow: e.PaletteRow,
		Light: e.Kind == KindLHTFlash, Shake: e.Magnitude,
		AssetID: e.AssetID, SequenceID: e.SequenceID,
		DurationsA: append([]int32(nil), e.DurationsA...),
		DurationsB: append([]int32(nil), e.DurationsB...),
		LoopA:      e.LoopA, LoopB: e.LoopB,
		FlashRadius: e.FlashRadius, FlashLevel: e.FlashLevel,
		HasFlashDisc: e.HasFlashDisc, Strip: e.Strip,
		NanolatheIndex: e.NanolatheIndex, NanolatheCount: e.NanolatheCount,
		NanolatheGeometryKnown: e.NanolatheGeometryKnown,
	}
	if e.ExpiryTick != 0 {
		view.ExpiryTick = e.ExpiryTick
	} else if e.Lifetime > 0 {
		view.ExpiryTick = e.Tick + uint32(e.Lifetime)
	}
	if !validTiming(FrameTiming{Durations: view.DurationsA, Loop: view.LoopA}) &&
		!validTiming(FrameTiming{Durations: view.DurationsB, Loop: view.LoopB}) &&
		s.resolver != nil && e.Graphic != "" {
		if timing, ok := s.resolver(e); ok && validTiming(timing) {
			view.DurationsA = append([]int32(nil), timing.Durations...)
			view.LoopA = timing.Loop
		}
	}
	if s.owner != nil {
		if !s.owner.AppendView(view) {
			s.noteDrop()
		}
	} else {
		s.pending = append(s.pending, view)
	}
	_ = now // the pool advances once per call; StartTick remains event-authored.
}

func (s *EffectService) removePending(source, target pool.Handle) {
	write := 0
	removed := false
	for _, view := range s.pending {
		if !removed && view.Kind == KindSmokeStart.String() && sameEndpoint(view.Source, source, view.Target, target) {
			removed = true
			continue
		}
		s.pending[write] = view
		write++
	}
	s.pending = s.pending[:write]
}

func sameEndpoint(a, b, c, d pool.Handle) bool {
	return (a == b && (c == d || b == 0 || d == 0)) || (a == 0 && b == 0)
}

func cloneViews(in []snapshot.EffectView) []snapshot.EffectView {
	if len(in) == 0 {
		return nil
	}
	out := make([]snapshot.EffectView, len(in))
	for i, view := range in {
		out[i] = view
		out[i].DurationsA = append([]int32(nil), view.DurationsA...)
		out[i].DurationsB = append([]int32(nil), view.DurationsB...)
	}
	return out
}

func (s *EffectService) noteDrop() {
	if s.dropped != ^uint64(0) {
		s.dropped++
	}
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
	case KindCOBSFX, KindNanolathe, KindMuzzleFlash, KindSmokeStart,
		KindProjectileTrail, KindImpact, KindWaterImpact, KindExplosion,
		KindLHTFlash, KindCorpse:
		return true
	default:
		return false
	}
}
