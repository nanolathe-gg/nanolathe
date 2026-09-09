package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// depositTerrain is a canonical-map shape: every cell carries the uniform
// SurfaceMetal seed and one three-by-three deposit footprint carries the
// raised byte an indestructible metal feature writes over it
// [05 R-FEAT-01 §7][05 R-PROD-01 §6].
func depositTerrain(seed, deposit uint8, cellX, cellZ int32) *world.Terrain {
	t := placementTerrain(64, 64, seed)
	for z := cellZ; z < cellZ+3; z++ {
		for x := cellX; x < cellX+3; x++ {
			t.PlotAt(x, z)[7] = deposit
		}
	}
	return t
}

func extractorPlacementDef(t *testing.T, m *Manager, key string) retailPlacementDef {
	t.Helper()
	pd, failure := resolveRetailPlacementDef(m, key)
	if failure.Proof != nil {
		t.Fatalf("resolve %q: %v", key, failure.Proof)
	}
	return pd
}

// TestExtractorSitesComeFromTheMetalSpotVector answers the WU-19-108 play-test
// report "the AI is building metal extractors in places with no metal".
//
// The exhaustive helper's candidate list *is* the battle-entry metal-spot
// vector, each record shifted by trunc((foot-3)/2), so every site it returns
// covers a deposit; when every deposit candidate is rejected it fails outright
// rather than settling for bare ground, because the whole placement attempt
// fails with it and there is no fall-through to the scatter helper
// [08 R-AI-03 §3].
func TestExtractorSitesComeFromTheMetalSpotVector(t *testing.T) {
	cat := placementCatalog("armmex", "ooooooooo", 0.001)
	def := cat.Units["armmex"]
	def.FootprintX, def.FootprintZ = 3, 3
	terrain := depositTerrain(3, 122, 20, 20)
	m := makePlacementManager(cat, terrain, 3)
	m.Strategic.MetalSpots = []MetalSpot{{CellX: 20, CellZ: 20, Metal: 122}}
	pd := extractorPlacementDef(t, m, "armmex")
	// The search origin is nowhere near the deposit: the helper reaches it
	// through the metal-spot vector, not through the origin's neighbourhood.
	origin := retailPlacementPoint{x: placementWorldCoordinate(2, 3), z: placementWorldCoordinate(2, 3)}

	res := retailExtractorHelperA(m, pd, origin, 160, terrain)
	if !res.Valid || res.CellX != 20 || res.CellZ != 20 {
		t.Fatalf("exhaustive site = (%d,%d) valid=%v, want the deposit anchor (20,20) [08 R-AI-03 §3]", res.CellX, res.CellZ, res.Valid)
	}
	// The score the accumulator carries is the footprint's own metal-byte sum,
	// which on a deposit is nine raised bytes rather than nine seed bytes
	// [08 R-AI-03 §3][05 R-PROD-01 §6].
	if want := int32(9 * 122); res.Score != want {
		t.Fatalf("exhaustive score = %d, want the footprint metal-byte sum %d", res.Score, want)
	}
	if m.RNG.Draws() != 0 {
		t.Fatalf("exhaustive helper drew %d times, want none [08 R-AI-03 §3]", m.RNG.Draws())
	}

	// With the only deposit occupied the helper fails. Bare ground scores
	// 9 x 3 = 27 here, which is positive, so an implementation that let a
	// non-deposit cell into the candidate list would happily accept one; the
	// failure is the proof that it cannot.
	for z := int32(20); z < 23; z++ {
		for x := int32(20); x < 23; x++ {
			terrain.PlotAt(x, z).SetOccupantA(9)
		}
	}
	blocked := retailExtractorHelperA(m, pd, origin, 160, terrain)
	if blocked.Valid {
		t.Fatalf("exhaustive helper accepted (%d,%d) with every deposit blocked, want failure [08 R-AI-03 §3]", blocked.CellX, blocked.CellZ)
	}
	if blocked.Reason != ReasonBlocked {
		t.Fatalf("blocked exhaustive reason = %d, want ReasonBlocked", blocked.Reason)
	}
}

// TestScatterIsTheOnlyExtractorPathToBareGround records where an extractor on
// bare ground legitimately comes from. The scatter helper's acceptance is
// `footprint metal sum <= surfaceMetal * footZ * footX * 2`, so on a canonical
// map a bare footprint sums to exactly half the limit and passes while a
// deposit footprint fails it [08 R-AI-03 §4][08 R-AI-03 §4-A]. An extractor
// definition reaches that helper only when the selector's RNG(255) draw is not
// strictly greater than the mission's SurfaceMetal word — four draws in 255 on
// a map that authors 3 — which is retail's own rate, not a defect
// [08 R-AI-03 §2].
func TestScatterIsTheOnlyExtractorPathToBareGround(t *testing.T) {
	cat := placementCatalog("armmex", "ooooooooo", 0.001)
	def := cat.Units["armmex"]
	def.FootprintX, def.FootprintZ = 3, 3
	terrain := depositTerrain(3, 122, 20, 20)
	m := makePlacementManager(cat, terrain, 3)
	pd := extractorPlacementDef(t, m, "armmex")

	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(40, 40), pd.extent)
	if err != nil {
		t.Fatal(err)
	}
	bare, err := retailPlacementScore(terrain, rect)
	if err != nil {
		t.Fatal(err)
	}
	depositRect, err := world.NewFootprintRect(world.NewFootprintAnchor(20, 20), pd.extent)
	if err != nil {
		t.Fatal(err)
	}
	onDeposit, err := retailPlacementScore(terrain, depositRect)
	if err != nil {
		t.Fatal(err)
	}
	limit := m.SurfaceMetal * pd.footZ * pd.footX * 2
	if bare > limit {
		t.Fatalf("bare-ground sum %d exceeds the scatter limit %d; the uniform seed must sum to half the limit [08 R-AI-03 §4-A]", bare, limit)
	}
	if onDeposit <= limit {
		t.Fatalf("deposit sum %d passes the scatter limit %d; the limit exists to keep the scatter helper off deposits [08 R-AI-03 §4-A]", onDeposit, limit)
	}

	// The selector: strictly greater draws take the exhaustive helper, and only
	// a draw at or below the word reaches the scatter helper [08 R-AI-03 §2].
	var scatterSeed uint32
	found := false
	for s := uint32(1); s < 200000 && !found; s++ {
		r := rng.NewSimulation(s)
		if int32(r.Uint32n(255)) <= m.SurfaceMetal {
			scatterSeed, found = s, true
		}
	}
	if !found {
		t.Skip("no seed in range draws at or below the surface-metal word")
	}
	r := rng.NewSimulation(scatterSeed)
	m2 := makePlacementManager(cat, terrain, 3)
	m2.RNG = &r
	m2.Strategic.MetalSpots = []MetalSpot{{CellX: 20, CellZ: 20, Metal: 122}}
	res := PlaceCandidate(m2, "armmex", terrain)
	if res.Helper != HelperB {
		t.Fatalf("a draw at or below the surface-metal word must select the scatter helper, got helper %v", res.Helper)
	}
	if res.Valid && res.Score > res.Limit {
		t.Fatalf("scatter accepted score %d above limit %d", res.Score, res.Limit)
	}
}
