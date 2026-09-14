package gpurender

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The frame and line families draw RUNS of consecutive one-pixel writes as one
// quad rather than one quad per pixel (draw.go). The coalescing is only sound if
// the quads cover exactly the pixels the per-pixel quads covered, including at a
// world factor, where the scheduler widens a sub-pixel span to one screen pixel
// (§16.3) and a run's far edge is therefore NOT simply the transform of its last
// column plus one.
//
// These tests lock that equality directly: the same pixel sequence is compiled
// twice — once through the family, once as one quad per pixel — and the two
// compiled vertex streams are rasterized at pixel centres and compared. The
// pixel sequence itself is the byte writer's, written out here so the reference
// is the classic algorithm and not the code under test [R-SEL-02A][03 §5.4].

// rasterGrid is the extent the compiled quads are rasterized over. It is wider
// than the fixture's framebuffer so a quad a world factor above one pushes past
// the framebuffer is still compared rather than silently dropped.
const rasterGrid = 192

// coveredPixels rasterizes one class's compiled quads at pixel centres, the rule
// a device applies to the two triangles of an axis-aligned quad. The result is a
// dense grid rather than a set, so nothing here depends on iteration order [I1].
func coveredPixels(verts []ebiten.Vertex) []bool {
	out := make([]bool, rasterGrid*rasterGrid)
	for i := 0; i+4 <= len(verts); i += 4 {
		x0, y0 := verts[i].DstX, verts[i].DstY
		x1, y1 := verts[i+3].DstX, verts[i+3].DstY
		for py := 0; py < rasterGrid; py++ {
			cy := float32(py) + 0.5
			if cy < y0 || cy >= y1 {
				continue
			}
			for px := 0; px < rasterGrid; px++ {
				cx := float32(px) + 0.5
				if cx >= x0 && cx < x1 {
					out[py*rasterGrid+px] = true
				}
			}
		}
	}
	return out
}

// worldFactor is one transform the compiled geometry is compared under: the
// rest factor, two factors below one (where the minimum-span rule bites) and one
// above.
type worldFactor struct {
	name    string
	k       float32
	ox, oy  float32
	enabled bool
}

var worldFactors = []worldFactor{
	{name: "rest", k: 1},
	{name: "half", k: 0.5, enabled: true},
	{name: "three-quarters", k: 0.75, ox: 0.3, oy: 0.7, enabled: true},
	{name: "double", k: 2, enabled: true},
}

// compileRuns compiles one family's command and returns the pixels it covers.
func compileRuns(t *testing.T, r *Renderer, wf worldFactor, emit func()) []bool {
	t.Helper()
	r.sched.resetFrame(64, 64)
	if wf.enabled {
		r.sched.setWorldTransform(wf.k, wf.ox, wf.oy)
	}
	emit()
	return coveredPixels(r.sched.classVerts(schedOpaque))
}

// compilePerPixel compiles the same pixel sequence as one 1×1 quad per pixel,
// which is what the families emitted before the runs.
func compilePerPixel(t *testing.T, r *Renderer, wf worldFactor, pixels [][2]int, idx uint8) []bool {
	t.Helper()
	return compileRuns(t, r, wf, func() {
		if !r.beginSolid(0, 0, r.clipW(), r.clipH()) {
			return
		}
		for _, p := range pixels {
			r.appendSolidQuad(float32(p[0]), float32(p[1]), float32(p[0]+1), float32(p[1]+1), idx)
		}
	})
}

func sameCoverage(t *testing.T, name string, got, want []bool) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			x, y := i%rasterGrid, i/rasterGrid
			t.Fatalf("%s: pixel (%d,%d) covered=%v, the per-pixel writes cover=%v", name, x, y, got[i], want[i])
		}
	}
}

// framePixelsClassic is the byte writer's frame: four edges, each clamped to the
// clip rectangle and then tested pixel by pixel against the clip rectangle and
// the framebuffer [R-SEL-02A].
func framePixelsClassic(cw, ch, rMinX, rMinY, rMaxX, rMaxY, clipMinX, clipMinY, clipMaxX, clipMaxY int) [][2]int {
	var out [][2]int
	if rMinX > rMaxX || rMinY > rMaxY || clipMinX > clipMaxX || clipMinY > clipMaxY {
		return out
	}
	write := func(x, y int) {
		if x < 0 || y < 0 || x >= cw || y >= ch {
			return
		}
		if x < clipMinX || x > clipMaxX || y < clipMinY || y > clipMaxY {
			return
		}
		out = append(out, [2]int{x, y})
	}
	horizontal := func(y int) {
		if y < clipMinY || y > clipMaxY {
			return
		}
		for x := maxInt(rMinX, clipMinX); x <= minInt(rMaxX, clipMaxX); x++ {
			write(x, y)
		}
	}
	vertical := func(x int) {
		if x < clipMinX || x > clipMaxX {
			return
		}
		for y := maxInt(rMinY, clipMinY); y <= minInt(rMaxY, clipMaxY); y++ {
			write(x, y)
		}
	}
	horizontal(rMinY)
	if rMaxY != rMinY {
		horizontal(rMaxY)
	}
	vertical(rMinX)
	if rMaxX != rMinX {
		vertical(rMaxX)
	}
	return out
}

