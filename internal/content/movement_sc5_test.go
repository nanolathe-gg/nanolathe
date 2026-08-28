package content

import "testing"

// TestMovementSC5Resolution locks the SC5 closure: the three clamps run
// unconditionally and every compiled profile is initialized from the startup
// template [04 §6.1 R-DOC04-A], with the eight keys read in parse order
// [02 §5 "Movement class record"].
func TestMovementSC5Resolution(t *testing.T) {
	// Eight keys in parse order; badslope/badwaterslope default to half of the
	// Max value just read [02 §5 "Movement class record"].
	sec := mustParseTDF(t, `[CLASS_TEST]
{
    Name=TestClass;
    FootPrintX=3;
    FootPrintZ=4;
    MaxWaterDepth=10;
    MinWaterDepth=5;
    MaxSlope=32;
    MaxWaterSlope=255;
}
`).Root.Sections()[0]
	mc := compileMovementSection(sec, "TestClass", Provenance{})
	if mc.FootprintX != 3 || mc.FootprintZ != 4 {
		t.Fatalf("footprint got %d/%d want 3/4", mc.FootprintX, mc.FootprintZ)
	}
	if mc.MaxWaterDepth != 10 || mc.MinWaterDepth != 5 {
		t.Fatalf("depths got %d/%d want 10/5", mc.MaxWaterDepth, mc.MinWaterDepth)
	}
	if mc.MaxSlope != 32 {
		t.Fatalf("MaxSlope got %d want 32", mc.MaxSlope)
	}
	if mc.BadSlope != 16 {
		t.Fatalf("BadSlope default Max>>1 got %d want 16", mc.BadSlope)
	}
	if mc.MaxWaterSlope != 255 {
		t.Fatalf("MaxWaterSlope got %d want 255", mc.MaxWaterSlope)
	}
	if mc.BadWaterSlope != 127 {
		t.Fatalf("BadWaterSlope default MaxWater>>1 got %d want 127", mc.BadWaterSlope)
	}

	// Unconditional clamps: an absent maxwaterslope parses to the template
	// 255, so clamp 1 is the identity and the authored MaxSlope survives —
	// the resolution of the old SC5 gated divergence [04 §6.1 R-DOC04-A].
	sec2 := mustParseTDF(t, `[CLASS_KBOT]
{
    Name=kbotsf2;
    FootPrintX=2;
    FootPrintZ=2;
    MaxSlope=32;
}
`).Root.Sections()[0]
	mc2 := compileMovementSection(sec2, "kbotsf2", Provenance{})
	if mc2.MaxSlope != 32 {
		t.Fatalf("absent maxwaterslope must carry the template 255 and leave MaxSlope authored [04 §6.1 R-DOC04-A], got %d", mc2.MaxSlope)
	}
	if mc2.BadSlope != 16 {
		t.Fatalf("BadSlope half of MaxSlope got %d want 16", mc2.BadSlope)
	}
	if mc2.MaxWaterSlope != 255 || mc2.BadWaterSlope != 127 {
		t.Fatalf("water slopes got %d/%d want template 255/127", mc2.MaxWaterSlope, mc2.BadWaterSlope)
	}

	// Authored MaxWaterSlope=20 below MaxSlope 32 → clamp 1 fires (unconditional).
	sec3 := mustParseTDF(t, `[CLASS_SHIP]
{
    Name=ship;
    MaxSlope=32;
    MaxWaterSlope=20;
}
`).Root.Sections()[0]
	mc3 := compileMovementSection(sec3, "ship", Provenance{})
	if mc3.MaxSlope != 20 {
		t.Fatalf("clamp 1 MaxWaterSlope<MaxSlope got %d want 20", mc3.MaxSlope)
	}

	// Template depths: an omitted maxwaterdepth carries 10000 and an omitted
	// minwaterdepth carries −10000, so stock boats rely on both template
	// depths [04 §6.1 R-DOC04-A].
	sec4 := mustParseTDF(t, `[CLASS_BOAT]
{
    Name=boatd3;
    MinWaterDepth=15;
}
`).Root.Sections()[0]
	mc4 := compileMovementSection(sec4, "boatd3", Provenance{})
	if mc4.MaxWaterDepth != 10000 {
		t.Fatalf("maxwaterdepth got %d want template 10000", mc4.MaxWaterDepth)
	}
	if mc4.MinWaterDepth != 15 {
		t.Fatalf("minwaterdepth got %d want authored 15", mc4.MinWaterDepth)
	}
}
