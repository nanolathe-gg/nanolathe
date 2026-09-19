package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Modern policy is a host extension, not a retail contract.
func TestGameplayChangesAtCommandBoundary(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 10}, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.SetGameplay(gameplay.Modern)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanGameplay, Gameplay: gameplay.Strict31}); err != nil {
		t.Fatal(err)
	}
	if !s.Combat.ModernTerrainAdmission || !s.Combat.ModernHoldFire || !s.Build.ModernConstructionClearance || !s.Build.OrderBinding.ModernHoldFire || !s.Build.OrderBinding.ModernBomberPass || !s.Build.OrderBinding.ModernGuardAssistance {
		t.Fatal("presentation changed combat before command boundary")
	}
	s.applyHumanCommands(11)
	if s.Gameplay != gameplay.Strict31 || s.Combat.ModernTerrainAdmission || s.Combat.ModernHoldFire || s.Build.ModernConstructionClearance || s.Build.OrderBinding.ModernHoldFire || s.Build.OrderBinding.ModernBomberPass || s.Build.OrderBinding.ModernGuardAssistance {
		t.Fatal("strict command did not apply")
	}
	s.SetGameplay("")
	if !s.Combat.ModernTerrainAdmission || !s.Combat.ModernHoldFire || !s.Build.ModernConstructionClearance || !s.Build.OrderBinding.ModernHoldFire || !s.Build.OrderBinding.ModernBomberPass || !s.Build.OrderBinding.ModernGuardAssistance {
		t.Fatal("default must be modern")
	}
}

// The admission preview and the actual projectile step must share the live
// phase-8 wind field, including subsequent redraws, rather than a launch-time
// composition copy (Modern policy, DESIGN_WEAPONS_PROJECTILES §2.3.1).
func TestModernAdmissionSharesSessionWind(t *testing.T) {
	s := strictNewSessionWithUnits(t, 1, 73, 23)
	if s.Combat == nil || s.Wind == nil || s.Combat.ProjectileWind != s.Wind {
		t.Fatal("terrain admission is not bound to projectile wind")
	}
	s.phaseWind(1)
	if s.Combat.ProjectileWind.LastChange != 1 {
		t.Fatal("terrain admission missed the phase-8 wind redraw")
	}
	s.SetGameplay(gameplay.Strict31)
	if s.Combat.ModernTerrainAdmission {
		t.Fatal("strict mode left modern admission active")
	}
}

// Existing and newly composed queues must observe the same central policy.
func TestModernOrderPolicyComposition(t *testing.T) {
	s := strictNewSessionWithUnits(t, 1, 73, 23)
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive {
			s.bindOrderQueue(u)
		}
	}
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31, gameplay.Modern} {
		s.SetGameplay(mode)
		modern := mode == gameplay.Modern
		if s.Combat.ModernHoldFire != modern || s.Build.ModernConstructionClearance != modern || s.Build.OrderBinding.ModernHoldFire != modern || s.Build.OrderBinding.ModernBomberPass != modern || s.Build.OrderBinding.ModernGuardAssistance != modern {
			t.Fatalf("policy projection differs for %s", mode)
		}
		if b := s.newOrderBinding(); b.ModernHoldFire != modern || b.ModernBomberPass != modern || b.ModernGuardAssistance != modern {
			t.Fatalf("new queue retained wrong policy for %s", mode)
		}
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive {
				q, ok := u.Orders.(*orders.Queue)
				if !ok {
					t.Fatalf("unit %d has no queue", u.Handle)
				}
				if q.Binding().ModernHoldFire != modern || q.Binding().ModernBomberPass != modern || q.Binding().ModernGuardAssistance != modern {
					t.Fatalf("unit %d retained wrong policy for %s", u.Handle, mode)
				}
			}
		}
	}
}

// Modern guard scans must not start resurrection work on ordinary reclaimable
// scenery, or mutate the corpse, economy or random streams while choosing work.
func TestModernGuardResurrectionEligibility(t *testing.T) {
	s := wreckOrientationSession(t)
	corpse := s.Features.PlaceAt(12, 12, s.Catalog.Features["wreckvictim_dead"])
	if corpse == nil {
		t.Fatal("place corpse")
	}
	b := s.newOrderBinding()
	view, ok := b.World.LookupFeature(12, 12)
	if !ok || b.Work.CanResurrectFeature == nil {
		t.Fatal("missing resurrection scan binding")
	}
	sim, crt := *s.SimRNG(), *s.CrtRNG()
	stock := s.Econ.Players[0].Stock
	for _, tc := range []struct {
		name string
		view orders.FeatureView
		want bool
	}{
		{"corpse", view, true},
		{"scenery", orders.FeatureView{DefinitionKey: "tree", Reclaimable: true}, false},
		{"unknown corpse", orders.FeatureView{DefinitionKey: "missing_dead", Reclaimable: true}, false},
		{"unreclaimable corpse", orders.FeatureView{DefinitionKey: view.DefinitionKey}, false},
	} {
		if got := b.Work.CanResurrectFeature(tc.view); got != tc.want {
			t.Errorf("%s eligibility = %v, want %v", tc.name, got, tc.want)
		}
	}
	if s.Features.InstanceAt(12, 12) != corpse || s.Econ.Players[0].Stock != stock || *s.SimRNG() != sim || *s.CrtRNG() != crt {
		t.Fatal("resurrection scan mutated corpse, stock or random streams")
	}
}

