package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type terrainShotResult uint8

const (
	terrainShotUnknown terrainShotResult = iota
	terrainShotClear
	terrainShotBlocked
)

// modernTerrainSampleBudget is a Modern policy work limit, not a retail
// constant. Exhaustion admits the shot: incomplete prediction proves nothing.
const modernTerrainSampleBudget = 4096

// modernTerrainAdmission is user-authorized Modern policy, not retail behavior.
// Retail admits without a terrain check [06 R-WPN-05 §1]. Preview uses the
// actual launch/motion kernels and post-motion contacts [06 §6.3][06 §6.4]
// [06 §6.6][06 §6.7][06 §8.1]. Only the resolved current target geometry is
// protected; this makes no promise about future movement or interceptions.
func modernTerrainAdmission(launch Slot, muzzle, aim Vec3, tick uint32, terrain *world.Terrain, target *units.Unit, wind *world.Wind) terrainShotResult {
	weapon := launch.Weapon
	if weapon == nil || terrain == nil || weapon.Burst != 0 || weapon.UnitsOnly || weapon.GroundBounce || weapon.NoExplode ||
		weapon.Interceptor || weapon.Cruise || weapon.WaterWeapon || weapon.Range < 0 || weapon.Range >= 32768 ||
		weapon.WeaponVelocity < 0 || weapon.StartVelocity < 0 || weapon.WeaponAcceleration < 0 ||
		!terrainPointValid(muzzle) || !terrainPointValid(aim) {
		return terrainShotUnknown
	}
	// Launch helpers narrow deltas and planar distances to signed words. Avoid
	// wrapped geometry: it cannot establish obstruction before the target.
	dx, dy, dz := aim.X.Raw()-muzzle.X.Raw(), aim.Y.Raw()-muzzle.Y.Raw(), aim.Z.Raw()-muzzle.Z.Raw()
	const max = int64(1<<31 - 1)
	if dx < -max || dx > max || dy < -max || dy > max || dz < -max || dz > max || dx*dx > max*max-dz*dz || (dx == 0 && dz == 0) {
		return terrainShotUnknown
	}
	box := UnitForArea{Min: aim, Max: aim}
	if target != nil && target.Def != nil {
		lo, hi := target.Def.BoundingExtents()
		box.Min = Vec3{X: target.X.Add(numeric.Fixed(lo[0])), Y: target.Y.Add(numeric.Fixed(lo[1])), Z: target.Z.Add(numeric.Fixed(lo[2]))}
		box.Max = Vec3{X: target.X.Add(numeric.Fixed(hi[0])), Y: target.Y.Add(numeric.Fixed(hi[1])), Z: target.Z.Add(numeric.Fixed(hi[2]))}
		if !terrainPointValid(box.Min) || !terrainPointValid(box.Max) || box.Min.X > box.Max.X || box.Min.Y > box.Max.Y || box.Min.Z > box.Max.Z {
			return terrainShotUnknown
		}
	}
	sample := terrainAdmissionSample{weapon: weapon, terrain: terrain, target: target, box: box, muzzle: muzzle, aim: aim}
	var preview Projectile
	creation, motion := liveCreationFamilyForWeapon(weapon), MotionFamilyForWeapon(weapon)
	switch {
	case creation == CreationOrdinary && (motion == MotionDirect || motion == MotionSelfProp):
		InitOrdinary(&preview, weapon, tick, muzzle, aim, 0)
	case creation == CreationVertical && motion == MotionSelfProp:
		InitVertical(&preview, weapon, tick, muzzle, aim, 0)
	case creation == CreationBallistic && motion == MotionBallistic:
		if weapon.WeaponVelocity == 0 || wind == nil || launch.DistanceWord < 0 || terrain.Gravity < 0 || terrain.Gravity.Raw() > max {
			return terrainShotUnknown
		}
		yaw, pitch := numeric.Angle(retailYawFromGo(launch.DesiredYaw)), numeric.Angle(launch.DesiredPitch)
		velocity := VelocityFromAngles(yaw, pitch, numeric.Fixed(weapon.WeaponVelocity))
		drop := int64(BallisticFlightTicks(launch.DistanceWord, weapon.WeaponVelocity)) * terrain.Gravity.Raw()
		if drop > max || velocity.Y.Raw()-drop < -max {
			return terrainShotUnknown
		}
		// Burn-blow's divide must be forward and nonzero before invoking its
		// initializer [06 §6.4]. Wrapped or expired deadlines prove nothing.
		if weapon.BurnBlow && numeric.MulRound(numeric.Cos(pitch), weapon.WeaponVelocity) <= 0 {
			return terrainShotUnknown
		}
		InitBallistic(&preview, weapon, tick, muzzle, aim, 0, pitch, yaw, launch.DistanceWord, terrain.Gravity)
	default:
		return terrainShotUnknown
	}
	if motion == MotionDirect && preview.Velocity.X == 0 && preview.Velocity.Z == 0 {
		return terrainShotUnknown
	}
	if !(motion == MotionBallistic && weapon.WeaponTimer == 0 && !weapon.BurnBlow) && preview.ExpiryTick <= tick {
		return terrainShotUnknown
	}
	if motion == MotionSelfProp && weapon.Guidance && !weapon.TwoPhase && weapon.TurnRate != 0 {
		// An immobile target makes the pursuit deterministic, so the whole
		// flight is provable. Fall back to the one-step superset otherwise.
		if result := guidedPursuitTerrainAdmission(weapon, tick, terrain, sample, target, muzzle, aim); result != terrainShotUnknown {
			return result
		}
		return guidedLaunchTerrainAdmission(preview, weapon, tick, terrain, sample)
	}
	// With burn-blow even a zero turn rate can impact on a target-dependent
	// steering failure. Do not assume the launch-time target remains live.
	if motion == MotionSelfProp && weapon.BurnBlow && !weapon.TwoPhase && weapon.Guidance {
		return terrainShotUnknown
	}
	launchTick := tick
	for n := 0; n < modernTerrainSampleBudget; n++ {
		var result AdvanceResult
		switch motion {
		case MotionDirect:
			result = AdvanceDirect(&preview, weapon, tick)
		case MotionBallistic:
			// Phase 8 redraws AFTER this tick's projectile work [01 §7.3]. Even
			// an overdue current vector is known for launchTick, but not beyond
			// the next projectile sample after a due phase-8 redraw. A map whose
			// draw is always zero has no future vector uncertainty.
			constantZero := wind.Min == 0 && wind.Max >= 0 && wind.Max <= 1 && wind.DirX == 0 && wind.DirZ == 0
			if tick != launchTick && tick-1 > wind.NextChange && !constantZero {
				return terrainShotUnknown
			}
			result = AdvanceBallistic(&preview, weapon, tick, Vec3{X: numeric.Fixed(wind.DirX), Z: numeric.Fixed(wind.DirZ)}, terrain.Gravity)
		case MotionSelfProp:
			// Stop before expiry/phase transition. The target-independent launch
			// stage is established; later guidance is outside this proof.
			if tick >= preview.ExpiryTick {
				return terrainShotUnknown
			}
			result = AdvanceSelfProp(&preview, weapon, tick, terrain.Gravity, numeric.FixedFromInt(int64(terrain.SeaLevel)), GuidanceEnv{})
		}
		if result != AdvanceAlive || !terrainPointValid(preview.Velocity) {
			return terrainShotUnknown
		}
		if result, done := sample.contact(preview.Pos); done {
			return result
		}
		if tick == ^uint32(0) {
			return terrainShotUnknown
		}
		tick++
	}
	return terrainShotUnknown
}

