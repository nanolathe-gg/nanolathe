package main

// Authored list boxes and their scrollbars: the visible-row arithmetic, the
// selection bar, the track and thumb, and the pointer that moves them
// [07 §5].

import (
	"math"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

type retailScrollbarGeometry struct {
	vertical   bool
	axisStart  int
	axisEnd    int
	thumbLen   int
	travel     int
	maxTop     int
	arrowStart int
	arrowEnd   int
}

func (g *gameShell) drawRetailList(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	g.drawListBox(c, r)
	// The selection and top row are read back after SetListTop clamps the top,
	// so the pre-clamp selection from this first read is deliberately dropped.
	items, _, top, ok := p.ListValuesAt(index)
	if !ok || len(items) == 0 || !g.hasRetailTextFont() {
		return
	}
	itemHeight := retailListItemHeight(gad, g.retailTextHeight())
	visible := retailVisibleListRows(r, itemHeight)
	maxTop := len(items) - visible
	if maxTop < 0 {
		maxTop = 0
	}
	p.SetListTopAt(index, top, maxTop)
	_, selected, top, _ := p.ListValuesAt(index)
	// The list painter selects the FNT the gadget's `fontnumber` picks from
	// the window's kind-7 records (the common font when none matches) and
	// then draws every row through the GAF pen, so the selected FNT is only
	// reached on the pen's null-slot fallback [03 R-FONT-01 §5][03 R-FONT-01 §6].
	rowFont := g.windowGadgetFont(p, gad)
	for row := 0; row < visible; row++ {
		idx := top + row
		if idx >= len(items) {
			break
		}
		// the retail implementation reserves the first two pixels of a listbox before
		// calculating rows. The same origin is used by its text renderer.
		y := int(r.Y) + 2 + row*itemHeight
		color := g.guiColor(byte(gad.ColorF & 0xff))
		g.drawRetailStringSelected(c, items[idx], int(r.X)+4, y, int(r.W)-4, color, 0, rowFont)
		// The highlight runs after the row's text, as the retail implementation does: the
		// operator remaps whatever is already in the rectangle, so the glyphs
		// are lifted along with the listbox interior.
		if idx == selected {
			g.drawListSelection(c, r, y, itemHeight)
		}
	}
}

// retailVisibleListRows is the row count used by the retail implementation: two pixels of
// the authored list rectangle are reserved before dividing by itemheight.
func retailVisibleListRows(r gui.Rect, itemHeight int) int {
	if itemHeight <= 0 {
		itemHeight = 1
	}
	visible := (int(r.H) - 2) / itemHeight
	if visible < 1 {
		visible = 1
	}
	return visible
}

func (g *gameShell) drawListBox(c *client.Client, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("LISTBOX")
	if !ok || len(e.Frames) < 9 || e.Frames[4].Frame == nil {
		return
	}
	// LISTBOX uses the common panel tiler, including its strict bottom
	// overflow and flush-edge overlap. Its builder clears authored origins;
	// writes remain inside the list's own rectangle [07 R-FE-02 §4][07 R-WGT-01 §4].
	x, y, w, h := int(r.X), int(r.Y), int(r.W), int(r.H)
	nineSliceFill(e, x, y, w, h, func(f *formats.GAFFrame, px, py int) {
		c.UIBlitClipped(f, px, py, x, y, w, h)
	})
}

// drawListSelection is the retail highlight. the retail implementation does not stamp art
// over the selected row: it hands the row rectangle to the retail implementation at level
// +30, and a non-negative level there indexes the 32-row PALETTE.LHT
// brightening table, so the row's own pixels are remapped one row at a time.
// That is what makes the selected entry read as a lit bar over the listbox
// interior rather than a painted block [the retail trace][03 §4.3.1].
func (g *gameShell) drawListSelection(c *client.Client, r gui.Rect, y, h int) {
	if g == nil || g.assets == nil || g.assets.pal == nil {
		return
	}
	c.UILightRect(g.assets.pal, int(r.X), y, int(r.W), h, retailListSelectionLevel)
}

// retailListSelectionLevel is the literal the retail implementation pushes for a selected
// list row, focused or not.
const retailListSelectionLevel = 30

func (g *gameShell) drawRetailScrollbar(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return
	}
	vertical := r.H >= r.W
	base := 0
	if !vertical {
		base = 10
	}
	// SLIDERS is partitioned exactly as the runtime builder expects:
	// base+0..2 are track end/middle pieces, base+3..5 are the three thumb
	// pieces, and base+6..9 are the normal/pressed arrow pairs. The vertical
	// family is frames 0..9; horizontal is frames 10..19 [07 §4].
	track0 := e.Frames[base+0].Frame
	track1 := e.Frames[base+1].Frame
	track2 := e.Frames[base+2].Frame
	thumb0 := e.Frames[base+3].Frame
	thumb1 := e.Frames[base+4].Frame
	thumb2 := e.Frames[base+5].Frame
	arrow0Normal := e.Frames[base+6].Frame
	arrow1Normal := e.Frames[base+8].Frame
	if track0 == nil || track1 == nil || track2 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil || arrow0Normal == nil || arrow1Normal == nil {
		return
	}
	arrow0 := arrow0Normal
	arrow1 := arrow1Normal
	leftMouseHeld := c != nil && c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft)

	left, top, right, bottom := int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)
	if vertical {
		arrowH := int(arrow0.Height)
		if int(arrow1.Height) > arrowH {
			arrowH = int(arrow1.Height)
		}
		if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: r.W, H: int32(arrowH)}) {
			if e.Frames[base+7].Frame != nil {
				arrow0 = e.Frames[base+7].Frame
			}
		}
		if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(bottom - arrowH), W: r.W, H: int32(arrowH)}) {
			if e.Frames[base+9].Frame != nil {
				arrow1 = e.Frames[base+9].Frame
			}
		}
		trackTop := top + arrowH
		trackBottom := bottom - arrowH
		trackW := int(track0.Width)
		trackX := left + (int(r.W)-trackW)/2
		blitRetailFrame(c, arrow0, left+(int(r.W)-int(arrow0.Width))/2, top)
		blitRetailFrame(c, arrow1, left+(int(r.W)-int(arrow1.Width))/2, bottom-int(arrow1.Height))
		drawRetailScrollbarTrack(c, track0, track1, track2, trackTop, trackX, trackBottom, false)

		l := listForAssocPanel(p, gad.Assoc)
		itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
		visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
		maxTop := 0
		if l != nil {
			maxTop = l.Len() - visible
			if maxTop < 0 {
				maxTop = 0
			}
		}
		total := 0
		if l != nil {
			total = l.Len()
		}
		thumbLen := retailScrollbarKnobSize(visible, total, int(r.H))
		travel := trackBottom - trackTop - thumbLen
		if travel < 0 {
			travel = 0
		}
		pos := 0
		if l != nil && maxTop > 0 {
			pos = l.Top() * travel / maxTop
		}
		thumbX := trackX + (trackW-int(thumb0.Width))/2
		drawRetailScrollbarThumb(c, thumb0, thumb1, thumb2, thumbX, trackTop+pos, thumbLen, false)
		return
	}

	arrowW := int(arrow0.Width)
	if int(arrow1.Width) > arrowW {
		arrowW = int(arrow1.Width)
	}
	if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: int32(arrowW), H: r.H}) {
		if e.Frames[base+7].Frame != nil {
			arrow0 = e.Frames[base+7].Frame
		}
	}
	if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(right - arrowW), Y: int32(top), W: int32(arrowW), H: r.H}) {
		if e.Frames[base+9].Frame != nil {
			arrow1 = e.Frames[base+9].Frame
		}
	}
	trackLeft := left + arrowW
	trackRight := right - arrowW
	trackH := int(track0.Height)
	trackY := top + (int(r.H)-trackH)/2
	blitRetailFrame(c, arrow0, left, top+(int(r.H)-int(arrow0.Height))/2)
	blitRetailFrame(c, arrow1, right-int(arrow1.Width), top+(int(r.H)-int(arrow1.Height))/2)
	drawRetailScrollbarTrack(c, track0, track1, track2, trackLeft, trackY, trackRight, true)
	l := listForAssocPanel(p, gad.Assoc)
	itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
	visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
	maxTop := 0
	if l != nil {
		maxTop = l.Len() - visible
		if maxTop < 0 {
			maxTop = 0
		}
	}
	total := 0
	if l != nil {
		total = l.Len()
	}
	thumbLen := retailScrollbarKnobSize(visible, total, int(r.W))
	travel := trackRight - trackLeft - thumbLen
	if travel < 0 {
		travel = 0
	}
	pos := 0
	if l != nil && maxTop > 0 {
		pos = l.Top() * travel / maxTop
	}
	// A kind-4 gadget with no associated list is a slider: its knob length is
	// the authored SLIDERS knob frame and its position is the knob word, not a
	// list origin [07 R-WGT-01 §5].
	if s := g.retailOptionsSlider(gad.Name); s != nil {
		thumbLen = s.knobSize
		pos = s.knob
	}
	thumbY := trackY + (trackH-int(thumb0.Height))/2
	drawRetailScrollbarThumb(c, thumb0, thumb1, thumb2, trackLeft+pos, thumbY, thumbLen, true)
	// A locked kind-4 gadget, or one carrying attribute 0x10, is inert and
	// drawn darkened by 20 steps — the same rectangle shader a greyed button
	// takes [07 R-WGT-01 §5][03 R-COMP-02 §5]. The lock word and the grey word
	// share one field here, as they do in the parser [07 R-WGT-01 §13].
	if (gad.GrayedOut != 0 || gad.Attribs&gui.AttribInert != 0) && g.assets != nil {
		c.UIShadeRect(g.assets.pal, int(r.X), int(r.Y), int(r.W), int(r.H), retailGreyedButtonShade)
	}
}

