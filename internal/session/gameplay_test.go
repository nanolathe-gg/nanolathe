package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// Modern policy is a host extension, not a retail contract.
func TestGameplayChangesAtCommandBoundary(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 10}, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.SetGameplay(gameplay.Modern)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanGameplay, Gameplay: gameplay.Strict31}); err != nil {
		t.Fatal(err)
	}
	if !s.Combat.ModernTerrainAdmission || !s.Combat.ModernHoldFire || !s.Build.ModernConstructionClearance || !s.Build.OrderBinding.ModernHoldFire || !s.Build.OrderBinding.ModernBomberPass {
		t.Fatal("presentation changed combat before command boundary")
	}
	s.applyHumanCommands(11)
	if s.Gameplay != gameplay.Strict31 || s.Combat.ModernTerrainAdmission || s.Combat.ModernHoldFire || s.Build.ModernConstructionClearance || s.Build.OrderBinding.ModernHoldFire || s.Build.OrderBinding.ModernBomberPass {
		t.Fatal("strict command did not apply")
	}
	s.SetGameplay("")
	if !s.Combat.ModernTerrainAdmission || !s.Combat.ModernHoldFire || !s.Build.ModernConstructionClearance || !s.Build.OrderBinding.ModernHoldFire || !s.Build.OrderBinding.ModernBomberPass {
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
		if s.Combat.ModernHoldFire != modern || s.Build.ModernConstructionClearance != modern || s.Build.OrderBinding.ModernHoldFire != modern || s.Build.OrderBinding.ModernBomberPass != modern {
			t.Fatalf("policy projection differs for %s", mode)
		}
		if b := s.newOrderBinding(); b.ModernHoldFire != modern || b.ModernBomberPass != modern {
			t.Fatalf("new queue retained wrong policy for %s", mode)
		}
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive {
				q, ok := u.Orders.(*orders.Queue)
				if !ok {
					t.Fatalf("unit %d has no queue", u.Handle)
				}
				if q.Binding().ModernHoldFire != modern || q.Binding().ModernBomberPass != modern {
					t.Fatalf("unit %d retained wrong policy for %s", u.Handle, mode)
				}
			}
		}
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
