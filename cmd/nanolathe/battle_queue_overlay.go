package main

// Ebitengine battle presentation adapter for the immutable queue overlay,
// shown by Shift or modern resource-build feedback.  The geometry and gating live in internal/hud so they can be
// tested without opening a window; this file only projects and rasterizes the
// returned instructions.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// DashChainEntry is the authored GAF entry that supplies the travelling-dash
// sprite chain.  It sits in the cursor GAF root beside the named cursors and is
// resolved by the same loader, which is why the chain reads as a row of small
// cursor-like sprites marching along a queue line [R-P0-11 §3].
const DashChainEntry = "pathicon"

// cursorArtCache caches the cursor GAF root for one mounted install.  Both the
// dash chain and the queued-order icons come out of it: `pathicon` is loaded in
// the same run of names as the twenty-one cursors, and an order descriptor's
// icon byte indexes that same handle array [R-P0-11 §3][07 §8].  A missing
// entry suppresses the helper rather than substituting artwork.
type cursorArtCache struct {
	fs   vfs.FSOps
	gaf  *formats.GAF
	done bool
}

var cursorArt cursorArtCache

func cursorArtGAF(fs vfs.FSOps) *formats.GAF {
	if fs == nil {
		return nil
	}
	if cursorArt.done && cursorArt.fs == fs {
		return cursorArt.gaf
	}
	cursorArt = cursorArtCache{fs: fs, done: true}
	if gaf, err := formats.LoadGAFFile(fs, client.CursorGAFPath); err == nil {
		cursorArt.gaf = gaf
	}
	return cursorArt.gaf
}

func dashChainEntry(fs vfs.FSOps) *formats.GAFEntry {
	gaf := cursorArtGAF(fs)
	if gaf == nil {
		return nil
	}
	if e, ok := gaf.Find(DashChainEntry); ok {
		return e
	}
	return nil
}

// queueIconEntry resolves one order-descriptor icon byte through the cursor
// index table, which is the handle array the icon helper indexes [R-P0-11 §3]
// [07 §8].  Slot 0 is the unused/overflow slot and is never a valid icon.
func queueIconEntry(fs vfs.FSOps, cursorIndex uint8) *formats.GAFEntry {
	if !render.IsValidCursorIndex(int(cursorIndex)) {
		return nil
	}
	gaf := cursorArtGAF(fs)
	if gaf == nil {
		return nil
	}
	entry, ok := render.ResolveCursorEntry(gaf, int(cursorIndex))
	if !ok || entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	return entry
}

// queueIconFrame is the icon helper's frame selector: `tick/(tpf*2) % nFrames`
// [R-P0-11 §3].
//
// `tpf` is a GAF frame reference's duration field: whole simulation ticks per
// frame, and the sibling dash-chain helper is established to take it from the
// entry's **first** frame reference rather than per frame [R-P0-11 §3].  Which
// of the two readings this helper uses cannot change what it computes: the
// duration is "constant across all frames of an entry" in every retail GAF
// [fmt gaf], so the first frame's value *is* the entry's value.  The clamp
// below only guards an authored 0, which would otherwise divide by zero.
func queueIconFrame(entry *formats.GAFEntry, tick uint32) (int32, bool) {
	if entry == nil || len(entry.Frames) == 0 {
		return 0, false
	}
	ticksPerFrame := int64(entry.Frames[0].Value)
	if ticksPerFrame < 1 {
		ticksPerFrame = 1
	}
	return int32(int64(tick) / (ticksPerFrame * 2) % int64(len(entry.Frames))), true
}

// drawQueueOverlay is the sole battle integration call required by QUEUE-02.
// It consumes only a published frame and input presentation state. Hidden
// overlays return before constructing instructions. Modern resource feedback
// reuses the same animation without changing Shift [07 §9][R-P0-11 §4]
// (DESIGN_INTERFACE_HUD_INPUT §3.10).
func drawQueueOverlay(c *client.Client, b *battleSession, f *frame.Frame, tick uint32, show bool, localOwner uint8, tracked, hovered pool.Handle) {
	if c == nil || b == nil || f == nil || !show {
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
		Tick:         tick,
		ShiftHeld:    show,
		LocalOwner:   localOwner,
		TrackedUnit:  tracked,
		HoveredUnit:  hovered,
		Project:      project,
		ShowRanges:   b.rangesShown(),
		Range:        b.snapshotRanges,
		GroundHeight: c.GroundHeightAt,
		// TODO(I10): bind the separate target-circle radius and replace
		// its old flat approximation with the established sixteen-segment
		// world projection. The target model radius needs a presentation
		// catalog binding [07 R-P0-11 §3].
		// The builder-capability test that gates the marker-only fallback
		// [R-P0-11 §3].  It reads an immutable compiled definition, the same
		// way the production dispatcher's builder check does; no live pool or
		// tick state is consulted [I6].
		Builder: func(v frame.UnitView) bool { return b.snapshotBuilder(v) },
		// The queued-order icon: the descriptor's icon byte selects the entry
		// from the cursor handle array and the helper animates it
		// [R-P0-11 §3][07 §8].
		Icon: func(cursorIndex uint8, at uint32) (int32, bool) {
			return queueIconFrame(queueIconEntry(b.fs, cursorIndex), at)
		},
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
	}
	chain := dashChainEntry(b.fs)
	// The overlay's positions come through the projection and scale with it;
	// its sprites do not, so a magnified view takes the resampled variant of
	// the dash and icon art — the same nearest resampling the client applies
	// to any world sprite it has no remastered variant for
	// (DESIGN_GPU_RENDERER §14.2, §14.3). At scale 1 this is the identity and
	// nothing composed changes.
	scale := viewScaleOf(b)
	for _, op := range hud.QueueOverlay(f, opts) {
		switch op.Kind {
		case hud.QueuePrimitiveMarker:
			for _, seg := range op.Segments {
				drawQueueLine(c, hud.QueuePoint{X: seg.X0, Y: seg.Y0}, hud.QueuePoint{X: seg.X1, Y: seg.Y1}, c.GUIColor(seg.Color))
			}
		case hud.QueuePrimitiveDash:
			drawDashChain(c, chain, op, project, scale)
		case hud.QueuePrimitiveCircle:
			// Chords carry the established GUI colour of their helper branch.
			if op.ColorKnown {
				drawQueueLine(c, op.A, op.B, c.GUIColor(op.Color))
			}
		case hud.QueuePrimitiveLabel:
			if b.hud != nil {
				c.UIText(b.hud.console, op.Text, int(op.A.X), int(op.A.Y), c.GUIColor(op.Color))
			}
		case hud.QueuePrimitiveIcon:
			drawQueueIcon(c, queueIconEntry(b.fs, op.IconCursor), op, scale)
		}
	}
}