func drawRetailScrollbarTrack(c *client.Client, first, middle, last *formats.GAFFrame, start, cross0, cross1 int, horizontal bool) {
	if first == nil || middle == nil || last == nil {
		return
	}
	if horizontal {
		blitRetailFrame(c, first, start, cross0)
		end := cross1 - int(last.Width)
		for x := start + int(first.Width); x < end; x += int(middle.Width) {
			blitRetailFrame(c, middle, x, cross0)
		}
		blitRetailFrame(c, last, end, cross0)
		return
	}
	blitRetailFrame(c, first, cross0, start)
	end := cross1 - int(last.Height)
	for y := start + int(first.Height); y < end; y += int(middle.Height) {
		blitRetailFrame(c, middle, cross0, y)
	}
	blitRetailFrame(c, last, cross0, end)
}

// retailScrollbarKnobSize is the authored knob length used by the scrollbar.
// It divides the associated list's visible row count by its item count, scales
// that by the scrollbar's own length less three pixels, rounds, and clamps the
// result up to ten. SLIDERS carries the knob as a one-pixel cap, a repeatable
// three-pixel middle and a one-pixel cap, so the length is a computed run and
// never the sum of those frames [the retail trace].
func retailScrollbarKnobSize(visible, total, barLength int) int {
	const minimum = 10
	if total <= 0 || visible <= 0 || barLength <= 3 {
		return minimum
	}
	span := barLength - 3
	size := int(math.Round(float64(visible) / float64(total) * float64(span)))
	if size < minimum {
		size = minimum
	}
	if size > span {
		size = span
	}
	return size
}

