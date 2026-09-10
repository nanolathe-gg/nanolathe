package content

import "testing"

// Numeric probing skips gaps, excludes prefix lookalikes, and resolves both
// duplicate sections and duplicate authored names by the first match
// [02 §4][02 "Movement class record"].
func TestMovementDiscoveryNumericSlotsAndFirstNames(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/moveinfo.tdf", data: `
[CLASS0] { Name=Tank; FootPrintX=2; MaxSlope=10; }
[CLASS2] { Name=tAnK; FootPrintX=7; MaxSlope=30; }
[CLASS2] { Name=DuplicateSection; FootPrintX=8; }
[cLaSs31] { Name=Last; FootPrintX=3; }
[CLASS32] { Name=Outside; }
[CLASSanything] { Name=NonNumeric; }
[CLASS01] { Name=LeadingZero; }
[CLASS-1] { Name=Negative; }
`})
	classes, err := CompileMovement(fs)
	if err != nil {
		t.Fatal(err)
	}
	if tank := classes["tank"]; tank == nil || tank.FootprintX != 2 || tank.MaxSlope != 10 {
		t.Fatalf("duplicate name selected %+v, want CLASS0", tank)
	}
	if last := classes["last"]; last == nil || last.FootprintX != 3 {
		t.Fatalf("gap-skipping last slot = %+v", last)
	}
	for _, excluded := range []string{"duplicatesection", "outside", "nonnumeric", "leadingzero", "negative"} {
		if classes[excluded] != nil {
			t.Errorf("unexpected class %q from a section retail does not visit", excluded)
		}
	}
}

func TestMovementDiscoveryIgnoresFileOrder(t *testing.T) {
	first := `[CLASS0] { Name=Shared; FootPrintX=2; MaxSlope=10; }`
	later := `[CLASS4] { Name=SHARED; FootPrintX=5; MaxSlope=30; }`
	var want string
	for i, source := range []string{first + later, later + first} {
		classes, err := CompileMovement(newFixtureFS(t, fixtureFile{path: "gamedata/moveinfo.tdf", data: source}))
		if err != nil {
			t.Fatal(err)
		}
		class := classes["shared"]
		if class == nil || class.FootprintX != 2 || class.MaxSlope != 10 {
			t.Fatalf("order %d selected %+v, want numeric first", i, class)
		}
		if i == 0 {
			want = class.Hash
		} else if class.Hash != want {
			t.Fatal("reordering numeric sections changed the winning definition hash")
		}
	}
}
