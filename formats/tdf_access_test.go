package formats

import "testing"

func TestParseTDFIntegerUsesRetailDecimalPrefix(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		value int32
	}{
		{name: "whitespace and negative sign", raw: " \t\r\n-12tail", value: -12},
		{name: "positive sign", raw: "+7", value: 7},
		{name: "empty", raw: "", value: 0},
		{name: "non numeric", raw: "true", value: 0},
		{name: "hex stops after decimal zero", raw: "0x10", value: 0},
		{name: "trailing junk", raw: "42abc", value: 42},
		{name: "wraps to 32 bits", raw: "4294967297", value: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ParseTDFInteger(test.raw); got != test.value {
				t.Fatalf("ParseTDFInteger(%q) = %d, want %d", test.raw, got, test.value)
			}
		})
	}
}

func TestTDFAccessorsUseRetailDuplicateAndNumericSemantics(t *testing.T) {
	document, err := ParseTDF([]byte(`
[DUP] {
    value=first;
    value=last;
    empty=;
    number=  -12junk;
    hex=0x10;
    textual=true;
    numeric=2trailing;
}
[DUP] { value=other-section; }
`))
	if err != nil {
		t.Fatal(err)
	}
	section := document.Root.Section("dup")
	if section == nil {
		t.Fatal("first DUP section was not found")
	}
	if value, _ := section.LastValue("value"); value != "last" {
		t.Fatalf("same-case duplicate value = %q, want last", value)
	}
	if value, present, err := section.Int("empty"); err != nil || !present || value != 0 {
		t.Fatalf("empty integer = (%d, %t, %v), want (0, true, nil)", value, present, err)
	}
	if value, present, err := section.Int("number"); err != nil || !present || value != -12 {
		t.Fatalf("prefix integer = (%d, %t, %v), want (-12, true, nil)", value, present, err)
	}
	if value, present, err := section.Int("hex"); err != nil || !present || value != 0 {
		t.Fatalf("hex integer = (%d, %t, %v), want (0, true, nil)", value, present, err)
	}
	if value, present, err := section.Bool("textual"); err != nil || !present || value {
		t.Fatalf("textual boolean = (%t, %t, %v), want (false, true, nil)", value, present, err)
	}
	if value, present, err := section.Bool("numeric"); err != nil || !present || !value {
		t.Fatalf("numeric boolean = (%t, %t, %v), want (true, true, nil)", value, present, err)
	}
	if _, present, err := section.Int("missing"); err != nil || present {
		t.Fatalf("missing integer = (present %t, %v), want absent", present, err)
	}
}

