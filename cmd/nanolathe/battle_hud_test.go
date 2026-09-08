package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestEmptySelectionClosesCommandWindows locks [07 §6] "Command-window switch
// is closed": while the selected-unit count is zero the switch closes the
// command windows down to the root <prefix>MAIN2.GUI and opens nothing, so no
// command page is composed; a non-empty selection with no single builder opens
// <prefix>GEN.GUI.
func TestEmptySelectionClosesCommandWindows(t *testing.T) {
	tank := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armstump"},
		UnitName:         "armstump",
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{tank.CanonicalKey: tank}}
	gen := &gui.Window{Gadgets: []gui.Gadget{{}, {Kind: gui.KindButton, Active: 1, Name: "MOVE"}}}
	hud := &retailBattleHUD{cat: cat, fs: vfs.New(), windows: map[string]*gui.Window{"gen": gen}}

	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: tank.UnitName})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: cat, hud: hud}

	empty := buf.Current()
	if window, _, err := hud.windowForRequired(b, empty); err != nil || window != nil {
		t.Fatalf("empty selection composed a command page: window=%v err=%v", window, err)
	}

	w = buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: tank.UnitName})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	if err := buf.Publish(2); err != nil {
		t.Fatal(err)
	}
	selected := buf.Current()
	if window, _, err := hud.windowForRequired(b, selected); err != nil || window != gen {
		t.Fatalf("single non-builder selection did not open the general page: window=%v err=%v", window, err)
	}
}

// TestProductQueueCountLabel locks the bit-0x04 count format of the count-label
// writer [07 R-P0-11 §2]: one total summed over the selected builder's primary
// and secondary lists, formatted "+%d", cleared at zero, and never counting
// another unit's queue.
func TestProductQueueCountLabel(t *testing.T) {
	f := &frame.Frame{CommandPage: frame.CommandPageView{Builder: 1}}
	f.OrderQueues = []frame.OrderQueueView{
		{
			Unit:      1,
			Primary:   []frame.OrderView{{BuildProduct: "armsolar", BuildCount: 5}, {BuildProduct: "armmex", BuildCount: 3}},
			Secondary: []frame.OrderView{{BuildProduct: "ARMSOLAR", BuildCount: 2}},
		},
		// Another builder's queue must not contribute.
		{Unit: 2, Primary: []frame.OrderView{{BuildProduct: "armsolar", BuildCount: 9}}},
	}
	if got := productQueueCountLabel(f, "ARMSOLAR"); got != "+7" {
		t.Fatalf("summed label = %q, want +7", got)
	}
	if got := productQueueCountLabel(f, "armmex"); got != "+3" {
		t.Fatalf("primary-only label = %q, want +3", got)
	}
	if got := productQueueCountLabel(f, "ARMWIN"); got != "" {
		t.Fatalf("zero total label = %q, want empty", got)
	}
	// No selected builder means no page and no counter.
	if got := productQueueCountLabel(&frame.Frame{OrderQueues: f.OrderQueues}, "ARMSOLAR"); got != "" {
		t.Fatalf("label without a selected builder = %q, want empty", got)
	}
}

// TestQueueCountLabelPenBuildVariant locks the button painter's pen
// arithmetic [03 R-FONT-01 §6] for the build-attribute variant (attribute bit
// 0x20) that every build-product button authors: the horizontal pen stays the
// centred formula, but the vertical pen anchors 4 pixels above the button's
// bottom edge rather than vertically centering, and `stages != 0` shifts both
// axes by one pixel.
func TestQueueCountLabelPenBuildVariant(t *testing.T) {
	// gx=10, gy=20, w=40, h=30 -> right=49, bottom=49. metric=11, tw=8.
	gad := gui.Gadget{Attribs: 0x20}
	r := gui.Rect{X: 10, Y: 20, W: 40, H: 30}
	x, y := queueCountLabelPen(gad, r, 8, 11)
	wantX := 10 + (49-8-10)/2 + 0 + 1 // centred formula, s=0
	wantY := 49 - 4 - 11 + 0          // bottom - 4 - metric + s
	if x != wantX || y != wantY {
		t.Fatalf("build-variant pen = (%d,%d), want (%d,%d)", x, y, wantX, wantY)
	}
	// Vertically this must land near the bottom of the button, not the
	// vertically-centred slot a plain left/centre button would use.
	if centredY := r.Y + (r.H-1-11)/2; y == int(centredY) {
		t.Fatalf("build-variant pen y = %d landed on the vertically-centred slot %d; retail anchors near the bottom edge", y, centredY)
	}

	gad.Stages = 1
	x2, y2 := queueCountLabelPen(gad, r, 8, 11)
	if x2 != wantX+1 || y2 != wantY+1 {
		t.Fatalf("staged build-variant pen = (%d,%d), want (%d,%d)", x2, y2, wantX+1, wantY+1)
	}
}

