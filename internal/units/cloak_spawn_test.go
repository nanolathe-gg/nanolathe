package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestSpawnSeedsTheCloakRequestBitFromInitCloaked locks the corrected
// [05 R-ECO-01 §9] / [03 R-VIS-01 §6] rule: the constructor seeds the
// cloak-REQUESTED status bit from the definition's `init_cloaked`, so a mine
// owes cloak upkeep from its first settlement pass with no player order. This
// file previously asserted the opposite; the doc text it cited was corrected in
// place by RWU-19-26.
func TestSpawnSeedsTheCloakRequestBitFromInitCloaked(t *testing.T) {
	def := &content.UnitDef{InitCloaked: true, CloakCost: 12, CloakCostMoving: 34, MaxDamage: 10}
	u := &Unit{Def: def}
	u.IsCloaked = false // whatever the slot held before reuse
	u.InitEconomyState()

	if !u.IsCloaked {
		t.Fatal("spawn left the cloak-requested bit clear for an init_cloaked definition [05 R-ECO-01 §9]")
	}
	// The debit is not gated on completion, so the charge is already due while
	// the unit is a nanoframe [05 R-ECO-01 §9].
	if cost := u.CloakCost(); cost != 12 {
		t.Fatalf("stationary cloak cost %v, want the authored 12 [05 R-ECO-01 §9]", cost)
	}
	// The definition flag itself is not consumed; it survives on the immutable
	// definition for the save boundary and diagnostics.
	if !u.Def.InitCloaked {
		t.Fatal("init_cloaked was consumed rather than left on the definition")
	}
	// `Cloak_Off` is the runtime toggler that clears the request again.
	u.SetCloaked(false)
	if cost := u.CloakCost(); cost != 0 {
		t.Fatalf("an uncloaked unit charges %v per pass, want nothing [05 R-ECO-01 §9]", cost)
	}
}

// TestSpawnClearsTheCloakRequestBitWithoutInitCloaked is the negative half:
// nothing else seeds the bit, so a definition without `init_cloaked` spawns
// cloak-unrequested even into a slot that last held a cloaked unit
// [05 R-ECO-01 §9].
func TestSpawnClearsTheCloakRequestBitWithoutInitCloaked(t *testing.T) {
	def := &content.UnitDef{CloakCost: 12, CloakCostMoving: 34, MaxDamage: 10}
	u := &Unit{Def: def}
	u.IsCloaked = true
	u.InitEconomyState()

	if u.IsCloaked {
		t.Fatal("spawn kept a stale cloak-requested bit from the reused slot [05 R-ECO-01 §9]")
	}
	if cost := u.CloakCost(); cost != 0 {
		t.Fatalf("an unrequested cloak charges %v per pass, want nothing [05 R-ECO-01 §9]", cost)
	}
}
