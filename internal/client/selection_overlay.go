package client

// The drag-selection rectangle is a presentation overlay, not authoritative
// selection state. It is composed after world/fog and before the UI stage
// [03 §1][R-SEL-02A].

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// SelectionDrag is the current input-owned drag gesture in logical framebuffer
// coordinates. The caller supplies the latch state because the renderer does
// not own input latches.
type SelectionDrag struct {
	Active bool
	StartX int32
	StartY int32
	EndX   int32
	EndY   int32

	// MobileBuildLatch is true while the armed order latch is MOBILEBUILD
	// (latch value 0xE) [07 §9]. Only then is the rectangle a build ghost,
	// which takes the site-validity colour on both of its frames; an ordinary
	// selection drag takes logical entry 15 outer and entry 0 inner
	// [07 §9 "Build placement is closed"].
	MobileBuildLatch bool
	// SpecialLatchFlag is the site-valid verdict for the armed placement, and
	// it picks which validity colour the ghost takes
	// [07 §9 "Build placement is closed"].
	//
	// Retail keeps it in one interface flags byte — not two. That byte's bit 3
	// is the button gate every order-button arm clears, and its bit 6 is the
	// site-valid bit: the in-view placement preview writes it from the
	// footprint verdict on each pointer update, the world click tests it before
	// issuing the build order, and the shared rectangle drawer tests it to pick
	// the ghost's colour [07 R-CAM-01 §14]. A single boolean here is therefore
	// the retail storage model, and the armed drag box is green-lit exactly
	// when the build click would be accepted: the caller passes the same
	// placement verdict the cursor and the click read. Descriptions of a
	// separate "pointer-flags byte" holding bit 6 apart from a "latch-flag
	// word" describe one byte twice.
	SpecialLatchFlag bool

	// VisiblePanel says the rail is at its visible detent. It no longer gates
	// whether the rectangle is drawn — see drawSelectionDrag — and is retained
	// so a caller can still report the panel state.
	VisiblePanel bool
}

// SetSelectionDrag publishes the presentation-only drag gesture used by the
// next composition. Clearing it removes the rectangle on the next frame.
func (c *Client) SetSelectionDrag(d SelectionDrag) {
	if c != nil {
		c.selectionDrag = d
	}
}

// selectionClip is the runtime surface descriptor's inclusive clip rectangle,
// which is what the selection solid-frame writer consumes. Coordinates are
// logical framebuffer/shell coordinates; the projection's +128/+32 origin is a
// separate record and must not be substituted for these [R-SEL-02A].
//
// It is the same rectangle whatever the rail is doing. The viewport subrect has
// exactly one writer — the builder that hard-codes left 128 and top 32 and
// derives `W−1` and `H−33` — and a census for a second, hidden-panel writer
// came back empty [03 §4.1]. So a slid or parked rail does not move the clip,
// and the overlay is drawn in every panel state.
//
// TODO(T23): §4.1 still lists a hidden-panel *expansion* of the subrect to
// `(0, 0, W−1, H−1)` as a prediction with no writer behind it, and doc 07's
// [R-SEL-02A] separately records a `(0, 32, W−1, H−33)` description of the same
// record from a transition/input path. Both would only move the clip's LEFT
// edge, and the placeholder here is the one with a traced writer. A retail
// capture of the descriptor at the selection draw with the rail retracted
// settles it; nothing else will.
// The clip is the FRAMEBUFFER's, not the record extent's: the rectangle is
// drawn from pointer coordinates outside the world region, so both the shape
// and the rectangle that bounds it are framebuffer pixels at every factor
// (DESIGN_GPU_RENDERER §16.3). At a rest factor the two extents are the same
// number, so this changes no composed pixel there.
func (c *Client) selectionClip() Rect {
	if c == nil {
		return Rect{MinX: 1, MinY: 1, MaxX: 0, MaxY: 0}
	}
	return Rect{MinX: 128, MinY: 32, MaxX: int32(c.width) - 1, MaxY: int32(c.height) - 33}
}