// TestQueueCountLabelPenLeftRightCentre locks the three named attributes the
// build-attribute variant is tested alongside, so a regression that folds the
// switch differently is caught here too [03 R-FONT-01 §6].
func TestQueueCountLabelPenLeftRightCentre(t *testing.T) {
	r := gui.Rect{X: 10, Y: 20, W: 40, H: 30}
	wantY := r.Y + (r.H-1-11)/2

	if x, y := queueCountLabelPen(gui.Gadget{Attribs: 1}, r, 8, 11); x != int(r.X)+3 || y != int(wantY) {
		t.Fatalf("left pen = (%d,%d), want (%d,%d)", x, y, int(r.X)+3, wantY)
	}
	if x, y := queueCountLabelPen(gui.Gadget{Attribs: 4}, r, 8, 11); x != int(r.X+r.W-1)-3-8 || y != int(wantY) {
		t.Fatalf("right pen = (%d,%d), want (%d,%d)", x, y, int(r.X+r.W-1)-3-8, wantY)
	}
	wantCentreX := int(r.X) + (int(r.X+r.W-1)-8-int(r.X))/2 + 1
	if x, y := queueCountLabelPen(gui.Gadget{Attribs: 2}, r, 8, 11); x != wantCentreX || y != int(wantY) {
		t.Fatalf("centre pen = (%d,%d), want (%d,%d)", x, y, wantCentreX, wantY)
	}
}

func generatedPageFixture(name string) *gui.Window {
	gadgets := make([]gui.Gadget, 10)
	for i := range gadgets {
		gadgets[i] = gui.Gadget{Name: "UNCHANGED", Art: "IGPATCH", GrayedOut: 3, CommonAttribs: 9}
	}
	return &gui.Window{Name: name, Gadgets: gadgets}
}

func TestGeneratedPageClonesTemplateAndPatchesAuthoredSlots(t *testing.T) {
	template := generatedPageFixture("guis/armdl.gui")
	h := &retailBattleHUD{
		fs:      vfs.New(),
		side:    &content.SideDef{NamePrefix: "ARM"},
		windows: map[string]*gui.Window{"armdl": template},
	}
	placements := []frame.GeneratedProductPlacement{
		{ProductKey: "slot-four", Button: 4},
		{ProductKey: "slot-one-old", Button: 1},
		{ProductKey: "slot-one-new", Button: 1}, // later authored claim wins
		{ProductKey: "invalid", Button: 255},
	}
	got, _, err := h.numberedPage("armlab2", placements)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got == template {
		t.Fatalf("generated page = %p, template = %p; want isolated clone", got, template)
	}
	assertSlot := func(button int, product string) {
		t.Helper()
		gad := got.Gadgets[button+4]
		if gad.Name != product || gad.Art != "IGPATCH" || gad.GAFFile&1 == 0 || gad.GrayedOut != 2 || gad.CommonAttribs != 4 {
			t.Fatalf("slot %d = %+v, want patched product %q", button, gad, product)
		}
	}
	assertSlot(4, "slot-four")
	assertSlot(1, "slot-one-new")
	if got.Gadgets[4].Name != "UNCHANGED" || got.Gadgets[6].Name != "UNCHANGED" {
		t.Fatalf("sparse unclaimed slots were changed: slot0=%+v slot2=%+v", got.Gadgets[4], got.Gadgets[6])
	}
	if template.Gadgets[5].Name != "UNCHANGED" || template.Gadgets[8].Name != "UNCHANGED" {
		t.Fatalf("source template was mutated: %+v", template.Gadgets)
	}
	again, _, err := h.numberedPage("armlab2", placements)
	if err != nil || again != got {
		t.Fatalf("generated page cache = %p, %v; want %p", again, err, got)
	}
}

