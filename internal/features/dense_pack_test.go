package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The three regressions for R12: the dense-pack rule of [05 R-FEAT-01 §3]
// step 3, restated for a fringe cell by [05 R-FEAT-01 §3-A]. A new feature
// REPLACES any non-indestructible feature it overlaps; an indestructible one
// under any covered cell vetoes the stamp; and a veto leaves the cells torn
// before it torn.

func newDensePackService(t *testing.T, w, h int) (*Service, *world.Terrain) {
	t.Helper()
	terrain := newTestTerrainP1(w, h)
	sim := rng.SimulationFromState(1)
	return NewService(terrain, &sim, nil, nil), terrain
}

// TestStampReplacesADestructibleTree is the wreck-over-clutter case: a unit
// dying on an ordinary tree must still leave its wreck, and the tree must be
// gone from both the grid and the animation side.
func TestStampReplacesADestructibleTree(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	tree := defP1("tree", 1, 1, "", "trees")
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	if svc.spawnFeatureAt(3, 3, tree) == nil {
		t.Fatal("tree refused on clear ground")
	}
	inst := svc.spawnFeatureAt(3, 3, wreck)
	if inst == nil {
		t.Fatal("wreck refused over a destructible tree [05 R-FEAT-01 §3 step 3]")
	}
	if inst.Def != wreck {
		t.Fatalf("anchor instance is %q, want the wreck", inst.Def.CanonicalKey)
	}
	def, bound := terrain.FeatureDefAt(terrain.PlotAt(3, 3).Feature())
	if !bound || def != wreck {
		t.Fatalf("plot anchor still carries the old definition")
	}
	if len(svc.Instances()) != 1 {
		t.Fatalf("%d instances after the replacement, want 1: the torn tree's record must be released with its slot [05 R-FEAT-01 §4 step 4]", len(svc.Instances()))
	}
}

// TestStampReplacesAcrossAFringeCell is [05 R-FEAT-01 §3-A]: a footprint that
// covers a PARTIAL cell of a 2x2 neighbour takes that neighbour's whole
// footprint with it, anchor included, rather than truncating its fringe.
func TestStampReplacesAcrossAFringeCell(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	big := defP1("big", 2, 2, "", "trees")
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	if svc.spawnFeatureAt(2, 2, big) == nil {
		t.Fatal("2x2 sprite refused on clear ground")
	}
	// (3,3) is the far fringe cell of the 2x2 anchored at (2,2).
	if !terrain.PlotAt(3, 3).IsFringe() {
		t.Fatal("fixture: (3,3) is not a fringe of the 2x2")
	}
	if svc.spawnFeatureAt(3, 3, wreck) == nil {
		t.Fatal("wreck refused over a destructible feature's fringe [05 R-FEAT-01 §3-A]")
	}
	if !terrain.PlotAt(2, 2).IsEmpty() {
		t.Fatal("the overlapped neighbour's anchor survived; a fringe teardown clears the whole footprint")
	}
	if svc.InstanceAt(2, 2) != nil {
		t.Fatal("the overlapped neighbour kept its instance after its grid entry went away")
	}
	if svc.InstanceAt(3, 3) == nil {
		t.Fatal("the wreck has no instance")
	}
}

// TestIndestructibleVetoesTheStamp is the deposit case: an indestructible
// feature under any covered cell refuses the whole placement, and the teardown
// never touches it [05 R-FEAT-01 §3 step 3][§4 step 3].
func TestIndestructibleVetoesTheStamp(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	deposit := defP1("rockmetal", 1, 1, "rockmetal.3do", "")
	deposit.Indestructible = true
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	if svc.spawnFeatureAt(4, 4, deposit) == nil {
		t.Fatal("deposit refused on clear ground")
	}
	if inst := svc.spawnFeatureAt(4, 4, wreck); inst != nil {
		t.Fatal("wreck stamped over an indestructible deposit")
	}
	def, bound := terrain.FeatureDefAt(terrain.PlotAt(4, 4).Feature())
	if !bound || def != deposit {
		t.Fatal("the indestructible deposit was disturbed by the vetoed stamp")
	}
	if svc.InstanceAt(4, 4) == nil || svc.InstanceAt(4, 4).Def != deposit {
		t.Fatal("the deposit lost its instance to a stamp that never ran")
	}
}

