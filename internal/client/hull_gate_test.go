package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// hullGateFrame builds a committed frame whose coverage grid is all fogged, so
// a test only has to light the one cell it means to probe. Tiles are 32 map
// pixels, so cell (u, row) is lit by index row*W+u [03 §3.2].
func hullGateFrame(w, h int32) *frame.Frame {
	return &frame.Frame{Visibility: frame.VisibilityView{
		Valid:         true,
		CoverageBytes: true,
		W:             w,
		H:             h,
		Visible:       make([]uint8, int(w*h)),
	}}
}

func lightCell(f *frame.Frame, u, row int32) {
	f.Visibility.Visible[int(row*f.Visibility.W+u)] = 1
}

// TestHullGateSamplesAccumulate locks the four-sample walk of [03 §3.2] step 5.
// The samples are not four independent offsets from the base point: one triple
// is carried through and each step mutates it, so the third sample is east AND
// north and the fourth keeps the north displacement while undoing the east one.
//
// The unit sits at the origin cell with a two-cell footprint, giving hull
// extents of 32 world units on X and Z — one visibility tile each — so each
// sample lands in its own cell and a single lit cell identifies which sample
// admitted.
func TestHullGateSamplesAccumulate(t *testing.T) {
	const cell = numeric.Fixed(32 << 16) // footprint 2 → 2<<20 = 32 world units
	unit := frame.UnitView{
		Slot:        1,
		Owner:       1,
		X:           numeric.Fixed(4 << 16),
		Y:           0,
		Z:           numeric.Fixed(4 << 16),
		HullXExtent: cell,
		HullZExtent: cell,
		// A zero height decrement keeps the shear out of the walk; the
		// decrement's own effect is asserted separately below.
		HullYExtent: 0,
	}

	cases := []struct {
		name   string
		u, row int32
	}{
		{"centre", 0, 0},
		{"east", 1, 0},
		{"north (east and north together)", 1, 1},
		{"west (north retained)", 0, 1},
	}
	for _, tc := range cases {
		f := hullGateFrame(4, 4)
		lightCell(f, tc.u, tc.row)
		if !SnapshotVisible(f, unit, 0) {
			t.Fatalf("%s sample did not admit: cell (%d,%d) is lit and no other", tc.name, tc.u, tc.row)
		}
	}

	// The one cell the walk must never visit is (0,1)+east — i.e. a lit cell at
	// (2,0) two X extents east — because no sample adds the X extent twice.
	f := hullGateFrame(4, 4)
	lightCell(f, 2, 0)
	if SnapshotVisible(f, unit, 0) {
		t.Fatal("a cell two X extents east admitted; the samples must accumulate, not double")
	}
}

// TestHullGateHeightDecrementShearsNorthSamples locks that the height decrement
// is applied to samples 2 and 3 only, and that it moves them through the
// half-height shear rather than through Z [03 §3.2] step 5.
func TestHullGateHeightDecrementShearsNorthSamples(t *testing.T) {
	// The unit stands 64 world units up, which shears its projected row by
	// 64>>1 = 32 map pixels — exactly one visibility tile — to a negative row.
	// Neither the centre nor the east sample can therefore admit from row 0.
	unit := frame.UnitView{
		Slot:  1,
		Owner: 1,
		X:     numeric.Fixed(4 << 16),
		Y:     numeric.Fixed(64 << 16),
		Z:     numeric.Fixed(4 << 16),
	}
	f := hullGateFrame(4, 4)
	lightCell(f, 0, 0)
	if SnapshotVisible(f, unit, 0) {
		t.Fatal("a sample admitted from row 0, but with no height decrement every sheared row is negative")
	}
	// The decrement subtracts from the sample height, not from Z: with it the
	// north and west samples project at height zero and land back on row 0.
	unit.HullYExtent = numeric.Fixed(64 << 16)
	if !SnapshotVisible(f, unit, 0) {
		t.Fatal("the height decrement did not move the north samples through the shear")
	}
}

