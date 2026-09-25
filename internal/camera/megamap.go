package camera

// The megamap lens: the full-screen overview's fit of the whole map into the
// battle viewport and its two conversions (DESIGN_INTERFACE_HUD_INPUT §3.15).
// It is a host presentation lens modelled on the community draw engine's
// shipped ProTA 4.8 megamap
// ([ProTA 4.8 shipped megamap](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)),
// not a retail camera concept, and nothing in the simulation reads it [I6].

// MegamapLens places the map image inside a viewport rectangle.
//
// ViewX/ViewY/ViewW/ViewH is the battle viewport in framebuffer pixels;
// X/Y/W/H is the fitted image inside it. ExtentW/ExtentH is the world extent
// the image stands for, in world units (map pixels).
type MegamapLens struct {
	ViewX, ViewY, ViewW, ViewH int32
	X, Y, W, H                 int32
	ExtentW, ExtentH           int32
}

// MegamapExtent is the one frame every megamap layer shares: the retail play
// area [fmt tnt], `(Width − 2) × 16` by `(Height − 8) × 16` world units from
// the TNT header's width and height in 16-pixel attribute cells, which is
// `(Width/2 − 1)` by `(Height/2 − 4)` 32-pixel tiles. The terrain picture is
// sampled over it, and icons, projectiles, rings, the overlay and the pointer
// are scaled by it.
//
// Host choice (DESIGN_INTERFACE_HUD_INPUT §3.15): the shipped build fits and
// samples its terrain picture over this area but scales everything drawn over
// it, and the pointer, by the larger `(Width − 1) × 16` by `(Height − 4) × 16`,
// so its overlays sit up and left of the terrain by up to 16 and 64 map pixels
// ([draw-engine-interface "Map scale"](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)).
// Nanolathe does not copy that offset.
func MegamapExtent(cellW, cellH int32) (int32, int32) {
	w, h := (cellW-2)*16, (cellH-8)*16
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// LayoutMegamap fits the extent's aspect into the viewport and centres it.
// The fit is the shipped picture's, in single-precision floats: with the view
// `w × h` and the extent `cols × rows`, a wider extent keeps the width and
// takes `trunc(w / cols × rows)` rows, a taller one keeps the height and takes
// `trunc(h / rows × cols)` columns. The extent is compared in world units,
// which is the tile comparison scaled by 32, a power of two, so every
// intermediate rounds identically [draw-engine-interface "Terrain picture"].
//
// Host choice: when the two aspects are exactly equal the shipped build makes
// a square of the smaller side; Nanolathe keeps the width branch instead, so
// the picture always has the extent's aspect.
//
// A spare margin is split evenly only when it is greater than two pixels; a
// margin of one or two pixels stays at the far edge, as the shipped build
// leaves it [draw-engine-interface "What the view shows"].
func LayoutMegamap(viewX, viewY, viewW, viewH, extentW, extentH int32) MegamapLens {
	l := MegamapLens{ViewX: viewX, ViewY: viewY, ViewW: viewW, ViewH: viewH, ExtentW: extentW, ExtentH: extentH}
	if viewW <= 0 || viewH <= 0 || extentW <= 0 || extentH <= 0 {
		return l
	}
	w, h := float32(viewW), float32(viewH)
	cols, rows := float32(extentW), float32(extentH)
	if cols < w/h*rows {
		l.H = viewH
		l.W = int32(h / rows * cols)
	} else {
		l.W = viewW
		l.H = int32(w / cols * rows)
	}
	l.W, l.H = min(max(l.W, 1), viewW), min(max(l.H, 1), viewH)
	l.X, l.Y = viewX, viewY
	if spare := viewW - l.W; spare > 2 {
		l.X += spare / 2
	}
	if spare := viewH - l.H; spare > 2 {
		l.Y += spare / 2
	}
	return l
}

// Valid reports whether the lens has an image.
func (l MegamapLens) Valid() bool { return l.W > 0 && l.H > 0 && l.ExtentW > 0 && l.ExtentH > 0 }

// ScaleX and ScaleY are the floating-point image-per-world factors the
// shipped build uses for both directions [draw-engine-interface "Map scale"].
func (l MegamapLens) ScaleX() float64 { return float64(l.W) / float64(l.ExtentW) }
func (l MegamapLens) ScaleY() float64 { return float64(l.H) / float64(l.ExtentH) }

// RowPitch is the four-aligned image width the shipped build clips icons to
// and scales ring radii by: the image width rounded down to a multiple of four
// [draw-engine-interface "Unit icons", "Rings"].
func (l MegamapLens) RowPitch() int32 { return l.W &^ 3 }

// Project maps a world point, in map pixels with its height, to image-local
// pixels: `trunc(x × W / extentW)`, `trunc((z − y/2) × H / extentH)` with
// floating-point factors [draw-engine-interface "Map scale"].
func (l MegamapLens) Project(x, y, z int32) (int32, int32) {
	if !l.Valid() {
		return 0, 0
	}
	return int32(float64(x) * l.ScaleX()), int32((float64(z) - float64(y)/2) * l.ScaleY())
}

// Unproject maps an image-local pixel back to a world ground point by dividing
// by the same factors and truncating. It deliberately does not correct for
// the half-height shear: the caller gives the point the terrain height at that
// spot [draw-engine-interface "Map scale"].
func (l MegamapLens) Unproject(px, py int32) (int32, int32) {
	if !l.Valid() {
		return 0, 0
	}
	return int32(float64(px) / l.ScaleX()), int32(float64(py) / l.ScaleY())
}

// ContainsScreen reports whether a framebuffer point lies on the image.
func (l MegamapLens) ContainsScreen(x, y int32) bool {
	return l.Valid() && x >= l.X && x < l.X+l.W && y >= l.Y && y < l.Y+l.H
}

// ContainsView reports whether a framebuffer point lies in the viewport the
// megamap covers, margins included.
func (l MegamapLens) ContainsView(x, y int32) bool {
	return x >= l.ViewX && x < l.ViewX+l.ViewW && y >= l.ViewY && y < l.ViewY+l.ViewH
}

// ClampScreen clamps a framebuffer point onto the image and returns it in
// image-local pixels. A camera move and the box selection both clamp the
// pointer to the image [draw-engine-interface "Entering and leaving",
// "Input while shown"].
func (l MegamapLens) ClampScreen(x, y int32) (int32, int32) {
	px, py := x-l.X, y-l.Y
	if px < 0 {
		px = 0
	} else if px >= l.W {
		px = l.W - 1
	}
	if py < 0 {
		py = 0
	} else if py >= l.H {
		py = l.H - 1
	}
	return px, py
}

// ScreenToWorld is Unproject of a framebuffer point clamped onto the image.
func (l MegamapLens) ScreenToWorld(x, y int32) (int32, int32) {
	px, py := l.ClampScreen(x, y)
	return l.Unproject(px, py)
}
