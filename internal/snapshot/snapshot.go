// Package snapshot is the presentation boundary between simulation and renderer.
//
// The simulation is authoritative and deterministic at 30 Hz. The renderer is
// presentation-only and interpolates between ticks for smooth modern motion.
// This is the one deliberate divergence from retail, which samples committed
// state with no interpolation [03 §2.4]. The sim never reads this package and
// never observes alpha (I6).
//
// Publish happens once at the end of a full tick after phase 12 [01 §4.4]
// (C15). If a frame renders with ticksToRun==0 the renderer reuses the same
// pair and alpha saturates at 1.0 with no extrapolation. On a 5-tick burst the
// renderer sees only the final pair; intermediate ticks are not drawn (C15).
//
// Alpha is computed in the client as clamp((nowNanos-tickStartNanos)/tickPeriodNanos,0,1)
// and passed in; no sim package computes or observes it (C16, I6).
package snapshot

import (
	"sync"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// PieceView is the presentation copy of one COB piece transform [03 §2.4] C21–C22.
// It carries the three uint16 rotation accumulators and script translation lanes
// [03 §2.4] C21. Rotation is 65,536 per circle and applied Z then X then Y via
// float trig round-to-nearest in the draw path (I2 allowlist: model draw trig).
// The last writer wins across TURN, turn-now and SPIN through one adapter [03 §2.4] C22.
type PieceView struct {
	Index            int           // piece index in model, -1 if unknown
	Name             string        // piece name for diagnostics/provenance
	RotX, RotY, RotZ uint16        // Z then X then Y rotation accumulators [03 §2.4] C21
	Tx, Ty, Tz       numeric.Fixed // script translation lanes [03 §2.4] C21; authored parent translation stays in Model
}

// UnitView is the presentation view of one live unit. It is published by the
// sim at tick end and consumed by the renderer. Fields are a stable snapshot
// of authoritative state; mutation after Publish does not affect the buffer.
type UnitView struct {
	Slot                 pool.Handle
	DefID                uint16
	Owner                uint8
	X, Y, Z              numeric.Fixed
	Heading, Pitch, Bank uint16 // 0..65535 per circle, I2 [04 §5.1] C25 [03 §2.4] C24
	Health, MaxHealth    int32
	BuildRemaining       float32     // I2 allowlist: resource/ledger carry
	Flags                uint32      // selected, cloaked, underwater, nanoframe, etc.
	Model                string      // authored 3DO model name for presentation [03 §2.4]
	FootX, FootZ         int8        // packed footprint extents in cells [04 §6.2]
	Pieces               []PieceView // COB piece transforms if VM bound [03 §2.4] C21–C22 [04 §4.6]; nil when no script
}

// ProjectileView is the projectile presentation view [06 §5.1] P0-I04.
type ProjectileView struct {
	Handle   pool.Handle
	X, Y, Z  numeric.Fixed
	WeaponID int32
	Shooter  pool.Handle
}

// FeatureView is the presentation view of one live feature [05 "Feature instance and terrain cell"].
// Published by the features phase and consumed by the renderer. Fields are a
// stable snapshot of authoritative state; mutation after Publish does not affect the buffer.
type FeatureView struct {
	CX, CZ       int32 // anchor cell
	X, Y, Z      numeric.Fixed
	DefName      string // canonical key or name
	Model        string // object model if any, else filename
	Health       int32
	MaxHealth    int32
	IsBurning    bool
	IsSinking    bool
	BurnTicks    int32
	FootX, FootZ int8
}

// EffectView is the presentation view of one fixed effect / strip object [03 §1] C5.
// Published from the fixed effect pool or strip objects and consumed by the renderer.
// Fields are a stable snapshot of authoritative state; mutation after Publish does not affect the buffer.
// TODO(T25): fixed effect pool not yet owned by Session; snapshot currently empty and publisher leaves it empty.
type EffectView struct {
	X, Y, Z    numeric.Fixed // position [03 §1]
	VX, VY, VZ numeric.Fixed // velocity if any
	Kind       string        // palette/effect discriminator if known
	HasModel   bool
	SeqA       int32 // current frame index for anim A if active
	SeqB       int32 // current frame index for anim B if active
}

// OrderView is the presentation view of one unit order head [04 §3][04 §7.3].
// It is published for selection/order overlays (waypoint lines) and is read-only for the HUD.
type OrderView struct {
	Unit                pool.Handle
	Target              pool.Handle
	GoalX, GoalY, GoalZ numeric.Fixed
	Kind                string // descriptor Name e.g. "Move_Ground" [04 §3]
	MoveState           uint8  // orders.MoveState if applicable
}

// ResourceView is the presentation copy of per-player economy stocks [05 "Player slot"] (I2 allowlist).
// It is published for HUD/resource bars and is read-only for the renderer.
type ResourceView struct {
	Player         uint8
	Metal          float32 // Stock[Metal] [05]
	Energy         float32 // Stock[Energy] [05]
	MetalCapacity  float32 // Capacity[Metal] [05]
	EnergyCapacity float32 // Capacity[Energy] [05]
}

// SoundEvent is one queued presentation sound cue [03 §8.3].
// Published from the audio queue for the client's audio sink (I6). Presentation-only.
// TODO(T25): Session does not yet own an audio.Queue; snapshot Sounds stays empty until wired.
type SoundEvent struct {
	Alias string      // resolved variant alias if known
	Slot  uint8       // slot id 1..23 [03 §8.3]
	Unit  pool.Handle // source unit if any
	Frame uint32      // tick when queued
}

// Frame is one published presentation frame. Tick is the authoritative global
// tick at publish time. Slices are owned by the Frame value; callers must not
// retain and mutate the slices passed to Publish after the call.
//
// Single-writer rule per docs/ORCHESTRATION.md §4: one writer per Frame field
// is serialized. Units is owned exclusively by phase-06/GATE2-SLICE (the Gate-2
// walker slice, straight-line stub per PHASES Gate 2) until WU-07-7 replaces the
// mover; no other dispatch may write Frame.Units concurrently.
type Frame struct {
	Tick uint32
	// Units — single writer: phase-06/GATE2-SLICE Gate-2 walker slice until WU-07-7 [PHASES Gate 2].
	Units       []UnitView
	Projectiles []ProjectileView
	Features    []FeatureView
	Effects     []EffectView
	Orders      []OrderView    // selection/order overlays (primary queue heads) [04 §3]
	Resources   []ResourceView // per-player stocks for HUD [05]
	Sounds      []SoundEvent   // queued presentation sound cues [03 §8.3] (I6)
	// Fog is the presentation fog cache snapshot [03 §3.3] C13.
	// It is copied from visibility.Service.Fog() each tick after the
	// visibility/sensor phase. Renderer reads it via render.BuildFogOps (I6).
	Fog FogView
}

// FogView is the presentation copy of the two-channel fog cache [03 §3.3] C13.
type FogView struct {
	W, H     int32
	Ch0, Ch1 []uint8
	Valid    bool
}

// Buffer is the double-buffered presentation state. The sim writes Current at
// tick end via Publish; the renderer reads Previous→Current with alpha via
// Read. The sim never reads this package (I6). Zero value is ready to use.
//
// A published frame is immutable. Publish deep-copies its input once, so the
// caller may reuse its slices, and then never touches the copy again — it only
// swaps pointers. That is what lets Read hand out the stored pointers directly
// instead of copying: the renderer's frames cannot change under it, because a
// later Publish replaces the pointers rather than the frames.
type Buffer struct {
	mu   sync.RWMutex
	prev *Frame
	cur  *Frame
}

// Publish stores f as the Current frame and shifts the previous Current to
// Previous. It deep-copies f once so the caller may reuse it after return, and
// is called after phase 12 of every sub-tick (C15). A nil frame is a no-op.
//
// On the first Publish both slots become the same frame, so interpolation does
// not start from a zero frame. On a burst of N sub-ticks Publish runs N times
// but only the final pair survives; intermediate ticks are not drawn (C15).
func (b *Buffer) Publish(f *Frame) {
	if b == nil || f == nil {
		return
	}
	published := cloneFrame(f)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == nil {
		b.prev = &published
		b.cur = &published
		return
	}
	b.prev = b.cur
	b.cur = &published
}

// Read returns the Previous and Current frames for interpolation. The frames
// are immutable and shared; the caller must not mutate them. If nothing has
// been published ok is false. When a render frame runs zero sub-ticks the same
// pair comes back and the caller clamps alpha to 1.0 — never extrapolate (C15).
func (b *Buffer) Read() (prev, cur *Frame, ok bool) {
	if b == nil {
		return nil, nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.cur == nil {
		return nil, nil, false
	}
	return b.prev, b.cur, true
}

// Lerp interpolates between prev and cur at alpha. Alpha is expected to be in
// [0,1] as computed by the client (C16) but is clamped defensively; values
// outside the range snap to the endpoints so no extrapolation occurs (C15).
// Presentation-only; the sim never calls it (I6).
func Lerp(prev, cur numeric.Fixed, alpha float32) numeric.Fixed {
	if alpha != alpha || alpha <= 0 { // alpha != alpha catches NaN
		return prev
	}
	if alpha >= 1 {
		return cur
	}
	delta := int64(cur) - int64(prev)
	return numeric.Fixed(int64(prev) + int64(float64(delta)*float64(alpha)))
}

// LerpAngle interpolates between prev and cur headings at alpha taking the
// shortest wrap on the 16-bit circle [04 §5.1] (I2). It is presentation-only
// and never writes sim state (I6). The result is not quantized to the
// simulation trig table; rendering uses float trig (I2 allowlist: model draw trig).
func LerpAngle(prev, cur uint16, alpha float32) uint16 {
	if alpha != alpha || alpha <= 0 {
		return prev
	}
	if alpha >= 1 {
		return cur
	}
	if prev == cur {
		return cur
	}
	delta := int16(cur - prev) // wraps via int16 shortest path [04 §8.1] C20
	return uint16(int32(prev) + int32(float64(int32(delta))*float64(alpha)))
}

// cloneFrame deep-copies f. Nil input yields zero Frame.
func cloneFrame(f *Frame) Frame {
	if f == nil {
		return Frame{}
	}
	nf := Frame{Tick: f.Tick}
	if len(f.Units) > 0 {
		nf.Units = make([]UnitView, len(f.Units))
		copy(nf.Units, f.Units)
		// Deep-copy per-unit piece slices so caller's reuse does not alias published frame.
		for i := range nf.Units {
			if len(f.Units[i].Pieces) > 0 {
				nf.Units[i].Pieces = make([]PieceView, len(f.Units[i].Pieces))
				copy(nf.Units[i].Pieces, f.Units[i].Pieces)
			}
		}
	}
	if len(f.Projectiles) > 0 {
		nf.Projectiles = make([]ProjectileView, len(f.Projectiles))
		copy(nf.Projectiles, f.Projectiles)
	}
	if len(f.Features) > 0 {
		nf.Features = make([]FeatureView, len(f.Features))
		copy(nf.Features, f.Features)
	}
	if len(f.Effects) > 0 {
		nf.Effects = make([]EffectView, len(f.Effects))
		copy(nf.Effects, f.Effects)
	}
	if len(f.Orders) > 0 {
		nf.Orders = make([]OrderView, len(f.Orders))
		copy(nf.Orders, f.Orders)
	}
	if len(f.Resources) > 0 {
		nf.Resources = make([]ResourceView, len(f.Resources))
		copy(nf.Resources, f.Resources)
	}
	if len(f.Sounds) > 0 {
		nf.Sounds = make([]SoundEvent, len(f.Sounds))
		copy(nf.Sounds, f.Sounds)
	}
	nf.Fog.W = f.Fog.W
	nf.Fog.H = f.Fog.H
	nf.Fog.Valid = f.Fog.Valid
	if len(f.Fog.Ch0) > 0 {
		nf.Fog.Ch0 = make([]uint8, len(f.Fog.Ch0))
		copy(nf.Fog.Ch0, f.Fog.Ch0)
	}
	if len(f.Fog.Ch1) > 0 {
		nf.Fog.Ch1 = make([]uint8, len(f.Fog.Ch1))
		copy(nf.Fog.Ch1, f.Fog.Ch1)
	}
	return nf
}
