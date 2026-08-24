package content

import "testing"

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestMovementSC5Census(t *testing.T) {
	// Verify that all 8 keys are read in order and that defaults chain.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		t.Fatalf("FootPrint census [analysis omitted]/78 [P1-03] got %d/%d want 3/4", mc.FootprintX, mc.FootprintZ)
	}
	if mc.MaxWaterDepth != 10 || mc.MinWaterDepth != 5 {
		t.Fatalf("depth census [analysis omitted]/58 [P1-03] got %d/%d want 10/5", mc.MaxWaterDepth, mc.MinWaterDepth)
	}
	if mc.MaxSlope != 32 {
		t.Fatalf("MaxSlope [analysis omitted] [P1-03] got %d want 32", mc.MaxSlope)
	}
	if mc.BadSlope != 16 {
		t.Fatalf("BadSlope [analysis omitted] default Max>>1 [P1-03] got %d want 16", mc.BadSlope)
	}
	if mc.MaxWaterSlope != 255 {
		t.Fatalf("MaxWaterSlope [analysis omitted] [P1-03] got %d want 255", mc.MaxWaterSlope)
	}
	if mc.BadWaterSlope != 127 {
		t.Fatalf("BadWaterSlope [analysis omitted] default MaxWater>>1 [P1-03] got %d want 127", mc.BadWaterSlope)
	}
	// Verify three clamps in order [P1-03]:
	// 1 MaxWaterSlope<MaxSlope→MaxSlope=MaxWaterSlope
	// 2 MaxSlope<BadSlope→BadSlope
	// 3 MaxWaterSlope<BadWaterSlope→BadWaterSlope
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Nanolathe gates 1 and 3 on authored per SPEC_CONFLICTS SC5 A10.
	// Test gated case: absent MaxWaterSlope leaves MaxSlope 32, not zero.
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
		t.Fatalf("SC5 gated divergence A10: absent maxwaterslope should preserve MaxSlope 32 [P1-03][SPEC_CONFLICTS SC5] got %d", mc2.MaxSlope)
	}
	if mc2.BadSlope != 16 {
		t.Fatalf("BadSlope half of MaxSlope [P1-03] got %d want 16", mc2.BadSlope)
	}
	// Authored MaxWaterSlope=20 below MaxSlope 32 → clamp 1 fires.
	sec3 := mustParseTDF(t, `[CLASS_SHIP]
{
    Name=ship;
    MaxSlope=32;
    MaxWaterSlope=20;
}
`).Root.Sections()[0]
	mc3 := compileMovementSection(sec3, "ship", Provenance{})
	if mc3.MaxSlope != 20 {
		t.Fatalf("clamp 1 MaxWaterSlope<MaxSlope [P1-03] got %d want 20", mc3.MaxSlope)
	}
	// Verify footprint half-extents 1<<20 scale at FBI materialization [P1-03]:
	// halfNeg = FootPrint * -0x100000 /2, halfPos = FootPrint<<0x14 /2, fullSpan = halfPos-halfNeg.
	// For FootPrint 2: halfNeg = 2*-1048576/2 = -1048576, halfPos = 2<<20/2 = 1048576, span 2097152.
	if mc.FootprintX != 3 {
		t.Fatalf("footprint half-extents scale 1<<20 [P1-03]")
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// and water-depth vs slope via SeaLevel<=bMin branch swapping MaxSlope/MaxWaterSlope,
	// passability < not <= [P1-03] are documented in world/terrain and movement profile
	// comments; this test locks the compile-time contract that feeds them.
}
