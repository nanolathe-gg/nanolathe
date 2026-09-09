package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// Preparation runs for every subject of every frame, so it must reuse its
// scratch rather than allocate per face, strip or outline endpoint
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"]. This needs no device:
// only the CPU preparation is measured.
func TestModelPreparationReusesFrameScratch(t *testing.T) {
	r := &Renderer{}
	g := fixtureGeometry(0, true,
		fixtureFace(0, 0, 6, 6, 20, 3),
		drawlist.ModelFace{Color: 4, Vertices: []drawlist.ModelVertex{
			{X: 1, Y: 1, Key: 30}, {X: 9, Y: 3, Key: 31}, {X: 5, Y: 9, Key: 32},
		}},
		fixturePinchedRing(2, 2),
		// A textured quad takes the parameter-image path, which must also grow
		// its packed bytes once and reuse them from then on.
		drawlist.ModelFace{Texture: &formats.GAFFrame{Width: 8, Height: 8}, Vertices: []drawlist.ModelVertex{
			{X: 2, Y: 1, U: 0, V: 0, Key: 30}, {X: 10, Y: 3, U: 7, V: 0, Key: 30},
			{X: 7, Y: 9, U: 7, V: 7, Key: 30}, {X: 0, Y: 7, U: 0, V: 7, Key: 30},
		}},
	)
	g.Outline = []drawlist.ModelFace{fixtureFace(0, 0, 6, 6, 20, 5)}
	prepare := func() {
		r.modelPrep.reset()
		r.modelAtlas.quads.reset()
		out := r.modelPrep.prepared.take(len(g.Faces))
		for i := range g.Faces {
			f := &g.Faces[i]
			r.prepareModelFace(&out[i], f, image.Point{}, polygonCrosses(f.Vertices))
		}
		r.prepareModelOutline(g)
	}
	prepare()
	if n := testing.AllocsPerRun(20, prepare); n != 0 {
		t.Fatalf("steady-state model preparation allocated %v objects per frame", n)
	}
}

// The preparation arenas hand out subslices of one retained backing array, so a
// slice taken before a growth must still be the caller's own storage afterwards
// [DESIGN_GPU_RENDERER.md §11.5 "CPU"]. A frame's prepared faces are read after
// later subjects have been prepared, so aliasing or copy-on-grow would corrupt
// them.
func TestFrameArenaKeepsEarlierSlicesValidAcrossGrowth(t *testing.T) {
	var a frameArena[int]
	first := a.take(4)
	for i := range first {
		first[i] = 100 + i
	}
	// Force several growths, filling each new slice with a distinct pattern.
	var later [][]int
	for n := 8; n <= 4096; n *= 2 {
		v := a.take(n)
		for i := range v {
			v[i] = n + i
		}
		later = append(later, v)
	}
	for i := range first {
		if first[i] != 100+i {
			t.Fatalf("slice taken before the growth was overwritten at %d: %d", i, first[i])
		}
	}
	for _, v := range later {
		for i := range v {
			if v[i] != len(v)+i {
				t.Fatalf("slice of length %d was overwritten at %d: %d", len(v), i, v[i])
			}
		}
	}
	// take must never hand the same element to two callers in one frame.
	if len(first) > 0 && len(later) > 0 && &first[0] == &later[0][0] {
		t.Fatal("two takes in one frame share an element")
	}
	// A steady-state frame rewinds rather than reallocating.
	frame := func() {
		a.reset()
		a.take(4)
		a.take(4096)
	}
	frame()
	before := cap(a.buf)
	if n := testing.AllocsPerRun(20, frame); n != 0 {
		t.Fatalf("steady-state arena use allocated %v objects per frame", n)
	}
	if cap(a.buf) != before {
		t.Fatalf("arena backing changed size in steady state: %d, want %d", cap(a.buf), before)
	}
}

func TestModelPadCountGrowsCoarsely(t *testing.T) {
	for _, c := range [][2]int{{0, 16}, {1, 16}, {16, 16}, {17, 32}, {4095, 4096}, {4096, 4096}, {4097, 8192}, {9000, 12288}} {
		if got := modelPadCount(c[0]); got != c[1] {
			t.Fatalf("modelPadCount(%d) = %d, want %d", c[0], got, c[1])
		}
	}
	// Padding never asks for more than the reserved slack past a list's end.
	for _, n := range []int{1, 100, 4095, 4096, 20000} {
		if modelPadCount(n)-n > modelVertexPadSlack {
			t.Fatalf("modelPadCount(%d) overshoots the reserved slack", n)
		}
	}
}