func drawRetailScrollbarThumb(c *client.Client, first, middle, last *formats.GAFFrame, x, y, length int, horizontal bool) {
	if first == nil || middle == nil || last == nil {
		return
	}
	if horizontal {
		capLen := int(first.Width) + int(last.Width)
		if length < capLen {
			length = capLen
		}
		blitRetailFrame(c, first, x, y)
		end := x + length - int(last.Width)
		for px := x + int(first.Width); px < end; px += int(middle.Width) {
			blitRetailFrame(c, middle, px, y)
		}
		blitRetailFrame(c, last, end, y)
		return
	}
	capLen := int(first.Height) + int(last.Height)
	if length < capLen {
		length = capLen
	}
	blitRetailFrame(c, first, x, y)
	end := y + length - int(last.Height)
	for py := y + int(first.Height); py < end; py += int(middle.Height) {
		blitRetailFrame(c, middle, x, py)
	}
	blitRetailFrame(c, last, x, end)
}

func (g *gameShell) listForAssoc(assoc int32) *ui.List {
	return listForAssocPanel(g.activePanel(), assoc)
}

func listForAssocPanel(p *ui.Panel, assoc int32) *ui.List {
	return p.ListAt(listIndexForAssocPanel(p, assoc))
}

func listIndexForAssocPanel(p *ui.Panel, assoc int32) int {
	if p == nil || p.Window == nil {
		return -1
	}
	for i, gad := range p.Window.Gadgets {
		if i > 0 && gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return i
		}
	}
	return -1
}

