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

// UnitView is the presentation view of one live unit. It is published by the
// sim at tick end and consumed by the renderer. Fields are a stable snapshot
// of authoritative state; mutation after Publish does not affect the buffer.
type UnitView struct {
	Slot                 pool.Handle
	DefID                uint16
	Owner                uint8
	X, Y, Z              numeric.Fixed
	Heading, Pitch, Bank uint16 // 0..65535 per circle, I2
	Health, MaxHealth    int32
	BuildRemaining       float32 // I2 allowlist: resource/ledger carry
	Flags                uint32  // selected, cloaked, underwater, nanoframe, etc.
	Model                string  // authored 3DO model name for presentation [03 §2.4]
	FootX, FootZ         int8    // packed footprint extents in cells [04 §6.2]
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

// EffectView is a placeholder for the effect presentation view.
// Later phases (render) populate this type and write Frame.Effects.
type EffectView struct{}

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

// cloneFrame deep-copies f. Nil input yields zero Frame.
func cloneFrame(f *Frame) Frame {
	if f == nil {
		return Frame{}
	}
	nf := Frame{Tick: f.Tick}
	if len(f.Units) > 0 {
		nf.Units = make([]UnitView, len(f.Units))
		copy(nf.Units, f.Units)
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