func terrainPointValid(point Vec3) bool {
	return point.X.Raw() == int64(int32(point.X.Raw())) && point.Y.Raw() == int64(int32(point.Y.Raw())) && point.Z.Raw() == int64(int32(point.Z.Raw()))
}

type terrainAdmissionSample struct {
	weapon      *content.WeaponDef
	terrain     *world.Terrain
	target      *units.Unit
	box         UnitForArea
	muzzle, aim Vec3
}

// contact samples exactly one post-motion plot, never the intervening segment
// [06 §8.1]. Current target contact precedes terrain; splash uses [06 §9.3].
func (s terrainAdmissionSample) contact(point Vec3) (terrainShotResult, bool) {
	if !terrainPointValid(point) {
		return terrainShotUnknown, true
	}
	dx, dz := s.aim.X.Raw()-s.muzzle.X.Raw(), s.aim.Z.Raw()-s.muzzle.Z.Raw()
	absX, absZ := dx, dz
	if absX < 0 {
		absX = -absX
	}
	if absZ < 0 {
		absZ = -absZ
	}
	coordinate, end, delta := point.Z, s.aim.Z, dz
	if absX >= absZ {
		coordinate, end, delta = point.X, s.aim.X, dx
	}
	// Reaching/passing the current target plane is admissible, including
	// quantized overshoot. Terrain at or beyond the target proves no block.
	if (delta > 0 && coordinate >= end) || (delta < 0 && coordinate <= end) {
		return terrainShotClear, true
	}
	cell := s.terrain.PlotAt(world.WorldToCell(point.X), world.WorldToCell(point.Z))
	if cell == nil {
		return terrainShotUnknown, true
	}
	if s.target != nil && s.target.Def != nil && s.target.Handle != 0 {
		lower, upper := contactBand(s.target)
		py := int32(point.Y.Raw())
		if (cell.OccupantA() == int16(s.target.Handle) && CollisionSlotYGate(py, lower, upper, 0)) ||
			(cell.OccupantB() == int16(s.target.Handle) && CollisionSlotYGate(py, lower, upper, 1)) {
			return terrainShotClear, true
		}
	}
	if int16(point.Y.Raw()>>16) < int16(cell.MinHeight()) {
		if !terrainBoxDistanceValid(point, s.box) {
			return terrainShotUnknown, true
		}
		if DistanceToBox(point, s.box) < BlastRadius(s.weapon.AreaOfEffect) {
			return terrainShotClear, true
		}
		return terrainShotBlocked, true
	}
	// A prior water contact can end flight before a later terrain sample;
	// opaque-liquid impact policy is not part of this terrain-only proof.
	if int16(point.Y.Raw()>>16) < int16(s.terrain.SeaLevel) {
		return terrainShotUnknown, true
	}
	return terrainShotUnknown, false
}