func (g *gameShell) listRectForAssoc(assoc int32) gui.Rect {
	return listRectForAssocPanel(g.activePanel(), assoc)
}

func listRectForAssocPanel(p *ui.Panel, assoc int32) gui.Rect {
	if p == nil || p.Window == nil {
		return gui.Rect{}
	}
	for i, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return p.Window.PlacedRect(i)
		}
	}
	return gui.Rect{}
}

func (g *gameShell) retailScrollbarGeometry(gad gui.Gadget, r gui.Rect) (retailScrollbarGeometry, bool) {
	if g == nil || g.assets == nil || g.assets.common == nil {
		return retailScrollbarGeometry{}, false
	}
	e, ok := g.assets.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return retailScrollbarGeometry{}, false
	}
	vertical := r.H >= r.W
	base := 0
	if !vertical {
		base = 10
	}
	arrow0 := e.Frames[base+6].Frame
	arrow1 := e.Frames[base+8].Frame
	thumb0 := e.Frames[base+3].Frame
	thumb1 := e.Frames[base+4].Frame
	thumb2 := e.Frames[base+5].Frame
	if arrow0 == nil || arrow1 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil {
		return retailScrollbarGeometry{}, false
	}
	listRect := g.listRectForAssoc(gad.Assoc)
	itemHeight := g.retailListAssocItemHeight(gad.Assoc)
	visible := retailVisibleListRows(listRect, itemHeight)
	maxTop := 0
	if l := g.listForAssoc(gad.Assoc); l != nil {
		maxTop = l.Len() - visible
		if maxTop < 0 {
			maxTop = 0
		}
	}
	geometry := retailScrollbarGeometry{
		vertical: vertical,
		maxTop:   maxTop,
	}
	if vertical {
		arrowExtent := int(arrow0.Height)
		if int(arrow1.Height) > arrowExtent {
			arrowExtent = int(arrow1.Height)
		}
		geometry.arrowStart = int(r.Y)
		geometry.arrowEnd = int(r.Y+r.H) - arrowExtent
		geometry.axisStart = int(r.Y) + arrowExtent
		geometry.axisEnd = int(r.Y+r.H) - arrowExtent
		geometry.thumbLen = int(thumb0.Height) + int(thumb1.Height) + int(thumb2.Height)
	} else {
		arrowExtent := int(arrow0.Width)
		if int(arrow1.Width) > arrowExtent {
			arrowExtent = int(arrow1.Width)
		}
		geometry.arrowStart = int(r.X)
		geometry.arrowEnd = int(r.X+r.W) - arrowExtent
		geometry.axisStart = int(r.X) + arrowExtent
		geometry.axisEnd = int(r.X+r.W) - arrowExtent
		geometry.thumbLen = int(thumb0.Width) + int(thumb1.Width) + int(thumb2.Width)
	}
	geometry.travel = geometry.axisEnd - geometry.axisStart - geometry.thumbLen
	if geometry.travel < 0 {
		geometry.travel = 0
	}
	return geometry, true
}

// the retail implementation raises a list's authored item height to at least one pixel
// beyond the active font height. This is the default used by the authored
// campaign and map lists when itemheight is zero.
func retailListItemHeight(gad gui.Gadget, fontHeight int) int {
	height := int(gad.ItemHeight)
	minimum := fontHeight + 1
	if height < minimum {
		height = minimum
	}
	return height
}

