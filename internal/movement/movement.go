// Package movement implements the Gate-2 straight-line movement stub.
// NOTE: whole file is the Gate-2 placeholder replaced by WU-07-7.
//
// This file is the Gate-2 placeholder sanctioned by PHASES.md playable-gate
// table (Gate 2 = "6 + 7-stub": Walker — rubber-band select, click to
// Move_Ground, armflea slides at 60 fps over a 30 Hz sim; A* deliberately
// stubbed as straight lerp instead, collision and visibility also stubbed).
// WU-07-7 (movement/steer.go + path) replaces this file with the real A*
// search, scheduler, route publication, and ground steering integration.
//
// No A*, no collision, no occupancy — explicitly stubbed per PHASES Gate 2.
// The stub steps a unit toward its order Node.GoalX/Z at a constant per-tick
// rate derived from the unit's MaxVelocity field when available; otherwise a
// TODO(question)-marked placeholder rate is used. Arrival snaps to the goal
// and holds. Integration is deterministic (players 0..9, slots ascending is
// preserved via units.World.Iter which is slot ascending; no hidden map
// iteration) per INVARIANTS I1. All arithmetic is fixed-point integer per
// INVARIANTS I2/I3; no floating point is used in authoritative state.
package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Mover is the Gate-2 straight-line mover.
// World is the live unit world; it is not nil when Tick is called.
type Mover struct {
	World *units.World
}

// Tick steps every alive unit with a primary order toward its GoalX/Z.
// It runs in the movement integration window after the orders/build window
// each sub-tick [01 §4.4] [GAP T15] (I7). Kernel registration places it in
// PhaseOrdersPathEconomy after the orders-pump callback so that a newly
// pushed Move_Ground is visible this same tick via the same-tick window.
//
// Rate: if UnitDef exposes MaxVelocity (Fixed 16.16 per [02 "Unit record"]),
// that value is used as the per-tick step in world units; otherwise a single
// TODO(question)-marked placeholder rate of 1 map pixel per tick is used. The
// rate field is the canonical MaxVelocity (default 0) which is already in
// 16.16 [02 "Unit record"][fmt tdf_typed.go FixedValue]; weapon conversions
// (×65536/30) are not applied to movement here. MaxVelocity reading stays
// behind TODO(question) for the slice per orchestrator direction.
func (m *Mover) Tick(tick uint32) {
	_ = tick
	if m == nil || m.World == nil {
		return
	}
	// Deterministic iteration: units.World.Iter returns slot-ascending (I1).
	// Phase 6's Tick also visits players 0..9 then slots ascending [01 §6.2];
	// this mover mirrors the stable ascending order via Iter.
	for _, u := range m.World.Iter() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			continue
		}
		head := q.Primary()[0]
		if head == nil {
			continue
		}
		// Only act on nodes that carry a ground goal. For Gate 2 that is
		// Move_Ground (and VTOL_Move variants, which share GoalX/Z). Other
		// orders have no meaningful Goal and are ignored.
		goalX := head.GoalX
		goalZ := head.GoalZ
		dx := int64(goalX - u.X)
		dz := int64(goalZ - u.Z)
		if dx == 0 && dz == 0 {
			continue
		}
		var rate numeric.Fixed
		if u.Def.MaxVelocity != 0 {
			rate = numeric.Fixed(int64(u.Def.MaxVelocity)) // [02 "Unit record"] Fixed 16.16 already
		} else {
			// TODO(question): no velocity field exposed for this def; placeholder
			// rate of 1 map pixel per tick (65536 world units). No authored value
			// to derive from, so a single constant with citation is used until
			// phase 7 supplies the profile-derived speed.
			rate = numeric.Fixed(1 * 65536) // TODO(question): placeholder rate when MaxVelocity is zero
		}
		stepToward(u, goalX, goalZ, rate)
	}
}

// isqrt returns floor(sqrt(n)) for n >= 0 using integer Newton iteration.
// No floating point is used (I2/I3). Deterministic and monotonic.
func isqrt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if n < 2 {
		return n
	}
	x := n
	y := (x + 1) >> 1
	for y < x {
		x = y
		y = (x + n/x) >> 1
	}
	return x
}

// stepToward moves u toward (goalX,goalZ) by at most rate world units using
// pure fixed-point integer math. Deltas are numeric.Fixed raw int64 [I2][I3];
// direction is normalized via integer sqrt and trunc-toward-zero division [I3].
// Snaps to the goal when within one step. Returns true on arrival.
func stepToward(u *units.Unit, goalX, goalZ, rate numeric.Fixed) bool {
	dx := int64(goalX - u.X)
	dz := int64(goalZ - u.Z)
	if dx == 0 && dz == 0 {
		return true
	}
	rateRaw := int64(rate)
	if rateRaw <= 0 {
		rateRaw = 65536 // ensure progress even if mis-configured
	}
	// Integer distance in Fixed raw units: sqrt(dx*dx+dz*dz).
	// dx, dz up to ~4000*65536=262M; squares ~6.8e16 fits in int64 (9e18).
	dist2 := dx*dx + dz*dz
	dist := isqrt(dist2)
	if dist <= rateRaw || dist == 0 {
		u.X = goalX
		u.Z = goalZ
		return true
	}
	// Straight-line step: delta = dx * rate / dist  (trunc toward zero [I3])
	// dx*rate up to ~1e14 fits in int64.
	u.X = numeric.Fixed(int64(u.X) + dx*rateRaw/dist)
	u.Z = numeric.Fixed(int64(u.Z) + dz*rateRaw/dist)
	return false
}

// Step is a test helper that steps a single unit toward a goal and reports
// arrival. It is deterministic and does not draw RNG.
func Step(u *units.Unit, goalX, goalZ, rate numeric.Fixed) bool {
	if u == nil {
		return true
	}
	return stepToward(u, goalX, goalZ, rate)
}
