package render

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// Event and Kind are local aliases for the ordered cue input; routing and admission remain outside the renderer.
type Event = frame.Event
type Kind = frame.Kind

const (
	KindCOBSFX          = frame.KindCOBSFX
	KindNanolathe       = frame.KindNanolathe
	KindMuzzleFlash     = frame.KindMuzzleFlash
	KindSmokeStart      = frame.KindSmokeStart
	KindProjectileTrail = frame.KindProjectileTrail
	KindImpact          = frame.KindImpact
	KindWaterImpact     = frame.KindWaterImpact
	KindExplosion       = frame.KindExplosion
	KindLHTFlash        = frame.KindLHTFlash
	KindCorpse          = frame.KindCorpse
)

// EffectCapacity is the fixed active-effect pool bound [01 §6.1][03 §1].
const EffectCapacity = 300 // [03 §1] fixed active-effect pool capacity

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
	AppendView(frame.EffectView) bool
	resolveUnresolvedPrimary(TimingResolver)
	SnapshotViews() []frame.EffectView
	RemoveMatching(pool.Handle, pool.Handle, string)
	SnapshotViewsInto([]frame.EffectView) []frame.EffectView
	AdmitShatter(FragmentRequest, func(uint32) uint32) bool
	SetFragmentStepContext(FragmentStepContext)
	FragmentMetadataInto([]FragmentMetadata) []FragmentMetadata
}

// EffectService is an admission/snapshot adapter around the one canonical
// render.FixedEffectPool. It owns no second cursor, expiration, or retirement
// lifecycle; Advance updates that pool once, then appends this window's
// detached event views [03 §1][I6].
type EffectService struct {
	max          int
	owner        EffectPool
	pending      []frame.EffectView
	nextID       uint32
	lastSequence uint64
	dropped      uint64
	resolver     TimingResolver
}

// newEffectService creates the presentation admission adapter. max <= 0 uses
// the fixed pool capacity; a smaller value is retained only as a presentation
// admission bound for existing callers.
func newEffectService(max int) *EffectService {
	if max <= 0 {
		max = EffectCapacity
	}
	return &EffectService{max: max, nextID: 1}
}

// NewEffectServiceWithPool binds the non-advancing presentation adapter to
// the canonical fixed pool owned by composition.
func NewEffectServiceWithPool(max int, owner EffectPool) *EffectService {
	s := newEffectService(max)
	s.owner = owner
	return s
}

// SetTimingResolver installs the resolver that supplies an admitted event's
// authored frame timing. A nil resolver leaves timing unresolved, which keeps
// the frame player inactive rather than inventing a lifetime [03 §4.4] [I9].
func (s *EffectService) SetTimingResolver(resolver TimingResolver) {
	if s == nil {
		return
	}
	s.resolver = resolver
	// COB Create can admit named art before the shell has installed its
	// asset-backed resolver. Hydrate only those unresolved primary players in
	// the canonical owner; this neither changes record order nor retries from
	// the per-frame path [03 §1][I6].
	if resolver != nil && s.owner != nil {
		s.owner.resolveUnresolvedPrimary(resolver)
	}
}

// Dropped is the running count of events the service refused, for diagnostics.
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
		if e.Kind == frame.KindSmokeEnd {
			s.removePending(e.Source, e.Target)
			if s.owner != nil {
				s.owner.RemoveMatching(e.Source, e.Target, KindSmokeStart.String())
			}
			continue
		}
		if !effectKind(e.Kind) {
			continue
		}
		s.Admit(tick, e)
	}
}

// Admit synchronously offers one effect to the canonical fixed pool. It is
// used by producers whose allocation happens inside an authoritative callback,
// where retaining a later event queue would change the shared pool's capacity
// decision [03 §1][I5].
func (s *EffectService) Admit(tick uint32, e Event) bool {
	if s == nil || !effectKind(e.Kind) {
		return false
	}
	return s.admit(tick, e)
}

// AdmitShatter synchronously forwards paired fragment admission to the sole
// fixed-effect owner. A refused quad consumes no fragment draws [04 R-COB-04 §3].
func (s *EffectService) AdmitShatter(req FragmentRequest, draw func(uint32) uint32) bool {
	if s == nil || s.owner == nil || draw == nil {
		return false
	}
	return s.owner.AdmitShatter(req, draw)
}

// SetFragmentStepContext installs the current terrain and water values for the
// fixed pool's in-loop fragment branch [04 R-COB-04 §3].
func (s *EffectService) SetFragmentStepContext(ctx FragmentStepContext) {
	if s == nil || s.owner == nil {
		return
	}
	s.owner.SetFragmentStepContext(ctx)
}

