package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestImpactArtHolderRules locks the explosion-art holder selection of
// [06 R-WFX-01 §1]. Three separate rules meet here and each of them was wrong
// before: the identity is a PAIR of authored keys, both keys are required, and
// a weapon has two holders — land, and one water-or-lava chosen by the map.
//
// The retired form published the GAF bank name as the whole identity and fell
// back to the ART name when the bank key was absent, so a composer received a
// file name where it expected an entry, or an entry with no bank, and could
// resolve neither.
func TestImpactArtHolderRules(t *testing.T) {
	ordinary := &world.Terrain{}
	lava := &world.Terrain{LavaWorld: true}

	full := &content.WeaponDef{
		ExplosionGaf: "fx", ExplosionArt: "explode2",
		WaterExplosionGaf: "fx", WaterExplosionArt: "h2oboom1",
		LavaExplosionGaf: "fx", LavaExplosionArt: "lavasplash",
	}

	if bank, entry := impactArt(full, false, ordinary); bank != "fx" || entry != "explode2" {
		t.Fatalf("land impact art = %q/%q, want fx/explode2", bank, entry)
	}
	// The water arm is the same predicate on every map; only which pair filled
	// the holder differs.
	if bank, entry := impactArt(full, true, ordinary); bank != "fx" || entry != "h2oboom1" {
		t.Fatalf("water impact art = %q/%q, want the WATER pair", bank, entry)
	}
	if bank, entry := impactArt(full, true, lava); bank != "fx" || entry != "lavasplash" {
		t.Fatalf("water impact on a lava world = %q/%q, want the LAVA pair", bank, entry)
	}
	// A lava world never consults the water pair. Stock content leaves 41 of
	// 198 weapons without a lava pair, and those correctly show nothing.
	waterOnly := &content.WeaponDef{
		ExplosionGaf: "fx", ExplosionArt: "explode2",
		WaterExplosionGaf: "fx", WaterExplosionArt: "h2oboom1",
	}
	if bank, entry := impactArt(waterOnly, true, lava); bank != "" || entry != "" {
		t.Fatalf("a lava world fell back to the water pair (%q/%q); the other pair is never read", bank, entry)
	}

	// Both keys or nothing — in either direction, on either holder.
	bankOnly := &content.WeaponDef{ExplosionGaf: "fx"}
	if _, entry := impactArt(bankOnly, false, ordinary); entry != "" {
		t.Fatalf("a bank with no entry name resolved %q; the holder stays null", entry)
	}
	entryOnly := &content.WeaponDef{ExplosionArt: "explode2"}
	if bank, entry := impactArt(entryOnly, false, ordinary); bank != "" || entry != "" {
		t.Fatalf("an entry with no bank resolved %q/%q; the holder stays null", bank, entry)
	}
	if bank, entry := impactArt(&content.WeaponDef{}, true, ordinary); bank != "" || entry != "" {
		t.Fatalf("an unauthored water holder resolved %q/%q", bank, entry)
	}
}
