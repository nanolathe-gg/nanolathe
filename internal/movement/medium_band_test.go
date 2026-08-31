package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestMediumBandClassifier locks the four structural properties of the
// `setSFXoccupy` classifier of [04 §9.1] that the previous invented mapping
// got wrong: the mover-mode gate owns band 0, above-water is 4 (not 0), the
// underwater tests are ordered overwrites starting from the cached band, and a
// visit that matches no test retains the cache.
func TestMediumBandClassifier(t *testing.T) {
	const sea = 10
	ter := syntheticWaterTer(16, 16, sea)
	def := &content.UnitDef{UnitName: "band", Waterline: 3}

	at := func(y int32, mode uint8) *units.Unit {
		u := &units.Unit{Def: def, Y: numeric.Fixed(int64(y) * 65536)}
		u.Move.Mode = mode
		return u
	}

	// Mode gate: 0 (attached/parked) and 3 (save-installed) are band 0 whatever
	// the height, and they never consult the cache [04 R-MOV-01 §8].
	for _, mode := range []uint8{0, 3} {
		if got := MediumBand(ter, at(sea+5, mode), 2); got != 0 {
			t.Fatalf("mode %d band = %d, want 0", mode, got)
		}
	}

	// Strictly above water is 4, and it too ignores the cache.
	if got := MediumBand(ter, at(sea+1, 1), 2); got != 4 {
		t.Fatalf("above water band = %d, want 4", got)
	}
	// Exactly at sea level is not above it: this unit's waterline is 3, so the
	// skirt test wins and the draft test does not.
	if got := MediumBand(ter, at(sea, 1), 0); got != 1 {
		t.Fatalf("at sea level band = %d, want 1 (skirt)", got)
	}

	// The skirt is the five units below sea level, exclusive: wy-wt > -5.
	if got := MediumBand(ter, at(sea-4, 1), 0); got != 1 {
		t.Fatalf("four below sea band = %d, want 1", got)
	}
	// wy-wt == -5 fails the skirt test; with the draft test matching at
	// waterline 3... it does not (3 + 5 != 10), so the cache is retained.
	if got := MediumBand(ter, at(sea-5, 1), 3); got != 3 {
		t.Fatalf("no test matched: band = %d, want the cached 3", got)
	}
	if got := MediumBand(ter, at(sea-5, 1), 0); got != 0 {
		t.Fatalf("no test matched with cache 0: band = %d, want 0", got)
	}

	// Draft exactly at the surface overwrites the skirt: wl + wy == wt with
	// waterline 3 is wy == 7, which is three below sea level and so also
	// inside the skirt. The later test wins.
	if got := MediumBand(ter, at(sea-3, 1), 0); got != 2 {
		t.Fatalf("draft at surface band = %d, want 2 (overwrites 1)", got)
	}

	// An airborne mover classifies on height like any other; mode 2 is not a
	// bypass.
	if got := MediumBand(ter, at(sea-3, 2), 0); got != 2 {
		t.Fatalf("airborne draft band = %d, want 2", got)
	}
}
