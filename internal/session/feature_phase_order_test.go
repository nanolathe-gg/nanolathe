package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Even reproduce=0 spends a simulation draw. Its placement after player work
// is observable through the real session phase trace [05 R-FEAT-01 §12][I4].
func TestFeatureReproductionDrawFollowsPlayerPhase(t *testing.T) {
	s := newLoopTestSession(t, 0)
	s.SeedSessionRNG(12345, 67890)
	tree := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "phase-order-tree"}, FootprintX: 1, FootprintZ: 1, Damage: 100, Reproduce: 0, ReproduceArea: 6}
	if s.Features.PlaceAt(5, 5, tree) == nil {
		t.Fatal("feature placement failed")
	}
	source := 5*int(s.World.CellW) + 5
	s.Features.SetCursor(source + 1)
	baseline := s.SimRNG().Draws()
	observed := false
	s.Econ.EndCondition = func(_ int, _ uint32) {
		observed = true
		if s.Features.Cursor() != source+1 || s.SimRNG().Draws() != baseline {
			t.Errorf("phase-5 consumer followed reproduction: cursor=%d draws=%d", s.Features.Cursor(), s.SimRNG().Draws()-baseline)
		}
	}
	s.EnablePhaseTrace()
	s.stepOneSubTick(1)
	if !observed {
		t.Fatal("phase-5 settlement consumer was not reached")
	}
	beforeFeature := baseline
	found := false
	for _, d := range s.PhaseDrawDeltas() {
		if d.Phase == "phase4-effects" && d.SimDelta != baseline {
			t.Fatalf("phase 4 spent reproduction draw: %d", d.SimDelta-baseline)
		}
		if d.Phase == "phase5-orders" {
			beforeFeature = d.SimDelta
		}
		if d.Phase == "phase6-feature" {
			found = true
			if d.SimDelta-beforeFeature != 1 {
				t.Fatalf("phase 6 simulation draws = %d, want 1", d.SimDelta-beforeFeature)
			}
		}
	}
	if !found || s.Features.LastReproIdx != source {
		t.Fatalf("feature visit missing: found=%v index=%d", found, s.Features.LastReproIdx)
	}
}
