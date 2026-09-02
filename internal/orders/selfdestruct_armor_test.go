package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestSelfDestructArmorGateReadsTheRuntimeBit locks the operand of the damage
// funnel's step-5 armor gate [06 R-DMG-01 §8]: it is the runtime armored
// posture — bit 1 of the unit's first state byte, the `set ARMORED` port — and
// never the FBI `armoredstate` key, which is parsed into a definition flag that
// no retail path reads. This call site passed the definition flag, so a unit
// merely AUTHORED `armoredstate=1` was treated as permanently armored here.
//
// At this row's amount the gate is inert both ways: step 5 tests `amount <
// 30000` strictly against the amount as it stands BEFORE the defender veterancy
// of step 6, and the row's amount is exactly 30000. The test therefore pins two
// things — that authoring `armoredstate` changes nothing at this site, and that
// the modifier the wrong operand would have applied is real, so the operand is
// worth getting right.
func TestSelfDestructArmorGateReadsTheRuntimeBit(t *testing.T) {
	// A halving modifier: had the gate fired, the loss would be visibly smaller.
	const halfIn1616 = int32(1) << 15

	loss := func(authored, runtime bool) int32 {
		def := &content.UnitDef{ArmoredState: authored, DamageModifier: halfIn1616}
		u := &units.Unit{Handle: 1, Def: def, Alive: true, Health: 40000, MaxHealth: 40000, Armored: runtime}
		applySelfDestructDamage(u)
		return 40000 - u.Health
	}

	plain := loss(false, false)
	if plain <= 0 {
		t.Fatalf("self destruct removed %d health", plain)
	}
	if got := loss(true, false); got != plain {
		t.Fatalf("an `armoredstate`-authored unit lost %d, want the unscaled %d: the definition flag is not the gate's operand", got, plain)
	}
	if got := loss(false, true); got != plain {
		t.Fatalf("a runtime-armored unit lost %d, want the unscaled %d: 30000 is not strictly below 30000", got, plain)
	}
	if got := loss(true, true); got != plain {
		t.Fatalf("both bits set lost %d, want the unscaled %d", got, plain)
	}

	// The same operand one unit of damage lower does bite, which is what makes
	// the two flags distinguishable and the wrong one a defect rather than a
	// spelling.
	unarmored := combat.ComputeScaledAmount(29999, 1, 0, 0, false, halfIn1616, false, false, false)
	armored := combat.ComputeScaledAmount(29999, 1, 0, 0, true, halfIn1616, false, false, false)
	if armored >= unarmored {
		t.Fatalf("armor gate did not scale below 30000: armored=%d unarmored=%d", armored, unarmored)
	}
}
