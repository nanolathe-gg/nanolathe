package formats

import "testing"

func TestLoadGUIRetainsSectionWithoutCommon(t *testing.T) {
	gui, err := LoadGUI([]byte(`
[GADGET0] { [COMMON] { id=0; name=HEADER; } }
[GADGET1] { text=raw-only; [EXTRA] { value=retained; } }
`))
	if err != nil {
		t.Fatalf("LoadGUI: %v", err)
	}
	if len(gui.Gadgets) != 2 {
		t.Fatalf("gadgets = %d, want 2", len(gui.Gadgets))
	}
	got := gui.Gadgets[1]
	if got.HasCommon {
		t.Fatal("GADGET1 unexpectedly has COMMON")
	}
	if got.Fields["text"] != "raw-only" {
		t.Fatalf("raw gadget fields = %#v, want text preserved", got.Fields)
	}
	if got.Section == nil || len(got.Section.Child) != 1 || got.Section.Child[0].Name != "EXTRA" {
		t.Fatalf("raw gadget section = %#v, want retained EXTRA child", got.Section)
	}
}

func TestLoadGUICommonStringsUseTypedLookup(t *testing.T) {
	gui, err := LoadGUI([]byte(`
[GADGET0] {
  [COMMON] {
    name=earlier;
    NAME=later;
    help=first help;
    HELP=last help;
  }
}
`))
	if err != nil {
		t.Fatalf("LoadGUI: %v", err)
	}
	if got := gui.Gadgets[0].Common.Name; got != "later" {
		t.Fatalf("common name = %q, want last parsed case variant", got)
	}
	if got := gui.Gadgets[0].Common.Help; got != "last help" {
		t.Fatalf("common help = %q, want last parsed case variant", got)
	}
}