func (c *Client) drawSelectionDrag() {
	if c == nil || !c.selectionDrag.Active {
		return
	}
	d := c.selectionDrag
	// The rail's state does not suppress the rectangle. This used to return
	// early unless the panel was at its visible detent, under an accepted-blocked
	// marker reading "hidden/panel-mode selection clipping remains unknown; the
	// chosen placeholder is to suppress the overlay pending a mode capture" — a
	// third arm neither candidate reading offers. Both descriptions of the clip
	// rectangle in [R-SEL-02A] draw the overlay and disagree only about its
	// left edge, and the composer's own clip has one writer that never consults
	// the rail [03 §4.1]. Suppressing it made a drag started while the rail was
	// mid-slide invisible for the whole gesture.
	r := NormalizeRect(d.StartX, d.StartY, d.EndX, d.EndY)
	// The ordinary drag-selection rectangle is white: outer logical entry 15,
	// inner entry 0. The armed MOBILEBUILD latch draws the build ghost instead,
	// and there validity is a colour change and not a shape change: both the
	// outer frame and the one-pixel inset inner frame take the same resolved
	// GUI semantic index — 10 when the site is legal, 4 when it is not
	// [07 §9 "Build placement is closed"]. Entry 0 is the inner frame of the
	// ordinary drag box alone. This previously painted the legal ghost in
	// entry 6 with a black inner frame, following doc 07 §6's account of the
	// pair rather than §9's; 6 and 10 are different colours in GUIPAL's first
	// sixteen entries. The pair is taken from the HUD's ghost constants so this
	// drawer and the battle screen's own ghost cannot drift apart.
	logicalOuter, logicalInner := byte(15), byte(0)
	if d.MobileBuildLatch {
		logicalOuter = hud.GhostColorIllegal
		if d.SpecialLatchFlag {
			logicalOuter = hud.GhostColorLegal
		}
		logicalInner = logicalOuter
	}
	// Resolve each logical entry once before the indexed writer; the frame
	// itself performs no palette lookup or per-pixel remap [R-SEL-02A].
	outer := c.paletteIndex(logicalOuter)
	inner := c.paletteIndex(logicalInner)
	clip := c.selectionClip()
	// Record then execute inline: classicSink.Fill's FillFrameInclusive style
	// runs the same drawIndexedFrameInclusive writer with the same inclusive clip
	// this used to call directly. Both the frame rect and the clip are carried in
	// the Fill family's extent form; the sink reconstructs the inclusive bounds
	// [R-SEL-02A].
	clipExt := inclusiveToExtent(clip)
	c.emitFill(drawlist.Fill{Rect: inclusiveToExtent(r), Index: outer, Style: drawlist.FillFrameInclusive, Clip: clipExt})
	inset := Rect{MinX: r.MinX + 1, MinY: r.MinY + 1, MaxX: r.MaxX - 1, MaxY: r.MaxY - 1}
	if inset.MinX <= inset.MaxX && inset.MinY <= inset.MaxY {
		c.emitFill(drawlist.Fill{Rect: inclusiveToExtent(inset), Index: inner, Style: drawlist.FillFrameInclusive, Clip: clipExt})
	}
}

// inclusiveToExtent converts a client inclusive rectangle (MinX..MaxX,
// MinY..MaxY) into the drawlist Fill family's extent form. The covered pixel set
// is preserved: [X, X+W) x [Y, Y+H) is exactly [MinX..MaxX] x [MinY..MaxY], so
// the executor recovers the original inclusive bounds as X+W-1 / Y+H-1 [C-G2].
func inclusiveToExtent(r Rect) drawlist.Rect {
	return drawlist.Rect{X: r.MinX, Y: r.MinY, W: r.MaxX - r.MinX + 1, H: r.MaxY - r.MinY + 1}
}

// drawIndexedFrameInclusive writes a one-pixel solid frame through both
// inclusive boundaries, clipping against clip. It is shared by selection and
// other indexed frame callers so endpoint behavior cannot drift [R-SEL-02A].
func drawIndexedFrameInclusive(dst []uint8, width, height int, r Rect, idx uint8, clip Rect) {
	if width <= 0 || height <= 0 || len(dst) < width*height || r.MinX > r.MaxX || r.MinY > r.MaxY || clip.MinX > clip.MaxX || clip.MinY > clip.MaxY {
		return
	}
	write := func(x, y int32) {
		if x < 0 || y < 0 || x >= int32(width) || y >= int32(height) {
			return
		}
		if x < clip.MinX || x > clip.MaxX || y < clip.MinY || y > clip.MaxY {
			return
		}
		dst[int(y)*width+int(x)] = idx
	}
	// Clip each original edge independently. Intersecting the rectangle first
	// would manufacture a new edge along the clip boundary [R-SEL-02A].
	horizontal := func(y int32) {
		if y < clip.MinY || y > clip.MaxY {
			return
		}
		left, right := max(r.MinX, clip.MinX), min(r.MaxX, clip.MaxX)
		for x := left; x <= right; x++ {
			write(x, y)
		}
	}
	vertical := func(x int32) {
		if x < clip.MinX || x > clip.MaxX {
			return
		}
		top, bottom := max(r.MinY, clip.MinY), min(r.MaxY, clip.MaxY)
		for y := top; y <= bottom; y++ {
			write(x, y)
		}
	}
	horizontal(r.MinY)
	if r.MaxY != r.MinY {
		horizontal(r.MaxY)
	}
	vertical(r.MinX)
	if r.MaxX != r.MinX {
		vertical(r.MaxX)
	}
}
