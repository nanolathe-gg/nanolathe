package client

// Snapshot-driven effect presentation.  Effects are admitted by simulation
// event producers and copied into the immutable frame; this adapter performs
// only indexed framebuffer work and never mutates the event source [I6].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// EffectDrawOptions supplies authored asset and LHT metadata.  Unknown frame
// or halo geometry is represented by a false return; the adapter does not
// invent durations, radii, or palette rows [I9].
type EffectDrawOptions struct {
	ResolveFrame func(snapshot.EffectView, int32) (*formats.GAFFrame, bool)
	LHTGeometry  func(snapshot.EffectView) (radius, level int, ok bool)
	// TerrainCoverage admits only indexed pixels belonging to the already
	// composed terrain. A nil predicate leaves a light effect unresolved; the
	// halo must never brighten units, effects, or HUD pixels [03 §4.3.1].
	TerrainCoverage func(x, y int) bool
}

// EffectDrawStats reports actual effect work.  Skipped means the immutable
// effect existed but its authored graphic or LHT geometry was unresolved.
type EffectDrawStats struct {
	Admitted int
	Sprites  int
	Halos    int
	Skipped  int
}

// DrawEffectViews draws snapshot effects in stable producer admission order.
// The caller supplies asset resolution because the content catalog is owned
// outside the client; no generic explosion/smoke sprite is selected here.
func (c *Client) DrawEffectViews(effects []snapshot.EffectView, options EffectDrawOptions) EffectDrawStats {
	var stats EffectDrawStats
	if c == nil || c.cam == nil {
		return stats
	}
	draws := render.BuildEffectDraws(effects)
	stats.Admitted = len(draws)
	for i, d := range draws {
		view := effects[i]
		if d.Light {
			if options.LHTGeometry == nil {
				stats.Skipped++
				continue
			}
			radius, level, ok := options.LHTGeometry(view)
			if !ok || radius <= 0 || level <= 0 || options.TerrainCoverage == nil {
				stats.Skipped++
				continue
			}
			x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
			c.drawLHTHalo(int(x-128), int(y-32), radius, level, options.TerrainCoverage)
			stats.Halos++
		}
		if d.Graphic == "" || options.ResolveFrame == nil {
			if !d.Light {
				stats.Skipped++
			}
			continue
		}
		frame, ok := options.ResolveFrame(view, d.FrameA)
		if !ok || frame == nil {
			stats.Skipped++
			continue
		}
		x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
		c.UIBlitAnchor(frame, int(x-128), int(y-32))
		stats.Sprites++
	}
	return stats
}

// drawLHTHalo applies the authored LHT row to the existing indexed terrain
// inside an explicitly supplied circular mask.  It does not mutate terrain
// or choose a radius/row; those come from the event/content resolver [03
// §4.3.1][F-P0-036].
func (c *Client) drawLHTHalo(cx, cy, radius, level int, terrainCoverage func(x, y int) bool) {
	if c == nil || c.pal == nil || radius <= 0 || level <= 0 {
		return
	}
	if terrainCoverage == nil {
		return
	}
	r2 := radius * radius
	for dy := -radius; dy <= radius; dy++ {
		py := cy + dy
		if py < 0 || py >= c.height {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy > r2 {
				continue
			}
			px := cx + dx
			if px < 0 || px >= c.width {
				continue
			}
			if !terrainCoverage(px, py) {
				continue
			}
			idx := py*c.width + px
			c.indexed[idx] = c.pal.LightLookup(level, c.indexed[idx])
		}
	}
}
