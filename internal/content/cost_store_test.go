package content

import (
	"fmt"
	"testing"
)

// The integer accessor precedes a single-float store [02 R-KEYS-01 §5].
func TestUnitCostsStoreSinglePrecision(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   float32
	}{
		{"16777215", 16777215}, {"16777216", 16777216},
		{"16777217", 16777216}, {"16777219", 16777220},
		{"-16777217", -16777216}, {"2147483647", 2147483648},
		{"16777217.9", 16777216},
	} {
		t.Run(tc.source, func(t *testing.T) {
			section := mustParseTDF(t, fmt.Sprintf("[UNITINFO]{unitname=cost; buildcostenergy=%s; buildcostmetal=%s; buildtime=16777217;}", tc.source, tc.source)).Root.Sections()[0]
			def := compileUnitSection(section, "units/cost.fbi", "", Provenance{})
			if def.BuildCostEnergy != tc.want || def.BuildCostMetal != tc.want {
				t.Fatalf("stored costs = %v/%v, want %v", def.BuildCostEnergy, def.BuildCostMetal, tc.want)
			}
			if def.BuildTime != 16777217 {
				t.Fatalf("integer buildtime changed: %d", def.BuildTime)
			}
		})
	}
}
