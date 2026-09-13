package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The viewport gate tests the committed center, including both last edges,
// before dispatch; an offscreen lens stops later projectiles [03 §5.4].
func (c *Client) admitProjectileLens(v frame.ProjectileView) bool {
	z := c.liveZoom()
	x := z.Project(int32(v.X>>16) - c.cam.X)
	y := z.Project(int32(v.Z>>16) - (int32(v.Y>>16) >> 1) - c.cam.Z)
	clip := c.battleViewportRect()
	return x >= clip.X && y >= clip.Y && x < clip.X+clip.W && y < clip.Y+clip.H
}

func (c *Client) drawProjectileLens(v frame.ProjectileView) {
	x, y := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	// TODO(question): retail leaves the lens transparent key uninitialized;
	// observe startup allocation contents to settle a session's key. Zero is
	// Nanolathe's deterministic host policy [SPEC_CONFLICTS SC18], not an
	// established retail constant [03 R-FX-01 §4].
	c.list.RecordLens(drawlist.Lens{X: x - camera.OriginX, Y: y - camera.OriginY, Scale: c.viewScale(), Clip: c.battleViewportRect(), Key: 0})
}

var _ drawlist.LensSink = classicSink{}

// Lens captures every needed sample before writing any output. The fixed map
// points all changed pixels inward to the admitted center, so clipping output
// first preserves the retail result without unused reads across backing rows
// [03 R-FX-01 §4]. Explicit bounds checks also protect authored test commands.
func (s classicSink) Lens(l drawlist.Lens) {
	c := s.c
	if c == nil {
		return
	}
	b := l.Bounds()
	x0, y0 := max(b.X, l.Clip.X, 0), max(b.Y, l.Clip.Y, 0)
	x1, y1 := min(b.X+b.W, l.Clip.X+l.Clip.W, int32(c.width)), min(b.Y+b.H, l.Clip.Y+l.Clip.H, int32(c.height))
	// The largest presentation scale is 2x (44 by 44). Stack storage avoids
	// framebuffer-sized copies and is private to this command's replay.
	var samples [44 * 44]byte
	var covered [44 * 44]bool
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			sx, sy, ok := l.Source(x, y)
			if !ok || sx < 0 || sy < 0 || sx >= int32(c.width) || sy >= int32(c.height) {
				continue
			}
			source := int(sy)*c.width + int(sx)
			if source >= len(c.indexed) {
				continue
			}
			i := (y-b.Y)*b.W + x - b.X
			samples[i] = c.indexed[source]
			covered[i] = samples[i] != l.Key
		}
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := (y-b.Y)*b.W + x - b.X
			dst := int(y)*c.width + int(x)
			if covered[i] && dst < len(c.indexed) {
				c.indexed[dst] = samples[i]
			}
		}
	}
}
