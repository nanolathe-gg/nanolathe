package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TacticalRangesAvailable keeps Enhanced guides independent of the icon zoom cut.
func (c *Client) TacticalRangesAvailable() bool {
	return c != nil && c.enhanced && c.cam != nil
}

// VisitTacticalUnits exposes identified on-screen unit anchors from the current
// publication at every zoom. Sensor contacts never grant access to unit metadata
// (DESIGN_GPU_RENDERER §20). Values and nested slices remain read-only.
func (c *Client) VisitTacticalUnits(f *frame.Frame, visit func(frame.UnitView)) {
	if !c.TacticalRangesAvailable() || visit == nil {
		return
	}
	if c.buffer != nil && c.buffer.Current() != nil {
		f = c.buffer.Current()
	}
	if f == nil || f.ViewingPlayer >= 10 {
		return
	}
	if c.StrategicIconsActive() {
		c.layoutStrategicMarkers(f, f.ViewingPlayer, &c.strategicDraw, false)
		for _, target := range c.strategicDraw.targets {
			if target > 0 {
				visit(f.Units[target-1])
			}
		}
		return
	}
	// Model views have no icon layout. Keep its visibility/carrier policy and
	// screen anchor margin without depending on marker alpha or generated art.
	c.strategicDraw.indexUnits(f)
	clip := c.battleViewportRect()
	view := c.presentationCameraView()
	const margin = strategicIconSize / 2
	for _, u := range f.Units {
		if u.Slot == 0 || !strategicUnitVisible(f, u, f.ViewingPlayer, c.strategicDraw.slots) {
			continue
		}
		x, y := presentationPoint(view, int64(u.X)>>16, (int64(u.Z)>>16)-(int64(u.Y)>>17))
		if x+margin <= int64(clip.X) || x-margin >= int64(clip.X+clip.W) || y+margin <= int64(clip.Y) || y-margin >= int64(clip.Y+clip.H) {
			continue
		}
		visit(u)
	}
}

// TacticalRangeColor resolves presentation ink against the installed palette.
func (c *Client) TacticalRangeColor(rgb [3]uint8) uint8 {
	if c == nil {
		return 0
	}
	return c.nearestIndex(rgb[0], rgb[1], rgb[2])
}

// DrawTacticalRange records one-pixel guides outside the scaled world region.
// Terrain-following endpoints retain max(center height, terrain height) from
// [07 R-P0-11 §3]. Adaptive screen tessellation and dashes are Enhanced design
// policy (DESIGN_GPU_RENDERER §20), not authoritative coverage arithmetic.
func (c *Client) DrawTacticalRange(x, y, z numeric.Fixed, radius int32, color uint8, dashed bool) {
	if !c.TacticalRangesAvailable() || radius <= 0 {
		return
	}
	clip := c.battleViewportRect()
	if clip.W <= 0 || clip.H <= 0 {
		return
	}
	// Reject malformed positions outside the game's integer coordinate domain.
	// Endpoint additions and zoom products then remain wide until after clipping,
	// including radii whose fixed-point representation exceeds a signed dword.
	for _, v := range [3]numeric.Fixed{x, y, z} {
		if whole := int64(v) >> 16; whole < math.MinInt32 || whole > math.MaxInt32 {
			return
		}
	}
	zoom := c.liveZoom().Norm()
	view := c.presentationCameraView()
	segments := strategicRangeSegments(radius, zoom)
	point := func(i int) (int64, int64) {
		a := numeric.Angle(uint16(i * 65536 / segments))
		// The trig table's scale/rounding is [04 §5.1]. Wide presentation
		// products avoid the retail guide's narrow magnitude wrap for large
		// authored ranges; no simulation state consumes them.
		px := (int64(x) >> 16) + (int64(radius)*int64(numeric.Sin(a))+4096)>>13
		pz := (int64(z) >> 16) + (int64(radius)*int64(numeric.Cos(a))+4096)>>13
		py := max(y, c.GroundHeightAt(numeric.Fixed(px<<16), numeric.Fixed(pz<<16)))
		return presentationPoint(view, px, pz-(int64(py)>>17))
	}
	ax, ay := point(0)
	for i := 1; i <= segments; i++ {
		bx, by := point(i)
		if !dashed || i%2 != 0 {
			if line, ok := clipStrategicRange(ax, ay, bx, by, clip); ok {
				line.Index = color
				c.emitLine(line)
			}
		}
		ax, ay = bx, by
	}
}

// About eight screen pixels per chord, with bounded work for every radius.
// Multiples of four preserve the cardinal extrema; 32..512 are display choices.
func strategicRangeSegments(radius int32, zoom camera.Zoom) int {
	screenRadius := int64(radius) * int64(zoom.Norm()) / int64(camera.ZoomUnit)
	return int(max(int64(32), min(int64(512), ((screenRadius*4/5+3)/4)*4)))
}

// Liang-Barsky clips wide projected coordinates before narrowing or rasterizing.
// Ratios are presentation-only floats; forming integer coordinate products here
// would overflow on extreme authored radii. The record owns only scalar values.
func clipStrategicRange(ax, ay, bx, by int64, clip drawlist.Rect) (drawlist.Line, bool) {
	if clip.W <= 0 || clip.H <= 0 || (ax == bx && ay == by) {
		return drawlist.Line{}, false
	}
	xmin, ymin := int64(clip.X), int64(clip.Y)
	xmax, ymax := xmin+int64(clip.W)-1, ymin+int64(clip.H)-1
	if max(ax, bx) < xmin || min(ax, bx) > xmax || max(ay, by) < ymin || min(ay, by) > ymax {
		return drawlist.Line{}, false
	}
	if min(ax, bx) >= xmin && max(ax, bx) <= xmax && min(ay, by) >= ymin && max(ay, by) <= ymax {
		return drawlist.Line{X0: int32(ax), Y0: int32(ay), X1: int32(bx), Y1: int32(by)}, true
	}
	dx, dy := bx-ax, by-ay
	lo, hi := 0.0, 1.0
	for _, edge := range [4][2]int64{{-dx, ax - xmin}, {dx, xmax - ax}, {-dy, ay - ymin}, {dy, ymax - ay}} {
		p, q := edge[0], edge[1]
		if p == 0 {
			if q < 0 {
				return drawlist.Line{}, false
			}
			continue
		}
		t := float64(q) / float64(p)
		if p < 0 {
			lo = max(lo, t)
		} else {
			hi = min(hi, t)
		}
		if lo > hi {
			return drawlist.Line{}, false
		}
	}
	coord := func(start, delta int64, t float64, low, high int64) int32 {
		return int32(max(low, min(high, int64(math.Round(float64(start)+t*float64(delta))))))
	}
	return drawlist.Line{X0: coord(ax, dx, lo, xmin, xmax), Y0: coord(ay, dy, lo, ymin, ymax), X1: coord(ax, dx, hi, xmin, xmax), Y1: coord(ay, dy, hi, ymin, ymax)}, true
}
