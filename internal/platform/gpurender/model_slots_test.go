package gpurender

import (
	"bytes"
	"fmt"
	"testing"

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
	)
	g.Outline = []drawlist.ModelFace{fixtureFace(0, 0, 6, 6, 20, 5)}
	prepare := func() {
		r.modelPrep.reset()
		out := r.modelPrep.prepared.take(len(g.Faces))
		for i := range g.Faces {
			out[i] = r.prepareModelFace(g.Faces[i])
		}
		r.prepareModelOutline(g)
	}
	prepare()
	if n := testing.AllocsPerRun(20, prepare); n != 0 {
		t.Fatalf("steady-state model preparation allocated %v objects per frame", n)
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
		if i == 0 {
			first = s
		} else if s.RasterDraws != first.RasterDraws {
			return fmt.Errorf("frame %d raster draws=%d, want the frame-0 count %d", i, s.RasterDraws, first.RasterDraws)
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
	return nil
}

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
