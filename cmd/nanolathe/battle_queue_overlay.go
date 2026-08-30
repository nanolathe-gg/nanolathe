package main

// Ebitengine battle presentation adapter for the immutable Shift queue
// overlay.  The geometry and gating live in internal/hud so they can be
// tested without opening a window; this file only projects and rasterizes the
// returned instructions.

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// DashChainEntry is the authored GAF entry that supplies the travelling-dash
// sprite chain.  It sits in the cursor GAF root beside the named cursors and is
// resolved by the same loader, which is why the chain reads as a row of small
// cursor-like sprites marching along a queue line [R-P0-11 §3].
const DashChainEntry = "pathicon"

// dashChain caches the resolved dash sprite entry for one mounted install.
// Resolution is by name from the cursor GAF root; a missing entry suppresses
// the chain rather than substituting artwork.
type dashChainCache struct {
	fs    vfs.FSOps
	entry *formats.GAFEntry
	done  bool
}

var dashChain dashChainCache

func dashChainEntry(fs vfs.FSOps) *formats.GAFEntry {
	if fs == nil {
		return nil
	}
	if dashChain.done && dashChain.fs == fs {
		return dashChain.entry
	}
	dashChain = dashChainCache{fs: fs, done: true}
	gaf, err := formats.LoadGAFFile(fs, client.CursorGAFPath)
	if err != nil {
		return nil
	}
	if e, ok := gaf.Find(DashChainEntry); ok {
		dashChain.entry = e
	}
	return dashChain.entry
}

// drawQueueOverlay is the sole battle integration call required by QUEUE-02.
// It consumes only a published frame and input presentation state.  Releasing
// Shift returns before constructing instructions and cannot mutate orders or
// influence an authoritative hash [07 §9][R-P0-11 §4].
func drawQueueOverlay(c *client.Client, b *battleSession, f *frame.Frame, tick uint32, shiftHeld bool, localOwner uint8, hovered pool.Handle) {
	if c == nil || b == nil || f == nil || !shiftHeld {
		return
	}
	// The composed world surface is viewport-relative: every world drawer
	// subtracts the projection's baked-in view origin before it writes a pixel.
	// WorldToScreenPx keeps that origin, so the overlay has to remove it or the
	// whole overlay lands one view origin down and to the right of the units and
	// the build ghost it is supposed to sit on [03 §2.5].
	project := func(x, y, z numeric.Fixed) hud.QueuePoint {
		sx, sy := c.WorldToScreenPx(x, y, z)
		return hud.QueuePoint{X: sx - camera.OriginX, Y: sy - camera.OriginY}
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
			// Retail projects the queued site exactly as it projects the armed
			// ghost: the cell-aligned footprint rectangle with the order's own
			// site height at both corners [07 §9]. Reusing the ghost's own
			// helper is what keeps the two from drifting apart.
			cx, cz := world.PlacementAnchor(o.GoalX, o.GoalZ, fx, fz)
			l, t, r, btm := b.siteRectToScreen(cx*16, cz*16, (cx+fx)*16, (cz+fz)*16, int32(o.GoalY>>16))
			return hud.QueueRect{Left: l, Top: t, Right: r, Bottom: btm}, true
		},
		// Target-unit radii and authored icon GAF metadata are not in the
		// immutable frame yet.  Suppressing these callbacks is required by the
		// clean-room contract; do not substitute a guessed radius/artwork.
	}
	chain := dashChainEntry(b.fs)
	for _, op := range hud.QueueOverlay(f, opts) {
		switch op.Kind {
		case hud.QueuePrimitiveMarker:
			for _, seg := range op.Segments {
				drawQueueLine(c, hud.QueuePoint{X: seg.X0, Y: seg.Y0}, hud.QueuePoint{X: seg.X1, Y: seg.Y1}, c.GUIColor(seg.Color))
			}
		case hud.QueuePrimitiveDash:
			drawDashChain(c, chain, op, project)
		case hud.QueuePrimitiveCircle:
			// The overlay color-index initialization for the circle helper is
			// not yet available at this presentation seam. A guessed GUI color
			// would contradict [R-P0-11 §3], so retain the immutable instruction
			// for inspection and suppress rasterization.
			if op.ColorKnown {
				drawQueueLine(c, op.A, op.B, c.GUIColor(op.Color))
			}
		case hud.QueuePrimitiveIcon:
			// TODO(question): the per-order queued-order icon indexes the cursor
			// handle array with the order descriptor's icon byte, which the
			// immutable frame does not carry (the frame publishes the order kind
			// as text, not its descriptor). Publishing the descriptor icon byte
			// on OrderView would settle it [R-P0-11 §3].
		}
	}
}

// drawDashChain blits the authored travelling-dash sprites along one queue
// segment.  The frame's authored offsets are its hotspot, exactly as for the
// software cursor: the sprite is placed so that pixel lands on the interpolated
// point [R-P0-11 §3][fmt gaf "Placement offsets"].
func drawDashChain(c *client.Client, entry *formats.GAFEntry, op hud.QueuePrimitive, project func(x, y, z numeric.Fixed) hud.QueuePoint) {
	if entry == nil || len(entry.Frames) == 0 {
		return
	}
	ticksPerFrame := int(entry.Frames[0].Value)
	hud.DashSprites(op.WorldA, op.WorldB, op.DashAge, ticksPerFrame, len(entry.Frames), func(index int, x, y, z numeric.Fixed) {
		ref := entry.Frames[index]
		if ref.Frame == nil {
			return
		}
		at := project(x, y, z)
		c.UIBlit(ref.Frame, int(at.X)-int(ref.Frame.XOffset), int(at.Y)-int(ref.Frame.YOffset))
	})
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
