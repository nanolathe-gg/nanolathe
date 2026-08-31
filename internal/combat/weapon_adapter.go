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

// orderControlInhibit is the established control-byte bit written by the
// order release/inhibit helpers. It is deliberately separate from the slot's
// Flags tracking bit; the source control byte's remaining fields are not yet
// represented [04 R-ORD-01 §1][R-ORDER-02 §2].
const orderControlInhibit = units.OrderControlInhibit

// ReleaseWeaponSlot clears the slot's dedicated control-byte latch and target.
// The semantic name of the underlying control byte remains an open research
// item. Projectile creation remains in StepWeaponsForUnit's normal unit phase
// [04 R-ORD-01 §1][R-ORDER-02 §2][06 §3.3].
func ReleaseWeaponSlot(u *units.Unit, idx int) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	s.OrderControl &^= orderControlInhibit
	s.Target = units.Target{Kind: units.TargetNone}
	return true
}

// InhibitWeaponSlot sets the dedicated control-byte latch and drops the
// target. Release and inhibit therefore remain distinct writes even though the
// unit model does not yet expose the source control byte by name
// [04 R-ORD-01 §1][R-ORDER-02 §2]. TODO(question): trace the omitted control
// byte and settle its exact assignment/notification stores.
func InhibitWeaponSlot(u *units.Unit, idx int) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	s.OrderControl |= orderControlInhibit
	s.Target = units.Target{Kind: units.TargetNone}
	return true
}

// SetManualWeaponTarget disables autonomous tracking and, when target is
// nonzero, installs the target while preserving the asynchronous Aim latch.
// A zero target is the order-side manual-mode latch without a target.
func SetManualWeaponTarget(u *units.Unit, idx int, target pool.Handle) bool {
	s := orderSlot(u, idx)
	if s == nil {
		return false
	}
	s.Flags &^= 0x10 // manual target disables automatic tracking [06 §1.2]
	if s.Weapon == nil {
		return true // the three-slot latch walk includes inactive slots
	}
	if target != 0 {
		s.Target = units.Target{Kind: units.TargetUnit, Unit: target}
		s.Flags |= 0x02
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
	s.Flags &^= 0x10
	wx := int32(x.Raw() >> 16)
	wz := int32(z.Raw() >> 16)
	if wz == -32768 {
		wz = -32767
	}
	s.Target = units.Target{Kind: units.TargetGround, X: numeric.Fixed(int64(wx) << 16), Z: numeric.Fixed(int64(wz) << 16)}
	s.Flags |= 0x02
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
	return acquireTargetForSlotRange(u, u.SlotAt(idx), idx, w, vis, terrain, sim, econ, limit, catalog)
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
