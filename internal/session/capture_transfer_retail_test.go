//go:build retail

package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRetailCommanderCapturesEnemyLab is WU-19-177's real-content regression:
// on the authored `ashap plateau` corpus, only the two commanders carry
// `cancapture` [05 R-WORK-01 §6] (a scripted scan of the compiled retail unit
// catalog found no third capturer), so the ARM commander is the captor and a
// directly-placed, finished CORE factory — `cancapture` clear, like every
// non-commander definition — is the victim.
//
// This exercises the same session-composed seam TestCommanderCapturesEnemyUnit
// locks with synthetic fixtures, but against the real catalog, the real COB
// bindings and the real movement/placement services: a completed `Capture`
// order must leave a captor-owned replacement standing at the victim's site,
// and the old record must be a genuinely different, dead record rather than
// the same handle with its owner field flipped [05 R-WORK-01 §15].
func TestRetailCommanderCapturesEnemyLab(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	captor := retailUnit(s, 0, retailARM)
	if captor == nil {
		t.Fatal("ARM commander not spawned")
	}
	if captor.Def == nil || !captor.Def.CanCapture {
		t.Fatalf("authored %s lost its cancapture bit", retailARM)
	}

	labDef, ok := f.cat.Unit(retailCORELab)
	if !ok || labDef == nil {
		t.Fatalf("authored %s missing", retailCORELab)
	}
	if labDef.CanCapture {
		t.Fatalf("authored %s unexpectedly carries cancapture; the phase-0 ladder's predicate 4 would then refuse it [05 R-WORK-01 §6]", retailCORELab)
	}
	siteX, siteZ := retailBuildSite(t, s, f.cat, captor, retailCORELab)
	siteY := s.World.HeightAt(siteX, siteZ)
	hVictim, err := s.Units.Create(labDef, 1, siteX, siteY, siteZ)
	if err != nil {
		t.Fatalf("place authored %s: %v", retailCORELab, err)
	}
	victim := s.Units.Unit(hVictim)
	if victim == nil {
		t.Fatalf("created %s not resolvable", retailCORELab)
	}
	// The ordinary creator finishes what it builds directly [05 "Nanoframe
	// allocation"]; Remaining is already 0, confirmed rather than assumed.
	if victim.Remaining != 0 {
		t.Fatalf("directly-created %s remaining = %v, want 0 (finished)", retailCORELab, victim.Remaining)
	}
	// A unit placed after battle entry needs the same mover registration
	// battle-entry placement gets for every authored unit [01 §6.1]; nothing
	// else in this path calls it for a handle allocated this way.
	s.Movement.EnsureUnit(victim)
	victimX, victimZ := victim.X, victim.Z

	captureID := orders.Lookup("Capture")
	if captureID == 0 {
		t.Fatal("Capture descriptor not registered")
	}
	orders.QueueForUnit(captor).Push(captureID, orders.NewNodeForOrder(captureID, hVictim, victim.X, victim.Y, victim.Z, s.Clock.GlobalTick, captor.Handle, false))

	var repl *units.Unit
	const round, maxRounds = 200, 15
	for r := 0; r < maxRounds && repl == nil; r++ {
		stepRetail(s, round)
		if victim.Owner == captor.Owner {
			t.Fatalf("the victim's own record changed owner in place; retail creates a fresh replacement and kills the old record instead [05 R-WORK-01 §15]")
		}
		for _, u := range s.Units.IterSliced() {
			if u == nil || u == victim {
				continue
			}
			if u.Def == labDef && u.Owner == captor.Owner {
				repl = u
				break
			}
		}
	}
	if repl == nil {
		t.Fatalf("no ARM-owned replacement of %s appeared; the commander's Capture order never transferred it", retailCORELab)
	}
	if repl.X != victimX || repl.Z != victimZ {
		t.Fatalf("replacement position = (%v,%v), want the victim's site (%v,%v) [05 R-WORK-01 §15]", repl.X, repl.Z, victimX, victimZ)
	}
	if victim.Alive && !victim.Dying {
		t.Fatal("the old lab record is still alive and not even marked dying after the transfer")
	}
	if !strings.EqualFold(repl.Def.UnitName, retailCORELab) {
		t.Fatalf("replacement definition = %s, want %s", repl.Def.UnitName, retailCORELab)
	}
}
