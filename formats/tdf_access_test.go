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
