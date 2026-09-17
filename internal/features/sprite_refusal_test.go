package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// spriteTree is a reclaimable sprite (filename-bearing) definition — the shape
// of every stock ignitable, e.g. `tree1` with `filename trees`
// [05 R-WORK-01 §5-A].
func spriteTree(name string) *content.FeatureDef {
	def := featureDef(name, 0, 250, 0)
	def.Filename = "trees"
	def.Reclaimable = true
	return def
}

// wreck3D is a reclaimable 3D definition — an authored `object` and no
// `filename`, the shape of `armaap_dead`.
func wreck3D(name string) *content.FeatureDef {
	def := featureDef(name, 1768, 0, 1680)
	def.Object = "armaap_dead"
	def.Reclaimable = true
	return def
}

func stampFor(t *testing.T, def *content.FeatureDef, cx, cz int) (*world.Terrain, *Service) {
	t.Helper()
	terrain := newEmptyTerrain(4, 4)
	terrain.FeatureDefs = []*content.FeatureDef{def}
	svc := NewService(terrain, nil, nil, nil)
	if inst := svc.spawnFeatureAt(cx, cz, def); inst == nil {
		t.Fatalf("could not stamp %s at (%d,%d)", def.CanonicalKey, cx, cz)
	}
	return terrain, svc
}

// TestSpriteWithLiveInstanceRefusesTheReclaimPayout is the payout guard of
// [05 R-FEAT-01 §15]: the transition refuses when the instance-attached bit of
// the cell the recorded position resolves to and the definition's sprite bit
// are both set — "a sprite feature that currently has a live animation
// instance", which is what "burning blocks reclaim" means. Here the recorded
// position is the anchor; TestReclaimGuardReadsTheRecordedCell separates them.
func TestSpriteWithLiveInstanceRefusesTheReclaimPayout(t *testing.T) {
	terrain, _ := stampFor(t, spriteTree("tree1"), 1, 1)

	// A freshly stamped sprite has no animation instance attached, so the
	// conjunction is false and the transition proceeds.
	metal, energy, ok := ReclaimTransition(terrain, 1, 1)
	if !ok || energy != 250 || metal != 0 {
		t.Fatalf("resting sprite: (%v, %v, %v), want the whole pool paid once", metal, energy, ok)
	}

	// With the anchor's instance-attached bit raised — what ignition and the
	// die/reclaim animation starts both do — the same transition refuses.
	terrain, _ = stampFor(t, spriteTree("tree1"), 2, 2)
	terrain.PlotAt(2, 2).SetOccupied(true)
	metal, energy, ok = ReclaimTransition(terrain, 2, 2)
	if ok || metal != 0 || energy != 0 {
		t.Fatalf("burning sprite: (%v, %v, %v), want a refusal with nothing paid [05 R-FEAT-01 §15]", metal, energy, ok)
	}
	if !terrain.PlotAt(2, 2).IsRealFeature() {
		t.Fatal("a refused transition must leave the feature standing")
	}
}

// TestThreeDWreckStaysReclaimableWithItsInstanceBitSet is the guard's bounded
// negative: the stamp gives a 3D definition the instance-attached bit from
// birth, but its definition bit is clear, so the conjunction never holds and a
// sinking wreck stays reclaimable throughout [05 R-FEAT-01 §15].
func TestThreeDWreckStaysReclaimableWithItsInstanceBitSet(t *testing.T) {
	terrain, _ := stampFor(t, wreck3D("armaap_dead"), 1, 1)
	if !terrain.PlotAt(1, 1).Occupied() {
		t.Fatal("the stamp did not set a 3D definition's instance-attached bit [05 R-FEAT-01 §15]")
	}
	metal, energy, ok := ReclaimTransition(terrain, 1, 1)
	if !ok || metal != 1768 || energy != 0 {
		t.Fatalf("3D wreck: (%v, %v, %v), want the metal pool paid [05 R-FEAT-01 §15]", metal, energy, ok)
	}
}

// TestReclaimGuardReadsTheRecordedCell locks which cell each of the guard's two
// bits comes from [05 R-FEAT-01 §15]: the cell bit is read from the cell the
// order's recorded position resolves to, BEFORE the hop to the anchor, while
// the definition bit comes from the anchor's catalog entry. A multi-cell sprite
// definition reclaimed from one of its fringe cells therefore reads that fringe
// cell's bit, which the stamp leaves clear, and is paid out even while the
// anchor carries a live animation instance. Reading the anchor for both bits
// refuses instead, which is the divergence this locks.
func TestReclaimGuardReadsTheRecordedCell(t *testing.T) {
	multiCell := func() *content.FeatureDef {
		def := spriteTree("grove")
		def.FootprintX, def.FootprintZ = 2, 2
		return def
	}

	// From the anchor cell the recorded position IS the anchor, so a live
	// instance refuses, exactly as it does for a one-cell tree.
	terrain, _ := stampFor(t, multiCell(), 1, 1)
	terrain.PlotAt(1, 1).SetOccupied(true)
	if metal, energy, ok := ReclaimTransition(terrain, 1, 1); ok || metal != 0 || energy != 0 {
		t.Fatalf("anchor cell: (%v, %v, %v), want a refusal with nothing paid [05 R-FEAT-01 §15]", metal, energy, ok)
	}

	// From a fringe cell of the same feature the guard reads the fringe cell's
	// clear bit and pays; everything after the guard is still the anchor's, so
	// the whole footprint is cleared.
	terrain, _ = stampFor(t, multiCell(), 1, 1)
	terrain.PlotAt(1, 1).SetOccupied(true)
	if !terrain.PlotAt(2, 1).IsFringe() {
		t.Fatal("the stamp did not make (2,1) a fringe member of the (1,1) anchor")
	}
	metal, energy, ok := ReclaimTransition(terrain, 2, 1)
	if !ok || energy != 250 || metal != 0 {
		t.Fatalf("fringe cell: (%v, %v, %v), want the whole pool paid once [05 R-FEAT-01 §15]", metal, energy, ok)
	}
	if terrain.PlotAt(1, 1).IsRealFeature() || terrain.PlotAt(2, 2).IsFringe() {
		t.Fatal("the payout must clear the ANCHOR's footprint, not the recorded cell's")
	}
}

// TestStampLeavesASpriteAnchorClear is the other half of the same write: a
// sprite definition acquires the bit only when an animation instance actually
// attaches, never at the stamp — otherwise every stock tree would be born
// unreclaimable.
func TestStampLeavesASpriteAnchorClear(t *testing.T) {
	terrain, _ := stampFor(t, spriteTree("tree1"), 1, 1)
	if terrain.PlotAt(1, 1).Occupied() {
		t.Fatal("the stamp set a sprite definition's instance-attached bit [05 R-FEAT-01 §15]")
	}
}
