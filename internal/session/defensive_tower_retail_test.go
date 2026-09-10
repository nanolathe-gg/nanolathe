package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A stationary guard must leave its wait when the observed target dies, then
// engage another enemy [04 R-ORD-01 §3][04 R-ORD-01 §6]. Previously the weapon
// forgot the dead target while Guard_NoMove kept ownership of an empty slot.
// Both original scripts fired once and then remained silent indefinitely.
func TestRetailDefensiveTowerReacquiresAfterTargetDeath(t *testing.T) {
	f := loadRetailFixture(t)
	for _, name := range []string{"ARMAMB", "ARMRL"} {
		for _, outsideSight := range []bool{false, true} {
			label := name + "/visible"
			if outsideSight {
				label = name + "/outside_sight"
			}
			t.Run(label, func(t *testing.T) {
				s := f.session(t)
				stepRetail(s, 2)
				tower := placeCompleteRetailUnit(t, s, name, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
				first := placeCompleteRetailUnit(t, s, "CORLAB", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(800))
				var second *units.Unit
				if !outsideSight {
					// Let the real projectile perform removal.
					first.Health = 1
				}
				before := int32(0)
				arrived, removedOutsideSight := false, false
				for i := 0; i < 1200 && (second == nil || second.Health == before); i++ {
					s.Econ.Players[0].Stock[economy.Energy] = 10000
					s.Step(s.Clock.ScaledAnchor + 1)
					if outsideSight && !removedOutsideSight {
						q := orders.QueueForUnit(tower)
						if q.Head() != nil && q.Head().Target == first.Handle && q.Head().Phase == 2 {
							// Isolate a target's removal after it has left the tower's vision.
							first.Z = numeric.FixedFromInt(2000)
							first.Y = s.World.HeightAt(first.X, first.Z)
							if s.Vis.VisiblePoint(0, first.X, first.Y, first.Z) {
								t.Fatal("remote target is still visible")
							}
							s.Units.DestroyBy(first.Handle, units.DeathKilled, 0)
							removedOutsideSight = true
						}
					}
					if !arrived && !first.Alive {
						arrived = true
						second = placeCompleteRetailUnit(t, s, "CORLAB", 1, numeric.FixedFromInt(800), numeric.FixedFromInt(800))
						before = second.Health
					}
				}
				if !arrived {
					t.Fatal("first target was not removed")
				}
				if second.Health >= before {
					slot := tower.SlotAt(0)
					t.Fatalf("%s never engaged the next enemy: health=%d target=%+v aim=%+v flags=%x", name, second.Health, slot.Target, slot.Aim, slot.Flags)
				}
			})
		}
	}
}

// STOP after ground fire returns the weapon to the default defensive order
// and permits automatic unit targeting again [04 R-ORD-01 §2].
func TestRetailDefensiveTowerStopAfterGroundFire(t *testing.T) {
	f := loadRetailFixture(t)
	for _, name := range []string{"ARMAMB", "ARMRL"} {
		t.Run(name, func(t *testing.T) {
			s := f.session(t)
			stepRetail(s, 2)
			tower := placeCompleteRetailUnit(t, s, name, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			enemy := placeCompleteRetailUnit(t, s, "CORLAB", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(800))
			step := func(n int) {
				for i := 0; i < n; i++ {
					s.Econ.Players[0].Stock[economy.Energy] = 10000
					s.Step(s.Clock.ScaledAnchor + 1)
				}
			}
			err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{tower.Handle}, Code: int(input.LatchAttack), Position: orders.ResolvePos{X: enemy.X, Y: enemy.Y, Z: enemy.Z}}})
			if err != nil {
				t.Fatal(err)
			}
			step(180)
			if tower.SlotAt(0).Target.Kind != units.TargetGround {
				t.Fatal("manual ground attack was not installed")
			}
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{tower.Handle}}}); err != nil {
				t.Fatal(err)
			}
			before := enemy.Health
			step(300)
			if tower.SlotAt(0).Target.Kind != units.TargetUnit || enemy.Health >= before {
				t.Fatalf("%s did not return to automatic unit targeting after Stop: target=%+v health=%d/%d", name, tower.SlotAt(0).Target, enemy.Health, before)
			}
		})
	}
}
