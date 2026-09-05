package frame

// EventBuffer contains deterministic ordered cue admission
// state. Producers submit value events at tick boundaries; consumers get
// an ordered value list and cannot feed anything back into authoritative state.

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Kind identifies the authored presentation cue. The ordering of these values
// is not a sorting key: EventBuffer preserves admission order [F-P0-031]. It is
// aliased to the snapshot boundary type so no stringly conversion is required.
type Kind = EventKind

const (
	KindInvalid         = EventKindInvalid
	KindCOBSFX          = EventKindCOBSFX
	KindNanolathe       = EventKindNanolathe
	KindMuzzleFlash     = EventKindMuzzleFlash
	KindSmokeStart      = EventKindSmokeStart
	KindSmokeEnd        = EventKindSmokeEnd
	KindProjectileTrail = EventKindProjectileTrail
	KindImpact          = EventKindImpact
	KindWaterImpact     = EventKindWaterImpact
	KindExplosion       = EventKindExplosion
	KindLHTFlash        = EventKindLHTFlash
	KindShake           = EventKindShake
	KindCorpse          = EventKindCorpse
	KindAudio           = EventKindAudio
	KindStatus          = EventKindStatus
)

const (
	defaultEventCapacity = 4096
)

// Event is a value-only presentation cue. Lifetime and ExpiryTick are
// supplied by the producer when established; zero means unknown and is not a
// guessed duration [F-P0-034][I9]. Coordinates remain fixed-point at this
// boundary. ID and Sequence are assigned by EventBuffer on admission.
type Event struct {
	ID                        uint32
	Sequence                  uint64
	Tick                      uint32
	Kind                      Kind
	Source                    pool.Handle
	Target                    pool.Handle
	Piece                     int32
	SFXType                   int32
	SFXClass                  SFXClass
	Graphic                   string
	X, Y, Z                   numeric.Fixed
	TargetX, TargetY, TargetZ numeric.Fixed
	Lifetime                  int32
	ExpiryTick                uint32
	Mode                      uint8
	Team                      uint8
	PaletteRow                int16
	Magnitude                 int32
	EffectID                  uint32
	// Authored asset/timing metadata is value-only and copied into the
	// frame.  A zero/empty value is an unresolved optional resource, never
	// permission to choose fallback art [03 §5.4][03 §5.5][I9].
	AssetID string
	// HasCalculatedFlash and CalculatedTable are the explosion pool's SECONDARY
	// cursor [06 R-WFX-01 §2]: every impact allocates a record with a
	// procedurally generated disc under its named art, and a weapon with no art
	// holder still shows the disc. The table index is 0..2; the flag carries
	// "there is one" so a zero value cannot read as table 0.
	HasCalculatedFlash     bool
	CalculatedTable        uint8
	SequenceID             string
	DurationsA             []int32
	DurationsB             []int32
	LoopA                  bool
	LoopB                  bool
	FlashRadius            int32
	FlashLevel             int32
	HasFlashDisc           bool
	Strip                  int8
	Producer               StripProducer
	NanolatheIndex         int32
	NanolatheCount         int32
	NanolatheGeometryKnown bool
	NanolatheActiveUntil   uint32
	// Feature reclaim/resurrection carries the authored target box through the
	// committed boundary; unit/build producers may leave it unset [05 R-WORK-01
	// §8].
	NanolatheTargetBoxKnown bool
	NanolatheTargetMin      [3]numeric.Fixed
	NanolatheTargetMax      [3]numeric.Fixed
	// NanolatheBoxAtSource says which END of the segment the six-word box
	// above belongs to. One submission routine serves both directions and
	// differs only in which argument it expands into a degenerate box and
	// which it takes as the six-word box [05 R-WORK-01 §8]: build, repair and
	// resurrection spray from the builder's nano piece INTO the box, while
	// unit reclaim, capture and feature reclaim spray FROM the box into the
	// builder's nano piece. Set means the latter, and the segment's
	// destination is then the published target point.
	NanolatheBoxAtSource bool
	Sound                string
	AudioPositional      bool
	AudioWater           bool
	AudioAudible         bool
	StatusKind           uint8
	StatusText           string
	StatusClass          uint8
}