// FragmentMetadataInto reads the canonical paired geometry table for a later
// publication adapter. It owns no second geometry store [04 R-COB-04 §3][I6].
func (s *EffectService) FragmentMetadataInto(dst []FragmentMetadata) []FragmentMetadata {
	if s == nil || s.owner == nil {
		return dst[:0]
	}
	return s.owner.FragmentMetadataInto(dst)
}

// Snapshot returns a detached copy of the canonical pool's stable order.
func (s *EffectService) Snapshot() []frame.EffectView {
	if s == nil {
		return nil
	}
	return s.SnapshotInto(nil)
}

// SnapshotInto copies the active effect views into dst, retaining destination
// capacity and nested authored timing slices for steady-state publication.
func (s *EffectService) SnapshotInto(dst []frame.EffectView) []frame.EffectView {
	if s == nil {
		return dst[:0]
	}
	if s.owner != nil {
		return s.owner.SnapshotViewsInto(dst)
	}
	return copyEffectViews(dst, s.pending)
}

func copyEffectViews(dst, src []frame.EffectView) []frame.EffectView {
	if cap(dst) < len(src) {
		dst = make([]frame.EffectView, 0, len(src))
	}
	dst = dst[:0]
	for _, srcView := range src {
		n := len(dst)
		dst = dst[:n+1]
		a, b := dst[n].DurationsA, dst[n].DurationsB
		dst[n] = srcView
		dst[n].DurationsA = append(a[:0], srcView.DurationsA...)
		dst[n].DurationsB = append(b[:0], srcView.DurationsB...)
	}
	return dst
}

func (s *EffectService) admit(now uint32, e Event) bool {
	if s.owner != nil && s.owner.Len() >= s.max {
		s.noteDrop()
		return false
	}
	id := e.ID
	if id == 0 {
		if s.nextID == 0 {
			s.noteDrop()
			return false
		}
		id = s.nextID
		s.nextID++
	} else if id >= s.nextID {
		s.nextID = id + 1
		if s.nextID == 0 {
			s.nextID = ^uint32(0)
		}
	}
	view := frame.EffectView{
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
		HasCalculatedFlash: e.HasCalculatedFlash, CalculatedTable: e.CalculatedTable,
		NanolatheIndex: e.NanolatheIndex, NanolatheCount: e.NanolatheCount,
		NanolatheGeometryKnown:  e.NanolatheGeometryKnown,
		NanolatheTargetBoxKnown: e.NanolatheTargetBoxKnown,
		NanolatheTargetMin:      e.NanolatheTargetMin, NanolatheTargetMax: e.NanolatheTargetMax,
		NanolatheBoxAtSource: e.NanolatheBoxAtSource,
	}
	if e.ExpiryTick != 0 {
		view.ExpiryTick = e.ExpiryTick
	} else if e.Lifetime > 0 {
		view.ExpiryTick = e.Tick + uint32(e.Lifetime)
	}
	// The record's two players are independent [03 §1]: the PRIMARY is the
	// event's named art and the SECONDARY is whatever generated table the
	// producer attached, each with its own authored timing [06 R-WFX-01 §2].
	// The primary lookup is therefore gated on the PRIMARY lacking timing and
	// on nothing else.
	//
	// Corrected: this used to require that NEITHER player had timing. Every
	// impact publishes the calculated flash's holds as the secondary timing,
	// so the condition was never true for an impact and its named art was
	// admitted with no player at all — art that could only ever appear as a
	// static frame 0, for exactly as long as the flash kept the record alive.
	if s.resolver != nil && e.Graphic != "" &&
		!validTiming(FrameTiming{Durations: view.DurationsA, Loop: view.LoopA}) {
		if timing, ok := s.resolver(e); ok && validTiming(timing) {
			view.DurationsA = append([]int32(nil), timing.Durations...)
			view.LoopA = timing.Loop
		}
	}
	if s.owner != nil {
		if !s.owner.AppendView(view) {
			s.noteDrop()
			return false
		}
	} else {
		// The pool is the normal publisher of per-player liveness and this
		// fixture fallback bypasses it, so mirror its admission rule here — a
		// player is live when its authored timing resolved — rather than
		// leaving pending views silently undrawable [03 §1][I9].
		view.ActiveA = validDurations(view.DurationsA)
		view.ActiveB = validDurations(view.DurationsB)
		s.pending = append(s.pending, view)
	}
	_ = now // the pool advances once per call; StartTick remains event-authored.
	return true
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
