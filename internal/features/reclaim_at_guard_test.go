package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestReclaimAtReadsTheGuardsCellBitFromTheRecordedCell locks the service-owned
// payout entry — the one a composed session binds `ReclaimFeature` to — against
// the same contract the terrain-only transition holds [05 R-FEAT-01 §15]
// [05 R-WORK-01 §5]: the guard's instance-attached cell bit is read from the
// cell the order's recorded position resolves to, BEFORE the hop to the anchor,
// while the definition bit comes from the anchor's catalog entry.
//
// Reading the anchor for both bits — which is what this entry did while the
// executor hopped for it — refuses the fringe row, so that row is the divergence
// this locks.
func TestReclaimAtReadsTheGuardsCellBitFromTheRecordedCell(t *testing.T) {
	grove := func() *content.FeatureDef {
		def := spriteTree("grove")
		def.FootprintX, def.FootprintZ = 2, 2
		return def
	}

	// From the anchor cell the recorded position IS the anchor, so its raised
	// bit and the sprite definition bit conjoin and the payout is refused.
	terrain, svc := stampFor(t, grove(), 1, 1)
	terrain.PlotAt(1, 1).SetOccupied(true)
	if metal, energy, ok := svc.ReclaimAt(1, 1); ok || metal != 0 || energy != 0 {
		t.Fatalf("anchor cell: (%v, %v, %v), want a refusal with nothing paid [05 R-FEAT-01 §15]", metal, energy, ok)
	}
	if !terrain.PlotAt(1, 1).IsRealFeature() {
		t.Fatal("a refused payout must leave the feature standing")
	}

	// From a fringe cell of the same feature the guard reads that cell's clear
	// bit and pays; everything after the guard is still the anchor's, so the
	// whole footprint is settled.
	terrain, svc = stampFor(t, grove(), 1, 1)
	terrain.PlotAt(1, 1).SetOccupied(true)
	if !terrain.PlotAt(2, 1).IsFringe() {
		t.Fatal("the stamp did not make (2,1) a fringe member of the (1,1) anchor")
	}
	metal, energy, ok := svc.ReclaimAt(2, 1)
	if !ok || energy != 250 || metal != 0 {
		t.Fatalf("fringe cell: (%v, %v, %v), want the whole pool paid once [05 R-FEAT-01 §15]", metal, energy, ok)
	}
	if terrain.PlotAt(1, 1).IsRealFeature() || terrain.PlotAt(2, 2).IsFringe() {
		t.Fatal("the payout must settle the ANCHOR's footprint, not the recorded cell's")
	}
}

// TestReclaimAtSingleCellGuardIsUnchanged is the bounded negative of the cell
// correction: for a one-cell feature the recorded position and the anchor are
// the same cell, so both halves of the guard behave exactly as before — a
// resting sprite pays, one carrying a live animation instance does not, and a
// 3D wreck pays throughout because its definition bit is clear
// [05 R-FEAT-01 §15].
func TestReclaimAtSingleCellGuardIsUnchanged(t *testing.T) {
	_, svc := stampFor(t, spriteTree("tree1"), 1, 1)
	if metal, energy, ok := svc.ReclaimAt(1, 1); !ok || energy != 250 || metal != 0 {
		t.Fatalf("resting sprite: (%v, %v, %v), want the whole pool paid once", metal, energy, ok)
	}

	terrain, svc := stampFor(t, spriteTree("tree1"), 2, 2)
	terrain.PlotAt(2, 2).SetOccupied(true)
	if metal, energy, ok := svc.ReclaimAt(2, 2); ok || metal != 0 || energy != 0 {
		t.Fatalf("burning sprite: (%v, %v, %v), want a refusal with nothing paid", metal, energy, ok)
	}

	terrain, svc = stampFor(t, wreck3D("armaap_dead"), 1, 1)
	if !terrain.PlotAt(1, 1).Occupied() {
		t.Fatal("the stamp did not set a 3D definition's instance-attached bit")
	}
	if metal, energy, ok := svc.ReclaimAt(1, 1); !ok || metal != 1768 || energy != 0 {
		t.Fatalf("3D wreck: (%v, %v, %v), want the metal pool paid [05 R-FEAT-01 §15]", metal, energy, ok)
	}
}
