package features

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// sinkVelocity is the fixed vertical velocity for submerged wrecks [05 "Feature sinking and water interaction"].
// -11468 fixed-point = -0.175 world units per tick, constant descent 5.25 world units per second at 30 Hz.
const sinkVelocity numeric.Fixed = numeric.Fixed(-11468)

// integrateSink is the 3D branch of the feature phase's active walk for an
// instance whose velocity is not all zero [05 R-FEAT-01 §13] — there is no
// separate sinking pass; visit3D reaches here after its dormant test.
//
//	pos.y += vel.y
//	floor  = coarseHeight(cell under pos)                 ; the (hmax + hmin) >> 1 query [03 §2.3]
//	if (floor << 16) < pos.y                              ; strictly above the floor
//	    if pos.y < (seaLevelByte << 16)                   ; strictly below the water plane
//	        vel = (0, −11468, 0)                          ; the latch
//	    else
//	        vel.y −= gravity                              ; the map's per-tick gravity [06 §5.1]
//	else
//	    pos.y = floor << 16 ; vel = (0, 0, 0)             ; hard snap, fraction discarded
//
// The dormant move (all-zero velocity) is tested before this on each visit, so
// a snapped instance is retired on the visit after it lands.
func (s *Service) integrateSink(inst *Instance) {
	if s.Terrain == nil || inst == nil {
		return
	}
	// Per-tick descent integrates position by velocity triple [05 ...].
	inst.Y = inst.Y.Add(inst.Vy)
	// Floor derived from plot min/max pair average [05 "Feature sinking and water interaction"].
	// The derived floor pair (PlotCell MinHeight/MaxHeight) averages to
	// the sampled floor height [02 "Terrain file"].
	// We use CoarseHeightAt which returns (Min+Max)/2 *65536 [03 §2.3].
	floor := s.Terrain.CoarseHeightAt(world.WorldToCell(inst.X), world.WorldToCell(inst.Z))
	sea := s.Terrain.SeaLevelWorld()

	// While strictly above sampled floor and strictly below water plane the
	// -11468 vertical latch re-applies every tick, so descent is constant
	// [05 "Feature sinking and water interaction"].
	if inst.Y.Raw() > floor.Raw() && inst.Y.Raw() < sea.Raw() {
		inst.Vy = sinkVelocity // [05 "Feature sinking and water interaction"] vy = -11468 fixed
		inst.IsSinking = true
		return
	}
	// Settling: bottom test at or below floor Y hard-snaps to average
	// (fraction discarded), velocities zero; the next visit's dormant test
	// retires the record [05 R-FEAT-01 §13].
	if inst.Y.Raw() <= floor.Raw() {
		// Fraction discarded: snap to integer floor height.
		inst.Y = numeric.Fixed(int64(floor.Int()) * 65536)
		inst.Vy = 0
		inst.IsSinking = false
		return
	}
	// Above surface gravity accelerates the fall until either floor clamp
	// or water entry resets it to constant rate [05 ...].
	if inst.Y.Raw() >= sea.Raw() {
		// Apply per-tick gravity. Terrain gravity is per-tick fixed [03 §2.2].
		// Gravity is positive magnitude; sinking is negative, so subtract.
		if s.Terrain.Gravity != 0 {
			inst.Vy = inst.Vy.Sub(s.Terrain.Gravity)
		} else {
			// Fallback gravity if map supplies 0: use 0x1FDB = 8155 [fmt ota] ~0.124
			inst.Vy = inst.Vy.Sub(numeric.Fixed(0x1FDB))
		}
		inst.IsSinking = true
	}
}

// StartSinking initiates sinking for a feature instance placed below waterline.
// Submerged start latches vy to -11468 when terrain at or below sea level and
// the DYING definition lacks the isfeature flag [05 "Feature sinking and water
// interaction"]. Isfeature corpses get no velocity and never descend. The flag
// lives on the dying unit's FBI record ([02 "Unit record"], UnitDef.IsFeature),
// so the death path passes it in as fromIsFeature.
func (s *Service) StartSinking(inst *Instance, fromIsFeature bool) {
	if inst == nil || s.Terrain == nil {
		return
	}
	if fromIsFeature {
		return // isfeature corpses never descend [05 "Feature sinking and water interaction"]
	}
	floor := s.Terrain.HeightAt(inst.X, inst.Z)
	sea := s.Terrain.SeaLevelWorld()
	// Medium classification uses interpolated terrain height under victim
	// against sea-level byte — never unit's own elevation [05 ...].
	// If terrain at or below sea level, submerged start.
	if floor.Raw() <= sea.Raw() {
		inst.Vy = sinkVelocity // [05 "Feature sinking and water interaction"] vy = -11468 fixed
		inst.IsSinking = true
		inst.Settled = false
		// The corpse is on the active list from its stamp; a velocity written
		// after a visit already retired it would otherwise never integrate.
		s.linkActive(inst)
		// Horizontal velocity zero [05 ...].
		// No land-path particle strip; underwater stamps are silent (no splash) [05 ...].
	}
}
