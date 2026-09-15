package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// Modern policy is a host extension, not a retail contract.
func TestGameplayChangesAtCommandBoundary(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 10}, Combat: &combat.Service{}}
	s.SetGameplay(gameplay.Modern)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanGameplay, Gameplay: gameplay.Strict31}); err != nil {
		t.Fatal(err)
	}
	if !s.Combat.ModernTerrainAdmission {
		t.Fatal("presentation changed combat before command boundary")
	}
	s.applyHumanCommands(11)
	if s.Gameplay != gameplay.Strict31 || s.Combat.ModernTerrainAdmission {
		t.Fatal("strict command did not apply")
	}
	s.SetGameplay("")
	if !s.Combat.ModernTerrainAdmission {
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
