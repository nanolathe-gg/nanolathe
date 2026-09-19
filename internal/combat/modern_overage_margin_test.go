package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestModernOveragePreferenceSurvivesRetentionMargin(t *testing.T) {
	s, w, terrain, shooter, a, weapon := modernCombatFixture(t)
	h, err := w.Create(a.Def, 1, cellCentre(8), numeric.FixedFromInt(10), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	b := w.Unit(h)
	// Equal threat scores divisible by five exercise the exact cancellation:
	// multiplying by 4/5 then allowing a 5/4 switch margin undoes the penalty.
	a.InstallWeapon(0, weapon)
	b.InstallWeapon(0, weapon)
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: a.Handle}
	q := modernTargetQuery(s, w, terrain, shooter, a, b)
	a.Health = 50 // a 60-damage shot is exactly 120 percent
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle {
		t.Fatal("allowed overage needlessly changed target")
	}
	a.Health = 49
	if got, _ := s.rules().SelectTarget(s, &q); got != b.Handle {
		t.Fatal("retention margin cancelled the overage preference")
	}
	b.Health = 49
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle {
		t.Fatal("equal overage caused target churn")
	}
	q.Candidates = q.Candidates[:1]
	if got, ok := s.rules().SelectTarget(s, &q); !ok || got != a.Handle {
		t.Fatal("sole indivisible finishing shot became illegal")
	}
}
