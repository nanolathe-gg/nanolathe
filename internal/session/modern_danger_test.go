package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func modernDangerSession(t *testing.T, mode gameplay.Mode) (*Session, *units.Unit, *units.Unit) {
	t.Helper()
	s := strictNewSessionWithUnits(t, 0, 73, 23)
	s.SetGameplay(mode)
	s.Vis.SetMode(0)
	def := *s.Catalog.Units["armcom"]
	def.StandingMoveOrder = 2
	def.StandingFireOrder = 2
	def.DefaultMissionType = "Standby"
	def.MaxVelocity = 2 << 16
	def.Acceleration = 1 << 14
	def.BrakeRate = 1 << 14
	var made [2]*units.Unit
	for i := range made {
		x, z := numeric.FixedFromInt(int64(200+64*i)), numeric.FixedFromInt(200)
		h, err := s.Units.Create(&def, uint8(i), x, s.World.HeightAt(x, z), z)
		if err != nil {
			t.Fatal(err)
		}
		s.CompleteUnit(h)
		made[i] = s.Units.Unit(h)
		s.bindOrderQueue(made[i])
		q := orders.QueueOfUnit(made[i])
		q.Push(orders.Lookup("Standby"), orders.Node{Owner: h, Flags: orders.FlagAutoOp})
	}
	if !s.dangerVisible(made[0], made[1]) {
		t.Fatal("fixture enemy must be visible")
	}
	return s, made[0], made[1]
}

// A damage notice must reach the pre-pump response even when the victim has no
// weapons. Strict's armed-victim gate must not accidentally suppress Modern
// self-preservation, and no new policy may run on the Strict path.
func TestModernDangerDamageReachesUnarmedMover(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s, victim, attacker := modernDangerSession(t, mode)
			s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{Victim: victim.Handle, Attacker: attacker.Handle, Nominal: 1, Kind: combat.KindOrdinary})
			responded := false
			for tick := uint32(2); tick <= 32; tick++ {
				s.phaseUnits(tick)
				if head := orders.QueueOfUnit(victim).Head(); head != nil && orders.DescriptorFor(head.ID).Name == "Move_Ground" {
					responded = true
					break
				}
			}
			if responded != (mode == gameplay.Modern) {
				t.Fatalf("unarmed response=%v in %s", responded, mode)
			}
		})
	}
}

func TestModernHiddenImpactWithdrawsWithoutTargeting(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s, victim, attacker := modernDangerSession(t, mode)
			attacker.Hidden = true
			if s.dangerVisible(victim, attacker) {
				t.Fatal("hidden attacker leaked through visibility")
			}
			head := orders.QueueOfUnit(victim).Head()
			sim, crt := *s.SimRNG(), *s.CrtRNG()
			stock := s.Econ.Players[victim.Owner].Stock
			// Incoming fire travels west. Escape should be west, independent of the
			// hidden attacker's current position and the victim's hull heading.
			victim.Move.Heading = 16384
			s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{Victim: victim.Handle, Attacker: attacker.Handle, Nominal: 1, Kind: combat.KindOrdinary, ImpactVelocityX: numeric.FixedFromInt(-20)})
			for tick := uint32(2); tick <= 32; tick++ {
				orders.StepDangerResponse(victim, tick)
			}
			now := orders.QueueOfUnit(victim).Head()
			if mode == gameplay.Modern {
				if now == head || now.ID != orders.Lookup("Move_Ground") || now.Target != 0 || now.GoalX >= victim.X {
					t.Fatalf("missing westward anonymous withdrawal: %+v", now)
				}
			} else if now != head {
				t.Fatal("Strict inserted Modern reaction")
			}
			if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[victim.Owner].Stock != stock {
				t.Fatal("impact policy consumed RNG or resources")
			}
			for i := 0; i < units.NumSlots; i++ {
				if victim.SlotAt(i).Target.Kind != units.TargetNone {
					t.Fatal("hidden impact acquired a weapon target")
				}
			}
		})
	}
}

func TestModernDangerCompositionPreservesManualRepair(t *testing.T) {
	s, victim, attacker := modernDangerSession(t, gameplay.Modern)
	q := orders.QueueOfUnit(victim)
	q.Push(orders.Lookup("RepairUnit"), orders.Node{Owner: victim.Handle, Target: victim.Handle})
	head := q.Head()
	s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{Victim: victim.Handle, Attacker: attacker.Handle, Nominal: 1, Kind: combat.KindOrdinary})
	orders.StepDangerResponse(victim, 2)
	orders.PurgeOrdersOnDamage(victim)
	if q.Head() != head || head.ID != orders.Lookup("RepairUnit") {
		t.Fatal("composed danger or damage purge interrupted manual repair")
	}
}
