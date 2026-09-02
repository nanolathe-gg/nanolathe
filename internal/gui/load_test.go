package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func testFS(t *testing.T, dir string) vfs.FSOps {
	t.Helper()
	fs := vfs.New()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs %s: %v", dir, err)
	}
	if err := fs.MountDirectory(abs, 10); err != nil {
		t.Fatalf("mount %s: %v", abs, err)
	}
	return fs
}

func retailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), "TotalAnnihilation")
	}
	if _, err := os.Stat(filepath.Join(root, "gamedata")); err != nil {
		t.Skip("retail assets not present")
	}
	return root
}

func TestLoadAllKinds(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("Load all_kinds: %v", err)
	}
	if w.Name != "all_kinds.gui" {
		t.Fatalf("Name = %q want all_kinds.gui", w.Name)
	}
	if w.Header.Panel != "MyPanel" {
		t.Fatalf("Header.Panel = %q want MyPanel", w.Header.Panel)
	}
	if w.Header.TotalGadgets != 12 {
		t.Fatalf("TotalGadgets = %d want 12", w.Header.TotalGadgets)
	}
	if len(w.Gadgets) < 13 {
		t.Fatalf("Gadgets len = %d want >=13 (header+12 kinds + slider children)", len(w.Gadgets))
	}
	// Verify twelve kinds present: map kind counts
	kinds := make(map[Kind]int)
	for _, g := range w.Gadgets {
		kinds[g.Kind]++
	}
	for _, k := range []Kind{KindPanel, KindButton, KindListBox, KindTextBox, KindScrollBar, KindLabel, KindSurface, KindFont, KindSlider, KindText, KindZero, KindPicture} {
		if kinds[k] == 0 {
			t.Fatalf("kind %d not found in all_kinds", k)
		}
	}
	// Button staged labels [07 §4]
	var btn *Gadget
	for i := range w.Gadgets {
		if w.Gadgets[i].Kind == KindButton {
			btn = &w.Gadgets[i]
			break
		}
	}
	if btn == nil {
		t.Fatal("button not found")
	}
	if len(btn.Labels) != 3 || btn.Labels[0] != "On" || btn.Labels[1] != "Off" || btn.Labels[2] != "Auto" {
		t.Fatalf("button Labels = %v want [On Off Auto]", btn.Labels)
	}
	if btn.Stages != 3 {
		t.Fatalf("button Stages = %d want 3", btn.Stages)
	}
	// Listbox itemheight
	found := false
	for _, g := range w.Gadgets {
		if g.Kind == KindListBox && g.ItemHeight == 14 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("listbox itemheight 14 not found")
	}
	// Font filename
	found = false
	for _, g := range w.Gadgets {
		if g.Kind == KindFont && g.FileName == "SMLFONT" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("font filename SMLFONT not found")
	}
	// Picture box at -2 anchored to far edge: x = 640-80=560
	for _, g := range w.Gadgets {
		if g.Kind == KindPicture {
			if g.Rect.X != 560 {
				t.Fatalf("picture x = %d want 560 (-2 sentinel)", g.Rect.X)
			}
			if g.Rect.Y != 400 {
				t.Fatalf("picture y = %d want 400", g.Rect.Y)
			}
			break
		}
	}
	// Defaults: check that gadgets with missing keys default to 0/empty
	w2, err := Load(testFS(t, "testdata"), "defaults.gui")
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if len(w2.Gadgets) != 3 {
		t.Fatalf("defaults gadgets = %d want 3", len(w2.Gadgets))
	}
	// Minimal button should have zero status/grayed etc.
	for _, g := range w2.Gadgets {
		if g.Name == "MINBUTTON" {
			if g.Status != 0 || g.GrayedOut != 0 || g.Stages != 0 {
				t.Fatalf("defaults button fields not zero: %+v", g)
			}
			if g.Text != "" {
				t.Fatalf("defaults button text = %q want empty", g.Text)
			}
		}
	}
}