// linePixelsClassic is the byte writer's Bresenham walk with its per-point
// framebuffer clip [03 §5.4].
func linePixelsClassic(cw, ch int, x0, y0, x1, y1 int32) [][2]int {
	var out [][2]int
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
		if x0 >= 0 && int(x0) < cw && y0 >= 0 && int(y0) < ch {
			out = append(out, [2]int{int(x0), int(y0)})
		}
		if x0 == x1 && y0 == y1 {
			break
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
	return out
}

// TestFrameRunsCoverTheSamePixelsAsPerPixelWrites locks the coalescing of
// drawFrameInclusive: four edge runs, or fewer when an edge is clipped away,
// covering exactly the byte writer's pixel set at every world factor.
func TestFrameRunsCoverTheSamePixelsAsPerPixelWrites(t *testing.T) {
	r, _ := schedulerFixture(t)
	const idx = 11
	cases := []struct {
		name                                   string
		rMinX, rMinY, rMaxX, rMaxY             int
		clipMinX, clipMinY, clipMaxX, clipMaxY int
	}{
		{"inside", 8, 6, 40, 30, 0, 0, 63, 63},
		{"whole framebuffer", 0, 0, 63, 63, 0, 0, 63, 63},
		{"off the left and top", -12, -7, 20, 18, 0, 0, 63, 63},
		{"past the right and bottom", 40, 44, 100, 90, 0, 0, 63, 63},
		{"clipped to an inner rectangle", 4, 4, 60, 60, 16, 12, 44, 40},
		{"clip cuts three edges", -5, -5, 70, 70, 10, 10, 50, 50},
		{"clip disjoint from the frame", 4, 4, 10, 10, 30, 30, 50, 50},
		{"one-pixel frame", 20, 20, 20, 20, 0, 0, 63, 63},
		{"one-pixel-tall frame", 10, 20, 30, 20, 0, 0, 63, 63},
		{"one-pixel-wide frame", 20, 10, 20, 30, 0, 0, 63, 63},
		{"entirely off the framebuffer", 80, 80, 90, 90, 0, 0, 63, 63},
		{"entirely above the framebuffer", 10, -30, 30, -10, 0, 0, 63, 63},
		{"empty clip rectangle", 10, 10, 30, 30, 20, 20, 10, 10},
		{"inverted frame", 30, 30, 10, 10, 0, 0, 63, 63},
	}
	for _, c := range cases {
		for _, wf := range worldFactors {
			want := compilePerPixel(t, r, wf,
				framePixelsClassic(r.clipW(), r.clipH(), c.rMinX, c.rMinY, c.rMaxX, c.rMaxY,
					c.clipMinX, c.clipMinY, c.clipMaxX, c.clipMaxY), idx)
			got := compileRuns(t, r, wf, func() {
				r.drawFrameInclusive(c.rMinX, c.rMinY, c.rMaxX, c.rMaxY,
					c.clipMinX, c.clipMinY, c.clipMaxX, c.clipMaxY, idx)
			})
			sameCoverage(t, c.name+" at "+wf.name, got, want)
		}
	}
}

// TestFrameCompilesFourRuns is the cost the coalescing exists for: a frame is at
// most four runs however large it is, where it was 2(W+H) one-pixel quads.
func TestFrameCompilesFourRuns(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 2, Y: 2, W: 60, H: 60}, Style: drawlist.FillOutline, Index: 5})
	if got := len(r.sched.classVerts(schedOpaque)); got != 4*quadVertices {
		t.Fatalf("a 60×60 selection frame compiled %d vertices, want %d (four edge runs)", got, 4*quadVertices)
	}
}

// TestLineRunsCoverTheSamePixelsAsPerPixelWrites locks the row coalescing of
// Line against the identical Bresenham walk, at every world factor.
func TestLineRunsCoverTheSamePixelsAsPerPixelWrites(t *testing.T) {
	r, _ := schedulerFixture(t)
	const idx = 23
	cases := []struct {
		name           string
		x0, y0, x1, y1 int32
	}{
		{"horizontal right", 4, 10, 50, 10},
		{"horizontal left", 50, 12, 4, 12},
		{"vertical down", 20, 2, 20, 60},
		{"vertical up", 22, 60, 22, 2},
		{"diagonal", 0, 0, 63, 63},
		{"anti-diagonal", 0, 63, 63, 0},
		{"shallow", 2, 5, 61, 19},
		{"shallow reversed", 61, 19, 2, 5},
		{"steep", 5, 2, 19, 61},
		{"single point", 30, 30, 30, 30},
		{"off the left edge", -40, 20, 30, 26},
		{"off the right edge", 30, 26, 120, 20},
		{"crossing the framebuffer", -30, -20, 100, 90},
		{"entirely off the framebuffer", -40, -40, -10, -10},
		{"grazing the top edge", -10, -1, 70, 1},
	}
	for _, c := range cases {
		for _, wf := range worldFactors {
			want := compilePerPixel(t, r, wf, linePixelsClassic(r.clipW(), r.clipH(), c.x0, c.y0, c.x1, c.y1), idx)
			got := compileRuns(t, r, wf, func() {
				r.Line(drawlist.Line{X0: c.x0, Y0: c.y0, X1: c.x1, Y1: c.y1, Index: idx})
			})
			sameCoverage(t, c.name+" at "+wf.name, got, want)
		}
	}
}

// TestHorizontalLineCompilesOneRun is the cost the line coalescing exists for.
func TestHorizontalLineCompilesOneRun(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	r.Line(drawlist.Line{X0: 2, Y0: 10, X1: 61, Y1: 10, Index: 5})
	if got := len(r.sched.classVerts(schedOpaque)); got != quadVertices {
		t.Fatalf("a 60-pixel horizontal line compiled %d vertices, want %d (one run)", got, quadVertices)
	}
}
