package gui

import (
	"fmt"
	"testing"
)

// Focus uses the same exact, bounded first-match lookup as later screen
// mutations [07 R-FE-02 §5]. It must not resolve through a folded alias.
func TestLoadDefaultFocusUsesExactGadgetName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{{"OK", 1}, {"ok", 2}, {"Ok", -1}, {"HEADER", -1}, {"1234567890abcdefZ", 4}} {
		t.Run(tc.name, func(t *testing.T) {
			window, err := LoadWithTranslation(guiRecordStoreFS(t, fmt.Sprintf(`
[GADGET0] { defaultfocus=%s; [COMMON] { id=0; name=HEADER; width=640; height=480; } }
[GADGET1] { [COMMON] { id=1; name=OK; } }
[GADGET2] { [COMMON] { id=1; name=ok; } }
[GADGET3] { [COMMON] { id=1; name=OK; } }
[GADGET4] { [COMMON] { id=1; name=1234567890abcdefA; } }
[GADGET5] { [COMMON] { id=1; name=1234567890abcdefB; } }
`, tc.name)), "panel.gui", nil)
			if err != nil {
				t.Fatal(err)
			}
			if window.Focus != tc.want {
				t.Fatalf("defaultfocus %q = %d, want %d", tc.name, window.Focus, tc.want)
			}
		})
	}
}

// The prefix fallback is separate from exact-name lookup. It binds the first
// matching button even when inactive; the later key service tests admission
// [07 R-FE-01 §12][07 R-WGT-01 §2].
func TestWindowDefaultPrefixesAreSeparateFromExactNames(t *testing.T) {
	w := &Window{Gadgets: []Gadget{
		{Kind: KindPanel, Name: "OK"},
		{Kind: KindLabel, Name: "OKlabel"},
		{Kind: KindButton, Name: " OK", Active: 1},
		{Kind: KindButton, Name: "nExTstep", Active: 0},
		{Kind: KindButton, Name: "OK", Active: 1},
		{Kind: KindButton, Name: " Cancel", Active: 1},
		{Kind: KindButton, Name: "cAnCeL", Active: 0},
		{Kind: KindButton, Name: "PREV", Active: 1},
	}}
	if w.EnterDefaultIndex() != 3 || w.EscapeDefaultIndex() != 6 {
		t.Fatal("empty defaults did not take first matching button prefixes")
	}
	w.Header.CrDefault, w.Header.EscDefault = "OK", "PREV"
	if w.EnterDefaultIndex() != 4 || w.EscapeDefaultIndex() != 7 {
		t.Fatal("authored defaults did not use exact names")
	}
	w.Header.CrDefault, w.Header.EscDefault = "ok", "prev"
	if w.EnterDefaultIndex() != -1 || w.EscapeDefaultIndex() != -1 {
		t.Fatal("nonempty missing defaults incorrectly used prefix fallback")
	}
}

func TestCallbackNameUsesFullTerminatedValue(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"SINGLE\x00suffix", "SINGLE", true},
		{"SINGLE", "SINGLE\x00suffix", true},
		{"SINGLE", "single", false},
		{"SINGLE", "SINGLE ", false},
		{"1234567890abcdefA", "1234567890abcdefB", false},
	} {
		if got := CallbackNameEqual(tc.left, tc.right); got != tc.want {
			t.Errorf("CallbackNameEqual(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestCallbackNameAndNamedLookupRemainDistinct(t *testing.T) {
	w := &Window{Gadgets: []Gadget{{}, {Name: "1234567890abcdefA"}, {Name: "1234567890abcdefB"}}}
	if got := w.GadgetIndex("1234567890abcdefB"); got != 1 {
		t.Fatalf("bounded lookup = %d, want first 16-byte match 1", got)
	}
	if CallbackNameEqual(w.Gadgets[1].Name, w.Gadgets[2].Name) {
		t.Fatal("full callback names incorrectly matched at 16-byte boundary")
	}
}