// Limits are presentation-only admission bounds. They do not limit the
// authoritative effect/projectile pools or the authored queue. Zero values use
// the corresponding snapshot safety bound.
//
// MaxEffectEvents is a Nanolathe bound with no retail counterpart, and it
// previously said the opposite behind an open-question marker: "exact retail
// family cap remains unresolved [F-P0-034]". There is no retail cap to resolve here,
// because retail has no per-tick event window at all — it spawns into the
// pools directly, and the pool bounds those spawns. Both pool caps are
// established: the fixed effect pool holds up to 300 fixed-size records and an
// append at or above the cap allocates nothing (no eviction, the newcomer is
// simply lost) [03 "Fixed effect pool"], and strip objects evict oldest-first
// past 400, giving a steady bound of 401 [03 §1][I5]. Those numbers belong to
// whatever owns the pools, never to this window: bounding one tick's
// submissions by 300 would drop spawns retail accepts, since a record admitted
// this tick may be one that retires this tick.
type Limits struct {
	MaxEvents       int
	MaxEffectEvents int
}

// EventBuffer admits typed value events in producer order. It has no callback,
// session, world, or simulation reference, so admission cannot provide
// authoritative feedback (EVENT-01/I6).
type EventBuffer struct {
	limits       Limits
	events       []Event
	nextID       uint32
	nextSequence uint64
	dropped      uint64
	overflow     bool
	exhausted    bool
	// effects counts the admitted non-status events in this window. It is
	// maintained as events are admitted and cleared by Reset, so the effect
	// bound costs O(1) per admission. Recounting the window on every Admit
	// made a tick with n events do O(n^2) classification work.
	effects int
}

// NewEventBuffer creates an admission window under limits. A non-positive
// MaxEvents takes the default capacity; a non-positive MaxEffectEvents takes
// the whole window, so this buffer never drops a submission the pools would
// have accepted.
func NewEventBuffer(limits Limits) *EventBuffer {
	if limits.MaxEvents <= 0 {
		limits.MaxEvents = defaultEventCapacity
	}
	if limits.MaxEffectEvents <= 0 {
		// An event-admission bound, not a pool bound: it defaults to the whole
		// window so this buffer never drops a submission the pools would have
		// taken. See the Limits doc for why 300 and 400 do not belong here.
		limits.MaxEffectEvents = limits.MaxEvents
	}
	return &EventBuffer{limits: limits, nextID: 1, nextSequence: 1}
}

// Admit appends one event if its kind, lifetime, and deterministic bounds are
// valid. Rejected events consume neither an ID nor a sequence number.
func (c *EventBuffer) Admit(e Event) bool {
	if c == nil {
		return false
	}
	if c.exhausted {
		c.noteDrop(true)
		return false
	}
	if !validKind(e.Kind) || e.Lifetime < 0 {
		c.noteDrop(false)
		return false
	}
	e = RoutedEvent(e)
	if len(c.events) >= c.limits.MaxEvents {
		c.noteDrop(true)
		return false
	}
	if !isStatusKind(e.Kind) && c.effects >= c.limits.MaxEffectEvents {
		c.noteDrop(true)
		return false
	}
	if c.nextID == 0 || c.nextSequence == 0 {
		c.exhausted = true
		c.noteDrop(true)
		return false
	}
	e.ID = c.nextID
	e.Sequence = c.nextSequence
	if c.nextID == ^uint32(0) {
		c.nextID = 0
	} else {
		c.nextID++
	}
	if c.nextSequence == ^uint64(0) {
		c.nextSequence = 0
	} else {
		c.nextSequence++
	}
	if !isStatusKind(e.Kind) {
		c.effects++
	}
	c.events = append(c.events, e)
	return true
}

// EmitAudio admits one already-gated positional audio cue for presentation.
// It shares the committed event ordering with impact and effect cues.
func (c *EventBuffer) EmitAudio(e Event) bool {
	e.Kind = KindAudio
	e.AudioPositional = true
	e.AudioAudible = true
	return c.Admit(e)
}

