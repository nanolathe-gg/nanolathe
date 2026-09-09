package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The remaster synthesizes only what the map actually shows
// (DESIGN_GPU_RENDERER §14.4 "Coverage"): the entries the placed features'
// definitions name, never a bank's unused art, and never a shadow twin —
// shadows are flat two-colour art the search handles badly and the client
// doubles them instead. A definition no plot cell references contributes
// nothing at all.
func TestDetailArtQueriesOnlyThePlacedFeaturesEntries(t *testing.T) {
	defs := []*content.FeatureDef{
		{Filename: "Trees", SeqName: "tree1", SeqNameShad: "tree1shad",
			SeqNameBurn: "tree1burn", SeqNameBurnShad: "tree1burnshad",
			SeqNameDie: "tree1die", SeqNameReclamate: "tree1rec"},
		{Filename: "trees", SeqName: "tree2", SeqNameShad: "tree2shad"},
		{Filename: "rocks", SeqName: "rock1"}, // authored but never placed
	}
	terrain := &world.Terrain{CellW: 2, CellH: 2, FeatureDefs: defs}
	terrain.Plot = make([]world.PlotCell, 4)
	// Two cells carry the two tree records; the rest are empty.
	terrain.Plot[0][8], terrain.Plot[0][9] = 0, 0
	terrain.Plot[1][8], terrain.Plot[1][9] = 1, 0
	terrain.Plot[2][8], terrain.Plot[2][9] = 0xFF, 0xFF
	terrain.Plot[3][8], terrain.Plot[3][9] = 0xFF, 0xFF

	queries := detailArtBankQueries(terrain)
	if len(queries) != 1 {
		t.Fatalf("banks = %v, want only the placed features' bank", queries)
	}
	// The filename is lower-cased, as the VFS path is [I1].
	query, ok := queries["trees"]
	if !ok {
		t.Fatalf("banks = %v, want a \"trees\" entry", queries)
	}
	for _, name := range []string{"tree1", "tree1burn", "tree1die", "tree1rec", "tree2"} {
		if !query.named[name] {
			t.Errorf("entry %q is named by a placed feature but is not a query", name)
		}
	}
	for _, name := range []string{"tree1shad", "tree1burnshad", "tree2shad"} {
		if !query.shadow[name] {
			t.Errorf("entry %q is a shadow twin but is not marked as one", name)
		}
	}
	if query.named["rock1"] {
		t.Error("an unplaced definition's entry became a query")
	}
}

// The two exclusions of §14.4 are different things: fire, explosion, smoke and
// reclaim art must not be offered as examples, because their colours never
// occur in idle art, but a definition that names one still gets it
// synthesized. The example filter is therefore separate from the skip set.
func TestDetailArtExampleFilterDropsColourLeakingEntries(t *testing.T) {
	bank := &formats.GAF{Entries: []formats.GAFEntry{
		{Name: "tree1"}, {Name: "tree1burn"}, {Name: "tree1rec"},
		{Name: "smoke3"}, {Name: "fire2"}, {Name: "boom1"}, {Name: "TREE2"},
	}}
	filtered := filterExampleBank(bank)
	got := map[string]bool{}
	for i := range filtered.Entries {
		got[filtered.Entries[i].Name] = true
	}
	if len(got) != 2 || !got["tree1"] || !got["TREE2"] {
		t.Fatalf("example entries = %v, want only the idle art", got)
	}
	if int(filtered.EntryCount) != len(filtered.Entries) {
		t.Errorf("EntryCount = %d, want %d", filtered.EntryCount, len(filtered.Entries))
	}
}

// --auto-remaster=false installs no provider at all, which is what leaves every
// asset to the client's nearest doubling (§14.4 "Switches"). The loading
// family still completes, because the bar it feeds is the mean of its
// families.
func TestAutoRemasterOffInstallsNoProviderButCompletesItsBar(t *testing.T) {
	reported := map[string]int{}
	art := detailArtFor(Options{AutoRemaster: false}, nil, nil, func(family string, percent int) {
		reported[family] = percent
	})
	if art != nil {
		t.Errorf("--auto-remaster=false installed a provider: %+v", art)
	}
	if reported[familyDetailArt] != 100 {
		t.Errorf("remaster family reported %d, want 100", reported[familyDetailArt])
	}
}

// A capture's view scale is settled before the load — there is no F9 in a
// capture — so a native capture never pays for art it cannot show, and its
// pixels are identical with the switch on or off.
func TestCaptureSkipsTheRemasterAtTheNativeScale(t *testing.T) {
	if art := captureDetailArt(Options{AutoRemaster: true, Zoom: 1}, nil, nil); art != nil {
		t.Errorf("a 1x capture built detail art: %+v", art)
	}
}
