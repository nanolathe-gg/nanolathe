package ai

import (
	"testing"
)

// The scatter helper's acceptance limit is `surfaceMetal * footZ * footX * 2`
// and the value it is compared against is the trial footprint's own metal-byte
// sum, which on a canonical map is `surfaceMetal * footX * footZ` — exactly
// half the limit [08 R-AI-03 §4]. The two therefore agree only while the
// manager's SurfaceMetal word is the same authored word that seeded the
// terrain's per-cell metal byte [08 R-AI-03 §4-A]. This test locks that
// relationship in both directions, because a manager whose word is zero
// rejects every geometrically valid site and the computer player then places
// nothing but metal extractors.
func TestScatterLimitTracksUniformSurfaceMetal(t *testing.T) {
	const uniform = 3
	cat := placementCatalog("armsolar", "oooo", 0)
	ter := placementTerrain(40, 40, uniform)

	matched := makePlacementManager(cat, ter, uniform)
	res := PlaceCandidate(matched, "armsolar", ter)
	if !res.Valid || res.Helper != HelperB {
		t.Fatalf("a site whose footprint sum is half the limit must be accepted: %+v", res)
	}
	if want := int32(uniform * 2 * 2 * 2); res.Limit != want {
		t.Fatalf("limit = %d, want surfaceMetal*footZ*footX*2 = %d", res.Limit, want)
	}
	if res.Score*2 != res.Limit {
		t.Fatalf("uniform-seed footprint sum %d is not half the limit %d", res.Score, res.Limit)
	}

	zeroed := makePlacementManager(cat, ter, 0)
	res = PlaceCandidate(zeroed, "armsolar", ter)
	if res.Valid {
		t.Fatalf("a zero SurfaceMetal word makes the limit zero and must reject: %+v", res)
	}
	if res.Reason != ReasonTooManyTrials {
		t.Fatalf("reason = %v, want the thirty-trial exhaustion", res.Reason)
	}
	exceeded := 0
	for _, reason := range res.TrialReasons {
		if reason == ReasonMetalScoreExceeded {
			exceeded++
		}
	}
	if exceeded == 0 {
		t.Fatalf("every geometrically valid trial should have failed the limit, got %v", res.TrialReasons)
	}
}
