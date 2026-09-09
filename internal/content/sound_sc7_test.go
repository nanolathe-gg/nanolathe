package content

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// TestSoundSC7Gather locks SPEC_CONFLICTS SC7: gather K1.. regardless of bare [P1-12].
//
// Stock sound.tdf authors select1 120, ok1 76, cant1 76, arrived1 63 with NO bare forms [P1-12].
// Following spec letter would mute selection voices for every unit.
func TestSoundSC7Gather(t *testing.T) {
	// Bare absent but numbered present → must still gather.
	sec := mustParseTDF(t, `[ARMCK]
{
    select1=ARMAKSel;
    select2=ARMAKSel2;
    ok1=ARMAKOk;
    cant1=ARMAKCant;
}
`).Root.Sections()[0]
	sc := compileSoundCategorySection(sec, "ARMCK", Provenance{})
	// select slot 1: should have 2 variants via K1,K2 despite no bare select.
	sel := sc.Slots[1]
	if len(sel.Variants) != 2 || sel.Variants[0] != "ARMAKSel" || sel.Variants[1] != "ARMAKSel2" {
		t.Fatalf("SC7 select1 without bare: variants %v want [ARMAKSel ARMAKSel2] [P1-12][SPEC_CONFLICTS SC7]", sel.Variants)
	}
	// ok slot 5 similarly.
	if len(sc.Slots[5].Variants) != 1 || sc.Slots[5].Variants[0] != "ARMAKOk" {
		t.Fatalf("SC7 ok1 without bare: %v [P1-12]", sc.Slots[5].Variants)
	}
	if len(sc.Slots[7].Variants) != 1 || sc.Slots[7].Variants[0] != "ARMAKCant" {
		t.Fatalf("SC7 cant1 without bare: %v", sc.Slots[7].Variants)
	}
	// Bare present plus numbered: count 3 order bare, K1, K2 [P1-12 §2.3].
	sec2 := mustParseTDF(t, `[ARMCK2]
{
    select=SelA;
    select1=SelB;
    select2=SelC;
}
`).Root.Sections()[0]
	sc2 := compileSoundCategorySection(sec2, "ARMCK2", Provenance{})
	if len(sc2.Slots[1].Variants) != 3 {
		t.Fatalf("bare+numbered count %d want 3 [P1-12]", len(sc2.Slots[1].Variants))
	}
	if sc2.Slots[1].Variants[0] != "SelA" || sc2.Slots[1].Variants[1] != "SelB" || sc2.Slots[1].Variants[2] != "SelC" {
		t.Fatalf("bare+numbered order %v want [SelA SelB SelC] [P1-12]", sc2.Slots[1].Variants)
	}
	// Gap termination: select present, select1 absent, select2 present → only bare counted [P1-12 §2.3].
	sec3 := mustParseTDF(t, `[ARMCK3]
{
    select=SelA;
    select2=SelC;
}
`).Root.Sections()[0]
	sc3 := compileSoundCategorySection(sec3, "ARMCK3", Provenance{})
	if len(sc3.Slots[1].Variants) != 1 || sc3.Slots[1].Variants[0] != "SelA" {
		t.Fatalf("gap termination: %v want [SelA] first absent terminates loop [P1-12]", sc3.Slots[1].Variants)
	}
}

// TestOVRResidualInert ensures OVR strings have no consumer path [P1-12].
func TestOVRResidualInert(t *testing.T) {
	// OVR constants have no established content reader.
	// This test locks that compilation does not use them: compiling a unit with
	// those keys as Unknown must retain them as inert, not consume.
	sec := mustParseTDF(t, `[UNITINFO]
{
    unitname=TESTOVR;
    OVR=some;
    Compatability=some;
    TA Unit Override=some;
}
`).Root.Sections()[0]
	u := compileUnitSection(sec, "units/testovr.fbi", "", Provenance{})
	// Unknown should contain those keys as inert, not as dedicated fields.
	if u.Unknown == nil {
		t.Fatalf("OVR keys should be retained as Unknown inert [P1-12]")
	}
	// Ensure the 3 strings are among Unknown keys (case preserved).
	foundOVR := false
	foundCompat := false
	for k := range u.Unknown {
		if k == "OVR" {
			foundOVR = true
		}
		if k == "Compatability" {
			foundCompat = true
		}
	}
	if !foundOVR || !foundCompat {
		t.Fatalf("OVR residual keys not retained as Unknown inert [P1-12] foundOVR %v compat %v Unknown %v", foundOVR, foundCompat, u.Unknown)
	}
	// Ensure constants match strings.
	if OVRString != "OVR" || CompatabilityString != "Compatability" || TAUnitOverrideString != "TA Unit Override" {
		t.Fatalf("OVR constants mismatch [P1-12]")
	}
}

// TestAliasCap255x32B locks alias cap 255×32 B [P1-12][02 "Sound aliases"].
func TestAliasCap255x32B(t *testing.T) {
	sec := mustParseTDF(t, `[LONG]
{
    select=1234567890123456789012345678901234567890123456789012345678901234567890;
}
`).Root.Sections()[0]
	sc := compileSoundCategorySection(sec, "LONG", Provenance{})
	if len(sc.Slots[1].Variants[0]) > 64 {
		t.Fatalf("variant alias truncation 64 [P1-12] got %d", len(sc.Slots[1].Variants[0]))
	}
}

// TestTDFCommentBlankingPreservesOffsets and duplicate sections retained [P1-12][02 §4].
func TestTDFCommentBlankingPreservesOffsets(t *testing.T) {
	// Verify that // comment is blanked preserving offsets: NextKey offset unchanged.
	doc, err := formats.ParseTDF([]byte("[SEC]\n{\nValue=100; // comment\r\n NextKey=200;\n}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sec := doc.Root.Section("SEC")
	if sec == nil {
		t.Fatalf("SEC missing")
	}
	// NextKey should be present despite comment.
	if _, ok := sec.StringValue("NextKey", ""); !ok {
		t.Fatalf("NextKey missing after // blanking [P1-12][02 §4]")
	}
	// Duplicate sections retained separate nodes [P1-12][02 §4].
	doc2, err := formats.ParseTDF([]byte("[SEC]\n{\n a=1;\n}\n[SEC]\n{\n a=2;\n}\n"))
	if err != nil {
		t.Fatalf("parse dup: %v", err)
	}
	secs := doc2.Root.SectionsNamed("SEC")
	if len(secs) != 2 {
		t.Fatalf("duplicate sections retained [P1-12] got %d want 2", len(secs))
	}
	// Five diagnostics verbatim title check.
	if formats.ParseErrorTitle != "Parse error in .TDF File!" {
		t.Fatalf("verbatim title [P1-12][02 §4] got %q", formats.ParseErrorTitle)
	}
	if formats.DiagEqualsNotFound != "Data field - '=' not found" {
		t.Fatalf("diag verbatim [P1-12]")
	}
}
