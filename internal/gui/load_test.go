package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type captionTranslator map[string]string

func (t captionTranslator) Translate(source string) string {
	if translated, ok := t[source]; ok {
		return translated
	}
	return source
}

func TestLoadWithTranslationLocalizesCaptionsBeforeBuild(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", captionTranslator{"On|Off|Auto": "Ja|Nein|Auto"})
	if err != nil {
		t.Fatalf("LoadWithTranslation: %v", err)
	}
	for _, g := range w.Gadgets {
		if g.Kind == KindButton {
			if g.Text != "Ja|Nein|Auto" || strings.Join(g.Labels, "|") != "Ja|Nein|Auto" {
				t.Fatalf("localized button = %q labels %q", g.Text, strings.Join(g.Labels, "|"))
			}
			return
		}
	}
	t.Fatal("button missing")
}

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

func TestLoadAllKinds(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
	if err != nil {
		t.Fatalf("Load all_kinds: %v", err)
	}
	if w.Name != "all_kinds.gui" {
		t.Fatalf("Name = %q want all_kinds.gui", w.Name)
	}
	if w.Header.Panel != "MyPanel" {
		t.Fatalf("Header.Panel = %q want MyPanel", w.Header.Panel)
	}
	if w.Header.TotalGadgets != 15 {
		t.Fatalf("TotalGadgets = %d want 15", w.Header.TotalGadgets)
	}
	// No record is ever dropped: the parser rejects no kind and the builder's
	// switch only decides whether build work follows [07 R-WGT-01 §11][§12].
	if len(w.Gadgets) != 16 {
		t.Fatalf("Gadgets len = %d want 16 (header + 15 records, none dropped) [07 R-WGT-01 §12]", len(w.Gadgets))
	}
	kinds := make(map[Kind]int)
	for _, g := range w.Gadgets {
		kinds[g.Kind]++
	}
	for _, k := range []Kind{KindPanel, KindButton, KindListBox, KindTextBox, KindScrollBar, KindLabel, KindSurface, KindFont, KindRawFile, KindLine, KindPanelAlias, KindPicture, KindScoreBar} {
		if kinds[k] == 0 {
			t.Fatalf("kind %d not found in all_kinds", k)
		}
	}
	// Kind 9 has no build arm and no per-kind keys, and is kept and serviced
	// all the same [07 R-WGT-01 §11][07 R-WGT-01 §12].
	noArm := -1
	for i, g := range w.Gadgets {
		if g.Name == "MYNOARM" {
			noArm = i
		}
	}
	if noArm < 0 {
		t.Fatal("kind-9 record dropped; the builder does no build work for it but the record is still serviced [07 R-WGT-01 §12]")
	}
	if w.Gadgets[noArm].Kind != 9 || w.Gadgets[noArm].Kind.RuntimeFamily() != 0 {
		t.Fatalf("kind-9 record = kind %d family %d; want kind 9, no runtime family", w.Gadgets[noArm].Kind, w.Gadgets[noArm].Kind.RuntimeFamily())
	}
	if got := w.HitTest(w.PlacedRect(noArm).X, w.PlacedRect(noArm).Y); got != noArm {
		t.Fatalf("kind-9 record hit test = %d want %d: the pass visits it like any other [07 R-WGT-01 §1]", got, noArm)
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
	w2, err := LoadWithTranslation(testFS(t, "testdata"), "defaults.gui", nil)
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
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
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

func TestMaxCharsCappedAt127(t *testing.T) {
	// The parser caps `maxchars` at 128 and the builder caps it again at 127;
	// doc 07 §4's "name capped at 127 bytes" was this cap, not the name
	// [07 R-WGT-01 §11][07 R-WGT-01 §12]. The name field is 16 bytes for every
	// kind and is retained in full here for diagnostics.
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	seen := false
	for _, g := range w.Gadgets {
		if g.Kind != KindTextBox {
			continue
		}
		seen = true
		if g.MaxChars != 127 {
			t.Fatalf("textbox maxchars = %d want 127 [07 R-WGT-01 §12]", g.MaxChars)
		}
		if !strings.HasPrefix(g.Name, "MYTEXTINPUT") || len(g.Name) <= 127 {
			t.Fatalf("textbox name = %q (len %d); the authored name is retained, not capped [07 R-WGT-01 §12]", g.Name, len(g.Name))
		}
	}
	if !seen {
		t.Fatal("no text input in all_kinds")
	}
}

func TestFileSlotArms(t *testing.T) {
	// Kind 7 loads `<font directory>\<filename>.FNT`; kind 8 loads the
	// authored filename verbatim into the same slot [07 R-WGT-01 §12].
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var font, raw *Gadget
	for i := range w.Gadgets {
		switch w.Gadgets[i].Kind {
		case KindFont:
			font = &w.Gadgets[i]
		case KindRawFile:
			raw = &w.Gadgets[i]
		}
	}
	if font == nil || raw == nil {
		t.Fatal("font or raw-file gadget missing")
	}
	if font.FilePath != "fonts/SMLFONT.FNT" {
		t.Fatalf("font file slot = %q want fonts/SMLFONT.FNT [07 R-WGT-01 §12]", font.FilePath)
	}
	if raw.FilePath != "embed1.dat" {
		t.Fatalf("raw file slot = %q want embed1.dat verbatim [07 R-WGT-01 §12]", raw.FilePath)
	}
}

func TestLabelArmInertOnEmptyLink(t *testing.T) {
	// The label arm sets attribute 0x10 on every label whose `link` is empty,
	// which is why plain caption labels never react
	// [07 R-WGT-01 §12][07 R-WGT-01 §7].
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var linked, caption *Gadget
	for i := range w.Gadgets {
		switch w.Gadgets[i].Name {
		case "MYLABEL":
			linked = &w.Gadgets[i]
		case "MYCAPTION":
			caption = &w.Gadgets[i]
		}
	}
	if linked == nil || caption == nil {
		t.Fatal("labels missing")
	}
	if caption.Attribs&AttribInert == 0 {
		t.Fatalf("caption label attribs = %#x; want attribute 0x10 set [07 R-WGT-01 §12]", caption.Attribs)
	}
	if linked.Attribs&AttribInert != 0 {
		t.Fatalf("linked label attribs = %#x; a non-empty link leaves 0x10 clear [07 R-WGT-01 §12]", linked.Attribs)
	}
}

func TestStoredWidthsHonored(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
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
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
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

func TestBuildSliderArrows(t *testing.T) {
	// [07 R-WGT-01 §5]. Horizontal: frame base 10, the short axis takes the
	// base frame's size, the second arrow sits at x+w-arrowW, the bar shrinks
	// by 2*arrowW and shifts right by arrowW, knobsize is the width of frame
	// base+5 and travel is w' - knobsize - 4.
	bar := Gadget{Kind: KindScrollBar, Assoc: 6, Active: 1, Range: 10, KnobSize: 4,
		Rect: Rect{X: 100, Y: 40, W: 200, H: 16}, SourceName: "GADGET14"}
	art := &SliderArt{BaseExtent: 14, KnobExtent: 9, ArrowExtent: 12}
	got, arrows := BuildSlider(bar, art)
	if len(arrows) != 2 {
		t.Fatalf("arrows = %d want 2 appended BUTTON gadgets [07 R-WGT-01 §5]", len(arrows))
	}
	if arrows[0].Kind != KindButton || arrows[1].Kind != KindButton {
		t.Fatalf("arrow kinds = %d,%d want button [07 R-WGT-01 §5]", arrows[0].Kind, arrows[1].Kind)
	}
	if arrows[0].Attribs != AttribSliderDecrement || arrows[1].Attribs != AttribSliderIncrement {
		t.Fatalf("arrow attribs = %#x,%#x want 0x3400,0x2c00 [07 R-WGT-01 §5]", arrows[0].Attribs, arrows[1].Attribs)
	}
	if base := SliderFrameBase(bar.Rect); base != 10 {
		t.Fatalf("frame base = %d want 10 for a horizontal bar [07 R-WGT-01 §5]", base)
	}
	if arrows[0].ArtFrame != 16 || arrows[1].ArtFrame != 18 {
		t.Fatalf("arrow frames = %d,%d want base+6,base+8 = 16,18 [07 R-WGT-01 §5]", arrows[0].ArtFrame, arrows[1].ArtFrame)
	}
	if arrows[0].Art != "SLIDERS" || arrows[1].Art != "SLIDERS" {
		t.Fatalf("arrow art = %q,%q want SLIDERS [07 R-WGT-01 §5]", arrows[0].Art, arrows[1].Art)
	}
	for i, a := range arrows {
		if a.Assoc != bar.Assoc || a.Active != bar.Active {
			t.Fatalf("arrow %d assoc/active = %d/%d want %d/%d [07 R-WGT-01 §5]", i, a.Assoc, a.Active, bar.Assoc, bar.Active)
		}
	}
	if arrows[0].Rect.X != 100 || arrows[1].Rect.X != 100+200-12 {
		t.Fatalf("arrow X = %d,%d want 100,288 [07 R-WGT-01 §5]", arrows[0].Rect.X, arrows[1].Rect.X)
	}
	if got.Rect.H != 14 {
		t.Fatalf("bar short axis = %d want the base frame's 14 [07 R-WGT-01 §5]", got.Rect.H)
	}
	if got.Rect.W != 200-2*12 || got.Rect.X != 100+12 {
		t.Fatalf("bar = x %d w %d want x 112 w 176 [07 R-WGT-01 §5]", got.Rect.X, got.Rect.W)
	}
	if got.KnobSize != 9 {
		t.Fatalf("knobsize = %d want width(frame base+5) = 9 [07 R-WGT-01 §5]", got.KnobSize)
	}
	if got.Range != int16(176-9-4) {
		t.Fatalf("travel = %d want w'-knobsize-4 = 163 [07 R-WGT-01 §5]", got.Range)
	}

	// Vertical keeps its authored travel; the second arrow sits at y+h-arrowH.
	vbar := Gadget{Kind: KindScrollBar, Active: 1, Range: 10, KnobSize: 4,
		Rect: Rect{X: 20, Y: 30, W: 16, H: 100}}
	vgot, varrows := BuildSlider(vbar, art)
	if SliderFrameBase(vbar.Rect) != 0 {
		t.Fatalf("vertical frame base = %d want 0 [07 R-WGT-01 §5]", SliderFrameBase(vbar.Rect))
	}
	if varrows[0].ArtFrame != 6 || varrows[1].ArtFrame != 8 {
		t.Fatalf("vertical arrow frames = %d,%d want 6,8 [07 R-WGT-01 §5]", varrows[0].ArtFrame, varrows[1].ArtFrame)
	}
	if varrows[1].Rect.Y != 30+100-12 {
		t.Fatalf("vertical second arrow Y = %d want 118 [07 R-WGT-01 §5]", varrows[1].Rect.Y)
	}
	if vgot.Range != 10 {
		t.Fatalf("vertical travel = %d want the authored 10 [07 R-WGT-01 §5]", vgot.Range)
	}
	if vgot.Rect.H != 100-2*12 || vgot.Rect.Y != 30+12 {
		t.Fatalf("vertical bar = y %d h %d want y 42 h 76 [07 R-WGT-01 §5]", vgot.Rect.Y, vgot.Rect.H)
	}

	// No art: travel := max(w,h) - 6 and no arrows are appended.
	nbar, narrows := BuildSlider(bar, nil)
	if len(narrows) != 0 {
		t.Fatalf("art-less arrows = %d want none [07 R-WGT-01 §5]", len(narrows))
	}
	if nbar.Range != 200-6 {
		t.Fatalf("art-less travel = %d want max(w,h)-6 = 194 [07 R-WGT-01 §5]", nbar.Range)
	}
}

func TestLoadDoesNotSynthesizeSliderChildren(t *testing.T) {
	// The kind-4 arm needs SLIDERS metrics; the loader holds no GAF handle, so
	// it leaves the authored record alone [07 R-WGT-01 §5].
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, g := range w.Gadgets {
		if g.Attribs == AttribSliderDecrement || g.Attribs == AttribSliderIncrement {
			t.Fatalf("Load synthesized a slider arrow (%s); art-dependent synthesis belongs to the builder [07 R-WGT-01 §5]", g.SourceName)
		}
	}
	for _, g := range w.Gadgets {
		if g.Name == "MYSLIDER" && (g.Range != 10 || g.KnobSize != 4) {
			t.Fatalf("MYSLIDER travel/knobsize = %d/%d; want the authored 10/4 untouched [07 R-WGT-01 §5]", g.Range, g.KnobSize)
		}
	}
}

func TestArtResolutionOrder(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "art_resolution.gui", nil)
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
	// cmd/nanolathe's gadgetArtEntry/modalGadgetFrameState walk (page -> side
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

func TestHitTestHoversGreyedButOnlyUngreyedFires(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := LoadWithTranslation(fs, "all_kinds.gui", nil)
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
	if !w.Fires(idx) {
		t.Fatalf("an active, un-greyed button should fire [07 R-WGT-01 §13]")
	}
	// The grey bit is tested at press/fire time, not at hover: the pass's hit
	// test skips only hidden gadgets, so a greyed gadget still becomes the
	// hovered gadget and still feeds HELPTEXT [07 R-WGT-01 §13].
	w.Gadgets[idx].GrayedOut = 1
	if w.HitTest(btn.Rect.X, btn.Rect.Y) != idx {
		t.Fatalf("greyed control must still be hovered so it feeds HELPTEXT [07 R-WGT-01 §13]")
	}
	if w.Fires(idx) {
		t.Fatalf("greyed button must neither capture nor fire [07 R-WGT-01 §13]")
	}
	w.Gadgets[idx].GrayedOut = 0
	w.Gadgets[idx].Active = 0
	if w.HitTest(btn.Rect.X, btn.Rect.Y) != -1 {
		t.Fatalf("hidden control should reject hit [07 R-WGT-01 §1]")
	}
	if w.Fires(idx) {
		t.Fatalf("hidden control should not fire [07 R-WGT-01 §1]")
	}
}

func TestRealGUIsAssetGuarded(t *testing.T) {
	root := testsupport.RetailRoot(t)
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
		w, err := LoadWithTranslation(fs, logical, nil)
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

func TestLoadScrollbarThickSignExtendsWord(t *testing.T) {
	// The numeric read-out receives a signed word widened to 32 bits,
	// unlike a direct 32-bit field store [07 R-WGT-01 §11].
	for _, tc := range []struct {
		authored string
		want     int32
	}{
		{"32767", 32767}, {"32768", -32768}, {"65535", -1},
		{"65536", 0}, {"-32769", 32767},
	} {
		t.Run(tc.authored, func(t *testing.T) {
			panel := fmt.Sprintf(`[HEADER] { [COMMON] { id=0; width=640; height=480; } }
[BAR] { [COMMON] { id=4; } thick=%s; }`, tc.authored)
			window, err := LoadWithTranslation(guiRecordStoreFS(t, panel), "panel.gui", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := window.Gadgets[1].Thick; got != tc.want {
				t.Fatalf("thick %s = %d, want %d", tc.authored, got, tc.want)
			}
		})
	}
}

func TestLoadQuickKeyLetterOrDecimalPrefix(t *testing.T) {
	// The parser chooses the letter branch before decimal conversion;
	// punctuation and decimal zero must not fall back to their first byte.
	// The builder has not assigned caption accelerators yet [07 R-WGT-01 §11].
	for _, tc := range []struct {
		authored string
		want     byte
	}{
		{"S", 'S'}, {"lower", 'l'}, {"83tail", 'S'}, {"+83", 'S'},
		{"-1", 255}, {"339", 'S'}, {"0", 0}, {"00", 0},
		{"0x53", 0}, {"+", 0}, {"!", 0}, {"", 0},
		{"00000000000000000083", 0},
	} {
		t.Run(tc.authored, func(t *testing.T) {
			panel := fmt.Sprintf(`[HEADER] { [COMMON] { id=0; width=640; height=480; } }
[BUTTON] { [COMMON] { id=1; } quickkey=%s; }`, tc.authored)
			window, err := LoadWithTranslation(guiRecordStoreFS(t, panel), "panel.gui", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := window.Gadgets[1].QuickKey; got != tc.want {
				t.Fatalf("quickkey %q = %d, want %d", tc.authored, got, tc.want)
			}
		})
	}
}
