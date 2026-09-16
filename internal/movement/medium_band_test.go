package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// bandUnit is a mover at whole height wy in the given committed mover mode.
func bandUnit(def *content.UnitDef, wy int32, mode uint8) *units.Unit {
	u := &units.Unit{Def: def, Y: numeric.Fixed(int64(wy) * 65536)}
	u.Move.Mode = mode
	return u
}

// TestMediumBandClassifier locks the four structural properties of the
// `setSFXoccupy` classifier of [04 §9.1] that the previous invented mapping
// got wrong: the mover-mode gate owns band 0, above-water is 4 (not 0), the
// underwater tests are ordered overwrites starting from the cached band, and a
// visit that matches no test retains the cache.
func TestMediumBandClassifier(t *testing.T) {
	const sea = 10
	ter := syntheticWaterTer(16, 16, sea)
	// A twelve-world-unit-tall model with a three-unit draft. The model top is
	// tall enough that none of the shallow cases below can reach band 3, which
	// is what keeps these assertions about bands 1 and 2 [04 R-MOV-01 §8a].
	def := &content.UnitDef{UnitName: "band", Waterline: 3, ModelTop: 12, ModelTopFixed: 12 << 16}

	at := func(y int32, mode uint8) *units.Unit { return bandUnit(def, y, mode) }

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
	// wy-wt == -5 fails the skirt test, the draft test does not match either
	// (3 + 5 != 10) and the model top is far above water, so the cache stands.
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

// TestMediumBandThreeIsReachable locks the band-3 overwrite of
// [04 R-MOV-01 §8a] — `mt + wy < wt`, where `mt` is the definition's model
// total-height word, so band 3 means the whole model sits below the water
// level. It was unreachable in this build while the operand was misidentified
// as a nonexistent model-bottom field; the walk it was waiting on does not
// exist ([02 R-CAT-01 §7] zeroes the definition's minimum-Y word instead).
func TestMediumBandThreeIsReachable(t *testing.T) {
	const sea = 10
	ter := syntheticWaterTer(16, 16, sea)

	// A tall model has to sink far enough that its top passes under the water
	// level: 12 + wy < 10 needs wy < -2.
	tall := &content.UnitDef{UnitName: "tall", Waterline: 3, ModelTop: 12, ModelTopFixed: 12 << 16}
	if got := MediumBand(ter, bandUnit(tall, -2, 1), 0); got != 0 {
		t.Fatalf("top exactly at the surface: band = %d, want the cached 0 (12 + -2 == 10 is not below)", got)
	}
	if got := MediumBand(ter, bandUnit(tall, -3, 1), 0); got != 3 {
		t.Fatalf("submerged band = %d, want 3", got)
	}

	// Band 3 is the last overwrite, so it beats both 1 and 2. A one-unit-tall
	// hull with a three-unit draft at wy == 7 satisfies the draft test
	// (3 + 7 == 10) and the submersion test (1 + 7 < 10) at once.
	flat := &content.UnitDef{UnitName: "flat", Waterline: 3, ModelTop: 1, ModelTopFixed: 1 << 16}
	if got := MediumBand(ter, bandUnit(flat, 7, 1), 0); got != 3 {
		t.Fatalf("draft and submersion both match: band = %d, want 3 (the later test wins)", got)
	}
	// And it beats the skirt on its own: wy == 6 is inside the skirt but the
	// draft test misses.
	if got := MediumBand(ter, bandUnit(flat, 6, 1), 0); got != 3 {
		t.Fatalf("skirt and submersion both match: band = %d, want 3", got)
	}

	// The classifier reads the model top as a signed 16-bit word, not as the
	// byte the LOS emitter takes [04 R-MOV-01 §8a][03 R-P0-18-A §1]. A model
	// 300 world units tall at wy == -40 is not submerged (300 - 40 = 260), but
	// the byte-masked form (300 & 0xFF == 44) would say it is.
	huge := &content.UnitDef{UnitName: "huge", Waterline: 3, ModelTop: 300 & 0xFF, ModelTopFixed: 300 << 16}
	if got := MediumBand(ter, bandUnit(huge, -40, 1), 0); got != 0 {
		t.Fatalf("wide model top: band = %d, want the cached 0; the byte-masked read would give 3", got)
	}

	// Band 3, the fully submerged model, is one of the two bands a stock hover
	// script answers with its wake [04 §9.1].
	if got := MediumBand(ter, bandUnit(tall, -3, 1), 0); got != 3 {
		t.Fatalf("a submerged mover's band = %d, want 3 [04 §9.1]", got)
	}
}
