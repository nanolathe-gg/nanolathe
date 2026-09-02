package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// ReleaseWeaponSlot clears the slot control byte's autonomy bit and the slot's
// target [04 R-ORD-01 §1][04 R-ORD-01 §7]. The byte is one byte, not two
// [06 R-WPN-05 §3]. Projectile creation remains in StepWeaponsForUnit's normal
// unit phase [06 §3.3].
func ReleaseWeaponSlot(u *units.Unit, idx int) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	s.Flags &^= units.SlotFlagAutonomous
	s.Target = units.Target{Kind: units.TargetNone}
	return true
}

// InhibitWeaponSlot sets the slot control byte's autonomy bit and drops the
// target [04 R-ORD-01 §1][04 R-ORD-01 §7]. Release and inhibit write the same
// bit in opposite directions, and it is the bit the autonomous scan requires
// [06 R-WPN-05 §3].
func InhibitWeaponSlot(u *units.Unit, idx int) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	s.Flags |= units.SlotFlagAutonomous
	s.Target = units.Target{Kind: units.TargetNone}
	return true
}

// SetManualWeaponTarget installs a target on one slot, when target is nonzero,
// while preserving the asynchronous Aim latch. A zero target is the order-side
// manual-mode latch without a target.
//
// It does NOT touch the control byte. Bit 4's only writers are the slot
// initializer and the two order verbs, and bit 1's only writer is the
// initializer [06 R-WPN-05 §3]; this used to clear 0x10 and set 0x02, which
// was inert only for as long as the order side kept its own copy of bit 4.
func SetManualWeaponTarget(u *units.Unit, idx int, target pool.Handle) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	if s.Weapon == nil {
		return true // the three-slot latch walk includes inactive slots
	}
	if target != 0 {
		s.Target = units.Target{Kind: units.TargetUnit, Unit: target}
	}
	return true
}

// FireWeaponTarget arms one slot against a unit. The normal combat phase later
// performs range, medium, aim, cost, and projectile admission [06 §3.3][06 §4].
func FireWeaponTarget(u *units.Unit, idx int, target pool.Handle, _ uint32) bool {
	if !SetManualWeaponTarget(u, idx, target) || target == 0 {
		return false
	}
	return true
}

// FireWeaponPoint arms one slot against a world point. Point targets are stored
// at whole-world precision by the slot representation [06 §1.2].
func FireWeaponPoint(u *units.Unit, idx int, x, z numeric.Fixed, _ uint32) bool {
	s := orderSlot(u, idx)
	if s == nil || s.Weapon == nil {
		return false
	}
	wx := int32(x.Raw() >> 16)
	wz := int32(z.Raw() >> 16)
	if wz == -32768 {
		wz = -32767
	}
	s.Target = units.Target{Kind: units.TargetGround, X: numeric.Fixed(int64(wx) << 16), Z: numeric.Fixed(int64(wz) << 16)}
	return true
}

// StopWeaponFiring is the slot-level stop operation used by air attack
// break-off legs. It deliberately leaves no presentation-only shot behind.
func StopWeaponFiring(u *units.Unit, idx int) bool { return InhibitWeaponSlot(u, idx) }

func orderSlot(u *units.Unit, idx int) *units.Slot {
	if u == nil || idx < 0 || idx >= units.NumSlots {
		return nil
	}
	return u.SlotAt(idx)
}

// AcquireWeaponTarget exposes the established combat acquisition path to an
// order executor. It uses the same deterministic candidate scan and physical
// gates as the ordinary weapon step; no order-specific target predicate is
// introduced [06 §3.1][06 §3.2].
func (s *Service) AcquireWeaponTarget(u *units.Unit, idx int, rangeLimit uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, sim *rng.Simulation) (pool.Handle, bool) {
	if s == nil || u == nil || idx < 0 || idx >= units.NumSlots || u.SlotAt(idx) == nil || u.SlotAt(idx).Weapon == nil {
		return 0, false
	}
	limit := int32(-1)
	if rangeLimit != 0 {
		limit = int32(rangeLimit)
	}
	return s.acquireTargetForSlotRange(u, u.SlotAt(idx), idx, w, vis, terrain, sim, econ, limit, catalog)
}