// Keep the blast kernel's signed raw distance and signed whole-word result
// unwrapped [06 §9.3]. Wider distances cannot support a conservative proof.
func terrainBoxDistanceValid(point Vec3, box UnitForArea) bool {
	const max = int64(1<<31 - 1)
	remaining := max * max
	for _, axis := range [...][3]numeric.Fixed{{point.X, box.Min.X, box.Max.X}, {point.Y, box.Min.Y, box.Max.Y}, {point.Z, box.Min.Z, box.Max.Z}} {
		var distance int64
		if axis[0] < axis[1] {
			distance = axis[1].Raw() - axis[0].Raw()
		} else if axis[0] > axis[2] {
			distance = axis[0].Raw() - axis[2].Raw()
		}
		if distance > max || distance*distance > remaining {
			return false
		}
		remaining -= distance * distance
	}
	return true
}

// guidedPursuitTerrainAdmission previews a guided self-propelled shot for its
// WHOLE flight, which guidedLaunchTerrainAdmission below cannot: that proof
// spans one tick, so a missile whose launch clears the ground and buries itself
// in a rising slope several ticks later is never provably blocked, and the
// shooter re-fires into the same hill forever.
//
// The extra reach is sound only because an immobile target removes the
// uncertainty the one-step proof exists to cover. Steering pursues the point
// [06 §6.7] resolves each tick, so a target that cannot move makes every later
// tick a function of the launch alone and the preview EXACT rather than a
// superset. A target's death does not perturb it either: the retained unit
// point and the record's stored point are the same point for something that
// never moved, so [06 §6.7]'s fallback follows the same path.
//
// Like the rest of this policy it reasons about terrain and the resolved target
// only. A shot refused here might have struck some third unit standing in the
// path; the one-step proof has always had that property, and a shooter with an
// engageable target that close would ordinarily have acquired it instead.
func guidedPursuitTerrainAdmission(weapon *content.WeaponDef, tick uint32, terrain *world.Terrain, sample terrainAdmissionSample, target *units.Unit, muzzle, aim Vec3) terrainShotResult {
	// Burn-blow detonates on a steering failure rather than flying on, so its
	// flight is not a function of the launch [06 §6.6].
	if weapon.BurnBlow || target == nil || target.Def == nil || target.Handle == 0 || !target.Alive || target.Dying {
		return terrainShotUnknown
	}
	// The same discriminant presentation uses for a building [05 "Construction
	// target state"]: a definition with no velocity has no mover to move it.
	if target.Def.MaxVelocity != 0 {
		return terrainShotUnknown
	}
	var preview Projectile
	InitOrdinary(&preview, weapon, tick, muzzle, aim, target.Handle)
	env := GuidanceEnv{
		Unit: func(h pool.Handle) *units.Unit {
			if h == target.Handle {
				return target
			}
			return nil
		},
		Terrain: terrain,
	}
	seaLevel := numeric.FixedFromInt(int64(terrain.SeaLevel))
	for n := 0; n < modernTerrainSampleBudget; n++ {
		// Stop at expiry exactly as the generic self-propelled preview does:
		// past it the record's behaviour is no longer the launch's [06 §6.6].
		if tick >= preview.ExpiryTick {
			return terrainShotUnknown
		}
		if AdvanceSelfProp(&preview, weapon, tick, terrain.Gravity, seaLevel, env) != AdvanceAlive {
			return terrainShotUnknown
		}
		if !terrainPointValid(preview.Velocity) {
			return terrainShotUnknown
		}
		if result, done := sample.contact(preview.Pos); done {
			return result
		}
		if tick == ^uint32(0) {
			return terrainShotUnknown
		}
		tick++
	}
	return terrainShotUnknown
}

