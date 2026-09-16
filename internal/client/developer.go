package client

import (
	"strconv"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// DeveloperOptions contains host presentation inputs, independent of gameplay
// mode and the authoritative RNGs (DESIGN_DEVELOPER_TOOLS §3.1).
type DeveloperOptions struct {
	Mode        uint8
	Information bool
	// Contours use 1/256-height units; zero spacing disables them.
	ContourSpacing, ContourOffset int32
	PickX, PickZ                  int32
	PickValid                     bool
}

// SetDeveloperOptions also invalidates paused worlds and prerecords: a paused
// selector change requires no publication or simulation step [I6].
func (c *Client) SetDeveloperOptions(v DeveloperOptions) {
	if c == nil {
		return
	}
	if v.Mode > 4 {
		v.Mode = 0
	}
	if v.ContourSpacing < 0 {
		v.ContourSpacing = 0
	}
	if c.developer == v {
		return
	}
	c.JoinPreRecord()
	c.developer = v
	c.pausedWorldRevision++
	c.BumpPresentationEpoch()
}

// SetDeveloperFont binds SMLFONT independently of the HUD's side font [03 §3.12].
func (c *Client) SetDeveloperFont(f *formats.FNT) {
	if c == nil {
		return
	}
	if c.developerFont == f {
		return
	}
	c.JoinPreRecord()
	c.developerFont = f
	c.pausedWorldRevision++
	c.BumpPresentationEpoch()
}

// The world recorder removes the fixed beam origin from every projection.
// Its camera includes the shell inset; add that inset back for the retail cell
// walk's viewport-local origin [03 §3.12][07 §10].
func (c *Client) developerClip() drawlist.Rect {
	r := c.battleViewportRect()
	if c.cam != nil && (!c.cam.AtRestStep() || c.camBlending) {
		w := c.worldSpace(true)
		factor := w.Factor
		if factor <= 0 {
			factor = float32(w.Zoom) / float32(camera.ZoomUnit)
		}
		factor /= float32(w.Step.Project(1))
		left := developerFloor((float32(r.X) - w.OffsetX) / factor)
		top := developerFloor((float32(r.Y) - w.OffsetY) / factor)
		right := developerCeil((float32(r.X+r.W) - w.OffsetX) / factor)
		bottom := developerCeil((float32(r.Y+r.H) - w.OffsetY) / factor)
		r = drawlist.Rect{X: left, Y: top, W: right - left, H: bottom - top}
	}
	return r
}

// The executor carries its world transform in float32. Round its inverse clip
// outwards before narrowing so fractional zoom cannot cut a visible mark.
func developerFloor(v float32) int32 {
	n := int32(v)
	if float32(n) > v {
		n--
	}
	return n
}
func developerCeil(v float32) int32 {
	n := int32(v)
	if float32(n) < v {
		n++
	}
	return n
}

func (c *Client) developerProject(x, y, z int32) Point {
	sx, sy := c.cam.WorldToScreen(numeric.Fixed(int64(x)<<16), numeric.Fixed(int64(y)<<16), numeric.Fixed(int64(z)<<16))
	return Point{sx - camera.OriginX, sy - camera.OriginY}
}

// Clip to the diagnostic viewport before the shared line primitive clips to
// the surface and initializes its raster [03 R-COMP-01 §2].
func (c *Client) developerLine(a, b Point, ink byte, clip drawlist.Rect) {
	if clip.W <= 0 || clip.H <= 0 {
		return
	}
	x0, y0, x1, y1 := int64(a.X), int64(a.Y), int64(b.X), int64(b.Y)
	if !clipIndexedLine(&x0, &y0, &x1, &y1, int64(clip.X), int64(clip.Y), int64(clip.X)+int64(clip.W)-1, int64(clip.Y)+int64(clip.H)-1) {
		return
	}
	c.emitLine(drawlist.Line{X0: int32(x0), Y0: int32(y0), X1: int32(x1), Y1: int32(y1), Index: ink})
}

func (c *Client) developerQuad(q [4]Point, ink byte, clip drawlist.Rect) {
	// Reuse the shared flat polygon edge walk, rebased to the viewport clip.
	// Its last row and column remain exclusive [03 R-RAST-01 §1].
	var xs, ys [4]int32
	for i, p := range q {
		xs[i] = p.X - clip.X
		ys[i] = p.Y - clip.Y
	}
	t := &c.developerScan
	t.width = int(clip.W)
	t.heightPx = int(clip.H)
	p := screenPoly{x: xs[:], y: ys[:]}
	t.polyScanLanes(&p, 0, 0, func(y, l, r int32, _, _ [spanAttrs]int64) {
		c.emitFill(drawlist.Fill{Rect: drawlist.Rect{X: l + clip.X, Y: y + clip.Y, W: r - l, H: 1}, Index: ink})
	})
}

func (c *Client) developerText(p Point, text string, ink byte, clip drawlist.Rect) {
	if c.developerFont != nil {
		c.emitGlyphs(drawlist.Glyphs{Font: c.developerFont, Text: text, X: p.X, Y: p.Y, Color: ink, Clip: clip, HasClip: true})
	}
}

func (c *Client) drawDeveloperTerrain(f *frame.Frame) {
	if c.cam == nil || f == nil || f.Developer == nil || (c.developer.Mode == 0 && c.developer.ContourSpacing == 0) {
		return
	}
	d := f.Developer
	if _, ok := visibilityGridSize(d.Width, d.Height, len(d.Cells)); !ok {
		return
	}
	clip := c.developerClip()
	if clip.W <= 0 || clip.H <= 0 {
		return
	}
	scale := c.viewScale()
	x0 := max(int32(0), (c.cam.X+scale.Inverse(clip.X))/16)
	z0 := max(int32(0), (c.cam.Z+scale.Inverse(clip.Y))/16)
	end := min(x0+scale.Inverse(clip.W)/16+1, d.Width-1)
	_, screenH := c.recordExtent()
	for z := z0; z < d.Height-1; z++ {
		stop := true
		for x := x0; x < end; x++ {
			i := int(z*d.Width + x)
			cells := [4]frame.DeveloperCell{d.Cells[i], d.Cells[i+1], d.Cells[i+1+int(d.Width)], d.Cells[i+int(d.Width)]}
			var q [4]Point
			var heights [4]int32
			for k, offset := range [4][2]int32{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
				heights[k] = int32(cells[k].Height)
				q[k] = c.developerProject((x+offset[0])*16, heights[k], (z+offset[1])*16)
			}
			if q[0].Y < int32(screenH) {
				stop = false
			}
			c.developerCell(d, x, z, q, cells[0], clip)
			if c.developer.ContourSpacing > 0 {
				c.developerContours(q, heights, int32(d.SeaLevel), clip)
			}
		}
		if stop {
			break
		}
	}
	if c.developer.Mode == 2 && c.developer.PickValid {
		y, _ := developerGroundHeight(d, c.developer.PickX, c.developer.PickZ)
		// The cursor resolver picks the water surface above submerged terrain
		// [07 §8]; keep that same height when projecting its diagnostic cross.
		y = max(y, int32(d.SeaLevel))
		p := c.developerProject(c.developer.PickX, y, c.developer.PickZ)
		ink := c.paletteIndex(15) // ring/viewport ink [03 §3.12][03 R-MM-01 §1]
		c.developerLine(Point{p.X - 2, p.Y}, Point{p.X + 2, p.Y}, ink, clip)
		c.developerLine(Point{p.X, p.Y - 2}, Point{p.X, p.Y + 2}, ink, clip)
	}
}

func (c *Client) developerCell(d *frame.DeveloperView, x, z int32, q [4]Point, v frame.DeveloperCell, clip drawlist.Rect) {
	line := func(a, b Point, ink byte) { c.developerLine(a, b, ink, clip) }
	cross := func(ink byte) { line(q[0], q[2], ink); line(q[1], q[3], ink) }
	grid := func(ink byte) { line(q[0], q[1], ink); line(q[0], q[3], ink) }
	switch c.developer.Mode {
	case 1:
		i := int(z*d.Width + x)
		if i < len(d.MovementTiers) {
			tier := d.MovementTiers[i]
			if tier < 3 {
				cross(c.paletteIndex([3]byte{4, 14, 10}[tier]))
			}
		}
		if x >= d.SearchWidth || z >= d.SearchHeight {
			return
		}
		i = int(z*d.SearchWidth + x)
		if i < 0 || i >= len(d.Search) {
			return
		}
		s := d.Search[i]
		if s.Status&4 != 0 {
			c.developerText(q[0], "G", byte(d.Tick*33+uint32(z*d.Width+x)*97), clip)
		}
		var ink byte
		switch s.Status {
		case 1:
			ink = c.paletteIndex(15)
		case 2:
			ink = c.paletteIndex(4)
		default:
			// TODO(question): combined search statuses inherit undefined retail scratch
			// colour; a retail frame's stack history would settle it [03 §3.12].
			return
		}
		if s.Direction >= 8 {
			return
		}
		dirs := [8]Point{{0, -1}, {-1, -1}, {-1, 0}, {-1, 1}, {0, 1}, {1, 1}, {1, 0}, {1, -1}}
		center := Point{q[0].X + 8, q[0].Y + 8}
		for k, idx := range [3]uint8{s.Direction, (s.Direction + 1) % 8, (s.Direction + 7) % 8} {
			n := int32(4)
			if k == 0 {
				n = 14
			}
			dir := dirs[idx]
			line(Point{center.X - n*dir.X, center.Y - n*dir.Y}, center, ink)
		}
	case 2, 3:
		ink := byte(13)
		if v.Height > d.SeaLevel {
			ink = 15
		}
		grid(c.paletteIndex(ink))
		if c.developer.Mode == 3 {
			c.developerText(Point{q[0].X + 2, q[0].Y + 2}, strconv.Itoa(int(v.Metal)), c.paletteIndex(15), clip)
			return
		}
		if v.Ground != 0 {
			c.developerQuad(q, byte(v.Ground), clip)
		} else if v.Feature != world.PlotFeatureNone {
			c.developerQuad(q, byte(v.Feature-56), clip)
		}
		if v.Air != 0 {
			cross(byte(v.Air))
		}
		if v.Building {
			var p [4]Point
			offsets := [4]Point{{0, 2}, {-2, 0}, {0, -2}, {2, 0}}
			for k := range q {
				b := q[(k+1)%4]
				p[k] = Point{(q[k].X+b.X)/2 + offsets[k].X, (q[k].Y+b.Y)/2 + offsets[k].Y}
			}
			for k := range p {
				line(p[k], p[(k+1)%4], c.paletteIndex(15))
			}
		}
	case 4:
		grid(c.paletteIndex(0))
		cx, cz := x/2, z/2
		if cx >= d.CoverageWidth || cz >= d.CoverageHeight {
			return
		}
		i := int(cz*d.CoverageWidth + cx)
		if i < 0 || i >= len(d.Coverage) || d.Coverage[i] == 0 {
			return
		}
		l, t := max(q[0].X-5, clip.X), max(q[0].Y-5, clip.Y)
		r, b := min(q[0].X+5, clip.X+clip.W-1), min(q[0].Y+5, clip.Y+clip.H-1)
		if l <= r && t <= b {
			c.emitFillInclusive(l, t, r, b, c.paletteIndex(15))
		}
	}
}

var contourColors = [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 111, 110, 109, 108, 107, 106, 88, 87, 86, 85, 84, 83, 82, 81, 80, 255, 255, 255, 255, 255, 255, 255}

type contourVertex struct {
	p Point
	h int64
}

// Four clockwise centroid triangles, descending levels, strict lower bound,
// and weighted interpolation with signed truncation [07 R-FE-02 §11].
func (c *Client) developerContours(q [4]Point, h [4]int32, sea int32, clip drawlist.Rect) {
	center := contourVertex{}
	var sx, sy int64
	for i, p := range q {
		sx += int64(p.X)
		sy += int64(p.Y)
		center.h += int64(h[i]) * 64
	}
	center.p = Point{int32((sx + 2) / 4), int32((sy + 2) / 4)}
	spacing, offset := int64(c.developer.ContourSpacing), int64(c.developer.ContourOffset)
	if spacing <= 0 {
		return
	}
	for i := range q {
		a, b, top := contourVertex{q[i], int64(h[i]) * 256}, contourVertex{q[(i+1)%4], int64(h[(i+1)%4]) * 256}, center
		if a.h > b.h {
			a, b = b, a
		}
		if b.h > top.h {
			b, top = top, b
		}
		if a.h > b.h {
			a, b = b, a
		}
		level := top.h/spacing*spacing + offset
		if level > top.h {
			level -= ((level - top.h + spacing - 1) / spacing) * spacing
		}
		for ; level > a.h; level -= spacing {
			low, high := a, b
			if level > b.h {
				low, high = b, top
			}
			if top.h == a.h || high.h == low.h {
				continue
			}
			p, r := contourIntersection(a, top, level), contourIntersection(low, high, level)
			ink := contourColors[((level>>8)-int64(sea)+256)>>4]
			c.developerLine(p, r, ink, clip)
		}
	}
}
func contourIntersection(a, b contourVertex, l int64) Point {
	d, t := b.h-a.h, l-a.h
	return Point{int32((int64(a.p.X)*(d-t) + int64(b.p.X)*t) / d), int32((int64(a.p.Y)*(d-t) + int64(b.p.Y)*t) / d)}
}

func (c *Client) drawDeveloperMovement(f *frame.Frame) {
	if !c.developer.Information || c.cam == nil || f == nil || f.Developer == nil {
		return
	}
	for _, u := range f.Units {
		if u.Owner != f.Selection.LocalPlayer || u.Flags&hud.SelectionFlag == 0 {
			continue
		}
		for _, d := range f.Developer.Units {
			if d.Slot != u.Slot || d.InstanceID != u.InstanceID {
				continue
			}
			if !d.MovementAvailable {
				return
			}
			if _, ok := visibilityGridSize(f.Developer.Width, f.Developer.Height, len(f.Developer.Cells)); !ok {
				return
			}
			clip := c.developerClip()
			x, z := d.FootprintX*16, d.FootprintZ*16
			height := func(x, z int32) int32 {
				h, _ := developerGroundHeight(f.Developer, x, z)
				return h
			}
			y := height(x, z)
			q := [4]Point{c.developerProject(x, y, z), c.developerProject(x+int32(u.FootX)*16, y, z), c.developerProject(x+int32(u.FootX)*16, y, z+int32(u.FootZ)*16), c.developerProject(x, y, z+int32(u.FootZ)*16)}
			for k := range q {
				c.developerLine(q[k], q[(k+1)%4], c.paletteIndex(15), clip)
			}
			ink := byte(12)
			if d.HasWaypoint {
				ink = 9
			}
			for k := 1; k < len(d.Route); k++ {
				a, b := d.Route[k-1], d.Route[k]
				c.developerLine(c.developerProject(a.X, height(a.X, a.Z), a.Z), c.developerProject(b.X, height(b.X, b.Z), b.Z), c.paletteIndex(ink), clip)
			}
			return
		}
		return
	}
}

// developerGroundHeight samples only the detached observation. Two sequential
// interpolations each truncate their signed delta [03 §2.3]; averaging the four
// corners in one expression changes descending slopes. Out-of-map queries keep
// the height query's negative sentinel; absent observations remain unavailable.
func developerGroundHeight(d *frame.DeveloperView, x, z int32) (int32, bool) {
	if d == nil {
		return 0, false
	}
	if _, ok := visibilityGridSize(d.Width, d.Height, len(d.Cells)); !ok {
		return 0, false
	}
	cx, cz := x>>4, z>>4
	if cx < 0 || cz < 0 || cx >= d.Width-1 || cz >= d.Height-1 {
		return -1, true
	}
	i := int(cz*d.Width + cx)
	w := int(d.Width)
	interp := func(a, b int32, f int32) int32 { return a + (b-a)*f/16 }
	fx, fz := x-cx*16, z-cz*16
	top := interp(int32(d.Cells[i].Height), int32(d.Cells[i+1].Height), fx)
	bottom := interp(int32(d.Cells[i+w].Height), int32(d.Cells[i+w+1].Height), fx)
	return interp(top, bottom, fz), true
}
