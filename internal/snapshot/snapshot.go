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
}

// ProjectileView is a placeholder for the projectile presentation view.
// Later phases (combat) populate this type and write Frame.Projectiles. One
// writer per Frame field; dispatches that extend Frame are serialized per
// ORCHESTRATION §4. Currently empty so the package compiles before phase 9.
type ProjectileView struct{}

// FeatureView is a placeholder for the feature presentation view.
// Later phases (features/world) populate this type and write Frame.Features.
type FeatureView struct{}

// EffectView is a placeholder for the effect presentation view.
// Later phases (render) populate this type and write Frame.Effects.
type EffectView struct{}

// Frame is one published presentation frame. Tick is the authoritative global
// tick at publish time. Slices are owned by the Frame value; callers must not
// retain and mutate the slices passed to Publish after the call.
type Frame struct {
	Tick        uint32
	Units       []UnitView
	Projectiles []ProjectileView
	Features    []FeatureView
	Effects     []EffectView
}

// Buffer is the double-buffered presentation state. The sim writes Current at
// tick end via Publish; the renderer reads Previous→Current with alpha via
// Read. The sim never reads this package (I6). Zero value is ready to use.
//
// The two frames are immutable after Publish: Publish deep-copies the input
// so the caller may reuse its slices, and Read returns deep copies so the
// renderer may retain the pointers without racing the next Publish.
type Buffer struct {
	mu      sync.RWMutex
	prev    Frame
	cur     Frame
	hasData bool
}

// Publish stores f as the Current frame and shifts the previous Current to
// Previous. It deep-copies all slices so the caller may reuse f after return.
// It is called once at the end of a full tick after phase 12 (C15). If f is
// nil the call is a no-op.
//
// On the first Publish both Previous and Current become f, so interpolation
// from Previous→Current does not start from a zero frame. On a burst of N
// ticks Publish is called N times but only the final pair survives; intermediate
// ticks are not drawn (C15).
func (b *Buffer) Publish(f *Frame) {
	if b == nil || f == nil {
		return
	}
	nf := cloneFrame(f)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.hasData {
		// First publish: both slots become nf but with independent slice storage
		// so later mutation of returned copies cannot alias.
		b.prev = cloneFrame(&nf)
		b.cur = nf
		b.hasData = true
		return
	}
	b.prev = b.cur
	b.cur = nf
}

// Read returns the Previous and Current frames for interpolation. The returned
// pointers are deep copies owned by the caller; mutating them does not affect
// the buffer. If no frame has been published ok is false and both pointers
// are nil. When ticksToRun==0 the same pair is returned again and the caller
// clamps alpha to 1.0 with no extrapolation (C15).
func (b *Buffer) Read() (prev, cur *Frame, ok bool) {
	if b == nil {
		return nil, nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.hasData {
		return nil, nil, false
	}
	p := cloneFrame(&b.prev)
	c := cloneFrame(&b.cur)
	return &p, &c, true
}

// Lerp interpolates between prev and cur at alpha. Alpha is expected to be
// in [0,1] as computed by the client (C16) but is clamped defensively; values
// outside the range snap to the endpoints so no extrapolation occurs (C15).
// Truncation is toward zero, matching retail's __ftol (I3). This is
// presentation-only; the sim never calls it (I6).
func Lerp(prev, cur numeric.Fixed, alpha float32) numeric.Fixed {
	if alpha <= 0 || alpha != alpha { // alpha != alpha catches NaN
		return prev
	}
	if alpha >= 1 {
		return cur
	}
	// Clamp defensively for -0 or small out-of-range due to float error.
	if alpha < 0 {
		alpha = 0
	} else if alpha > 1 {
		alpha = 1
	}
	delta := int64(cur) - int64(prev)
	// delta*alpha in float64 then trunc toward zero via int64 conversion (I3).
	// Using float64 for the product preserves more integer precision than float32
	// while still truncating; the cast through float64(alpha) matches the float32
	// alpha contract without forcing 32-bit intermediate overflow.
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
	return nf
}
