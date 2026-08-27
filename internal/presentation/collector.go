// Package presentation contains deterministic, presentation-only admission
// contracts. Producers submit value events at tick boundaries; consumers get
// a copied ordered list and cannot feed anything back into authoritative state.
package presentation

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// Kind identifies the authored presentation cue. The ordering of these values
// is not a sorting key: Collector preserves admission order [F-P0-031]. It is
// aliased to the snapshot boundary type so no stringly conversion is required.
type Kind = snapshot.EventKind

const (
	KindInvalid         = snapshot.EventKindInvalid
	KindCOBSFX          = snapshot.EventKindCOBSFX
	KindNanolathe       = snapshot.EventKindNanolathe
	KindMuzzleFlash     = snapshot.EventKindMuzzleFlash
	KindSmokeStart      = snapshot.EventKindSmokeStart
	KindSmokeEnd        = snapshot.EventKindSmokeEnd
	KindProjectileTrail = snapshot.EventKindProjectileTrail
	KindImpact          = snapshot.EventKindImpact
	KindWaterImpact     = snapshot.EventKindWaterImpact
	KindExplosion       = snapshot.EventKindExplosion
	KindLHTFlash        = snapshot.EventKindLHTFlash
	KindShake           = snapshot.EventKindShake
	KindSound           = snapshot.EventKindSound
	KindCorpse          = snapshot.EventKindCorpse
)

// SFXClass is the typed COB SFX classification carried by an event. Numeric
// values follow cob.ClassifySFX; the collector deliberately does not import
// the COB VM, avoiding a presentation-to-authority dependency.
type SFXClass = snapshot.SFXClass

const (
	SFXVector     = snapshot.SFXVector
	SFXWhiteSmoke = snapshot.SFXWhiteSmoke
	SFXBlackSmoke = snapshot.SFXBlackSmoke
	SFXSubBubbles = snapshot.SFXSubBubbles
)

// Event is a value-only presentation cue. Lifetime and ExpiryTick are
// supplied by the producer when established; zero means unknown and is not a
// guessed duration [F-P0-034][I9]. Coordinates remain fixed-point at this
// boundary. ID and Sequence are assigned by Collector on admission.
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
	Alias                     string
	X, Y, Z                   numeric.Fixed
	TargetX, TargetY, TargetZ numeric.Fixed
	Lifetime                  int32
	ExpiryTick                uint32
	Mode                      uint8
	Team                      uint8
	PaletteRow                int16
	Magnitude                 int32
	EffectID                  uint32
	SoundID                   int32
	// Authored asset/timing metadata is value-only and copied into the
	// snapshot.  A zero/empty value is an unresolved optional resource, never
	// permission to choose fallback art [03 §5.4][03 §5.5][I9].
	AssetID                string
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
}

// Limits are presentation-only admission bounds. They do not limit the
// authoritative effect/projectile pools or the authored queue. Zero values use
// the corresponding snapshot safety bound.
type Limits struct {
	MaxEvents       int
	MaxEffectEvents int // TODO(question): exact retail family cap remains unresolved [F-P0-034]
	MaxSounds       int
}

// Collector admits typed value events in producer order. It has no callback,
// session, world, or simulation reference, so admission cannot provide
// authoritative feedback (EVENT-01/I6).
type Collector struct {
	limits       Limits
	events       []Event
	nextID       uint32
	nextSequence uint64
	dropped      uint64
	overflow     bool
	exhausted    bool
}

func NewCollector(limits Limits) *Collector {
	if limits.MaxEvents <= 0 {
		limits.MaxEvents = snapshot.MaxSnapshotEvents
	}
	if limits.MaxEffectEvents <= 0 {
		// This is an event-admission bound, not the fixed active-effect pool
		// bound. The exact retail family allocation is unresolved.
		limits.MaxEffectEvents = limits.MaxEvents
	}
	if limits.MaxSounds <= 0 {
		limits.MaxSounds = snapshot.MaxSnapshotSounds
	}
	return &Collector{limits: limits, nextID: 1, nextSequence: 1}
}