// TestTDFDuplicateKeyPolicy locks [02 §4]'s duplicate policy: an identical
// spelling replaces the value in place (last write wins); a case-variant
// spelling coexists as a second entry, inserted at the lower bound and so
// ahead of the variants already there; typed lookups read that lower bound and
// therefore return the LAST variant parsed [fmt tdf "Duplicate keys"].
//
// This fixture does not discriminate the two readings — `KEY` is both the last
// variant parsed and the byte-wise smaller spelling — so
// TestTDFCaseVariantLastParsedWins below carries the discriminating case.
func TestTDFDuplicateKeyPolicy(t *testing.T) {
	doc, err := ParseTDF([]byte("[A]\n{\nkey=1;\nkey=2;\nKEY=3;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := doc.Root.Sections()[0]
	// Three authored records collapse to two distinct spellings.
	values := sec.Values("key")
	if len(values) != 2 || values[0] != "3" || values[1] != "2" {
		t.Fatalf("Values(key) = %v, want [3 2]", values)
	}
	// Head of the fold run: KEY was parsed last, so it was inserted in front.
	if v, _ := sec.FirstValue("key"); v != "3" {
		t.Fatalf("typed lookup over variants: IntValue(key) = %q, want 3 (KEY variant)", v)
	}
	if v := sec.IntValue("key", -99); v != 3 {
		t.Fatalf("IntValue(key) = %d, want 3", v)
	}
	// LastValue is the upper bound of the run.
	if v, _ := sec.LastValue("key"); v != "2" {
		t.Fatalf("LastValue(key) = %q, want 2", v)
	}
	// The legacy trio shares the typed family's location now.
	if n, ok, err := sec.Int("key"); err != nil || !ok || n != 3 {
		t.Fatalf("Section.Int = %d,%v,%v, want 3,true,nil", n, ok, err)
	}
}

// TestTDFIdenticalSpellingLastWins locks replace-in-place for one spelling
// across source positions [02 §4].
func TestTDFIdenticalSpellingLastWins(t *testing.T) {
	doc, err := ParseTDF([]byte("[C]\n{\nk=1;\nk=2;\nz=5;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := doc.Root.Sections()[0]
	if v, _ := sec.FirstValue("k"); v != "2" {
		t.Fatalf("FirstValue(k) = %q, want 2 (last write wins)", v)
	}
}

// TestTDFCaseVariantLastParsedWins is the discriminating fixture for the
// duplicate-key rule of [fmt tdf "Duplicate keys"]: among spellings that differ
// only in case, the one a typed accessor returns is the LAST one parsed, not
// the byte-wise smallest.
//
// Retail's parser does not sort this vector, it builds it by insertion: each
// assignment takes a case-insensitive lower bound, compares byte-for-byte
// against the entry at that position only, replaces in place on an exact
// spelling match and otherwise inserts AT the lower bound — in front of the
// fold-equal run. The accessors take the same lower bound, so they read
// whichever variant was inserted most recently.
//
// The fixture is authored in the shape twelve stock aircraft records use: an
// upper-camel spelling early and an all-lower spelling late, with different
// values. Byte order would pick the capital ('M' 0x4D < 'm' 0x6D) and give 0;
// retail gives 255, which is what lets an amphibious aircraft treat water as
// landable ground [04 R-AIR-01 §6a]. The values are ours, not copied bytes.
func TestTDFCaseVariantLastParsedWins(t *testing.T) {
	doc, err := ParseTDF([]byte("[UNITINFO]\n{\nMaxWaterDepth=0;\nMaxSlope=10;\nmaxwaterdepth=255;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := doc.Root.Sections()[0]
	if v := sec.IntValue("maxwaterdepth", -1); v != 255 {
		t.Fatalf("IntValue(maxwaterdepth) = %d, want 255: the accessor must read the LAST case variant parsed, "+
			"not the byte-wise smaller spelling [fmt tdf \"Duplicate keys\"]", v)
	}
	// The lookup is itself case-insensitive, so every spelling of the key
	// reaches the same head-of-run entry.
	if v := sec.IntValue("MaxWaterDepth", -1); v != 255 {
		t.Fatalf("IntValue(MaxWaterDepth) = %d, want 255: the lookup folds case before it searches", v)
	}
	// Both variants survive as distinct entries; only their order changed.
	if values := sec.Values("maxwaterdepth"); len(values) != 2 || values[0] != "255" || values[1] != "0" {
		t.Fatalf("Values(maxwaterdepth) = %v, want [255 0]: case variants coexist, newest first", values)
	}
	// A key with no variant is untouched by any of this.
	if v := sec.IntValue("maxslope", -1); v != 10 {
		t.Fatalf("IntValue(maxslope) = %d, want 10", v)
	}
}

// TestTDFCaseVariantReversedOrder is the mirror of the fixture above: with the
// lower-case spelling authored first, the accessor returns the upper-case one,
// because that is now the last parsed. A reading that ordered variants by their
// bytes would answer the same way here and differently above, which is why both
// halves are asserted [fmt tdf "Duplicate keys"].
func TestTDFCaseVariantReversedOrder(t *testing.T) {
	doc, err := ParseTDF([]byte("[A]\n{\nmaxwaterdepth=255;\nMaxWaterDepth=0;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := doc.Root.Sections()[0]
	if v := sec.IntValue("maxwaterdepth", -1); v != 0 {
		t.Fatalf("IntValue(maxwaterdepth) = %d, want 0: the last variant parsed wins in both directions", v)
	}
}

// TestTDFRepeatedSpellingBehindAVariantIsInsertedNotReplaced locks the exact
// scope of the replace-in-place arm, which is narrower than "the same spelling
// collapses to one entry": the byte-for-byte comparison runs against the entry
// AT the lower bound — the head of the fold-equal run — and nothing else. A
// spelling that has been pushed behind a case variant is therefore no longer
// what that comparison sees, so a third assignment with that spelling is
// INSERTED at the head rather than folded into the entry further back. One
// spelling can consequently hold two entries [fmt tdf "Duplicate keys"].
//
// The collapse in TestTDFIdenticalSpellingLastWins is the ordinary case: with
// no variant in between, the repeat does land on its own entry and replaces it.
func TestTDFRepeatedSpellingBehindAVariantIsInsertedNotReplaced(t *testing.T) {
	// `k` first, then `K` (inserted in front of it), then `k` again — which sees
	// `K` at the lower bound, does not match it, and is inserted in front again.
	doc, err := ParseTDF([]byte("[A]\n{\nk=1;\nK=2;\nk=3;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := doc.Root.Sections()[0]
	if v := sec.IntValue("k", -1); v != 3 {
		t.Fatalf("IntValue(k) = %d, want 3: the last assignment parsed heads the fold run", v)
	}
	if values := sec.Values("k"); len(values) != 3 || values[0] != "3" || values[1] != "2" || values[2] != "1" {
		t.Fatalf("Values(k) = %v, want [3 2 1]: the head comparison only sees the run head, so `k` keeps two entries", values)
	}
}
