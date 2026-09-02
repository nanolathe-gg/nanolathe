package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestSpawnClearsTheCloakRequestBit locks [05 R-ECO-01 §9]: the
// cloak-REQUESTED status bit is cleared at spawn and is not seeded from
// `init_cloaked`. Its only writers are the `Cloak_On` / `Cloak_Off` order
// handlers, so an init-cloaked definition owes no cloak upkeep until a player
// asks for cloak. `init_cloaked` itself feeds the initial-posture path and the
// visibility predicate, both of which read the definition, not this bit
// [03 R-VIS-01 §6].
func TestSpawnClearsTheCloakRequestBit(t *testing.T) {
	def := &content.UnitDef{InitCloaked: true, CloakCost: 12, CloakCostMoving: 34, MaxDamage: 10}
	u := &Unit{Def: def}
	u.IsCloaked = true // whatever the slot held before reuse
	u.InitEconomyState()

	if u.IsCloaked {
		t.Fatal("spawn left the cloak-requested bit set from init_cloaked [05 R-ECO-01 §9]")
	}
	if cost := u.CloakCost(); cost != 0 {
		t.Fatalf("an unrequested cloak charges %v per pass, want nothing [05 R-ECO-01 §9]", cost)
	}
	// The definition flag itself survives for the initial-posture and
	// visibility consumers, which read it off the immutable definition.
	if !u.Def.InitCloaked {
		t.Fatal("init_cloaked was consumed rather than left on the definition")
	}
	// `Cloak_On` is the toggler; after it the stationary cost is due.
	u.SetCloaked(true)
	if cost := u.CloakCost(); cost != 12 {
		t.Fatalf("stationary cloak cost %v, want the authored 12 [05 R-ECO-01 §9]", cost)
	}
}