// Exercise the new scan through real work adapters: one nearby damaged ally,
// then a corpse, then the revived unit's ordinary repair, and finally Guard.
func TestModernGuardCompletesNearbyWorkAndResumes(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s := wreckOrientationSession(t)
			s.SetGameplay(mode)
			builderDef := s.Catalog.Units["wreckcon"]
			builderDef.CanReclamate = true
			builderDef.BuildDistance = 512
			patientDef := s.Catalog.Units["wreckvictim"]
			patientDef.BuildCostEnergy = 100
			create := func(defName string, x int32) *units.Unit {
				h, err := s.Units.Create(s.Catalog.Units[defName], 0, world.CellToWorld(x), 0, world.CellToWorld(12))
				if err != nil {
					t.Fatal(err)
				}
				u := s.Units.Unit(h)
				s.Movement.EnsureUnit(u)
				s.bindOrderQueue(u)
				return u
			}
			builder := create("wreckcon", 11)
			ward := create("wreckcon", 10)
			patient := create("wreckvictim", 13)
			patient.Health = 50
			corpse := s.Features.PlaceAt(12, 12, s.Catalog.Features["wreckvictim_dead"])
			if corpse == nil {
				t.Fatal("place corpse")
			}
			q := orders.QueueForUnit(builder)
			q.CancelAll()
			q.Push(orders.Lookup("Follow_Ground"), orders.Node{Owner: builder.Handle, Target: ward.Handle, Phase: 1})
			guard := q.Primary()[0]
			buckets := s.Econ.UnitBuckets(builder.Handle)
			sim, crt := *s.SimRNG(), *s.CrtRNG()
			for tick := uint32(1); tick <= 200; tick++ {
				builder.InBuildStance = true
				q.Pump(builder, tick)
				if len(q.Primary()) == 0 {
					t.Fatalf("queue emptied at tick %d; node=%+v diagnostics=%v", tick, guard, q.Diagnostics())
				}
			}
			if len(q.Primary()) != 1 || q.Primary()[0] != guard || guard.Target != ward.Handle {
				for _, n := range q.Primary() {
					t.Logf("order=%s phase=%d gate=%x deadline=%d target=%d", orders.DescriptorFor(n.ID).Name, n.Phase, n.DynamicGate, n.Deadline, n.Target)
				}
				t.Fatalf("work did not resume original guard: patient health=%d energy=%+v", patient.Health, buckets[economy.Energy])
			}
			if mode == gameplay.Strict31 {
				if patient.Health != 50 || s.Features.InstanceAt(12, 12) != corpse || buckets[economy.Energy].Accepted != 0 || *s.SimRNG() != sim || *s.CrtRNG() != crt {
					t.Fatal("Strict guard performed nearby work")
				}
				return
			}
			if patient.Health != patient.MaxHealth || s.Features.InstanceAt(12, 12) != nil {
				t.Fatalf("nearby work incomplete: patient health=%d corpse=%v", patient.Health, s.Features.InstanceAt(12, 12))
			}
			var revived *units.Unit
			for _, u := range s.Units.Iter() {
				if u != nil && u.Alive && u != patient && u.Def == patientDef {
					revived = u
				}
			}
			if revived == nil || revived.Health != revived.MaxHealth || buckets[economy.Energy].Accepted <= 0 {
				t.Fatalf("revived unit did not finish ordinary paid repair: unit=%v energy=%v", revived, buckets[economy.Energy].Accepted)
			}
		})
	}
}

// Modern makes Hold Fire an unconditional launch gate. Stock armed units that
// start held must expose the command that lets the player release that gate.
func TestModernStockHoldFireCanBeReleased(t *testing.T) {
	catalog, _ := retailcat.Shared(t)
	for _, key := range catalog.SortedUnitKeys() {
		def, _ := catalog.Unit(key)
		if def.StandingFireOrder != 0 {
			continue
		}
		if (def.Weapon1Def != nil || def.Weapon2Def != nil || def.Weapon3Def != nil) && !def.FireStandOrders {
			t.Errorf("%s starts armed on Hold Fire without a fire-stance control", key)
		}
	}
}