// The slot atlas replaces one scratch surface set per subject, so a page's
// stages must cost the same number of device draws whatever the frame holds.
// Runs inside the existing opt-in device loop.
func checkModelSlotFrames() error {
	pal := fixturePalette()
	a, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	b, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	source := fixtureModelList()
	models := source.ModelCommands()
	frame := func(w, h int, dx, dy int32, copies int) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: int32(w), H: int32(h)}, Index: 7, Style: drawlist.FillSolid})
		for c := 0; c < copies; c++ {
			for _, original := range models {
				cmd := original
				cmd.Geometry = cmd.Geometry.Clone()
				moveModelFixture(cmd.Geometry, dx, dy)
				l.RecordModel(cmd)
			}
		}
		l.RecordExpand()
		return l
	}
	var first ModelStats
	var firstPasses int
	for i := 0; i < 5; i++ {
		w, h := 80, 48
		if i > 1 {
			w, h = 128, 80
		}
		dx, dy := int32(0), int32(0)
		if i == 2 {
			dx, dy = 17, 11
		}
		if i == 3 {
			dx, dy = -5, -4
		}
		l := frame(w, h, dx, dy, 1)
		// b replays the same frame twice: no per-frame page state may leak
		// from one Execute into the next.
		x, y := make([]byte, w*h*4), make([]byte, w*h*4)
		a.Execute(&l, w, h).ReadPixels(x)
		b.Execute(&l, w, h)
		b.Execute(&l, w, h).ReadPixels(y)
		if !bytes.Equal(x, y) {
			return fmt.Errorf("slot atlas frame %d differs when the frame is repeated", i)
		}
		s := a.ModelStats()
		if s.SlotOverflows != 0 || s.SlotPages == 0 || s.RasterPixels == 0 {
			return fmt.Errorf("frame %d slot accounting: %+v", i, s)
		}
		// The stage is ordered by destination, so its device destination
		// switches are a property of which stages the frame needs and never of
		// the number of subjects or pages
		// [DESIGN_GPU_RENDERER.md §11.5 "Model slot passes"]. This fixture frame
		// exercises every stage: key, colour, reveal, both resolves, outline and
		// waterline/Digger clipping.
		if p := a.modelStagePasses(); p > modelStageMaxPasses {
			return fmt.Errorf("frame %d model stage passes=%d, want at most %d", i, p, modelStageMaxPasses)
		}
		if i == 0 {
			first, firstPasses = s, a.modelStagePasses()
		} else if s.RasterDraws != first.RasterDraws {
			return fmt.Errorf("frame %d raster draws=%d, want the frame-0 count %d", i, s.RasterDraws, first.RasterDraws)
		} else if p := a.modelStagePasses(); p != firstPasses {
			return fmt.Errorf("frame %d model stage passes=%d, want the frame-0 count %d", i, p, firstPasses)
		}
	}
	// A frame that cannot fit the shared pages sends its remaining subjects
	// through the per-subject fallback route, which must produce the same
	// pixels as the atlas it could not fit into.
	small, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	small.modelPageLimit = 6
	l := frame(80, 48, 0, 0, 1)
	x, y := make([]byte, 80*48*4), make([]byte, 80*48*4)
	a.Execute(&l, 80, 48).ReadPixels(x)
	small.Execute(&l, 80, 48).ReadPixels(y)
	if s := small.ModelStats(); s.SlotOverflows == 0 {
		return fmt.Errorf("a six-row atlas did not exercise the fallback route: %+v", s)
	}
	if !bytes.Equal(x, y) {
		return fmt.Errorf("the per-subject fallback route differs from the slot atlas")
	}
	// Four independent copies of every subject cost the same atlas passes.
	crowd := frame(128, 80, 0, 0, 4)
	a.Execute(&crowd, 128, 80)
	if s := a.ModelStats(); s.RasterDraws != first.RasterDraws || s.GPU != 4*first.GPU {
		return fmt.Errorf("crowded frame raster draws=%d bodies=%d, want %d draws and %d bodies", s.RasterDraws, s.GPU, first.RasterDraws, 4*first.GPU)
	}
	if p := a.modelStagePasses(); p != firstPasses {
		return fmt.Errorf("crowded frame model stage passes=%d, want the single-copy count %d", p, firstPasses)
	}
	// The attached-unit staging is ordered by destination too, so four copies of
	// every group cost the frame the same device destination switches as one: the
	// staging atlas gives each group a disjoint region and merges child k of every
	// group in a single pass (docs/DESIGN_GPU_RENDERER.md §13.3, model_stage.go).
	if s := a.ModelStats(); s.Passes != first.Passes {
		return fmt.Errorf("crowded frame destination switches=%d for %d composed groups, want the single-copy count %d for %d",
			s.Passes, s.ComposedGroups, first.Passes, first.ComposedGroups)
	}
	return nil
}

// modelStageMaxPasses is the contract of the destination-ordered slot stage:
// the clear of each plane, the key work, the colour work, the reveal ping-pong,
// the resolves that ride it, the separate live key/colour passes, and final clipping
// [DESIGN_GPU_RENDERER.md §11.5 "Model slot passes"].
const modelStageMaxPasses = 10

func moveModelFixture(g *drawlist.ModelGeometry, dx, dy int32) {
	if g == nil {
		return
	}
	g.AnchorX += dx
	g.AnchorY += dy
	moveModelFixture(g.Shadow, dx, dy)
	for _, child := range g.Children {
		moveModelFixture(child.Geometry, dx, dy)
	}
}
