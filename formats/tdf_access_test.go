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
// spelling coexists as a second sorted entry; typed lookups return the
// lower-bound entry — the first variant in case-insensitive sort order
// (MEDIUM confidence per C10).
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
	// Lower bound of the fold run: KEY sorts before key byte-wise ('K' 0x4B < 'k' 0x6B).
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