// EmitStatus admits a semantic unit-caption request. It is intentionally a
// value event: the session owns the request, while the client owns caption
// arbitration and message-line drawing [03 §8.3][07 R-HUD-03 §14][I6].
func (c *EventBuffer) EmitStatus(e Event) bool {
	e.Kind = KindStatus
	return c.Admit(e)
}

// Reset starts a new deterministic admission window. IDs and sequence values
// remain monotonic across windows, while this window's events and admission
// status are cleared.
func (c *EventBuffer) Reset() {
	if c == nil {
		return
	}
	c.events = c.events[:0]
	c.effects = 0
	c.dropped = 0
	c.overflow = false
}

// Dropped reports admissions rejected by a bound or invalid value. Overflow
// remains visible to the caller so a bounded presentation stream cannot fail
// silently. Dropped is scoped to the current admission window.
func (c *EventBuffer) Dropped() uint64 {
	if c == nil {
		return 0
	}
	return c.dropped
}

// Overflow reports whether any admission was refused for want of room since
// the last Reset.
func (c *EventBuffer) Overflow() bool { return c != nil && c.overflow }

func (c *EventBuffer) noteDrop(overflow bool) {
	if overflow {
		c.overflow = true
	}
	if c.dropped != ^uint64(0) {
		c.dropped++
	}
}

