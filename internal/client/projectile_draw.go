package client

// Snapshot-driven projectile presentation for the Ebitengine software
// framebuffer.  The adapter is intentionally separate from frame.go so the
// frame owner can insert it at the researched projectile strip without taking
// ownership of render dispatch or asset policy.

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
)

// ProjectileDrawStats reports what the adapter actually rendered.  A
// missing model/graphic is reported as Skipped; it is never converted into a
// generic marker.
type ProjectileDrawStats struct {
	Dispatched int
	Models     int
	Sprites    int
	Strokes    int
	Skipped    int
	Aborted    bool
}

// DrawProjectileViews draws each typed rendertype instruction from the
// committed projectile views. No historical frame is retained [03 §2.4].
//
// frameCount and colors are resolved by immutable content at the call site.
// A nil resolver deliberately leaves frame-selected families absent instead
// of inventing a frame count or palette color [I9].
func (c *Client) DrawProjectileViews(current []frame.ProjectileView, now uint32, visible func(frame.ProjectileView) bool, admitGlobalGAF func(frame.ProjectileView) bool, options render.ProjectileDispatchOptions) ProjectileDrawStats {
	var stats ProjectileDrawStats
	if c == nil || c.cam == nil || len(current) == 0 {
		return stats
	}
	draws, aborted := render.BuildProjectileDraws(current, now, visible, admitGlobalGAF, options)
	stats.Aborted = aborted
	// Rendertype 2 admission failure aborts the entire projectile renderer, not
	// merely the failing record. BuildProjectileDraws retains earlier
	// instructions for diagnostics, but those instructions must not reach the
	// framebuffer after an abort [03 §5.4].
	if aborted {
		return stats
	}
	stats.Dispatched = len(draws)
	for _, d := range draws {
		view := projectileViewByHandle(current, d.Handle)
		if view.Handle == 0 {
			stats.Skipped++
			continue
		}
		switch d.RenderType {
		case render.RenderTypeBaseSpriteModel, render.RenderTypeBaseModelDistinct, render.RenderTypeRecordOrientation:
			// These are the three researched model-bearing families.  Do not let
			// an authored Model field override beam/GAF/segmented dispatch; the
			// rendertype byte is the family authority [03 §5.4].
			if view.Model == "" || c.unitModelFor(view.Model) == nil {
				stats.Skipped++
				continue
			}
			if d.BaseFrame == nil {
				stats.Skipped++
				continue
			}
			// Retail emits the common base sprite before the model. Asset
			// admission above ensures a missing model cannot leave a partial
			// sprite behind.
			x, y := c.cam.WorldToScreen(view.X, view.Y, view.Z)
			c.UIBlitAnchor(d.BaseFrame, int(x-128), int(y-32))
			stats.Sprites++
			if !c.drawProjectileModel(view) {
				stats.Skipped++
				continue
			}
			stats.Models++
		case render.RenderTypeBeam:
			stats.Strokes += c.drawProjectileBeam(d, view)
		case render.RenderTypeSegmented:
			// Segment jitter is only admitted by a caller that has supplied
			// the researched CRT stream.  With no segment list there is no
			// authored geometry to draw; do not substitute a marker.
			if len(d.Segments) == 0 {
				stats.Skipped++
				continue
			}
			stats.Strokes += c.drawProjectileSegments(d)
			stats.Strokes += c.drawProjectileSegmentsSecond(d)
		default:
			if d.FrameAsset == nil {
				// Sprite/GAF renderers are asset-specific and are intentionally
				// resolved by the integration owner. An absent asset is not a
				// license to draw a 3×3 placeholder [F-P0-035].
				stats.Skipped++
				continue
			}
			x, y := c.cam.WorldToScreen(view.X, view.Y, view.Z)
			c.UIBlitAnchor(d.FrameAsset, int(x-128), int(y-32))
			stats.Sprites++
		}
	}
	return stats
}

func projectileViewByHandle(views []frame.ProjectileView, handle uint16) frame.ProjectileView {
	v, _ := projectileViewByHandleOK(views, handle)
	return v
}

func projectileViewByHandleOK(views []frame.ProjectileView, handle uint16) (frame.ProjectileView, bool) {
	for _, v := range views { // snapshot order is stable; no map dependence [I1]
		if uint16(v.Handle) == handle {
			return v, true
		}
	}
	return frame.ProjectileView{}, false
}

func (c *Client) drawProjectileBeam(d render.ProjectileDraw, v frame.ProjectileView) int {
	if c == nil || c.cam == nil {
		return 0
	}
	// Use the committed snapshot endpoints rather than the mutable combat
	// projectile.  Tail is explicit in ProjectileView for beam latch state.
	hx, hy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	tx, ty := c.cam.WorldToScreen(v.TailX, v.TailY, v.TailZ)
	strokes := render.BeamStrokes([2]int32{hx - 128, hy - 32}, [2]int32{tx - 128, ty - 32}, d.Color, d.Color2)
	for _, stroke := range strokes {
		c.drawIndexedLine(stroke.X0, stroke.Y0, stroke.X1, stroke.Y1, indexedColor(stroke.Color))
	}
	return len(strokes)
}

func (c *Client) drawProjectileSegments(d render.ProjectileDraw) int {
	count := 0
	for i := 1; i < len(d.Segments); i++ {
		a := d.Segments[i-1]
		b := d.Segments[i]
		ax, ay := c.cam.WorldToScreen(a.X, a.Y, a.Z)
		bx, by := c.cam.WorldToScreen(b.X, b.Y, b.Z)
		c.drawIndexedLine(ax-128, ay-32, bx-128, by-32, indexedColor(d.Color))
		count++
	}
	return count
}

func (c *Client) drawProjectileSegmentsSecond(d render.ProjectileDraw) int {
	count := 0
	for i := 1; i < len(d.Segments2); i++ {
		a := d.Segments2[i-1]
		b := d.Segments2[i]
		ax, ay := c.cam.WorldToScreen(a.X, a.Y, a.Z)
		bx, by := c.cam.WorldToScreen(b.X, b.Y, b.Z)
		c.drawIndexedLine(ax-128, ay-32, bx-128, by-32, indexedColor(d.Color2))
		count++
	}
	return count
}

func indexedColor(v int32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// drawIndexedLine is the integer Bresenham primitive used by beam families
// [03 §5.4]. It writes indexed pixels only and clips each point to the
// software framebuffer.
func (c *Client) drawIndexedLine(x0, y0, x1, y1 int32, color byte) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	sx := int32(1)
	if x0 > x1 {
		sx = -1
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	sy := int32(1)
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		if x0 >= 0 && x0 < int32(c.width) && y0 >= 0 && y0 < int32(c.height) {
			c.indexed[y0*int32(c.width)+x0] = color
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}
