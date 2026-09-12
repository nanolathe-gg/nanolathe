package client

// Snapshot-driven projectile presentation for the Ebitengine software
// framebuffer.  The adapter is intentionally separate from frame.go so the
// frame owner can insert it at the researched projectile strip without taking
// ownership of render dispatch or asset policy.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
	// The dispatch list is rebuilt from the committed views every frame and is
	// read only inside this call, so it lives in a retained buffer.
	draws, aborted := render.BuildProjectileDrawsInto(c.projectileDraws[:0], current, now, visible, admitGlobalGAF, options)
	c.projectileDraws = draws[:cap(draws)]
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
			if view.Model == "" || c.modelForProjectile(view) == nil {
				stats.Skipped++
				continue
			}
			if d.BaseFrame == nil {
				stats.Skipped++
				continue
			}
			// The common frame is the ground shadow, not a projectile-body
			// sprite. Its Y anchor is the record's cached average floor height
			// [03 §5.4][06 R-WFX-01 §4].
			if c.drawProjectileShadow(d.BaseFrame, view) {
				stats.Sprites++
			}
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
			if d.BaseFrame != nil && c.drawProjectileShadow(d.BaseFrame, view) {
				stats.Sprites++
			}
			x, y := c.cam.WorldToScreen(view.X, view.Y, view.Z)
			// The sprite/GAF render types blit their frame keyed at the anchor
			// [03 §5.4]; Anchored selects the offset-subtracting placement (WU-1.7b).
			// The remaster covers feature banks only, so a projectile frame takes
			// its nearest-doubled variant in the detail view
			// (DESIGN_GPU_RENDERER §14.2, §14.3).
			sprite := drawlist.Sprite{Frame: c.viewFrame(d.FrameAsset), X: x - 128, Y: y - 32, Kind: drawlist.BlitKeyed, Anchored: true, Emissive: true}
			sprite.ReflectWater = c.reflectionWaterAt(view.X, view.Z)
			sprite.ReflectionHeight = c.reflectionHeight(view.Y)
			c.emitSprite(sprite)
			stats.Sprites++
		}
	}
	return stats
}

// drawProjectileShadow blits frame 0 of the shared `shadow` entry as the
// record's ground shadow. Render types 1, 3, 4 and 6 all draw it, all the same
// way [03 §5.4].
//
// The projection is the ordinary orthographic one with the record's cached
// average floor height standing in for the projectile's own height:
// `(X − viewX + 128, (Z − floor/2) − viewZ + 32)`. Passing the floor as the
// height argument is exactly that — the shear term is `height >> 1` and the
// cached floor is a non-negative average of two height bytes, so the shift and
// the halving agree [03 §2.5][06 §8.1]. Using the projectile's own Y instead
// would float the ground sprite up with the shot, which is the defect this
// replaces.
//
// A record whose point resolved to no plot cell has no cached floor and draws
// no shadow; that is the off-map case the collision gate retires without
// sampling terrain [06 §8.1].
func (c *Client) drawProjectileShadow(shadow *formats.GAFFrame, v frame.ProjectileView) bool {
	if c == nil || c.cam == nil || shadow == nil || !v.FloorHeightValid {
		return false
	}
	floor := numeric.Fixed(int64(v.FloorHeight) << 16)
	sx, sy := c.cam.WorldToScreen(v.X, floor, v.Z)
	// The ground shadow is a plain keyed frame-anchor blit [03 §5.4]; Anchored
	// selects the offset-subtracting placement (WU-1.7b).
	c.emitSprite(drawlist.Sprite{Frame: c.viewFrame(shadow), X: sx - camera.OriginX, Y: sy - camera.OriginY, Kind: drawlist.BlitKeyed, Anchored: true})
	return true
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
	for i, stroke := range strokes {
		// Each beam stroke is one indexed line; the sink runs the raw Bresenham
		// primitive [03 §5.4].
		line := drawlist.Line{X0: stroke.X0, Y0: stroke.Y0, X1: stroke.X1, Y1: stroke.Y1, Index: indexedColor(stroke.Color), Emissive: true}
		c.setLineReflection(&line, render.ProjectilePoint{X: v.X, Y: v.Y, Z: v.Z}, render.ProjectilePoint{X: v.TailX, Y: v.TailY, Z: v.TailZ})
		if d.Color2 != 0 && i == 0 {
			// The secondary stroke swaps head and tail [03 §5.4].
			line.ReflectionHeight0, line.ReflectionHeight1 = line.ReflectionHeight1, line.ReflectionHeight0
		}
		c.emitLine(line)
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
		line := drawlist.Line{X0: ax - 128, Y0: ay - 32, X1: bx - 128, Y1: by - 32, Index: indexedColor(d.Color), Emissive: true}
		c.setLineReflection(&line, a, b)
		c.emitLine(line)
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
		line := drawlist.Line{X0: ax - 128, Y0: ay - 32, X1: bx - 128, Y1: by - 32, Index: indexedColor(d.Color), Emissive: true}
		c.setLineReflection(&line, a, b)
		c.emitLine(line)
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