func (g *gameShell) retailListAssocItemHeight(assoc int32) int {
	return retailListAssocItemHeightPanel(g, g.activePanel(), assoc)
}

func retailListAssocItemHeightPanel(g *gameShell, p *ui.Panel, assoc int32) int {
	if p == nil || p.Window == nil {
		return g.retailTextHeight() + 1
	}
	for _, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return retailListItemHeight(gad, g.retailTextHeight())
		}
	}
	return g.retailTextHeight() + 1
}

func (g *gameShell) adjustRetailScrollbar(gad gui.Gadget, delta int) {
	if g == nil || delta == 0 {
		return
	}
	// Scrollbar and slider are one kind [07 R-WGT-01 §5]; a kind-4 gadget on
	// the open options page drives its own value rather than a list origin.
	if g.adjustRetailSlider(gad, delta) {
		return
	}
	l := g.listForAssoc(gad.Assoc)
	if l == nil || l.Len() == 0 {
		return
	}
	r := g.listRectForAssoc(gad.Assoc)
	visible := retailVisibleListRows(r, g.retailListAssocItemHeight(gad.Assoc))
	maxTop := l.Len() - visible
	if maxTop < 0 {
		maxTop = 0
	}
	if p := g.activePanel(); p != nil {
		_ = p.SetListTopAt(listIndexForAssocPanel(p, gad.Assoc), l.Top()+delta, maxTop)
	}
}

func (g *gameShell) clickRetailScrollbar(index int, gad gui.Gadget, r gui.Rect, x, y int32) {
	if g.clickRetailSlider(gad, r, x, y) {
		return
	}
	geometry, ok := g.retailScrollbarGeometry(gad, r)
	if !ok {
		return
	}
	coordinate := int(x)
	if geometry.vertical {
		coordinate = int(y)
	}
	// The runtime builder appends two ordinary button gadgets for the arrow
	// frames. Their callbacks move the associated list one row at a time.
	if coordinate < geometry.axisStart {
		// Keep the arrow armed until the left button is released. The ordinary
		// child button callback is not run on the down edge.
		return
	}
	if coordinate >= geometry.axisEnd {
		// Keep the arrow armed until the left button is released. The ordinary
		// child button callback is not run on the down edge.
		return
	}
	l := g.listForAssoc(gad.Assoc)
	if l == nil {
		return
	}
	thumbPos := geometry.axisStart
	if geometry.maxTop > 0 {
		thumbPos += l.Top() * geometry.travel / geometry.maxTop
	}
	if coordinate < thumbPos || coordinate >= thumbPos+geometry.thumbLen {
		// the retail implementation starts capture only when the click is in the
		// calculated knob rectangle; clicking the track beside it does not
		// invent page-step behavior.
		return
	}
	if p := g.activePanel(); p != nil {
		_ = p.BeginScrollDrag(index, geometry.vertical, int32(coordinate), geometry.maxTop, geometry.travel)
	}
}

func (g *gameShell) updateRetailScrollbarDrag(mouse *input.MouseState) {
	if g.updateRetailSliderDrag(mouse) {
		return
	}
	p := g.activePanel()
	if p == nil || mouse == nil || !p.ScrollDragging() {
		return
	}
	// The panel owns pointer capture and the integer thumb mapping. Geometry
	// remains in this file because it comes from authored frame dimensions.
	_ = p.UpdateScrollDrag(int32(mouse.X), int32(mouse.Y), mouse.Held(input.MouseButtonLeft))
}

func (g *gameShell) releaseRetailScrollbar(gad gui.Gadget, r gui.Rect, x, y int32) {
	if g.releaseRetailSlider(gad) {
		return
	}
	geometry, ok := g.retailScrollbarGeometry(gad, r)
	if !ok {
		return
	}
	coordinate := int(x)
	if geometry.vertical {
		coordinate = int(y)
	}
	if coordinate < geometry.axisStart {
		g.adjustRetailScrollbar(gad, -1)
	} else if coordinate >= geometry.axisEnd {
		g.adjustRetailScrollbar(gad, 1)
	}
}
