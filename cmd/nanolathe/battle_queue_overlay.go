package main

// Ebitengine battle presentation adapter for the immutable Shift queue
// overlay.  The geometry and gating live in internal/hud so they can be
// tested without opening a window; this file only projects and rasterizes the
// returned instructions.

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// drawQueueOverlay is the sole battle integration call required by QUEUE-02.
// It consumes only a published frame and input presentation state.  Releasing
// Shift returns before constructing instructions and cannot mutate orders or
// influence an authoritative hash [07 §9][R-P0-11 §4].
func drawQueueOverlay(c *client.Client, f *frame.Frame, tick uint32, shiftHeld bool, localOwner uint8, hovered pool.Handle) {
	if c == nil || f == nil || !shiftHeld {
		return
	}
	project := func(x, y, z numeric.Fixed) hud.QueuePoint {
		sx, sy := c.WorldToScreenPx(x, y, z)
		return hud.QueuePoint{X: sx, Y: sy}
	}
	opts := hud.QueueOverlayOptions{
		Tick:        tick,
		ShiftHeld:   shiftHeld,
		LocalOwner:  localOwner,
		HoveredUnit: hovered,
		Project:     project,
		BuildRect: func(o frame.OrderView) (hud.QueueRect, bool) {
			fx, fz := int32(o.FootX), int32(o.FootZ)
			if fx <= 0 || fz <= 0 {
				// A missing authored footprint is not a square to be guessed.
				return hud.QueueRect{}, false
			}
			cx, cz := world.PlacementAnchor(o.GoalX, o.GoalZ, fx, fz)
			left := numeric.Fixed(int64(cx*16) << 16)
			top := numeric.Fixed(int64(cz*16) << 16)
			right := numeric.Fixed(int64((cx+fx)*16) << 16)
			bottom := numeric.Fixed(int64((cz+fz)*16) << 16)
			l := project(left, o.GoalY, top)
			r := project(right, o.GoalY, bottom)
			return hud.QueueRect{Left: l.X, Top: l.Y, Right: r.X, Bottom: r.Y}, true
		},
		// Target-unit radii and authored icon GAF metadata are not in the
		// immutable frame yet.  Suppressing these callbacks is required by the
		// clean-room contract; do not substitute a guessed radius/artwork.
	}
	for _, op := range hud.QueueOverlay(f, opts) {
		switch op.Kind {
		case hud.QueuePrimitiveMarker:
			for _, seg := range op.Segments {
				drawQueueLine(c, hud.QueuePoint{X: seg.X0, Y: seg.Y0}, hud.QueuePoint{X: seg.X1, Y: seg.Y1}, c.GUIColor(seg.Color))
			}
		case hud.QueuePrimitiveDash, hud.QueuePrimitiveCircle:
			// Dash artwork/cadence and overlay color-index initialization are
			// not yet available at this presentation seam. A solid line with a
			// guessed GUI color would contradict [R-P0-11 §3], so retain the
			// immutable instruction for inspection and suppress rasterization.
			if op.ColorKnown {
				drawQueueLine(c, op.A, op.B, c.GUIColor(op.Color))
			}
		case hud.QueuePrimitiveIcon:
			// TODO(question): authored queue-icon GAF lookup and blit anchor
			// remain unresolved [R-P0-11 §3]. The HUD helper emits an icon only
			// when an integration supplies immutable authored frame metadata.
		}
	}
}

// drawQueueLine is the indexed-framebuffer equivalent of retail's integer
// line primitive.  UIFillRect clips every pixel to the client viewport.
func drawQueueLine(c *client.Client, a, b hud.QueuePoint, color uint8) {
	dx := absInt32(b.X - a.X)
	dy := absInt32(b.Y - a.Y)
	sx, sy := int32(1), int32(1)
	if a.X > b.X {
		sx = -1
	}
	if a.Y > b.Y {
		sy = -1
	}
	err := dx - dy
	for {
		c.UIFillRect(int(a.X), int(a.Y), 1, 1, color)
		if a == b {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			a.X += sx
		}
		if e2 < dx {
			err += dx
			a.Y += sy
		}
	}
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
