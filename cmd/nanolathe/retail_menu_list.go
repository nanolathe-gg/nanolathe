package main

// Authored list boxes and their scrollbars: the visible-row arithmetic, the
// selection bar, the track and thumb, and the pointer that moves them
// [07 §5].

import (
	"math"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
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
	// The list owner installs maxTop while it fills rows. Drawing reads that
	// state without deriving or mutating a competing visible-row limit.
	items, selected, top, ok := p.ListValuesAt(index)
	if !ok || len(items) == 0 || !g.hasRetailTextFont() {
		return
	}
	// The list painter selects the FNT the gadget's `fontnumber` picks from
	// the window's kind-7 records (the common font when none matches) and
	// then draws every row through the GAF pen, so the selected FNT is only
	// reached on the pen's null-slot fallback [03 R-FONT-01 §5][03 R-FONT-01 §6].
	rowFont := g.windowGadgetFont(p, gad)
	measure, metric := g.retailTextMetrics(rowFont)
	rowHeight := int(gad.ItemHeight)
	if rowHeight == 0 {
		rowHeight = metric + 1
	}
	// Painting reserves a full font metric below each admitted row. Its
	// boundary differs from click/scroll geometry. Fill has already normalized
	// the stored row height [07 R-WGT-01 §4].
	for row := 0; int(r.H)-(row+1)*rowHeight >= metric; row++ {
		idx := top + row
		if idx >= len(items) {
			break
		}
		y := int(r.Y) + 2 + row*rowHeight
		text, _, _ := strings.Cut(items[idx], "\x00")
		// Alignment measures the stored text before the heading prefix is
		// skipped. Its inclusive row rectangle is also the shade boundary
		// [07 R-WGT-01 §4].
		x, width := retailListTextPen(r, gad.Attribs, measure(text))
		heading := p.ListRowFlagAt(index, idx) == 1 || strings.HasPrefix(text, "&G")
		if p.ListRowFlagAt(index, idx) != 1 && strings.HasPrefix(text, "&") && len(text) >= 2 {
			text = text[2:]
		}
		color := g.guiColor(byte(gad.ColorF & 0xff))
		if rowHeight > metric+6 {
			remaining := int(r.H) - 1
			for line, run := range retailListWrapLines(text, measure, width) {
				if remaining < 1 {
					break
				}
				lineY := y + line*(metric+2)
				g.drawRetailStringSelected(c, run, x, lineY, width, color, 0, rowFont)
				remaining -= metric + 2
			}
		} else {
			g.drawRetailStringSelected(c, text, x, y, width, color, 0, rowFont)
		}
		if heading {
			if g.assets != nil && g.assets.pal != nil {
				for level := -19; level >= -22; level-- {
					c.UIShadeRect(g.assets.pal, int(r.X)+2, y, int(r.W)-1, rowHeight+1, level)
				}
			}
		} else if idx == selected && gad.Attribs&0x100 == 0 {
			g.drawListSelection(c, r, y, rowHeight)
		}
	}
}

// retailListWrapLines keeps the longest space- or CR-delimited run that fits
// the supplied width. Exact fits remain on the current line [07 R-WGT-01 §4].
func retailListWrapLines(text string, measure func(string) int, width int) []string {
	if text == "" || measure == nil || width < 0 {
		return []string{text}
	}
	var lines []string
	for len(text) != 0 {
		end := len(text)
		if cut := strings.IndexAny(text, " \r"); cut >= 0 {
			end = cut
		}
		best := end
		for cursor := end; cursor < len(text); {
			if text[cursor] == '\r' {
				break
			}
			for cursor < len(text) && text[cursor] == ' ' {
				cursor++
			}
			if cursor >= len(text) || text[cursor] == '\r' {
				break
			}
			next := cursor
			for next < len(text) && text[next] != ' ' && text[next] != '\r' {
				next++
			}
			if measure(text[:next]) > width {
				break
			}
			best, cursor = next, next
		}
		if best == 0 {
			best = end
		}
		lines = append(lines, text[:best])
		text = text[best:]
		if len(text) > 0 && text[0] == '\r' {
			text = text[1:]
		} else {
			text = strings.TrimLeft(text, " ")
		}
	}
	return lines
}

