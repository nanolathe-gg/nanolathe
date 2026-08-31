package client

// The drag-selection rectangle is a presentation overlay, not authoritative
// selection state. It is composed after world/fog and before the UI stage
// [03 §1][R-SEL-02A].

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
	// TODO(question): what writes latch-flag bit 0x40 while the MOBILEBUILD
	// latch is armed is unknown. The bit's documented role is selecting
	// immediate-versus-special helptext and its only established writers are
	// the order-button dispatcher's arming chain, which never arms MOBILEBUILD
	// — the battle-HUD build-button handler does, and it is not recorded as
	// touching the flags word [07 §9]. Tracing the build-button handler's
	// writes to the latch-flags word would settle it; until then Nanolathe
	// leaves the bit clear and the armed drag takes entry 4.
	SpecialLatchFlag bool

	// VisiblePanel is true for the captured visible-panel battle surface. The
	// hidden/panel-mode descriptor is not established and is never inferred.
	VisiblePanel bool
}

// SetSelectionDrag publishes the presentation-only drag gesture used by the
// next composition. Clearing it removes the rectangle on the next frame.
func (c *Client) SetSelectionDrag(d SelectionDrag) {
	if c != nil {
		c.selectionDrag = d
	}
}

// visiblePanelSelectionClip is the captured visible-panel runtime surface
// descriptor. Coordinates are logical framebuffer/shell coordinates; the
// projection's +128/+32 origin is a separate record [R-SEL-02A].
func (c *Client) visiblePanelSelectionClip() Rect {
	if c == nil {
		return Rect{MinX: 1, MinY: 1, MaxX: 0, MaxY: 0}
	}
	// TODO(T23): hidden/panel-mode selection clipping is not established; the
	// chosen placeholder is to suppress that overlay until a mode-specific
	// retail capture settles the seam.
	return Rect{MinX: 128, MinY: 32, MaxX: int32(c.width) - 1, MaxY: int32(c.height) - 33}
}

func (c *Client) drawSelectionDrag() {
	if c == nil || !c.selectionDrag.Active {
		return
	}
	d := c.selectionDrag
	if !d.VisiblePanel {
		// TODO(T23): hidden/panel-mode selection clipping remains unknown; the
		// chosen placeholder is to suppress the overlay pending a mode capture.
		return
	}
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
	clip := c.visiblePanelSelectionClip()
	drawIndexedFrameInclusive(c.indexed, c.width, c.height, r, outer, clip)
	inset := Rect{MinX: r.MinX + 1, MinY: r.MinY + 1, MaxX: r.MaxX - 1, MaxY: r.MaxY - 1}
	if inset.MinX <= inset.MaxX && inset.MinY <= inset.MaxY {
		drawIndexedFrameInclusive(c.indexed, c.width, c.height, inset, inner, clip)
	}
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
