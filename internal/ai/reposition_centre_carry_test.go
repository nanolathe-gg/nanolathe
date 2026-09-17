package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRepositionPassCarriesTheCaptureMembersHeight locks the shared-centre
// write of [08 R-AI-01 §3] pass 2.
//
// A capture-capable member stores its own height INTO the pass's centre, not
// into the per-member target copy, and the write is never undone. The member's
// own distance is unaffected — its vertical term is forced to zero — but the
// patrol destination submitted for it carries that height, and so does every
// later member's full three-dimensional distance. Because that distance selects
// between the three branches and one of them draws `RNG(65536)`, the carry-over
// changes how many simulation draws the tick consumes.
//
// The fixture is the smallest arrangement that shows both halves: member A is
// capture-capable and sits exactly on the centre in X and Z but 400 world units
// above it, member B is not capture-capable and sits exactly on the centre in
// all three axes. Without the carry-over B's distance is zero, which takes the
// random-hop branch and draws; with it B's distance is 400 world units, which
// is at or above the 320 threshold and takes the centre branch, which does not.
func TestRepositionPassCarriesTheCaptureMembersHeight(t *testing.T) {
	capture := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "carry-capture"},
		UnitName:         "carry-capture",
		Side:             "ARM",
		BMCode:           1,
		CanMove:          true,
		CanPatrol:        true,
		CanCapture:       true,
		MaxDamage:        100,
	}
	plain := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "carry-plain"},
		UnitName:         "carry-plain",
		Side:             "ARM",
		BMCode:           1,
		CanMove:          true,
		CanPatrol:        true,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		capture.CanonicalKey: capture,
		plain.CanonicalKey:   plain,
	}}

	const centreUnits = int32(1000)
	const captureHeightUnits = int32(400)
	centreX := fixedWordFromUnits(centreUnits)
	centreZ := fixedWordFromUnits(centreUnits)
	centreY := numeric.Fixed(0)

	w := newAIFixtureWorld(4, cat)
	// The vector order is the pass order, so the capture-capable member has to
	// be created — and listed — first for the carry-over to reach the other.
	hCapture, err := w.Create(capture, 1, centreX, fixedWordFromUnits(captureHeightUnits), centreZ)
	if err != nil {
		t.Fatal(err)
	}
	hPlain, err := w.Create(plain, 1, centreX, 0, centreZ)
	if err != nil {
		t.Fatal(err)
	}
	a, b := w.Unit(hCapture), w.Unit(hPlain)
	a.Remaining, b.Remaining = 0, 0

	sim := rng.NewSimulation(7)
	m := &Manager{
		Player:            1,
		Catalog:           cat,
		RNG:               &sim,
		GroupConstruction: []pool.Handle{hCapture, hPlain},
	}
	// Five build-capable own units is what pass 2's capture arm requires; the
	// plain member has no such gate [08 R-AI-01 §3].
	m.constructionRepositionPass(10, w, centreX, centreY, centreZ, 5)

	// Member A's own distance is zero, which takes the mirrored-target arm and
	// draws nothing; member B's is 400 world units once the height is carried,
	// which takes the centre arm and also draws nothing. A pass that measured B
	// against the unmutated centre would find distance zero and draw one angle.
	if got := sim.Draws(); got != 0 {
		t.Fatalf("reposition pass drew %d times, want 0: the later member must measure against the carried height, not the original centre [08 R-AI-01 §3]", got)
	}

	// The capture-capable member's patrol destination is the centre carrying
	// its own height.
	captureGoal, ok := lastPatrolGoal(t, a)
	if !ok {
		t.Fatal("capture-capable member received no patrol record")
	}
	if captureGoal != fixedWordFromUnits(captureHeightUnits) {
		t.Fatalf("capture member patrol goal Y = %d, want %d (its own height written into the centre)", captureGoal, fixedWordFromUnits(captureHeightUnits))
	}

	// The later member's destination is that same mutated centre.
	plainGoal, ok := lastPatrolGoal(t, b)
	if !ok {
		t.Fatal("non-capture member received no patrol record")
	}
	if plainGoal != fixedWordFromUnits(captureHeightUnits) {
		t.Fatalf("later member patrol goal Y = %d, want %d (the carried height), not the original centre height", plainGoal, fixedWordFromUnits(captureHeightUnits))
	}
}

// lastPatrolGoal returns the goal height of the last primary record a unit
// holds, which for this fixture is the patrol the reposition pass submitted.
func lastPatrolGoal(t *testing.T, u *units.Unit) (numeric.Fixed, bool) {
	t.Helper()
	q := orders.QueueOfUnit(u)
	if q == nil {
		return 0, false
	}
	primary := q.Primary()
	if len(primary) == 0 {
		return 0, false
	}
	return primary[len(primary)-1].GoalY, true
}
