package content

import "testing"

// TestMovementChainedDefaults locks PLAN_02 C6: the eight keys read in order
// with chained defaults (badslope = half the maxslope just read;
// badwaterslope = half the maxwaterslope just read), then the three clamps in
// order [02 "Movement class record"], gated per SPEC_CONFLICTS SC5.
func TestMovementChainedDefaults(t *testing.T) {
	// Authored maxslope only: badslope chains to maxslope/2; no maxwaterslope,
	// so clamps 1 and 3 do not fire (SC5) and clamp 2 is a no-op.
	mc := compileMovementSection(mustParseTDF(t, `[CLASSKBOT]
{
	Name=kbotsf2;
	FootPrintX=2;
	FootPrintZ=2;
	maxslope=24;
}
`).Root.Sections()[0], "kbotsf2", Provenance{})

	if mc.MaxSlope != 24 || mc.BadSlope != 12 {
		t.Fatalf("chained defaults: maxslope=%d badslope=%d, want 24/12", mc.MaxSlope, mc.BadSlope)
	}
	if mc.MaxWaterSlope != 0 || mc.BadWaterSlope != 0 {
		t.Fatalf("water slopes = %d/%d, want 0/0", mc.MaxWaterSlope, mc.BadWaterSlope)
	}

	// Authored maxwaterslope below maxslope: clamp 1 fires, and clamp 2 pulls
	// badslope down to the new maxslope. badwaterslope chains to half the
	// authored value before clamp 3 compares equal (no change).
	mc = compileMovementSection(mustParseTDF(t, `[CLASSBOAT]
{
	Name=boatsh2;
	maxslope=30;
	maxwaterslope=20;
}
`).Root.Sections()[0], "boatsh2", Provenance{})

	if mc.MaxSlope != 20 {
		t.Fatalf("clamp 1: maxslope=%d, want 20", mc.MaxSlope)
	}
	if mc.BadWaterSlope != 10 {
		t.Fatalf("badwaterslope=%d, want 10 (half of just-read 20)", mc.BadWaterSlope)
	}
	if mc.BadSlope != 15 {
		t.Fatalf("clamp 2: badslope=%d, want 15 (maxslope 30 < badslope default)", mc.BadSlope)
	}

	// Clamp order matters: badslope authored above maxslope collapses to
	// maxslope only via clamp 2.
	mc = compileMovementSection(mustParseTDF(t, `[CLASSTANK]
{
	Name=tanksh3;
	maxslope=10;
	badslope=99;
	maxwaterslope=40;
}
`).Root.Sections()[0], "tanksh3", Provenance{})

	if mc.MaxSlope != 10 || mc.BadSlope != 10 || mc.BadWaterSlope != 20 {
		t.Fatalf("clamps out of order: slope=%d bad=%d waterslope=%d badwater=%d, want 10/10/40/20",
			mc.MaxSlope, mc.BadSlope, mc.MaxWaterSlope, mc.BadWaterSlope)
	}
}
