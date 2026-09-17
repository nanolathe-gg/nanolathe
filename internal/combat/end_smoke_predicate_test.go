package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestEndSmokeArmPredicate locks the one predicate that selects the central
// impact's two arms: the water arm is taken only when the impact cell is water
// AND no unit was hit directly, and the end-smoke flag is tested inside the
// land/direct arm, where the end puff replaces the explosion art
// [06 §13.2][06 R-WFX-01 §2].
//
// The load-bearing row is {water cell, direct unit}: it takes the land/direct
// arm, so an end-smoke weapon emits the end puff and no art there, and a weapon
// without the flag takes the LAND art holder over water. Gating end smoke on
// the cell alone inverted both.
func TestEndSmokeArmPredicate(t *testing.T) {
	endSmoke := &content.WeaponDef{
		SoundHit:          "hit",
		SoundWater:        "splash",
		ExplosionGaf:      "explo.gaf",
		ExplosionArt:      "exploart",
		WaterExplosionGaf: "wexplo.gaf",
		WaterExplosionArt: "wexploart",
		EndSmoke:          true,
	}
	plain := &content.WeaponDef{
		SoundHit:          "hit",
		SoundWater:        "splash",
		ExplosionGaf:      "explo.gaf",
		ExplosionArt:      "exploart",
		WaterExplosionGaf: "wexplo.gaf",
		WaterExplosionArt: "wexploart",
	}
	cases := []struct {
		name   string
		weapon *content.WeaponDef
		direct bool
		water  bool
		want   []string
	}{
		{"end smoke, land cell, no direct unit", endSmoke, false, false, []string{"hit:hit", "endsmoke"}},
		{"end smoke, land cell, direct unit", endSmoke, true, false, []string{"hit:hit", "endsmoke"}},
		{"end smoke, water cell, direct unit", endSmoke, true, true, []string{"hit:hit", "endsmoke"}},
		{"end smoke, water cell, no direct unit", endSmoke, false, true, []string{"water:splash", "watergaf:wexplo.gaf/wexploart"}},
		{"no end smoke, land cell, no direct unit", plain, false, false, []string{"hit:hit", "landgaf:explo.gaf/exploart"}},
		{"no end smoke, land cell, direct unit", plain, true, false, []string{"hit:hit", "landgaf:explo.gaf/exploart"}},
		{"no end smoke, water cell, direct unit", plain, true, true, []string{"hit:hit", "landgaf:explo.gaf/exploart"}},
		{"no end smoke, water cell, no direct unit", plain, false, true, []string{"water:splash", "watergaf:wexplo.gaf/wexploart"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runImpactPresentation(tc.weapon, tc.direct, tc.water).events
			if len(got) != len(tc.want) {
				t.Fatalf("presentation events [06 §13.2]: got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("presentation events [06 §13.2]: got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