// TestHullGateSeaLevelRejection locks step 3 of the gate: a base height below
// the scaled sea-level byte is not visible unless the runtime exemption bit is
// set, and the comparison is against that scaled byte, not against zero
// [03 §3.2][03 §2.2].
func TestHullGateSeaLevelRejection(t *testing.T) {
	// Every cell is lit, so the samples always admit and step 3 alone decides.
	f := hullGateFrame(4, 4)
	for i := range f.Visibility.Visible {
		f.Visibility.Visible[i] = 1
	}
	f.Visibility.SeaLevel = numeric.Fixed(60 << 16)

	submerged := frame.UnitView{Slot: 1, Owner: 1, X: numeric.Fixed(4 << 16), Y: numeric.Fixed(59 << 16), Z: numeric.Fixed(40 << 16)}
	if SnapshotVisible(f, submerged, 0) {
		t.Fatal("a base height below sea level was visible without the exemption bit")
	}
	exempt := submerged
	exempt.UnderwaterExempt = true
	if !SnapshotVisible(f, exempt, 0) {
		t.Fatal("the underwater exemption did not admit a submerged unit")
	}
	// Exactly at sea level is not below it: the compare is strict.
	atSurface := submerged
	atSurface.Y = f.Visibility.SeaLevel
	if !SnapshotVisible(f, atSurface, 0) {
		t.Fatal("a unit exactly at sea level was rejected; the compare must be strict")
	}
	// A zero sea level must not reject a unit at height zero, which is what a
	// comparison against zero rather than the scaled byte would do.
	f.Visibility.SeaLevel = 0
	ground := submerged
	ground.Y = 0
	if !SnapshotVisible(f, ground, 0) {
		t.Fatal("a unit at height zero was rejected on a map whose sea level is zero")
	}
}

// TestHullGateOwnerAndCloakPrecedeDepth locks the step order: the owner bypass
// and the cloak early-out both run before the depth test, so an owner sees its
// own submarine and a cloaked foreign unit is rejected for cloak rather than
// reaching the samples [03 §3.2].
func TestHullGateOwnerAndCloakPrecedeDepth(t *testing.T) {
	f := hullGateFrame(4, 4)
	for i := range f.Visibility.Visible {
		f.Visibility.Visible[i] = 1
	}
	f.Visibility.SeaLevel = numeric.Fixed(60 << 16)

	own := frame.UnitView{Slot: 1, Owner: 0, Y: numeric.Fixed(10 << 16)}
	if !SnapshotVisible(f, own, 0) {
		t.Fatal("owner bypass did not precede the depth test")
	}
	cloaked := frame.UnitView{Slot: 2, Owner: 1, X: numeric.Fixed(4 << 16), Y: numeric.Fixed(70 << 16), Z: numeric.Fixed(40 << 16), Cloaked: true}
	if SnapshotVisible(f, cloaked, 0) {
		t.Fatal("a cloaked foreign unit was admitted by the samples")
	}
	cloaked.Decloaking = true
	if !SnapshotVisible(f, cloaked, 0) {
		t.Fatal("a decloaking unit did not fall through to the samples")
	}
}

// TestHullGateNorthUsesWholeHeightWord locks that the north samples subtract
// the whole published height word and not half of it [03 §3.2] step 5. The
// numbered list's "half-height subtracted from the height" is corrected by the
// sample table's `Y - ey` and by the paragraph naming `ey` a definition field
// that is "neither ... half of the unit's height".
//
// The unit stands 64 world units up, one visibility tile of shear. With the
// whole word the north samples land back on row 0; with half of it they land
// half a tile short and the lit cell never admits.
func TestHullGateNorthUsesWholeHeightWord(t *testing.T) {
	f := hullGateFrame(4, 4)
	lightCell(f, 0, 0)
	unit := frame.UnitView{
		Slot:        1,
		Owner:       1,
		X:           numeric.Fixed(4 << 16),
		Y:           numeric.Fixed(64 << 16),
		Z:           numeric.Fixed(4 << 16),
		HullYExtent: numeric.Fixed(64 << 16),
	}
	if !SnapshotVisible(f, unit, 0) {
		t.Fatal("the whole height word did not bring the north samples back to row 0")
	}
	halved := unit
	halved.HullYExtent /= 2
	if SnapshotVisible(f, halved, 0) {
		t.Fatal("half the height word admitted; the gate must subtract the whole definition field")
	}
}