// Events returns a copy of the admitted values in producer order.
func (c *EventBuffer) Events() []Event {
	if c == nil || len(c.events) == 0 {
		return nil
	}
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// StagingEvents exposes the current admission window to the session publisher
// for one synchronous publication pass. The returned slice aliases staging and
// must not be retained after Reset; external consumers should use Events or
// SnapshotEvents instead.
func (c *EventBuffer) StagingEvents() []Event {
	if c == nil || len(c.events) == 0 {
		return nil
	}
	return c.events
}

// EventBatch is the immutable hand-off plus admission status. A consumer can
// therefore distinguish an empty frame from a bounded frame that dropped
// authored cues.
type EventBatch struct {
	Events   []EventView
	Dropped  uint64
	Overflow bool
}

// Snapshot is the immutable hand-off for one tick: the detached event views
// plus the drop count and overflow flag that describe the window.
func (c *EventBuffer) Snapshot() EventBatch {
	if c == nil {
		return EventBatch{}
	}
	return EventBatch{Events: c.SnapshotEvents(), Dropped: c.dropped, Overflow: c.overflow}
}

// SnapshotEvents converts admitted values to the immutable snapshot schema.
// The returned slice and all string/value fields are detached from EventBuffer.
func (c *EventBuffer) SnapshotEvents() []EventView {
	if c == nil || len(c.events) == 0 {
		return nil
	}
	return c.SnapshotEventsInto(nil)
}

// SnapshotEventsInto copies the current staging window into dst, retaining
// destination capacity and nested duration storage. The returned values are
// detached from the staging events.
func (c *EventBuffer) SnapshotEventsInto(dst []EventView) []EventView {
	if c == nil || len(c.events) == 0 {
		return dst[:0]
	}
	if cap(dst) < len(c.events) {
		dst = make([]EventView, 0, len(c.events))
	}
	dst = dst[:0]
	for _, e := range c.events {
		n := len(dst)
		dst = dst[:n+1]
		a, b := dst[n].DurationsA, dst[n].DurationsB
		dst[n] = EventView{
			ID:       e.ID,
			Sequence: e.Sequence,
			Tick:     e.Tick,
			Kind:     e.Kind,
			Source:   e.Source,
			Target:   e.Target,
			EffectID: e.EffectID,
			Piece:    e.Piece,
			SFXType:  e.SFXType,
			SFXClass: e.SFXClass,
			Graphic:  e.Graphic,
			X:        e.X, Y: e.Y, Z: e.Z,
			TargetX: e.TargetX, TargetY: e.TargetY, TargetZ: e.TargetZ,
			Lifetime:   e.Lifetime,
			ExpiryTick: e.ExpiryTick,
			Mode:       e.Mode,
			Team:       e.Team,
			PaletteRow: e.PaletteRow,
			Magnitude:  e.Magnitude,
			AssetID:    e.AssetID, SequenceID: e.SequenceID,
			DurationsA: append(a[:0], e.DurationsA...), DurationsB: append(b[:0], e.DurationsB...),
			LoopA: e.LoopA, LoopB: e.LoopB,
			FlashRadius: e.FlashRadius, FlashLevel: e.FlashLevel, HasFlashDisc: e.HasFlashDisc,
			Strip: e.Strip, NanolatheIndex: e.NanolatheIndex, NanolatheCount: e.NanolatheCount,
			NanolatheGeometryKnown:  e.NanolatheGeometryKnown,
			NanolatheActiveUntil:    e.NanolatheActiveUntil,
			NanolatheTargetBoxKnown: e.NanolatheTargetBoxKnown,
			NanolatheTargetMin:      e.NanolatheTargetMin, NanolatheTargetMax: e.NanolatheTargetMax,
			NanolatheBoxAtSource: e.NanolatheBoxAtSource,
			Sound:                e.Sound, AudioPositional: e.AudioPositional, AudioWater: e.AudioWater, AudioAudible: e.AudioAudible,
			StatusKind: e.StatusKind, StatusText: e.StatusText, StatusClass: e.StatusClass,
		}
	}
	return dst
}

// isStatusKind reports whether a kind is excluded from the effect bound. The
// bound counts every admitted event that is not a status record.
func isStatusKind(k Kind) bool { return k == KindStatus }

func validKind(k Kind) bool { return k >= KindCOBSFX && k <= KindStatus }

func (c *EventBuffer) emit(kind Kind, e Event) bool {
	e.Kind = kind
	return c.Admit(e)
}

// EmitCOBSFX admits a COB SFX cue. It heads the typed entry points below,
// which retain the common value payload while preventing a producer from
// accidentally admitting a cue under the wrong kind.
func (c *EventBuffer) EmitCOBSFX(e Event) bool { return c.emit(KindCOBSFX, e) }

// EmitNanolathe admits a nanolathe beam cue.
func (c *EventBuffer) EmitNanolathe(e Event) bool { return c.emit(KindNanolathe, e) }

// EmitMuzzleFlash admits a muzzle-flash cue.
func (c *EventBuffer) EmitMuzzleFlash(e Event) bool { return c.emit(KindMuzzleFlash, e) }

// EmitSmokeStart admits the start of a smoke trail.
func (c *EventBuffer) EmitSmokeStart(e Event) bool { return c.emit(KindSmokeStart, e) }

// EmitSmokeEnd admits the end of a smoke trail.
func (c *EventBuffer) EmitSmokeEnd(e Event) bool { return c.emit(KindSmokeEnd, e) }

// EmitProjectileTrail admits a projectile trail cue.
func (c *EventBuffer) EmitProjectileTrail(e Event) bool { return c.emit(KindProjectileTrail, e) }

// EmitImpact admits a ground impact cue.
func (c *EventBuffer) EmitImpact(e Event) bool { return c.emit(KindImpact, e) }

// EmitWaterImpact admits a water impact cue.
func (c *EventBuffer) EmitWaterImpact(e Event) bool { return c.emit(KindWaterImpact, e) }

// EmitExplosion admits an explosion cue.
func (c *EventBuffer) EmitExplosion(e Event) bool { return c.emit(KindExplosion, e) }

// EmitLHTFlash admits a PALETTE.LHT light-flash cue.
func (c *EventBuffer) EmitLHTFlash(e Event) bool { return c.emit(KindLHTFlash, e) }

// EmitShake admits a camera-shake cue.
func (c *EventBuffer) EmitShake(e Event) bool { return c.emit(KindShake, e) }

// EmitCorpse admits a corpse-creation cue.
func (c *EventBuffer) EmitCorpse(e Event) bool { return c.emit(KindCorpse, e) }
