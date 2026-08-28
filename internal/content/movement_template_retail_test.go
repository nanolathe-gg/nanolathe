//go:build retail

package content

import "testing"

// TestMovementStockTemplateCompile compiles the reference install's
// moveinfo.tdf and asserts that the startup template plus the three
// unconditional clamps reproduce the stock compiled classes [04 §6.1
// R-DOC04-A]: an omitted maxwaterslope carries the template 255 so clamp 1 is
// the identity, and every land class compiles to its authored slope limit.
// These rows are the SC5 measured table (docs/SPEC_CONFLICTS.md SC5, resolved).
func TestMovementStockTemplateCompile(t *testing.T) {
	fs := mountRetail(t)
	classes, err := CompileMovement(fs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := map[string][4]int32{
		// class:    maxSlope, badSlope, maxWaterSlope, badWaterSlope
		"kbotss2":    {32, 16, 255, 127},
		"tankdh3":    {15, 7, 30, 15},    // authored maxwaterslope 30 passes the clamps unchanged
		"tankhover3": {12, 12, 255, 255}, // hover authors both water slopes
	}
	for key, w := range want {
		mc, ok := classes[key]
		if !ok {
			t.Fatalf("stock class %q missing from the compiled catalog", key)
		}
		got := [4]int32{mc.MaxSlope, mc.BadSlope, mc.MaxWaterSlope, mc.BadWaterSlope}
		if got != w {
			t.Fatalf("%s slopes = %v, want %v [04 §6.1 R-DOC04-A]", key, got, w)
		}
	}
	// Template depths: stock boats omit maxwaterdepth and compile to the
	// template 10000 (no depth ceiling); land classes omit minwaterdepth and
	// carry −10000 so the shallow gate can never fire [04 §6.1 R-DOC04-A].
	boats, ok := classes["boats4"]
	if !ok {
		t.Fatal("stock class boats4 missing")
	}
	if boats.MaxWaterDepth != 10000 || boats.MinWaterDepth != 3 {
		t.Fatalf("boats4 depths = %d/%d, want template 10000 + authored 3", boats.MaxWaterDepth, boats.MinWaterDepth)
	}
	kbot, ok := classes["kbotss2"]
	if !ok {
		t.Fatal("stock class kbotss2 missing")
	}
	if kbot.MinWaterDepth != -10000 {
		t.Fatalf("kbotss2 minwaterdepth = %d, want template −10000", kbot.MinWaterDepth)
	}
	// The dissolved MaxSlope=0 paradox stays dissolved: no stock class
	// compiles to a zero slope limit.
	for key, mc := range classes {
		if mc.MaxSlope == 0 {
			t.Fatalf("%s compiled to MaxSlope 0; the template pre-fill must prevent clamp 1 from zeroing authored slopes", key)
		}
	}
}