// drawDashChain blits the authored travelling-dash sprites along one queue
// segment.  The frame's authored offsets are its hotspot, exactly as for the
// software cursor: the sprite is placed so that pixel lands on the interpolated
// point [R-P0-11 §3][fmt gaf "Placement offsets"].
func drawDashChain(c *client.Client, entry *formats.GAFEntry, op hud.QueuePrimitive, project func(x, y, z numeric.Fixed) hud.QueuePoint, scale camera.ViewScale) {
	if entry == nil || len(entry.Frames) == 0 {
		return
	}
	ticksPerFrame := int(entry.Frames[0].Value)
	// The spacing along the segment is world distance and scales with the
	// projection; only the sprite itself needs the variant.
	hud.DashSprites(op.WorldA, op.WorldB, op.DashAge, ticksPerFrame, len(entry.Frames), func(index int, x, y, z numeric.Fixed) {
		f := overlayViewFrame(entry.Frames[index].Frame, scale)
		if f == nil {
			return
		}
		at := project(x, y, z)
		c.UIBlit(f, int(at.X)-int(f.XOffset), int(at.Y)-int(f.YOffset))
	})
}

// overlayViewFrame is the world-anchored overlay's own frame selector: the
// authored frame natively, and its nearest-resampled variant at a magnified
// view — doubled at 2x — built once per source frame
// and kept for the process's life (DESIGN_GPU_RENDERER §14.3). Frames are
// immutable after load, so the source pointer identifies the variant; the
// cursor GAF this art comes from is loaded once per mounted install, like
// cursorArt above.
func overlayViewFrame(f *formats.GAFFrame, scale camera.ViewScale) *formats.GAFFrame {
	if f == nil || scale.Native() {
		return f
	}
	cache := overlayDetailFrames
	if variant, ok := cache[f]; ok {
		return variant
	}
	variant := f.Doubled()
	cache[f] = variant
	return variant
}

// overlayDetailFrames is the doubled-overlay-art cache. It is presentation state keyed by immutable frames and is
// only ever looked up, never ranged, so it produces no order [I1][I6].
var overlayDetailFrames = map[*formats.GAFFrame]*formats.GAFFrame{}

// drawQueueIcon blits the queued-order icon at the order's anchor.  The frame
// comes from the cursor handle array slot the descriptor's icon byte names, and
// its authored placement offsets are the hotspot exactly as they are for the
// software pointer and for the dash chain [R-P0-11 §3][07 §8]
// [fmt gaf "Placement offsets"].  The anchor is already projected through the
// half-height shear by the overlay's own projection callback.
//
// The queue walker emits the attack-icon range rings before this blit. Their
// GUI12/4 tick-parity colour belongs to those lines; the icon itself follows
// the ordinary GAF path [07 R-P0-11 §3].
func drawQueueIcon(c *client.Client, entry *formats.GAFEntry, op hud.QueuePrimitive, scale camera.ViewScale) {
	if entry == nil || !op.IconKnown {
		return
	}
	index := int(op.IconFrame)
	if index < 0 || index >= len(entry.Frames) {
		return
	}
	f := overlayViewFrame(entry.Frames[index].Frame, scale)
	if f == nil {
		return
	}
	c.UIBlit(f, int(op.Center.X)-int(f.XOffset), int(op.Center.Y)-int(f.YOffset))
}

// drawQueueLine is the indexed-framebuffer equivalent of retail's integer
// line primitive.  UIFillRect clips every pixel to the client viewport.
func drawQueueLine(c *client.Client, a, b hud.QueuePoint, color uint8) {
	dx := numeric.Abs(b.X - a.X)
	dy := numeric.Abs(b.Y - a.Y)
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