// Admit appends one event if its kind, lifetime, and deterministic bounds are
// valid. Rejected events consume neither an ID nor a sequence number.
func (c *Collector) Admit(e Event) bool {
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
	if e.Kind == KindSound {
		if c.countSounds() >= c.limits.MaxSounds {
			c.noteDrop(true)
			return false
		}
	} else if c.countEffects() >= c.limits.MaxEffectEvents {
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

// Reset starts a new deterministic admission window. IDs and sequence values
// remain monotonic across windows, while this window's events and admission
// status are cleared.
func (c *Collector) Reset() {
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
func (c *Collector) Dropped() uint64 {
	if c == nil {
		return 0
	}
	return c.dropped
}

func (c *Collector) Overflow() bool { return c != nil && c.overflow }

func (c *Collector) noteDrop(overflow bool) {
	if overflow {
		c.overflow = true
	}
	if c.dropped != ^uint64(0) {
		c.dropped++
	}
}

func (c *Collector) Events() []Event {
	if c == nil || len(c.events) == 0 {
		return nil
	}
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// EventBatch is the immutable hand-off plus admission status. A consumer can
// therefore distinguish an empty frame from a bounded frame that dropped
// authored cues.
type EventBatch struct {
	Events   []snapshot.EventView
	Dropped  uint64
	Overflow bool
}

func (c *Collector) Snapshot() EventBatch {
	if c == nil {
		return EventBatch{}
	}
	return EventBatch{Events: c.SnapshotEvents(), Dropped: c.dropped, Overflow: c.overflow}
}

// SnapshotEvents converts admitted values to the immutable snapshot schema.
// The returned slice and all string/value fields are detached from Collector.
func (c *Collector) SnapshotEvents() []snapshot.EventView {
	if c == nil || len(c.events) == 0 {
		return nil
	}
	out := make([]snapshot.EventView, len(c.events))
	for i, e := range c.events {
		out[i] = snapshot.EventView{
			ID:       e.ID,
			Sequence: e.Sequence,
			Tick:     e.Tick,
			Kind:     e.Kind,
			Source:   e.Source,
			Target:   e.Target,
			EffectID: e.EffectID,
			SoundID:  e.SoundID,
			Piece:    e.Piece,
			SFXType:  e.SFXType,
			SFXClass: e.SFXClass,
			Graphic:  e.Graphic,
			Alias:    e.Alias,
			X:        e.X, Y: e.Y, Z: e.Z,
			TargetX: e.TargetX, TargetY: e.TargetY, TargetZ: e.TargetZ,
			Lifetime:   e.Lifetime,
			ExpiryTick: e.ExpiryTick,
			Mode:       e.Mode,
			Team:       e.Team,
			PaletteRow: e.PaletteRow,
			Magnitude:  e.Magnitude,
			AssetID:    e.AssetID, SequenceID: e.SequenceID,
			DurationsA: append([]int32(nil), e.DurationsA...), DurationsB: append([]int32(nil), e.DurationsB...),
			LoopA: e.LoopA, LoopB: e.LoopB,
			FlashRadius: e.FlashRadius, FlashLevel: e.FlashLevel, HasFlashDisc: e.HasFlashDisc,
			Strip: e.Strip, NanolatheIndex: e.NanolatheIndex, NanolatheCount: e.NanolatheCount,
			NanolatheGeometryKnown: e.NanolatheGeometryKnown,
		}
	}
	return out
}

func (c *Collector) countSounds() int {
	n := 0
	for _, e := range c.events {
		if e.Kind == KindSound {
			n++
		}
	}
	return n
}

func (c *Collector) countEffects() int {
	return len(c.events) - c.countSounds()
}

func validKind(k Kind) bool { return k >= KindCOBSFX && k <= KindCorpse }

func (c *Collector) emit(kind Kind, e Event) bool {
	e.Kind = kind
	return c.Admit(e)
}

// The typed entry points retain the common value payload while preventing a
// producer from accidentally admitting a cue under the wrong kind.
func (c *Collector) EmitCOBSFX(e Event) bool          { return c.emit(KindCOBSFX, e) }
func (c *Collector) EmitNanolathe(e Event) bool       { return c.emit(KindNanolathe, e) }
func (c *Collector) EmitMuzzleFlash(e Event) bool     { return c.emit(KindMuzzleFlash, e) }
func (c *Collector) EmitSmokeStart(e Event) bool      { return c.emit(KindSmokeStart, e) }
func (c *Collector) EmitSmokeEnd(e Event) bool        { return c.emit(KindSmokeEnd, e) }
func (c *Collector) EmitProjectileTrail(e Event) bool { return c.emit(KindProjectileTrail, e) }
func (c *Collector) EmitImpact(e Event) bool          { return c.emit(KindImpact, e) }
func (c *Collector) EmitWaterImpact(e Event) bool     { return c.emit(KindWaterImpact, e) }
func (c *Collector) EmitExplosion(e Event) bool       { return c.emit(KindExplosion, e) }
func (c *Collector) EmitLHTFlash(e Event) bool        { return c.emit(KindLHTFlash, e) }
func (c *Collector) EmitShake(e Event) bool           { return c.emit(KindShake, e) }
func (c *Collector) EmitSound(e Event) bool           { return c.emit(KindSound, e) }
func (c *Collector) EmitCorpse(e Event) bool          { return c.emit(KindCorpse, e) }