func TestGeneratedPageOverlaysExistingNumberedPage(t *testing.T) {
	source := generatedPageFixture("guis/coralab1.gui")
	h := &retailBattleHUD{
		fs:      vfs.New(),
		side:    &content.SideDef{NamePrefix: "COR"},
		windows: map[string]*gui.Window{"coralab1": source},
	}
	got, _, err := h.numberedPage("coralab1", []frame.GeneratedProductPlacement{{ProductKey: "corfast", Button: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Gadgets[7].Name != "corfast" || got.Gadgets[7].CommonAttribs != 4 || got.Gadgets[7].GrayedOut != 2 {
		t.Fatalf("existing IGPATCH overlay = %+v", got.Gadgets[7])
	}
	if source.Gadgets[7].Name != "UNCHANGED" || source.Gadgets[7].Art != "IGPATCH" {
		t.Fatalf("cached physical page was mutated: %+v", source.Gadgets[7])
	}
}

func TestAbsentNumberedPageFallsBackToDLWithoutPlacements(t *testing.T) {
	template := generatedPageFixture("guis/armdl.gui")
	h := &retailBattleHUD{
		fs:      vfs.New(),
		side:    &content.SideDef{NamePrefix: "ARM"},
		windows: map[string]*gui.Window{"armdl": template},
	}
	got, _, err := h.numberedPage("unresolved2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got == template || got.Name != template.Name {
		t.Fatalf("zero-placement fallback = %#v, want cloned ARMDL", got)
	}
}

// queueCountFontFixture is an authored stand-in for the window's GAF font
// slot: 256 frame slots addressed by byte value, a capital I that fixes the
// line metric (its height plus two) and the load-time baseline normalization,
// and glyphs whose widths differ from the FNT fixture's so the two families
// produce different pens. No retail bytes are copied here.
func queueCountFontFixture() *formats.GAFEntry {
	font := &formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	add := func(code byte, w, h uint16, index byte) {
		frame := &formats.GAFFrame{Width: w, Height: h, YOffset: 11}
		frame.Pixels = make([]byte, int(w)*int(h))
		frame.Transparent = make([]bool, int(w)*int(h))
		for i := range frame.Pixels {
			frame.Pixels[i] = index
		}
		font.Frames[code] = formats.GAFFrameRef{Frame: frame}
	}
	add('I', 5, 12, 3)
	add('+', 6, 5, 7)
	add('3', 7, 12, 9)
	return font
}

// queueCountFNTFixture is the side font stand-in the GAF pen must NOT use for
// a product button's caption: a deliberately different height and advance, so
// a pen computed from it cannot coincide with the GAF pen.
func queueCountFNTFixture() *formats.FNT {
	fnt := &formats.FNT{Height: 20}
	for _, code := range []byte{'+', '3'} {
		fnt.Glyphs[code] = &formats.FNTGlyph{Width: 30, Height: 20, Bits: make([]byte, 20*4)}
	}
	return fnt
}

type productCaptionStage struct {
	hud  *retailBattleHUD
	gad  gui.Gadget
	rect gui.Rect
	text string
}

func (s productCaptionStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.hud.drawProductButtonCaption(c, s.gad, s.rect, s.text)
}

// TestProductButtonCaptionDrawsThroughGAFPen locks the family the side page's
// queue count is drawn with. Every text call in the retail button painter goes
// through the GAF-font pen with mode 0, so the count the count-label writer
// leaves in the toy's text slot [07 R-P0-11 §2] is measured and drawn with the
// window's current GAF-font slot — slot 0, hattfont12, because the painter
// switches to slot 1 only for the small-font attribute bit 0x8000 and no
// count-bearing product button authors it [03 R-FONT-01 §5][03 R-FONT-01 §6].
// Drawing it with the side font's FNT, as this HUD did before WU-19-221, is
// the wrong family.
func TestProductButtonCaptionDrawsThroughGAFPen(t *testing.T) {
	font := queueCountFontFixture()
	fnt := queueCountFNTFixture()
	pal := &palette.Tables{}
	for i := range pal.Base {
		pal.Base[i] = [4]byte{byte(i), byte(255 - i), byte(i / 2), 255}
	}
	h := &retailBattleHUD{modalFont: font, guiFont: fnt, pal: pal}
	gad := gui.Gadget{Kind: gui.KindButton, Attribs: 0x20, CommonAttribs: 4}
	r := gui.Rect{X: 8, Y: 12, W: 64, H: 64}
	const text = "+3"

	x, y, used := h.productButtonCaptionLayout(gad, r, text)
	if used != font {
		t.Fatalf("caption family = %v, want the GAF-font slot", used)
	}
	// Measured by the GAF width (frame widths summed) and placed by the GAF
	// line metric (capital-I height + 2), not by the FNT's advance or header
	// height [03 R-FONT-01 §6].
	wantX, wantY := queueCountLabelPen(gad, r, retailGAFTextWidth(font, text), retailGAFTextHeight(font))
	if x != wantX || y != wantY {
		t.Fatalf("GAF pen = (%d,%d), want (%d,%d)", x, y, wantX, wantY)
	}
	fntX, fntY := queueCountLabelPen(gad, r, client.MeasureText(fnt, text), int(fnt.Height))
	if x == fntX && y == fntY {
		t.Fatalf("GAF pen (%d,%d) is indistinguishable from the FNT pen; the fixture cannot tell the families apart", x, y)
	}

	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 128, Height: 128})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(pal)
	cl.SetUIStage(productCaptionStage{hud: h, gad: gad, rect: r, text: text})
	image := cl.ComposeFrame()

	// The second glyph lands one advance along the pen; the GAF-font blitter
	// places it at penX - XOffset, penY - normalizedYOffset, where loading
	// subtracted the capital-I height from every frame's Y offset [07 §4].
	digit := font.Frames['3'].Frame
	plus := font.Frames['+'].Frame
	blitX := x + int(plus.Width) - int(digit.XOffset)
	blitY := y - (int(digit.YOffset) - retailGAFBaselineHeight(font))
	wantR, wantG, wantB, wantA := pal.RGBA(digit.Pixels[0])
	got := image.RGBAAt(blitX+1, blitY+1)
	if got.R != wantR || got.G != wantG || got.B != wantB || got.A != wantA {
		t.Fatalf("digit pixel at (%d,%d) = %v, want the GAF frame's palette index %d", blitX+1, blitY+1, got, digit.Pixels[0])
	}
	// Nothing may be drawn on the row the FNT pen would have used but the GAF
	// pen does not reach.
	if fntY+1 != blitY {
		stray := image.RGBAAt(blitX+1, fntY+1)
		if stray.A != 0 && (stray.R != 0 || stray.G != 0 || stray.B != 0) {
			blankR, blankG, blankB, _ := pal.RGBA(0)
			if stray.R != blankR || stray.G != blankG || stray.B != blankB {
				t.Fatalf("pixel at the FNT pen row (%d,%d) = %v, want the untouched background", blitX+1, fntY+1, stray)
			}
		}
	}
}

// TestProductButtonCaptionNullSlotFallsBackToFNT locks the GAF pen's null-slot
// rule: with no GAF font loaded the caption goes to the FNT drawer, and the
// caller's width limit is dropped [03 R-FONT-01 §6].
func TestProductButtonCaptionNullSlotFallsBackToFNT(t *testing.T) {
	fnt := queueCountFNTFixture()
	h := &retailBattleHUD{guiFont: fnt}
	gad := gui.Gadget{Kind: gui.KindButton, Attribs: 0x20, CommonAttribs: 4}
	r := gui.Rect{X: 8, Y: 12, W: 64, H: 64}

	x, y, used := h.productButtonCaptionLayout(gad, r, "+3")
	if used != nil {
		t.Fatalf("null slot returned a GAF font %v", used)
	}
	wantX, wantY := queueCountLabelPen(gad, r, client.MeasureText(fnt, "+3"), int(fnt.Height))
	if x != wantX || y != wantY {
		t.Fatalf("FNT fallback pen = (%d,%d), want (%d,%d)", x, y, wantX, wantY)
	}
}