// guidedLaunchTerrainAdmission considers EVERY possible first-step steering
// result, so a unit that moves before projectile work cannot invalidate the
// proof. Yaw and pitch can each move at most TurnRate [06 §6.7]. Their
// Cartesian product is deliberately a superset of pursuit outcomes.
func guidedLaunchTerrainAdmission(preview Projectile, weapon *content.WeaponDef, tick uint32, terrain *world.Terrain, sample terrainAdmissionSample) terrainShotResult {
	if weapon.BurnBlow || weapon.TurnRate < 0 || weapon.TurnRate >= 32768 {
		return terrainShotUnknown
	}
	// Acceleration precedes guidance. Use the existing kernel to obtain this
	// tick's speed while suppressing only target-dependent steering on a copy.
	unguided := *weapon
	unguided.Guidance = false
	accelerated := preview
	if AdvanceSelfProp(&accelerated, &unguided, tick, terrain.Gravity, numeric.FixedFromInt(int64(terrain.SeaLevel)), GuidanceEnv{}) != AdvanceAlive {
		return terrainShotUnknown
	}
	turn := int32(weapon.TurnRate)
	yawEnd, pitchEnd := int32(preview.Yaw)+turn, int32(preview.Pitch)+turn
	count := 0
	for yaw := int32(preview.Yaw) - turn; yaw <= yawEnd; yaw += 128 - ((yaw + 32) & 127) {
		for pitch := int32(preview.Pitch) - turn; pitch <= pitchEnd; pitch += 128 - ((pitch + 32) & 127) {
			count++
			if count > modernTerrainSampleBudget {
				return terrainShotUnknown
			}
			// Shared trig is constant inside each 128-angle cell whose boundary
			// is shifted by 32 [06 §6.4]. Visiting its first included angle covers
			// the entire cell, including wrap at the end of the uint16 circle.
			v := VelocityFromAngles(numeric.Angle(yaw), numeric.Angle(pitch), accelerated.Speed)
			point := Vec3{X: preview.Pos.X.Add(v.X), Y: preview.Pos.Y.Add(v.Y), Z: preview.Pos.Z.Add(v.Z)}
			result, done := sample.contact(point)
			if !done || result != terrainShotBlocked {
				return terrainShotUnknown
			}
		}
	}
	return terrainShotBlocked
}