// retailListTextPen keeps the row's inclusive bounds and the builder's
// attribute precedence [07 R-WGT-01 §4].
func retailListTextPen(r gui.Rect, attrs uint32, textWidth int) (x, width int) {
	left, right := int(r.X)+2, int(r.X+r.W)
	switch {
	case attrs&1 != 0:
		return left, right - left + 1
	case attrs&4 != 0:
		return right - textWidth, textWidth
	case attrs&2 != 0:
		x = max(left, (left+right-textWidth)/2)
		return x, right - x + 1
	default:
		// TODO(T25): retail leaves the pen scratch unset when no alignment
		// bit is present; retain the existing host inset for that case.
		return int(r.X) + 4, int(r.W) - 4
	}
}

// retailVisibleListRows retains the click/scroll row count and its existing
// minimum-one host fallback. Painting uses its separate metric boundary
// [07 R-WGT-01 §4].
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
	c.UILightRect(g.assets.pal, int(r.X)+2, y, int(r.W)-1, h+1, retailListSelectionLevel)
}

// retailListSelectionLevel is the literal the retail implementation pushes for a selected
// list row, focused or not.
const retailListSelectionLevel = 30

func (g *gameShell) drawRetailScrollbar(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p != nil && p.ListAt(listIndexForAssocPanel(p, gad.Assoc)) != nil {
		g.drawRetailListScrollbar(c, p, index, gad, r)
		return
	}
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
	var mouse input.MouseState
	leftMouseHeld := false
	if c != nil && c.Input() != nil && c.Input().Mouse != nil {
		mouse, _ = c.Input().PointerSample()
		leftMouseHeld = mouse.Held(input.MouseButtonLeft)
	}

	left, top, right, bottom := int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)
	if vertical {
		arrowH := int(arrow0.Height)
		if int(arrow1.Height) > arrowH {
			arrowH = int(arrow1.Height)
		}
		if leftMouseHeld && pointInRect(int32(mouse.X), int32(mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: r.W, H: int32(arrowH)}) {
			if e.Frames[base+7].Frame != nil {
				arrow0 = e.Frames[base+7].Frame
			}
		}
		if leftMouseHeld && pointInRect(int32(mouse.X), int32(mouse.Y), gui.Rect{X: int32(left), Y: int32(bottom - arrowH), W: r.W, H: int32(arrowH)}) {
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
		listIndex := listIndexForAssocPanel(p, gad.Assoc)
		itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
		visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
		maxTop := p.ListMaxTopAt(listIndex)
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
	if leftMouseHeld && pointInRect(int32(mouse.X), int32(mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: int32(arrowW), H: r.H}) {
		if e.Frames[base+7].Frame != nil {
			arrow0 = e.Frames[base+7].Frame
		}
	}
	if leftMouseHeld && pointInRect(int32(mouse.X), int32(mouse.Y), gui.Rect{X: int32(right - arrowW), Y: int32(top), W: int32(arrowW), H: r.H}) {
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
	listIndex := listIndexForAssocPanel(p, gad.Assoc)
	itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
	visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
	maxTop := p.ListMaxTopAt(listIndex)
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
	if s := g.retailOptionsSliderAt(index); s != nil {
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

func (g *gameShell) drawRetailListScrollbar(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return
	}
	// Runtime construction has already removed the arrow extents from this
	// rectangle and appended the two arrow buttons. Paint the retained knob
	// serviced by Panel, within that same track [07 R-WGT-01 §5].
	vertical := gad.Attribs&1 == 0
	base := 0
	if !vertical {
		base = 10
	}
	track0, track1, track2 := e.Frames[base].Frame, e.Frames[base+1].Frame, e.Frames[base+2].Frame
	thumb0, thumb1, thumb2 := e.Frames[base+3].Frame, e.Frames[base+4].Frame, e.Frames[base+5].Frame
	if track0 == nil || track1 == nil || track2 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil {
		return
	}
	knobSize, _ := p.SliderMetricsAt(index, g.retailTextHeight())
	knob := p.SliderKnobAt(index)
	x, y := int(r.X), int(r.Y)
	if vertical {
		drawRetailScrollbarTrack(c, track0, track1, track2, y, x, y+int(r.H), false)
		thumbX := x + int(track0.Width)/2 - int(thumb0.Width)/2
		thumbY := y + 3 + knob
		length := min(knobSize, int(r.H)-6)
		drawRetailScrollbarThumbClipped(c, thumb0, thumb1, thumb2, thumbX, thumbY, length, false, r)
	} else {
		drawRetailScrollbarTrack(c, track0, track1, track2, x, y, x+int(r.W), true)
		thumbX := min(x+3+knob, x+int(r.W)-int(thumb0.Width)-2)
		thumbY := y + int(track0.Height)/2 - int(thumb0.Height)/2
		length := min(knobSize, int(r.W)-6)
		drawRetailScrollbarThumbClipped(c, thumb0, thumb1, thumb2, thumbX, thumbY, length, true, r)
	}
	if (gad.GrayedOut != 0 || gad.Attribs&gui.AttribInert != 0) && g.assets != nil {
		c.UIShadeRect(g.assets.pal, int(r.X), int(r.Y), int(r.W), int(r.H), retailGreyedButtonShade)
	}
}

// The final body tile and cap stay inside the painted knob's trailing clip
// boundary even when the repeatable middle does not divide its length
// [07 R-WGT-01 §5].
func drawRetailScrollbarThumbClipped(c *client.Client, first, middle, last *formats.GAFFrame, x, y, length int, horizontal bool, r gui.Rect) {
	if length <= 0 {
		return
	}
	clipX, clipY, clipW, clipH := x, y, int(first.Width), min(length, int(r.Y+r.H)-3-y)
	firstLen, middleLen, lastLen := int(first.Height), int(middle.Height), int(last.Height)
	if horizontal {
		clipW, clipH = min(length, int(r.X+r.W)-3-x), int(first.Height)
		firstLen, middleLen, lastLen = int(first.Width), int(middle.Width), int(last.Width)
	}
	if clipW <= 0 || clipH <= 0 || middleLen <= 0 {
		return
	}
	blit := func(f *formats.GAFFrame, pos int) {
		px, py := x, y+pos
		if horizontal {
			px, py = x+pos, y
		}
		c.UIBlitClipped(f, px, py, clipX, clipY, clipW, clipH)
	}
	blit(first, 0)
	for pos := firstLen; pos < length-lastLen; pos += middleLen {
		blit(middle, pos)
	}
	blit(last, length-lastLen)
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
	maxTop := 0
	if p := g.activePanel(); p != nil {
		maxTop = p.ListMaxTopAt(listIndexForAssocPanel(p, gad.Assoc))
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

func (g *gameShell) adjustRetailScrollbar(index int, gad gui.Gadget, delta int) {
	if g == nil || delta == 0 {
		return
	}
	// Scrollbar and slider are one kind [07 R-WGT-01 §5]; a kind-4 gadget on
	// the open options page drives its own value rather than a list origin.
	if g.adjustRetailSlider(index, delta) {
		return
	}
	l := g.listForAssoc(gad.Assoc)
	if l == nil || l.Len() == 0 {
		return
	}
	if p := g.activePanel(); p != nil {
		listIndex := listIndexForAssocPanel(p, gad.Assoc)
		_ = p.SetListTopAt(listIndex, l.Top()+delta, p.ListMaxTopAt(listIndex))
	}
}

func (g *gameShell) clickRetailScrollbar(index int, gad gui.Gadget, r gui.Rect, x, y int32) {
	if g.clickRetailSlider(index, r, x, y) {
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

func (g *gameShell) releaseRetailScrollbar(index int, gad gui.Gadget, r gui.Rect, x, y int32) {
	if g.releaseRetailSlider(index) {
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
		g.adjustRetailScrollbar(index, gad, -1)
	} else if coordinate >= geometry.axisEnd {
		g.adjustRetailScrollbar(index, gad, 1)
	}
}
