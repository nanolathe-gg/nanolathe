// EventBuffer contains deterministic ordered cue admission
// state. Producers submit value events at tick boundaries; consumers get
// an ordered value list and cannot feed anything back into authoritative state.
package frame

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
	Sound                   string
	AudioPositional         bool
	AudioWater              bool
	AudioAudible            bool
	StatusKind              uint8
	StatusText              string
	StatusClass             uint8
}

// Limits are presentation-only admission bounds. They do not limit the
// authoritative effect/projectile pools or the authored queue. Zero values use
// the corresponding snapshot safety bound.
type Limits struct {
	MaxEvents       int
	MaxEffectEvents int // TODO(question): exact retail family cap remains unresolved [F-P0-034]
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
}

func NewEventBuffer(limits Limits) *EventBuffer {
	if limits.MaxEvents <= 0 {
		limits.MaxEvents = defaultEventCapacity
	}
	if limits.MaxEffectEvents <= 0 {
		// This is an event-admission bound, not the fixed active-effect pool
		// bound. The exact retail family allocation is unresolved.
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
	if c.countEffects() >= c.limits.MaxEffectEvents {
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

func (c *EventBuffer) Overflow() bool { return c != nil && c.overflow }

func (c *EventBuffer) noteDrop(overflow bool) {
	if overflow {
		c.overflow = true
	}
	if c.dropped != ^uint64(0) {
		c.dropped++
	}
}

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
			Sound: e.Sound, AudioPositional: e.AudioPositional, AudioWater: e.AudioWater, AudioAudible: e.AudioAudible,
			StatusKind: e.StatusKind, StatusText: e.StatusText, StatusClass: e.StatusClass,
		}
	}
	return dst
}

func (c *EventBuffer) countEffects() int {
	count := 0
	for _, e := range c.events {
		if e.Kind != KindStatus {
			count++
		}
	}
	return count
}

func validKind(k Kind) bool { return k >= KindCOBSFX && k <= KindStatus }

func cloneEvent(e Event) Event {
	e.DurationsA = append([]int32(nil), e.DurationsA...)
	e.DurationsB = append([]int32(nil), e.DurationsB...)
	return e
}

func (c *EventBuffer) emit(kind Kind, e Event) bool {
	e.Kind = kind
	return c.Admit(e)
}

// The typed entry points retain the common value payload while preventing a
// producer from accidentally admitting a cue under the wrong kind.
func (c *EventBuffer) EmitCOBSFX(e Event) bool          { return c.emit(KindCOBSFX, e) }
func (c *EventBuffer) EmitNanolathe(e Event) bool       { return c.emit(KindNanolathe, e) }
func (c *EventBuffer) EmitMuzzleFlash(e Event) bool     { return c.emit(KindMuzzleFlash, e) }
func (c *EventBuffer) EmitSmokeStart(e Event) bool      { return c.emit(KindSmokeStart, e) }
func (c *EventBuffer) EmitSmokeEnd(e Event) bool        { return c.emit(KindSmokeEnd, e) }
func (c *EventBuffer) EmitProjectileTrail(e Event) bool { return c.emit(KindProjectileTrail, e) }
func (c *EventBuffer) EmitImpact(e Event) bool          { return c.emit(KindImpact, e) }
func (c *EventBuffer) EmitWaterImpact(e Event) bool     { return c.emit(KindWaterImpact, e) }
func (c *EventBuffer) EmitExplosion(e Event) bool       { return c.emit(KindExplosion, e) }
func (c *EventBuffer) EmitLHTFlash(e Event) bool        { return c.emit(KindLHTFlash, e) }
func (c *EventBuffer) EmitShake(e Event) bool           { return c.emit(KindShake, e) }
func (c *EventBuffer) EmitCorpse(e Event) bool          { return c.emit(KindCorpse, e) }
