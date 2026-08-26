package content

import "testing"

func TestUnitPlacementProfileAuthoredAndDefaults(t *testing.T) {
	authored := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMSOLAR;
 MaxSlope=10;
 MaxWaterDepth=12;
 MinWaterDepth=3;
 Waterline=2;
}
`).Root.Sections()[0], "units/armsolar.fbi", "", Provenance{})
	if authored.MaxSlope != 10 || authored.MaxWaterDepth != 12 || authored.MinWaterDepth != 3 || authored.Waterline != 2 {
		t.Fatalf("authored placement profile = slope %d maxwater %d minwater %d waterline %d, want 10/12/3/2", authored.MaxSlope, authored.MaxWaterDepth, authored.MinWaterDepth, authored.Waterline)
	}
	for _, key := range []string{"MaxSlope", "MaxWaterDepth", "MinWaterDepth"} {
		for unknown := range authored.Unknown {
			if unknown == key {
				t.Fatalf("typed placement key %q retained as inert Unknown", key)
			}
		}
	}

	defaults := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMLAB;
}
`).Root.Sections()[0], "units/armlab.fbi", "", Provenance{})
	if defaults.MaxSlope != 0 || defaults.MaxWaterDepth != 0 || defaults.MinWaterDepth != 0 || defaults.Waterline != 0 {
		t.Fatalf("default placement profile = slope %d maxwater %d minwater %d waterline %d, want all zero", defaults.MaxSlope, defaults.MaxWaterDepth, defaults.MinWaterDepth, defaults.Waterline)
	}
}
