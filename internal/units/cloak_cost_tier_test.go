package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The case relations below come from the instruction-level verification of
// 2026-09-16, recorded as Established in [05 R-PROD-01 §7] and [05 R-ECO-01 §9]:
// the settlement block selects on the CACHED movement-rate tier of
// [04 R-MOV-01 §6], not on scalar speed and not on the movement-mode bits. The
// three cases lock the places where a speed test and a tier test disagree, which
// is exactly where a future edit could silently regress the selector.
func TestCloakCostSelectsOnTheMovementRateTier(t *testing.T) {
	def := &content.UnitDef{CloakCost: 12, CloakCostMoving: 34, MaxDamage: 10}

	// Tier 0 with non-zero speed: the classifier forces tier 0 for a blocked
	// mover and for a unit carried by a transport, whatever its residual speed,
	// so such a unit pays the STATIONARY cost [04 R-MOV-01 §6].
	blocked := &Unit{Def: def, IsCloaked: true}
	blocked.Move.Speed = numeric.FixedFromRaw(3 << 16)
	blocked.MoveTier = 0
	if cost := blocked.CloakCost(); cost != 12 {
		t.Fatalf("blocked/carried mover with residual speed charged %v, want the stationary 12 [05 R-ECO-01 §9]", cost)
	}

	// Tier 1 with zero speed: the classifier counts the turn residual, so a unit
	// turning in place pays the MOVING cost [04 R-MOV-01 §6].
	turning := &Unit{Def: def, IsCloaked: true}
	turning.Move.Speed = 0
	turning.MoveTier = 1
	if cost := turning.CloakCost(); cost != 34 {
		t.Fatalf("unit turning in place at zero speed charged %v, want the moving 34 [05 R-ECO-01 §9]", cost)
	}

	// Tier never written: a building has no mover, so MoveTier keeps its seed 0
	// and the structure always pays the stationary cost. Only definitions that
	// author distinct costs can show the difference at all.
	building := &Unit{Def: def, IsCloaked: true}
	if cost := building.CloakCost(); cost != 12 {
		t.Fatalf("structure with an unwritten tier charged %v, want the stationary 12 [05 R-PROD-01 §7]", cost)
	}

	// Tiers 2 and 3 are moving tiers too — the test is non-zero, not "== 1".
	for _, tier := range []uint8{2, 3} {
		u := &Unit{Def: def, IsCloaked: true, MoveTier: tier}
		if cost := u.CloakCost(); cost != 34 {
			t.Fatalf("tier %d charged %v, want the moving 34 [05 R-ECO-01 §9]", tier, cost)
		}
	}
}
