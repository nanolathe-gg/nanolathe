package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Each manager owns its cursor. An unvisited player does not advance when
// another player runs, and wrapped visits retain cursor order [06 §3.2].
type autonomousScanCursor struct{ next [combatPlayerSlots]int }

func autonomousScanBudget(limit int) int { return int(uint16(limit))/autonomousScanDivisor + 1 }

func (c *autonomousScanCursor) nextRecord(player uint8, limit int) int {
	record := c.next[player]
	if record >= limit {
		record = 0
	}
	c.next[player] = (record + 1) % limit
	return record
}

func autonomousScanAdmitsUnit(u *units.Unit) bool {
	return u != nil && u.Def != nil && u.Remaining == 0 &&
		u.Flags&units.ArmedStatus != 0 &&
		u.Flags>>units.StandingFireShift&units.StandingFieldMask == stanceFireAtWill
}

// StepAutonomousForPlayer runs the manager's maintenance pass after AI tasks
// and before strategic refresh, LOS publication and settlement [06 §3.2]
// [05 "Authoritative settlement order"]. Free records spend budget. Callbacks
// are deferred; this pass does not run the weapon pipeline or a COB drain.
func (s *Service) StepAutonomousForPlayer(player uint8, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation) {
	if s == nil || w == nil || player >= combatPlayerSlots {
		return
	}
	start, end, ok := w.SliceForPlayer(int(player))
	if !ok || end < start {
		return
	}
	limit := end - start + 1
	for visit := 0; visit < autonomousScanBudget(w.UnitLimit()); visit++ {
		record := s.scanCursor.nextRecord(player, limit)
		u := w.Unit(pool.Handle(start + record))
		if !autonomousScanAdmitsUnit(u) {
			continue
		}
		for idx := 0; idx < NumSlots; idx++ {
			slot := u.SlotAt(idx)
			if !slot.IsEnabled() || !slot.IsAutonomous() || !s.rules().AutonomousSlot(slot.Weapon, s.PlayerControlByteFor(player)) {
				continue
			}
			if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
				// TODO(question): retention reads freed raw target fields in retail.
				// Until their full free-writer set is mapped, the compact freed
				// record resolves absent here [06 "Missing and unknown"].
				target := w.Unit(slot.Target.Unit)
				if !s.rules().ReconsiderTarget(u, slot, target, vis) && target != nil && target.Def != nil &&
					!registryOwnerDeclaresAllianceWithCandidate(player, target.Owner, econ) &&
					IsPreferredCategoryMask(target.Def.DefinitionMask(), badMaskForSlot(u.Def, idx)) &&
					!(slot.Weapon.Paralyzer && target.Stunned) {
					continue
				}
			}
			// Failed retention immediately acquires again; it does not clear a
			// successful replacement's Aim latch or spend a separate visit [06 §3.2].
			if slot.Weapon.Interceptor {
				if _, position, found := interceptorScanCandidate(s, u, slot, catalog); found {
					// Acquisition uses the common point setter: the current projectile
					// position narrows to whole-world words before aiming or saving
					// [06 §11.2][04 R-ORD-01 §1][08 R-SAVE-WEAPON-01].
					FireWeaponPoint(u, idx, position.X, position.Z, 0)
					continue
				}
			} else if u.Flags>>units.StandingFireShift&units.StandingFieldMask == stanceFireAtWill {
				if target, found := s.acquireTargetForSlot(u, slot, idx, w, vis, terrain, simRNG, econ, catalog); found {
					slot.Target = units.Target{Kind: units.TargetUnit, Unit: target}
					u.Pending &^= units.PendingSlotSetterClear
					continue
				}
			}
			// The ordinary target-word clear posts TargetCleared only for a
			// nonempty target pair; it leaves control and Aim words alone
			// [06 R-WPN-04 §1][06 §3.2].
			if slot.Target.Kind != units.TargetNone {
				slot.Target = units.Target{Kind: units.TargetNone}
				if bridge := s.callbackBridgeForUnit(u); bridge != nil {
					bridge.TargetCleared(int32(idx))
				}
			}
		}
	}
}
