//go:build retail

package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestCampaignSelectionFindsStockMenuCandidate locks the consequence of the
// downloadable enforcement's scope [02 §5] "downloadable enforcement",
// [02 R-CAT-01 §8] step 3: only a download record's first product may have the
// bit forced on, never a CANBUILD button name.
//
// Campaign candidate selection rejects every definition carrying
// `downloadable` [08 R-P0-05 §3]. When the catalog also forced the bit on
// every menu-button definition, the commander's whole stock build menu was
// rejected and a campaign computer player could build nothing at all. A stock
// commander menu must therefore yield a candidate in mission mode.
func TestCampaignSelectionFindsStockMenuCandidate(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	const builderKey = "armcom"
	def, ok := cat.Unit(builderKey)
	if !ok {
		t.Fatalf("%s missing from the stock catalog", builderKey)
	}
	page := cat.BuildMenus[builderKey]
	if page == nil || len(page.Buttons) == 0 {
		t.Fatalf("%s has no compiled build menu", builderKey)
	}

	// Every resolvable button gets an equal, positive class vector so the
	// selection's own gates — not a missing vector — decide the outcome.
	strat := &Strategic{Catalog: cat, ClassVectors: map[string]ClassVector{}}
	profile := &Profile{Weight: map[string]int32{}}
	resolvable := 0
	for _, button := range page.Buttons {
		key := content.CanonicalKey(button)
		if _, ok := cat.Unit(key); !ok {
			continue
		}
		resolvable++
		strat.ClassVectors[key] = ClassVector{C0: 100}
		profile.Weight[key] = 100
	}
	if resolvable == 0 {
		t.Fatalf("no %s menu button resolves to a definition", builderKey)
	}

	builder := &units.Unit{Def: def, Owner: 1, Alive: true}
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	sim := rng.NewSimulation(37)
	sel := &testSelector{player: 1, catalog: cat, strategic: strat, profile: profile, rng: &sim, missionMode: 1}

	got, ok := Select(sel, builder, econ)
	if !ok {
		t.Fatalf("campaign selection over %d resolvable %s menu buttons returned no candidate; the downloadable gate [08 R-P0-05 §3] rejected the whole stock menu", resolvable, builderKey)
	}
	chosen, found := cat.Unit(content.CanonicalKey(got.DefKey))
	if !found {
		t.Fatalf("selected %q does not resolve in the catalog", got.DefKey)
	}
	if chosen.Downloadable {
		t.Fatalf("campaign selection returned a downloadable definition %q", got.DefKey)
	}
}
