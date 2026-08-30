package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestMetalDepositSeedsFootprint locks [05 R-FEAT-01 §7]: the deposit pass
// runs after the uniform seed and after the feature stamps, writes the low
// byte of the definition's metal over the whole anchor footprint, and writes
// nothing for a reclaimable feature that merely carries metal. Without it a
// canonical map keeps the uniform seed everywhere and every extractor yields
// the bare footprint count, which was the reported playtest defect.
func TestMetalDepositSeedsFootprint(t *testing.T) {
	deposit := &content.FeatureDef{FootprintX: 3, FootprintZ: 3, Metal: 86, Indestructible: true}
	rock := &content.FeatureDef{FootprintX: 2, FootprintZ: 2, Metal: 100}

	attrs := flat(8, 8, 1)
	attrs[1*8+1].Feature = 0 // deposit anchor at (1,1), footprint (1..3)
	attrs[1*8+5].Feature = 1 // reclaimable rock anchor at (5,1)
	ter := synth(t, 8, 8, attrs, []*content.FeatureDef{deposit, rock})

	// Uniform seed first, exactly as battle setup orders it.
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 4}}}, 0); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}
	ter.SeedFeatureMetalDeposits()

	for cz := int32(1); cz <= 3; cz++ {
		for cx := int32(1); cx <= 3; cx++ {
			if got := ter.PlotAt(cx, cz).Metal(); got != 86 {
				t.Fatalf("deposit cell (%d,%d) metal = %d, want 86", cx, cz, got)
			}
		}
	}
	// One cell past the footprint keeps the uniform seed: the deposit
	// overrides inside its footprint and nowhere else.
	if got := ter.PlotAt(4, 1).Metal(); got != 4 {
		t.Fatalf("cell (4,1) metal = %d, want the uniform seed 4", got)
	}
	// A reclaimable rock's metal is reclaim reward only — it never seeds.
	if got := ter.PlotAt(5, 1).Metal(); got != 4 {
		t.Fatalf("reclaimable rock cell (5,1) metal = %d, want the uniform seed 4", got)
	}

	// Battle setup runs the pass once per placement source — after the
	// terrain-file stamps, and again after each mission feature-placement
	// helper — so a second run over an unchanged plot must change nothing.
	snapshot := make([]PlotCell, len(ter.Plot))
	copy(snapshot, ter.Plot)
	ter.SeedFeatureMetalDeposits()
	for i := range ter.Plot {
		if ter.Plot[i] != snapshot[i] {
			t.Fatalf("second SeedFeatureMetalDeposits changed plot cell %d: %v -> %v", i, snapshot[i], ter.Plot[i])
		}
	}

	// The extractor sum reads those bytes: nine cells of 86 plus one each.
	got, err := ter.SampleMetal(1, 1, 3, 3, 1)
	if err != nil {
		t.Fatalf("SampleMetal: %v", err)
	}
	if got != 9*87 {
		t.Fatalf("SampleMetal over the deposit = %v, want %d", got, 9*87)
	}
}
