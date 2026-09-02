package path

import "testing"

// TestGoalRadiiKeepTheirTwoUnitSystems locks the dual-unit contract of
// [04 §7.4]: the heuristic clamp compares the RAW authored radius against the
// inflated octile, while the arrival predicate compares a `>>4`-quantized
// radius against a squared cell distance. Doc 04 records the mismatch as
// established — "two unit systems coexist in one family and must be reproduced
// as-is, not 'fixed'".
//
// The relationship under test is that disagreement, not either side's numbers.
// A cell must exist that the arrival predicate ACCEPTS as inside the radius
// while the heuristic still reports a non-zero cost to reach it. Unifying the
// two units — the obvious "fix" — makes the two sides agree and fails here.
func TestGoalRadiiKeepTheirTwoUnitSystems(t *testing.T) {
	center := Cell{X: 100, Z: 100}
	// dx = 10 on a radius of 160: the inflated octile is 18*10 = 180, which is
	// outside the raw radius, while 10 is exactly the quantized radius 160>>4.
	probe := Cell{X: center.X + 10, Z: center.Z}

	t.Run("point goal", func(t *testing.T) {
		g := PointGoal(center, 160)
		if !g.StartSatisfied(probe) {
			t.Fatalf("arrival compares the >>4-quantized radius [04 §7.2]: it must accept this cell")
		}
		if h := g.H(probe); h == 0 {
			t.Fatalf("the h clamp compares the RAW radius against the inflated octile [04 §7.4]: h must stay non-zero for a cell arrival already accepts; got 0")
		}
	})

	t.Run("annulus goal", func(t *testing.T) {
		g := AnnulusGoal(center, 0, 160)
		if !g.StartSatisfied(probe) {
			t.Fatalf("arrival compares >>4-quantized radii [04 §7.4]: it must accept this cell")
		}
		if h := g.H(probe); h == 0 {
			t.Fatalf("the V-shaped zero band compares RAW radii [04 §7.4]: h must stay non-zero for a cell arrival already accepts; got 0")
		}
	})
}
