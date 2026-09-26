package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Zero changes only the placement comparison, so completed-builder counts
// 5..126 remain eligible for both passes [research/extensions/ta-zero-engine.md
// "Historical Classic AI construction cutoff"].
func TestZeroBuilderPlacementOverlap(t *testing.T) {
	baseline, world, builder, ledger, random, _ := constructionOrderGateFixture(t)
	builder.Def.CanCapture = true
	baseline.constructionPlacePass(90, world, ledger, baseline.Strategic.CenterX, baseline.Strategic.CenterZ, 4)
	acceptedDraws, acceptedState := random.Draws(), random.State
	for _, tc := range []struct {
		count           int32
		place, position bool
	}{
		{4, true, false}, {5, true, true},
		{126, true, true}, {127, false, true},
	} {
		m, w, builder, econ, random, submissions := constructionOrderGateFixture(t)
		builder.Def.CanCapture = true
		m.Community.AIBuilderPlacementLimit = 127
		m.Community.AIBuilderStopThreshold = true // Numeric declaration takes precedence.
		stocks := econ.Players[1].Stock
		beforeState := random.State
		m.constructionPlacePass(90, w, econ, m.Strategic.CenterX, m.Strategic.CenterZ, tc.count)
		if got := *submissions == 1; got != tc.place {
			t.Errorf("count %d placement=%v, want %v", tc.count, got, tc.place)
		}
		if econ.Players[1].Stock != stocks {
			t.Fatal("placement selection spent resources before ordinary construction")
		}
		if tc.place && (random.Draws() != acceptedDraws || random.State != acceptedState) {
			t.Fatal("admitted Zero placement changed the ordinary random sequence")
		}
		if !tc.place && (random.Draws() != 0 || random.State != beforeState) {
			t.Fatal("refused placement consumed randomness")
		}
		m, w, builder, _, _, _ = constructionOrderGateFixture(t)
		builder.Def.CanCapture = true
		m.Community.AIBuilderPlacementLimit = 127
		m.constructionRepositionPass(90, w, m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ, tc.count)
		if got := orders.QueueOfUnit(builder).LenPrimary() > 0; got != tc.position {
			t.Errorf("count %d reposition=%v, want %v", tc.count, got, tc.position)
		}
	}
}