// WeaponCanEngage answers the hover attack's engagement query using the same
// planar range relation used by shot admission. The detailed aim/projectile
// gates remain in StepWeaponsForUnit [04 R-AIR-01 §8][06 §3.3].
func WeaponCanEngage(u *units.Unit, idx int, target *units.Unit) bool {
	s := orderSlot(u, idx)
	if s == nil || s.Weapon == nil || u == nil || target == nil || !target.Alive {
		return false
	}
	return WithinRange(u.X, u.Z, target.X, target.Z, s.Weapon.Range)
}

// CanEngageSlotTarget is the shot-admission gate the attack-order handlers ask
// before they bind a weapon slot to a target [04 R-ORD-01 §7]. It answers "may
// slot idx be pointed at this target right now", and it is the same predicate
// `Attack_Chase` phases 1 and 3 and `Guard_NoMove` phase 2 branch on
// [04 R-ORD-01 §3].
//
// The gate, in retail's order:
//
//   - A **water weapon** requires the target to be in the water. Unless the
//     target's definition is a `floater`, its whole-unit Y must not be above
//     the map's sea level; and when the target is `canhover`, its whole-unit Y
//     plus half its model top height must not be above sea level either. A
//     hovercraft rides high enough that half its height clears the surface, so
//     the two tests together are "the hull is under water".
//   - A **non-water** weapon requires BOTH ends to be out of the water: the
//     shooter's whole-unit Y plus its model top height, and the target's,
//     strictly greater than sea level. The shooter half is the same predicate
//     the shot-time gate applies [06 §3.3]; the target half is this gate's own.
//   - A `toairweapon` additionally requires the target's committed mover mode
//     to read **2** (airborne) [04 R-MOV-01 §8].
//   - A `ballistic` weapon additionally requires a ballistic solution that is
//     not the no-solution sentinel.
//   - Finally the ordinary planar range test of [06 §3.3] against the slot
//     weapon's authored `range`.
//
// The gate performs no terrain, visibility or sensor test, and it does not
// consult reload, ammunition or cost — those belong to the slot pipeline.
func (s *Service) CanEngageSlotTarget(u *units.Unit, target *units.Unit, idx int, terrain *world.Terrain) bool {
	slot := orderSlot(u, idx)
	if slot == nil || slot.Weapon == nil || u == nil || u.Def == nil || target == nil || !target.Alive || target.Def == nil {
		return false
	}
	w := slot.Weapon
	sea := int32(0)
	if terrain != nil {
		sea = int32(terrain.SeaLevel)
	}
	if !unitToUnitAdmitsBeforeRange(gateEndForUnit(u), gateEndForUnit(target), sea, w.WaterWeapon, w.ToAirWeapon) {
		return false
	}
	if !w.WaterWeapon && w.Ballistic && !hasBallisticSolution(u, target, w, terrain) {
		return false
	}
	return WithinRange(u.X, u.Z, target.X, target.Z, w.Range)
}

// unitGateEnd carries one end's operands for the unit-to-unit acquisition and
// order-installation gate [06 §3.1][06 R-WPN-05 §1]. Both ends contribute a
// whole-unit Y word and a model top-height word; only the target end's medium
// flags and committed mover mode are read.
type unitGateEnd struct {
	// Y is the whole-unit height word `(int16)(Y >> 16)` both height clauses
	// compare, sign-truncated exactly as retail's 16.16 position word is
	// [06 §3.1][06 R-WPN-05 §9].
	Y int32
	// ModelTop is the definition's model total-height whole-unit word — the
	// word §3.1 calls `referenceHeight` and §1 `ModelTop` [03 R-P0-18-A §1].
	ModelTop int32
	// MoverMode is the committed mover mode, the operand of the `toairweapon`
	// clause [04 R-MOV-01 §8][06 R-WPN-05 §1] clause 3.
	MoverMode uint8
	// Floater and CanHover are the unit definition's capability-word A bits 19
	// and 12, the water branch's pair [04 R-SPEC-01 §0][06 §3.1].
	Floater, CanHover bool
}

