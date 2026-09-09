package main

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
)

// drawWindowPanel shares the authored panel resolution and clipped child-surface
// fill between frontend windows and battle modals [07 R-FE-02 §4][07 R-WGT-01 §12].
func drawWindowPanel(c *client.Client, window *gui.Window, page, common *formats.GAF, color func(byte) byte) {
	if c == nil || window == nil || window.Rect.W <= 0 || window.Rect.H <= 0 {
		return
	}
	r := window.Rect
	x, y, w, h := int(r.X), int(r.Y), int(r.W), int(r.H)
	if entry := windowPanelEntry(window.Header.Panel, page, common); entry != nil {
		nineSliceFill(entry, x, y, w, h, func(f *formats.GAFFrame, px, py int) { c.UIBlitClipped(f, px, py, x, y, w, h) })
		return
	}
	// Without art, fill + sunken uses fields 17 on top/left, 0 on bottom/right,
	// and 20 inside. Inclusive line endpoints and their draw order matter at
	// shared corners, including a window smaller than the bevel [07 R-FE-02 §4].
	drawGUIBevel(c, r, color(17), color(0), color(20))
}

// drawGUIBevel fills and draws the eight ordered edge runs shared by art-less
// windows and buttons. Callers select the exact top/left and bottom/right
// colours rather than inferring them from a visual raised/sunken label
// [07 R-FE-02 §4].
func drawGUIBevel(c *client.Client, r gui.Rect, topLeft, bottomRight, fill byte) {
	if c == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	x, y, w, h := int(r.X), int(r.Y), int(r.W), int(r.H)
	c.UIFillRect(x, y, w, h, fill)
	line := func(x1, y1, x2, y2 int, index byte) {
		left, top := max(x, min(x1, x2)), max(y, min(y1, y2))
		right, bottom := min(x+w-1, max(x1, x2)), min(y+h-1, max(y1, y2))
		if left <= right && top <= bottom {
			c.UIFillRect(left, top, right-left+1, bottom-top+1, index)
		}
	}
	right, bottom := x+w-1, y+h-1
	dark, light := topLeft, bottomRight
	line(x, y, right, y, dark)
	line(x, y+1, right-1, y+1, dark)
	line(x, y, x, bottom, dark)
	line(x+1, y, x+1, bottom-1, dark)
	line(right, y+1, right, bottom, light)
	line(right-1, y+2, right-1, bottom, light)
	line(x+1, bottom, right, bottom, light)
	line(x+2, bottom-1, right, bottom-1, light)
}

// drawGUIBevelClipped is the same ordered bevel written into a modal's
// private surface. The frontend path already draws into a window-sized
// surface; battle composes directly into the presentation surface and needs
// this explicit child boundary [07 R-FE-02 §4].
func drawGUIBevelClipped(c *client.Client, r gui.Rect, topLeft, bottomRight, fill byte, clip gui.Rect) {
	if c == nil || r.W <= 0 || r.H <= 0 || clip.W <= 0 || clip.H <= 0 {
		return
	}
	x, y, w, h := int(r.X), int(r.Y), int(r.W), int(r.H)
	clipFill(c, x, y, w, h, fill, clip)
	line := func(x1, y1, x2, y2 int, index byte) {
		left, top := max(x, min(x1, x2)), max(y, min(y1, y2))
		right, bottom := min(x+w-1, max(x1, x2)), min(y+h-1, max(y1, y2))
		if left <= right && top <= bottom {
			clipFill(c, left, top, right-left+1, bottom-top+1, index, clip)
		}
	}
	right, bottom := x+w-1, y+h-1
	line(x, y, right, y, topLeft)
	line(x, y+1, right-1, y+1, topLeft)
	line(x, y, x, bottom, topLeft)
	line(x+1, y, x+1, bottom-1, topLeft)
	line(right, y+1, right, bottom, bottomRight)
	line(right-1, y+2, right-1, bottom, bottomRight)
	line(x+1, bottom, right, bottom, bottomRight)
	line(x+2, bottom-1, right, bottom-1, bottomRight)
}

func clipFill(c *client.Client, x, y, w, h int, color byte, clip gui.Rect) {
	if c == nil || w <= 0 || h <= 0 {
		return
	}
	left, top := max(x, int(clip.X)), max(y, int(clip.Y))
	right, bottom := min(x+w, int(clip.X+clip.W)), min(y+h, int(clip.Y+clip.H))
	if left < right && top < bottom {
		c.UIFillRect(left, top, right-left, bottom-top, color)
	}
}

// windowPanelEntry resolves a window's panel entry: the window's own GAF, then
// the common GUI GAF, then the common GAF's literal `BackTile` [07 R-WGT-01 §12]
// [07 R-FE-02 §4]. A name that resolves nowhere, or an empty one, falls to
// `BackTile`; only a common GAF without `BackTile` yields nil.
func windowPanelEntry(name string, page, common *formats.GAF) *formats.GAFEntry {
	if name != "" {
		for _, gaf := range []*formats.GAF{page, common} {
			if gaf == nil {
				continue
			}
			if entry, ok := gaf.Find(name); ok && len(entry.Frames) != 0 {
				return entry
			}
		}
	}
	if common != nil {
		if entry, ok := common.Find("BackTile"); ok && len(entry.Frames) != 0 {
			return entry
		}
	}
	return nil
}

// nineSliceFill is the GUI tile fill for a resolved art entry over the
// rectangle `(x0, y0)` of size `w x h` [07 R-FE-02 §4].
//
// An entry with fewer than two frames is stamped once at the origin and is
// not tiled. Otherwise frame 0's size is the tile pitch and every tile picks
// its frame by band and column, `band + column` in 0..8:
//
//	band:   0 on the first row; then 6 (bottom) when the tile would overflow
//	        the rectangle (`y + tileH > h`), else 3 (middle);
//	column: 2 (right) when the tile reaches or passes the right edge
//	        (`x + tileW >= w`), else 1 (middle) unless `x == 0`, then 0 (left).
//
// A row that overflows is pulled flush to the bottom (`y = h - tileH`) and a
// right column flush to the right (`x = w - tileW`), overlapping the previous
// tile rather than being clipped. The two edge tests differ in strictness — a
// row that ends exactly on the bottom edge is a middle band, a column that
// ends exactly on the right edge is the right column — and are kept as traced.
func nineSliceFill(entry *formats.GAFEntry, x0, y0, w, h int, blit func(f *formats.GAFFrame, x, y int)) {
	if entry == nil || len(entry.Frames) == 0 || w <= 0 || h <= 0 {
		return
	}
	frame := func(i int) *formats.GAFFrame {
		if i < 0 || i >= len(entry.Frames) {
			return nil
		}
		return entry.Frames[i].Frame
	}
	first := frame(0)
	if first == nil || first.Width == 0 || first.Height == 0 {
		return
	}
	if len(entry.Frames) < 2 {
		blit(first, x0, y0)
		return
	}
	tileW, tileH := int(first.Width), int(first.Height)
	for y := 0; y < h; y += tileH {
		band := 0
		if y != 0 {
			band = 3
			if y+tileH > h {
				band = 6
			}
		}
		if y+tileH > h {
			y = h - tileH
		}
		for x := 0; x < w; x += tileW {
			column := 0
			if x+tileW >= w {
				column = 2
				x = w - tileW
			} else if x != 0 {
				column = 1
			}
			if f := frame(band + column); f != nil {
				blit(f, x0+x, y0+y)
			}
		}
	}
}