// TestVetoLeavesEarlierTeardownDone is the rule that makes the dense-pack loop
// order observable: the loop walks the footprint row-major, tearing down as it
// goes, and "if that teardown returns 0 the stamp returns 0 immediately,
// leaving already-torn cells torn" [05 R-FEAT-01 §3 step 3].
func TestVetoLeavesEarlierTeardownDone(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	tree := defP1("tree", 1, 1, "", "trees")
	deposit := defP1("rockmetal", 1, 1, "rockmetal.3do", "")
	deposit.Indestructible = true
	wreck := defP1("wreck", 2, 1, "wreck.3do", "")
	// Row-major over a 2x1 footprint anchored at (1,1): the tree at (1,1) is
	// visited first, the deposit at (2,1) second.
	if svc.spawnFeatureAt(1, 1, tree) == nil {
		t.Fatal("tree refused on clear ground")
	}
	if svc.spawnFeatureAt(2, 1, deposit) == nil {
		t.Fatal("deposit refused on clear ground")
	}
	if inst := svc.spawnFeatureAt(1, 1, wreck); inst != nil {
		t.Fatal("2x1 wreck stamped despite an indestructible cell under its footprint")
	}
	if !terrain.PlotAt(1, 1).IsEmpty() {
		t.Fatal("the earlier destructible cell was not left torn after the later veto")
	}
	if svc.InstanceAt(1, 1) != nil {
		t.Fatal("the torn tree kept its instance; the teardown must be reconciled synchronously")
	}
	def, bound := terrain.FeatureDefAt(terrain.PlotAt(2, 1).Feature())
	if !bound || def != deposit {
		t.Fatal("the vetoing deposit was disturbed")
	}
}

// TestArenaRefusalIsTestedAfterTheTeardown locks step 4's position in the
// order: the pop happens after the dense-pack loop, so a 3D feature torn down
// by the footprint has already returned its slot to the free list and its
// replacement fits in a full arena [05 R-FEAT-01 §3 steps 3-4][§5 step 6].
func TestArenaRefusalIsTestedAfterTheTeardown(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	successor := defP1("wreck2", 1, 1, "wreck2.3do", "")
	if svc.spawnFeatureAt(1, 1, wreck) == nil {
		t.Fatal("wreck refused on clear ground")
	}
	// Fill the arena to exactly its capacity with the one real occupant plus
	// synthetic ones, so the successor can only be admitted by the slot the
	// teardown at (1,1) frees.
	filler := defP1("filler", 1, 1, "filler.3do", "")
	for i := len(svc.instances); i < FeatureAnimSlots; i++ {
		svc.setInstance(10000+i, &Instance{Def: filler})
	}
	if got := svc.arenaOccupants(); got != FeatureAnimSlots {
		t.Fatalf("fixture arena holds %d occupants, want %d", got, FeatureAnimSlots)
	}
	if svc.spawnFeatureAt(1, 1, successor) == nil {
		t.Fatal("successor refused although its predecessor's slot was freed by the teardown first")
	}
	def, bound := terrain.FeatureDefAt(terrain.PlotAt(1, 1).Feature())
	if !bound || def != successor {
		t.Fatal("the successor did not reach the grid")
	}
	// With no teardown to fund it, a further 3D stamp on clear ground is
	// refused and leaves the cell empty.
	other := defP1("other", 1, 1, "other.3do", "")
	if svc.spawnFeatureAt(5, 5, other) != nil {
		t.Fatal("a full arena admitted a 3D stamp that freed nothing")
	}
	if !terrain.PlotAt(5, 5).IsEmpty() {
		t.Fatal("a refused stamp left its ordinal on the grid; retail writes the anchor inside step 4, after the pop")
	}
}