// gateEndForUnit reads one end's operands off a live unit. A unit with no
// resolved definition contributes a zero model top and no medium flags, which
// is a fixture case: retail units always have a definition.
func gateEndForUnit(u *units.Unit) unitGateEnd {
	if u == nil {
		return unitGateEnd{}
	}
	end := unitGateEnd{Y: wholeY(u), MoverMode: u.Move.Mode}
	if u.Def != nil {
		end.ModelTop = u.Def.ModelTop
		end.Floater = u.Def.Floater
		end.CanHover = u.Def.CanHover
	}
	return end
}

// unitToUnitAdmitsBeforeRange is the ONE body of the unit-to-unit gate's
// clauses that precede the ballistic solve — the gate [06 R-WPN-05 §9] names as
// a single routine whose callers are the autonomous acquisition's per-candidate
// test, the attack/guard order handlers and the cursor-shape chooser. Its
// clauses, in [06 §3.1] / [06 R-WPN-05 §1] order:
//
//   - a **water weapon** tests the target alone: unless the target's definition
//     carries `floater`, its whole-unit Y must not exceed the sea-level byte;
//     and when it carries `canhover`, its whole-unit Y plus HALF its model top
//     word must not exceed it either. Both compares are signed and reject on
//     strictly greater. The branch then goes straight to range — no shooter
//     test, no air test, no ballistic test;
//   - a **non-water weapon** requires both ends out of the water:
//     `(int16)Y + ModelTop > sea` on the shooter and on the target, each
//     strictly greater;
//   - a `toairweapon` additionally requires the target's committed mover mode
//     to read exactly 2 [06 R-WPN-05 §1] clause 3.
//
// Range is NOT here: it is the gate's LAST clause and the callers apply it
// after their own ballistic step, which is the order both sections give.
func unitToUnitAdmitsBeforeRange(shooter, target unitGateEnd, sea int32, waterWeapon, toAir bool) bool {
	if waterWeapon {
		if !target.Floater && target.Y > sea {
			return false
		}
		// `referenceHeight >> 1` — an arithmetic shift, not a truncating
		// divide; the two agree for the non-negative model tops the model walk
		// produces and the shift is what [06 §3.1] writes.
		if target.CanHover && target.Y+(target.ModelTop>>1) > sea {
			return false
		}
		return true
	}
	if shooter.Y+shooter.ModelTop <= sea {
		return false
	}
	if target.Y+target.ModelTop <= sea {
		return false
	}
	if toAir && target.MoverMode != airborneMoverMode {
		return false
	}
	return true
}

// ShotTimeAdmitsPoint is the shot-time physical gate of [06 §3.3] asked against
// a world POINT instead of a unit: the planar range test, and for a non-water
// weapon the shooter-side sea-level clause and, when the weapon is `ballistic`,
// a trajectory solution that is not the no-solution sentinel. It is the shooter
// half alone — the target-side clauses belong to the acquisition gate of
// [06 §3.1] (CanEngageSlotTarget) and have no point form. It performs no
// terrain, hill, visibility or sensor test, and consults neither reload nor
// ammunition nor cost.
//
// It is exported for the same reason SlotAcquisitionAdmits is: a caller outside
// this package needs the gate but does not carry its operands. The computer
// player's rally task is that caller — a member with no mover is admitted only
// when its first weapon slot can reach the rally point [08 R-AI-01 §19] — and
// session composition binds it there rather than letting the planner carry a
// second copy of the gate. It draws no RNG.
func (s *Service) ShotTimeAdmitsPoint(u *units.Unit, idx int, x, y, z numeric.Fixed, terrain *world.Terrain) bool {
	slot := orderSlot(u, idx)
	if slot == nil || slot.Weapon == nil || u == nil || u.Def == nil {
		return false
	}
	return shotTimeAdmits(u, slot.Weapon, x, y, z, terrain)
}