func TestSentinelCentering(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Slider with xpos=-1 width=200 should be centered: (640-200)/2 = 220
	found := false
	for _, g := range w.Gadgets {
		if g.Name == "MYSLIDER" {
			if g.Rect.RawX != -1 {
				t.Fatalf("slider rawX = %d want -1", g.Rect.RawX)
			}
			if g.Rect.X != 220 {
				t.Fatalf("slider centered X = %d want 220", g.Rect.X)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("MYSLIDER not found")
	}
}

func TestNameCappedAt127(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, g := range w.Gadgets {
		if g.Kind == KindTextBox {
			if len(g.Name) > 127 {
				t.Fatalf("textbox name len = %d >127 capped per [07 §4]", len(g.Name))
			}
		}
	}
	// Also check that the intentionally long name was truncated
	for _, g := range w.Gadgets {
		if strings.HasPrefix(g.Name, "MYTEXTINPUT") {
			if len(g.Name) != 127 {
				t.Fatalf("long name truncated len = %d want 127", len(g.Name))
			}
		}
	}
}

func TestMaxCharsCappedAt128(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, g := range w.Gadgets {
		if g.Kind == KindTextBox {
			if g.MaxChars != 128 {
				t.Fatalf("textbox maxchars = %d want 128 capped per [07 §4]", g.MaxChars)
			}
		}
	}
}

func TestStoredWidthsHonored(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, g := range w.Gadgets {
		if g.Name == "MYBUTTON" {
			if g.Rect.W != 100 || g.Rect.H != 20 {
				t.Fatalf("button stored widths not honored: W=%d H=%d want 100,20", g.Rect.W, g.Rect.H)
			}
		}
	}
}

func TestScrollbarAssociation(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// List MYLIST assoc 5 range 20, scrollbar MYSCROLL same assoc but authored range 99 should be overridden to 20 per C7 [07 §4].
	var list RangeCheck
	var scroll *Gadget
	for i := range w.Gadgets {
		g := &w.Gadgets[i]
		if g.Name == "MYLIST" {
			list = RangeCheck{Range: g.Range, KnobPos: g.KnobPos, KnobSize: g.KnobSize}
		}
		if g.Name == "MYSCROLL" {
			scroll = g
		}
	}
	if scroll == nil {
		t.Fatal("MYSCROLL not found")
	}
	if scroll.Range != list.Range || scroll.KnobPos != list.KnobPos || scroll.KnobSize != list.KnobSize {
		t.Fatalf("scrollbar not overridden from list: scroll %+v vs list %+v", *scroll, list)
	}
}

type RangeCheck struct{ Range, KnobPos, KnobSize int16 }

func TestSliderSynthesizesChildren(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	count := 0
	for _, g := range w.Gadgets {
		if strings.HasPrefix(g.Name, "MYSLIDER_S") {
			count++
			if g.Kind != KindScrollBar {
				t.Fatalf("slider child kind = %d want scrollbar", g.Kind)
			}
		}
	}
	if count != 2 {
		t.Fatalf("slider children count = %d want 2 per [07 §4]", count)
	}
}

func TestArtResolutionOrder(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "art_resolution.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// OwnArtButton has own art OwnArtButton, should resolve first [07 §4]
	foundOwn := false
	for _, g := range w.Gadgets {
		if g.Name == "OwnArtButton" {
			srcs := g.ArtSources("")
			if len(srcs) == 0 || srcs[0] != (ArtSource{Name: "OwnArtButton"}) {
				t.Fatalf("OwnArtButton art sources = %+v want first {Name: OwnArtButton} per [07 §4]", srcs)
			}
			foundOwn = true
		}
	}
	if !foundOwn {
		t.Fatal("OwnArtButton not found")
	}
	// Header panel fallback BackTile chain [07 §4]
	if w.Header.Panel != "MyHeaderPanel" {
		t.Fatalf("header panel = %q want MyHeaderPanel", w.Header.Panel)
	}
	// Empty name gadget should have empty art but window fallback would be BackTile for panel only
	// Check that panel gadget has BackTile in sources
	for _, g := range w.Gadgets {
		if g.Kind == KindPanel {
			srcs := g.ArtSources("")
			hasBackTile := false
			for _, s := range srcs {
				if s.Name == "BackTile" {
					hasBackTile = true
					break
				}
			}
			// Panel should have BackTile as fallback if not already present; but header panel is MyHeaderPanel, still should include BackTile as ultimate fallback
			if !hasBackTile {
				t.Fatalf("panel art sources missing BackTile fallback per [07 §4]: %+v", srcs)
			}
		}
	}
	// The side-specific interface GAF is the middle link between a gadget's
	// own entry and the built-in fallback [07 §4], mirroring the file order
	// cmd/nanolathe's gadgetArtEntry/modalGadgetFrame walk (page -> side
	// intGAF -> common). Passing a side handle inserts it in that slot.
	for _, g := range w.Gadgets {
		if g.Name == "OwnArtButton" {
			srcs := g.ArtSources("ARMINT")
			if len(srcs) < 2 || srcs[1] != (ArtSource{GAF: "ARMINT", Name: "OwnArtButton"}) {
				t.Fatalf("OwnArtButton art sources with side = %+v want second {GAF: ARMINT, Name: OwnArtButton} per [07 §4]", srcs)
			}
		}
	}
}

func TestHitTestInclusiveAndGrayedReject(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "all_kinds.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// MYBUTTON at 10,10 size 100x20 => inclusive bounds 10..109, 10..29 [07 §3]
	idx := -1
	for i, g := range w.Gadgets {
		if g.Name == "MYBUTTON" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("MYBUTTON not found")
	}
	btn := w.Gadgets[idx]
	if w.HitTest(btn.Rect.X, btn.Rect.Y) != idx {
		t.Fatalf("HitTest top-left inclusive failed")
	}
	if w.HitTest(btn.Rect.X+btn.Rect.W-1, btn.Rect.Y+btn.Rect.H-1) != idx {
		t.Fatalf("HitTest bottom-right inclusive failed")
	}
	if w.HitTest(btn.Rect.X+btn.Rect.W, btn.Rect.Y) != -1 {
		t.Fatalf("HitTest outside should miss")
	}
	// Grayed reject [07 §3]
	w.Gadgets[idx].GrayedOut = 1
	if w.HitTest(btn.Rect.X, btn.Rect.Y) != -1 {
		t.Fatalf("grayed control should reject hit per [07 §3]")
	}
	w.Gadgets[idx].GrayedOut = 0
	w.Gadgets[idx].Active = 0
	if w.HitTest(btn.Rect.X, btn.Rect.Y) != -1 {
		t.Fatalf("hidden control should reject hit per [07 §3]")
	}
}

func TestRealGUIsAssetGuarded(t *testing.T) {
	root := retailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount game dir %s: %v", root, err)
	}
	entries, err := fs.ReadDir("guis")
	if err != nil {
		t.Fatalf("ReadDir guis: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("no guis entries found")
	}
	parsed := 0
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name), ".gui") {
			continue
		}
		logical := "guis/" + e.Name
		w, err := Load(fs, logical)
		if err != nil {
			t.Fatalf("Load %s: %v", logical, err)
		}
		if len(w.Gadgets) == 0 {
			t.Fatalf("%s: gadget count 0", logical)
		}
		// Every window should have a header panel and at least one gadget
		if w.Header.Panel == "" {
			// Panel may be BackTile fallback; ensure header rect is plausible
			if w.Rect.W == 0 || w.Rect.H == 0 {
				t.Fatalf("%s: window rect zero", logical)
			}
		}
		parsed++
	}
	if parsed == 0 {
		t.Fatalf("no .gui files parsed")
	}
	t.Logf("parsed %d guis from %s", parsed, root)
}
