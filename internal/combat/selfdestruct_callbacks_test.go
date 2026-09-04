package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
)

// The damage funnel starts HitByWeapon and TakeDamage from a single equality
// test against damage kind 1 [06 §9.1 step 7]. Self-destruct is kind 3, so it
// emits NEITHER — the packet subtracts health, latches death or clamps, and
// returns [06 R-WPN-05 §11 "Every non-projectile caller passes a zero direction
// word"]. This build emitted both with a zero direction byte until WU-19-154.
//
// The test rides the survivor path, which is the only one where the absence is
// observable at all: a lethal self-destruct destroys the unit before any
// callback site is reached either way.
func TestSelfDestructEmitsNoScriptCallbacks(t *testing.T) {
	w, _, victim, _ := newTestWorldAndUnits(t)
	// A script that defines both callbacks, so a start would find a real entry
	// point rather than failing the name lookup [04 §5.3].
	code := []uint32{0x10065000} // return [04 §4.3]
	prog := progWithAim(code, "HitByWeapon", 0)
	prog.Scripts["TakeDamage"] = 0
	prog.ScriptsByID = []int{0, 0}
	vm := cob.NewVM(prog)
	attachTestCOB(victim, vm)

	// Survive the 30000: veterancy scaling leaves the amount at 30000 for a
	// unit with no kills, so health has to start above it.
	victim.Health = 40000
	victim.MaxHealth = 40000

	svc := &Service{}
	before := vm.ActiveThreadCount()
	if !svc.ApplySelfDestructDamage(w, victim.Handle, 6) {
		t.Fatal("self destruct did not apply")
	}
	if victim.Dying {
		t.Fatalf("fixture is wrong: the victim died, health %d", victim.Health)
	}
	if victim.Health != 10000 {
		t.Fatalf("health %d after a 30000 self-destruct from 40000, want 10000 [06 §9.1]", victim.Health)
	}
	if got := vm.ActiveThreadCount(); got != before {
		t.Fatalf("self destruct started %d script thread(s), want none: only damage kind 1 emits HitByWeapon/TakeDamage [06 §9.1 step 7]", got-before)
	}

	// The harness itself has to be able to see a start, or the assertion above
	// is vacuous.
	victim.ScriptState.Binding.Callbacks.HitByWeapon(0)
	if vm.ActiveThreadCount() == before {
		t.Fatal("harness is blind: a direct HitByWeapon start did not register a thread")
	}
}