// shotTimeAdmits is the one body of the shot-time gate, shared by the point
// form above and by the slot pipeline's own admission site. [06 R-WPN-05 §9]
// states the gate's clauses exhaustively and in evaluation order, and states
// that it has NO target-side clause of any kind — no target height or model
// top, no `floater`/`canhover` medium test, no `toairweapon` mover-mode test,
// no alliance, no category. The target contributes only its point:
//
//  1. range — inclusive, signed 32-bit, on the raw 16.16 deltas shooter →
//     target point, for water and non-water weapons alike, evaluated FIRST;
//  2. non-water only — the shooter's whole-unit Y word plus its definition's
//     model top-height word, strictly greater than the map's sea-level byte;
//  3. non-water and `ballistic` only — refuse on the solver's no-solution
//     sentinel, given the shooter-minus-target deltas on all three axes.
//
// A water weapon runs clause 1 and admits. The gate performs no terrain, hill,
// visibility or sensor test and consults neither reload nor ammunition nor
// cost; the target-side clauses live in the acquisition/order-installation
// gate alone (CanEngageSlotTarget, IsValidAcquisitionCandidate), which orders
// its own range test LAST [06 §3.1][06 R-WPN-05 §1].
func shotTimeAdmits(u *units.Unit, weapon *content.WeaponDef, x, y, z numeric.Fixed, terrain *world.Terrain) bool {
	if u == nil || weapon == nil {
		return false
	}
	if !WithinRange(u.X, u.Z, x, z, weapon.Range) {
		return false
	}
	if weapon.WaterWeapon {
		return true
	}
	sea := int32(0)
	if terrain != nil {
		sea = int32(terrain.SeaLevel)
	}
	if wholeY(u)+modelTop(u) <= sea {
		return false
	}
	if weapon.Ballistic && !hasBallisticSolutionToPoint(u, x, y, z, weapon, terrain) {
		return false
	}
	return true
}

// modelTop is the shooter's definition model total-height whole-unit word, the
// addend clause 2 carries [03 R-P0-18-A §1]. A unit with no resolved definition
// contributes nothing, which is a fixture case: retail units always have one.
func modelTop(u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 0
	}
	return u.Def.ModelTop
}

// airborneMoverMode is the committed mover-mode value the `toairweapon` gate
// requires of its target [04 R-MOV-01 §8]: 2, airborne. It closes the operand
// [02 R-KEYS-01 §2] recorded as the one inference in `toairweapon`'s otherwise
// established reader census.
const airborneMoverMode uint8 = 2

// wholeY is the unit's world Y truncated to the whole-unit word the sea-level
// comparisons use — retail's `(int16)(Y >> 16)`, the signed high half of the
// 16.16 position word [06 §3.1][06 R-WPN-05 §9]. Our Fixed is wider than
// retail's 32 bits, so the int16 step is written out rather than implied by the
// storage; it changes nothing inside a map's height range and keeps the compare
// exact at the extremes.
func wholeY(u *units.Unit) int32 { return int32(int16(u.Y.Raw() >> 16)) }

// hasBallisticSolution reports whether the ballistic solver returns anything
// other than its no-solution sentinel for this shooter/target pair; the gate
// only asks whether a solution exists [04 R-ORD-01 §7].
//
// The gate hands the solver the source-to-target delta, the weapon's
// `weaponvelocity` and its `minbarrelangle`, and takes gravity from the world
// rather than from the weapon record — which is the same triple the slot
// pipeline's own aim step supplies to BallisticSolve [06 §3.3][06 §6.4]. A
// zero velocity has no solution, and the solver's own sentinel covers the rest.
func hasBallisticSolution(u *units.Unit, target *units.Unit, w *content.WeaponDef, terrain *world.Terrain) bool {
	return hasBallisticSolutionToPoint(u, target.X, target.Y, target.Z, w, terrain)
}

// hasBallisticSolutionToPoint is the same query against a world point, which is
// the form the point gate above needs. The unit form delegates to it so the
// solver's operands are written down once.
func hasBallisticSolutionToPoint(u *units.Unit, x, y, z numeric.Fixed, w *content.WeaponDef, terrain *world.Terrain) bool {
	vel := numeric.Fixed(int64(w.WeaponVelocity))
	if vel.Raw() == 0 {
		return false
	}
	var grav numeric.Fixed
	if terrain != nil {
		grav = terrain.Gravity
	}
	dx := x.Sub(u.X)
	dy := y.Sub(u.Y)
	dz := z.Sub(u.Z)
	_, ok := BallisticSolve(dx, dy, dz, vel, grav, w.MinBarrelAngle)
	return ok
}
