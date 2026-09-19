package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Exercise the user's factory-then-tower example with authored weapon ranges,
// categories and sensor gates. Both player controller types share the policy.
func TestModernRetailTargetsFactoryThenInRangeTower(t *testing.T) {
	f := loadRetailFixture(t)
	for _, owner := range []uint8{0, 1} {
		t.Run(map[uint8]string{0: "human", 1: "computer"}[owner], func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(gameplay.Modern)
			stepRetail(s, 2)
			s.Vis.RefreshMode(0, true, [10]bool{true, true}, nil)
			for _, u := range s.Units.Iter() {
				if u != nil {
					u.Hidden = true // isolate this selection probe from starting commanders
				}
			}
			shooter := placeCompleteRetailUnit(t, s, "ARMLLT", owner, numeric.FixedFromInt(1200), numeric.FixedFromInt(1200))
			factory := placeCompleteRetailUnit(t, s, "CORLAB", 1-owner, numeric.FixedFromInt(1200), numeric.FixedFromInt(1320))
			tower := placeCompleteRetailUnit(t, s, "CORLLT", 1-owner, numeric.FixedFromInt(2200), numeric.FixedFromInt(1200))
			shooter.Flags = (shooter.Flags &^ (units.StandingFieldMask << units.StandingFireShift)) | (2 << units.StandingFireShift)
			slot := shooter.SlotAt(0)
			if !s.Combat.CanEngageSlotTarget(shooter, factory, 0, s.World) || s.Combat.CanEngageSlotTarget(shooter, tower, 0, s.World) {
				t.Fatal("fixture requires factory in range and tower outside range")
			}
			s.Combat.RebuildTargetRegistryIfDue(30, owner, s.Units, s.Vis, s.World, s.Econ)
			scan := func() {
				// A complete cursor rotation without advancing orders or firing.
				for i := 0; i < s.Units.UnitLimit(); i++ {
					s.Combat.StepAutonomousForPlayer(owner, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG())
				}
			}
			scan()
			if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != factory.Handle {
				t.Fatalf("opportunity target=%+v, want factory %d", slot.Target, factory.Handle)
			}
			// Fixture relocation changes only the target comparison's operands;
			// no mover or projectile simulation runs during this selection probe.
			tower.X = numeric.FixedFromInt(1360)
			tower.Y = s.World.HeightAt(tower.X, tower.Z)
			scan()
			if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != tower.Handle {
				t.Fatalf("retained target=%+v, want newly engageable tower %d", slot.Target, tower.Handle)
			}
			combat.ReleaseWeaponSlot(shooter, 0)
			combat.SetManualWeaponTarget(shooter, 0, factory.Handle)
			scan()
			if slot.Target.Unit != factory.Handle {
				t.Fatal("automatic priority displaced explicit factory target")
			}
		})
	}
}

// A stationary guard takes ownership of slot zero after its first acquisition.
// Keep running the real order/COB pipeline so this cannot pass merely because
// the isolated maintenance scan handles still-autonomous slots correctly.
func TestModernRetailGuardReconsidersOwnedWeapon(t *testing.T) {
	f := loadRetailFixture(t)
	for _, owner := range []uint8{0, 1} {
		t.Run(map[uint8]string{0: "human", 1: "computer"}[owner], func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(gameplay.Modern)
			stepRetail(s, 2)
			for _, ai := range s.AI {
				if ai != nil {
					for i := range ai.Deadlines {
						ai.Deadlines[i] = ^uint32(0)
					}
				}
			}
			s.Vis.RefreshMode(0, true, [10]bool{true, true}, nil)
			for _, u := range s.Units.Iter() {
				if u != nil {
					u.Hidden = true
				}
			}
			shooter := placeCompleteRetailUnit(t, s, "ARMLLT", owner, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			factory := placeCompleteRetailUnit(t, s, "CORLAB", 1-owner, numeric.FixedFromInt(600), numeric.FixedFromInt(750))
			tower := placeCompleteRetailUnit(t, s, "CORLLT", 1-owner, numeric.FixedFromInt(1600), numeric.FixedFromInt(600))
			tower.Flags &^= units.StandingFieldMask << units.StandingFireShift
			shooter.Flags = (shooter.Flags &^ (units.StandingFieldMask << units.StandingFireShift)) | (2 << units.StandingFireShift)
			slot := shooter.SlotAt(0)
			for i := 0; i < 120; i++ {
				s.Econ.Players[owner].Stock[economy.Energy] = 10000
				s.Step(s.Clock.ScaledAnchor + 1)
				if slot.Target.Unit == factory.Handle && !slot.IsAutonomous() {
					break
				}
			}
			if slot.Target.Unit != factory.Handle || slot.IsAutonomous() {
				t.Fatalf("guard never took factory target: %+v", slot)
			}
			tower.X = numeric.FixedFromInt(760)
			tower.Y = s.World.HeightAt(tower.X, tower.Z)
			for i := 0; i < 120 && slot.Target.Unit != tower.Handle; i++ {
				s.Econ.Players[owner].Stock[economy.Energy] = 10000
				s.Step(s.Clock.ScaledAnchor + 1)
			}
			if slot.Target.Unit != tower.Handle {
				t.Fatalf("guard retained factory after tower entered range: %+v", slot.Target)
			}
		})
	}
}
