package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// placeWreck stamps one 3D definition at the anchor the way a corpse is placed
// and returns its live instance, which the stamp leaves with a zero accumulator.
func placeWreck(t *testing.T, svc *Service, def *content.FeatureDef, cx, cz int) *Instance {
	t.Helper()
	inst := svc.PlaceCorpse(world.Cell{X: int32(cx), Z: int32(cz)}, [3]numeric.Fixed{
		world.CellToWorld(int32(cx)), numeric.Fixed(10 * 65536), world.CellToWorld(int32(cz)),
	}, Orientation{}, def, false, 0)
	if inst == nil {
		t.Fatal("corpse refused")
	}
	if inst.DamageAccumulator != 0 {
		t.Fatalf("stamp left accumulator %#x, want 0", inst.DamageAccumulator)
	}
	return inst
}

// TestThreeDDamageAccumulates locks step 7 of [05 R-FEAT-01 §8]: hits add into
// the instance's 16-bit accumulator with wrap, and the wreck dies on the first
// hit that leaves the definition's `damage` at or below it, compared unsigned.
// The definition's damage is a stored 16-bit word and the weapon's default an
// int16, so the int32 parameters are truncated to those words.
func TestThreeDDamageAccumulates(t *testing.T) {
	cases := []struct {
		name   string
		damage int32   // definition `damage`
		hits   []int32 // successive weapon `[DAMAGE] default` words
		dead   []bool  // expected return of each DamageFeature call
		acc    uint16  // accumulator after the last hit when it survived
	}{
		{
			// (a) at-or-above, not strictly above: 99 survives, 100 kills.
			name:   "dies at exactly damage <= accumulator",
			damage: 100, hits: []int32{40, 40, 19, 1}, dead: []bool{false, false, false, true},
		},
		{
			// (b) 16-bit wrap: 0xFFF0 + 0x20 wraps to 0x0010, below the
			// threshold again, so the wreck survives a hit a countdown would
			// have counted as lethal.
			name:   "wraps at 16 bits",
			damage: 0xFFF8, hits: []int32{0xFFF0, 0x20}, dead: []bool{false, false}, acc: 0x0010,
		},
		{
			// A negative default adds its wrapped word: -1 is 0xFFFF, which
			// with damage 0xFFFF kills on that single hit.
			name:   "negative default adds its 16-bit word",
			damage: 0xFFFF, hits: []int32{-1}, dead: []bool{true},
		},
		{
			// (c) damage = 0 dies on the first hit of any strength, including
			// a zero-damage weapon, because 0 <= accumulator always holds.
			name:   "damage zero dies on first hit",
			damage: 0, hits: []int32{0}, dead: []bool{true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(newTestTerrainP1(6, 6), nil, nil, nil)
			def := defP1("wreck3d", 1, 1, "wreck.3do", "")
			def.Damage = tc.damage
			inst := placeWreck(t, svc, def, 2, 2)
			for i, hit := range tc.hits {
				got := svc.DamageFeature(2, 2, hit)
				if got != tc.dead[i] {
					t.Fatalf("hit %d (%d): dead=%v, want %v (accumulator %#x)", i, hit, got, tc.dead[i], inst.DamageAccumulator)
				}
			}
			last := tc.dead[len(tc.dead)-1]
			if !last && tc.acc != 0 && inst.DamageAccumulator != tc.acc {
				t.Fatalf("accumulator %#x, want %#x", inst.DamageAccumulator, tc.acc)
			}
			if last && svc.InstanceAt(2, 2) != nil {
				t.Fatal("dead wreck still has a live instance")
			}
		})
	}
}

// TestNoInstanceDamageSumIsUnsigned locks step 6 of [05 R-FEAT-01 §8]: the
// anchor word's sum is formed in 32 bits from the two zero-extended words and
// compared unsigned against the definition's 16-bit damage, so a sum past 16
// bits always dies rather than wrapping, and a negative default is its unsigned
// word.
func TestNoInstanceDamageSumIsUnsigned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage int32
		word   uint16
		hit    int32
		dead   bool
		stored uint16
	}{
		{"below threshold stores the sum", 100, 30, 40, false, 70},
		{"at threshold dies", 100, 60, 40, true, 0},
		{"sum past 16 bits dies instead of wrapping", 0xFFF8, 0xFFF0, 0x20, true, 0},
		{"negative default is its unsigned word", 0xFFFF, 0, -1, true, 0},
		{"damage zero dies on a zero hit", 0, 0, 0, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terrain := newTestTerrainP1(4, 4)
			svc := NewService(terrain, nil, nil, nil)
			def := defP1("bush", 1, 1, "", "bush.gaf")
			def.Damage = tc.damage
			if svc.PlaceAt(1, 1, def) == nil {
				t.Fatal("placement refused")
			}
			idx := 1*int(terrain.CellW) + 1
			terrain.Plot[idx].SetAnchorWord(tc.word)
			if got := svc.DamageFeature(1, 1, tc.hit); got != tc.dead {
				t.Fatalf("dead=%v, want %v", got, tc.dead)
			}
			if !tc.dead && terrain.Plot[idx].AnchorWord() != tc.stored {
				t.Fatalf("anchor word %#x, want %#x", terrain.Plot[idx].AnchorWord(), tc.stored)
			}
		})
	}
}

// TestThreeDDamageSurvivesSaveRoundTrip locks (d): a partly damaged wreck's
// accumulator rides the 3D save record's 0x06..0x07 word and is restored after
// the stamp [08 R-SAVE-FEATURE-01], so the reloaded wreck dies on the same next
// hit the unsaved one would have.
func TestThreeDDamageSurvivesSaveRoundTrip(t *testing.T) {
	svc := NewService(newTestTerrainP1(8, 8), nil, nil, nil)
	wreck := defP1("savewreck", 1, 1, "savewreck.3do", "")
	wreck.Damage = 100
	placeWreck(t, svc, wreck, 3, 4)
	if svc.DamageFeature(3, 4, 70) {
		t.Fatal("70 of 100 killed the wreck")
	}
	image, err := svc.RetailFeatureImage()
	if err != nil || len(image.ThreeD) != 1 {
		t.Fatalf("image: %d 3D records, err=%v", len(image.ThreeD), err)
	}

	reloaded := NewService(newTestTerrainP1(8, 8), nil, nil, nil)
	back, err := reloaded.RestoreAt(3, 4, wreck, 2, image.ThreeD[0].Data)
	if err != nil || back == nil {
		t.Fatalf("restore: inst=%v err=%v", back, err)
	}
	if back.DamageAccumulator != 70 {
		t.Fatalf("restored accumulator %d, want 70", back.DamageAccumulator)
	}
	if reloaded.DamageFeature(3, 4, 29) {
		t.Fatal("99 of 100 killed the reloaded wreck")
	}
	if !reloaded.DamageFeature(3, 4, 1) {
		t.Fatal("100 of 100 did not kill the reloaded wreck")
	}
	if reloaded.InstanceAt(3, 4) != nil {
		t.Fatal("dead wreck still has a live instance after reload")
	}
}
