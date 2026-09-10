package client

// The drag-selection rectangle is a presentation overlay, not authoritative
// selection state. It is composed after world/fog and before the UI stage
// [03 §1][R-SEL-02A].

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

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
	// (latch value 0xE) [07 §9]. Only then does the outer frame take the 6/4
	// pair; an ordinary selection drag takes logical entry 15
	// [07 R-P0-11 §1 "The drawing."][07 §6 "Frame composition passes"].
	MobileBuildLatch bool
	// SpecialLatchFlag mirrors latch-flag bit 0x40, which picks outer entry 6
	// over entry 4 while MOBILEBUILD is armed [07 §6].
	//
	// The bit has a writer, and one only: it is the pointer-flags byte's
	// **site-valid bit**, written by the in-view placement preview — which the
	// frame handler runs only while the pointer is over the view and the latch
	// is MOBILEBUILD — and cleared by the world rebuild
	// [07 R-CAM-01 §14 step 1]. That section says so in as many words: "This
	// bit is also the 'special latch flag' that picks the drag-box colour in
	// §9." So the armed drag box is green-lit exactly when the build click
	// would be accepted, and the caller passes the same placement verdict the
	// cursor and the click read. This previously carried an open-question marker,
	// "what writes latch-flag bit 0x40 while the MOBILEBUILD latch is armed is
	// unknown", which was wrong twice over — it hunted the helptext role of the
	// bit, and it looked in the order-button dispatcher's arming chain, which
	// is not where the write lives.
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
	// inner entry 0 [07 R-P0-11 §1 "The drawing."]. The 6/4 pair belongs to the
	// armed MOBILEBUILD latch alone — entry 6 when latch-flag bit 0x40 is set,
	// 4 when it is clear [07 §6 "Frame composition passes"]. An earlier reading
	// had this inverted, taking entry 4 for every drag and reaching 15 only on
	// a branch the caller could not select, which painted the selection box
	// dark red (logical 4 resolves to a dark red physical index).
	logicalOuter := byte(15)
	if d.MobileBuildLatch {
		logicalOuter = 4
		if d.SpecialLatchFlag {
			logicalOuter = 6
		}
	}
	// Resolve each logical entry once before the indexed writer; the frame
	// itself performs no palette lookup or per-pixel remap [R-SEL-02A].
	outer := c.paletteIndex(logicalOuter)
	inner := c.paletteIndex(0)
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
		left, right := maxInt32(r.MinX, clip.MinX), minInt32(r.MaxX, clip.MaxX)
		for x := left; x <= right; x++ {
			write(x, y)
		}
	}
	vertical := func(x int32) {
		if x < clip.MinX || x > clip.MaxX {
			return
		}
		top, bottom := maxInt32(r.MinY, clip.MinY), minInt32(r.MaxY, clip.MaxY)
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

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
