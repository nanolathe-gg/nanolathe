package content

import "testing"

// TestMovementChainedDefaults locks the eight-key parse order with chained
// defaults (badslope = (maxslope just read & 0xFF) >> 1; likewise
// badwaterslope), the field-width truncation, and the three unconditional
// ordered clamps [02 §5 "Movement class record"][04 §6.1 R-DOC04-A].
func TestMovementChainedDefaults(t *testing.T) {
	// Authored maxslope only: badslope chains to half of it; maxwaterslope is
	// omitted and carries the template 255, so clamp 1 (255 < 24) and clamp 3
	// (255 < 12) are identities [04 §6.1 R-DOC04-A].
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
	if mc.MaxWaterSlope != 255 || mc.BadWaterSlope != 127 {
		t.Fatalf("water slopes = %d/%d, want template 255/127 [04 §6.1 R-DOC04-A]", mc.MaxWaterSlope, mc.BadWaterSlope)
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

// TestMovementTemplateInitialization locks the startup template contract
// [04 §6.1 R-DOC04-A]: every compiled profile starts from 255 slopes and
// ±10000 depths before any parse, so omitted keys carry template values and
// the three clamps run unconditionally with no key-presence gate.
func TestMovementTemplateInitialization(t *testing.T) {
	// The stock shape: a class authoring maxslope and omitting maxwaterslope
	// compiles to the authored slope limit with the template water pair —
	// the SC5 gated-clamp divergence produced exactly this without the gate.
	mc := compileMovementSection(mustParseTDF(t, `[CLASSSS2]
{
	Name=kbotss2;
	FootPrintX=2;
	FootPrintZ=2;
	maxwaterdepth=12;
	maxslope=32;
}
`).Root.Sections()[0], "kbotss2", Provenance{})
	if mc.MaxSlope != 32 {
		t.Fatalf("maxslope=%d, want authored 32 (clamp 1 is the identity at template 255)", mc.MaxSlope)
	}
	if mc.MaxWaterSlope != 255 {
		t.Fatalf("maxwaterslope=%d, want template 255", mc.MaxWaterSlope)
	}
	if mc.BadWaterSlope != 127 {
		t.Fatalf("badwaterslope=%d, want 127 (half of the just-read 255)", mc.BadWaterSlope)
	}
	if mc.BadSlope != 16 {
		t.Fatalf("badslope=%d, want 16 (half of the just-read 32)", mc.BadSlope)
	}
	if mc.MaxWaterDepth != 12 {
		t.Fatalf("maxwaterdepth=%d, want authored 12", mc.MaxWaterDepth)
	}
	// minwaterdepth omitted: template −10000, the gate can never fire.
	if mc.MinWaterDepth != -10000 {
		t.Fatalf("minwaterdepth=%d, want template −10000", mc.MinWaterDepth)
	}

	// A class omitting everything but the name compiles to the pure template:
	// footprint 0/0, depths ±10000, slopes 255/127/255/127.
	pure := compileMovementSection(mustParseTDF(t, `[CLASSEMPTY]
{
	Name=empty;
}
`).Root.Sections()[0], "empty", Provenance{})
	if pure.FootprintX != 0 || pure.FootprintZ != 0 {
		t.Fatalf("footprint %d/%d, want 0/0", pure.FootprintX, pure.FootprintZ)
	}
	if pure.MaxWaterDepth != 10000 || pure.MinWaterDepth != -10000 {
		t.Fatalf("depths %d/%d, want template 10000/−10000", pure.MaxWaterDepth, pure.MinWaterDepth)
	}
	if pure.MaxSlope != 255 || pure.BadSlope != 127 || pure.MaxWaterSlope != 255 || pure.BadWaterSlope != 127 {
		t.Fatalf("slopes %d/%d/%d/%d, want template 255/127/255/127",
			pure.MaxSlope, pure.BadSlope, pure.MaxWaterSlope, pure.BadWaterSlope)
	}
}

// TestMovementFieldWidthTruncation locks the per-field storage conversions
// [02 §5 "Per-field conversion"]: 16-bit signed stores for footprints and
// depths, low-8-bit stores for slopes, and the bad-default logical shift on
// the low byte of the just-read value.
func TestMovementFieldWidthTruncation(t *testing.T) {
	mc := compileMovementSection(mustParseTDF(t, `[CLASSWIDE]
{
	Name=wide;
	maxslope=300;
	maxwaterslope=256;
	maxwaterdepth=70000;
	minwaterdepth=-70001;
}
`).Root.Sections()[0], "wide", Provenance{})
	// 300 & 0xFF = 44; badslope default = 44 >> 1 = 22. Clamp 1: 0 < 44 pulls
	// maxslope to maxwaterslope's stored 0, and clamp 2 then pulls badslope
	// down with it.
	if mc.MaxWaterSlope != 0 {
		t.Fatalf("maxwaterslope=%d, want stored 0 (256 & 0xFF)", mc.MaxWaterSlope)
	}
	if mc.MaxSlope != 0 {
		t.Fatalf("maxslope=%d, want 0 after clamp 1", mc.MaxSlope)
	}
	if mc.BadSlope != 0 {
		t.Fatalf("badslope=%d, want 0 after clamp 2", mc.BadSlope)
	}
	if mc.MaxWaterDepth != 4464 {
		t.Fatalf("maxwaterdepth=%d, want low 16 bits 4464", mc.MaxWaterDepth)
	}
	if mc.MinWaterDepth != -4465 {
		t.Fatalf("minwaterdepth=%d, want low 16 bits sign-extended −4465", mc.MinWaterDepth)
	}
}
