package client

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func trailSole(name string, parent int, x, y, z, width, length int64) compiledmodel.Piece {
	f := numeric.FixedOne
	return compiledmodel.Piece{Name: name, Parent: parent, Translate: [3]numeric.Fixed{numeric.Fixed(x) * f, numeric.Fixed(y) * f, numeric.Fixed(z) * f},
		Vertices:   [][3]numeric.Fixed{{-numeric.Fixed(width) * f / 2, 0, -numeric.Fixed(length) * f / 2}, {numeric.Fixed(width) * f / 2, 0, -numeric.Fixed(length) * f / 2}, {numeric.Fixed(width) * f / 2, 0, numeric.Fixed(length) * f / 2}, {-numeric.Fixed(width) * f / 2, 0, numeric.Fixed(length) * f / 2}},
		Primitives: []compiledmodel.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}}}
}

func TestTrailGeometryCombinesSoleAndToeAndIgnoresSelection(t *testing.T) {
	m := &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{
		trailSole("base", -1, 0, 0, 0, 32, 40),
		{Name: "lthigh", Parent: 0, Translate: [3]numeric.Fixed{-6 * numeric.FixedOne, 20 * numeric.FixedOne, 0}},
		trailSole("lleg", 1, 0, -20, 0, 12, 24),
		trailSole("ltoes", 2, 0, 0, 16, 10, 8),
		{Name: "rthigh", Parent: 0, Translate: [3]numeric.Fixed{6 * numeric.FixedOne, 20 * numeric.FixedOne, 0}},
		trailSole("rleg", 4, 0, -20, 0, 12, 24),
		trailSole("rtoes", 5, 0, 0, 16, 10, 8),
		{Name: "foot_emit", Parent: 0, Vertices: [][3]numeric.Fixed{{1000 * numeric.FixedOne, 0, 0}}},
	}}
	// Selection geometry and an unreferenced outlier are intentionally wider
	// than the body. Only actual surface primitives own the trail dimensions.
	root := &m.Pieces[0]
	root.Vertices = append(root.Vertices, [3]numeric.Fixed{-1000 * numeric.FixedOne, 0, 0}, [3]numeric.Fixed{1000 * numeric.FixedOne, 0, 0}, [3]numeric.Fixed{0, 0, 1000 * numeric.FixedOne}, [3]numeric.Fixed{9999 * numeric.FixedOne, 0, 0})
	root.Primitives = append([]compiledmodel.Primitive{{VertexIndices: []uint16{4, 5, 6}}}, root.Primitives...)
	root.Selection = true
	got := measureTrailGeometry(m)
	if got.footWidth != 12 || got.footLength != 32 || got.footSpread != 6 || got.bodyWidth != 32 {
		t.Fatalf("geometry %+v: want combined sole/toe 12x32, spread 6, body 32", got)
	}
}

func TestTrailGeometryPointedLegUsesClippedTip(t *testing.T) {
	f := numeric.FixedOne
	for _, tc := range []struct {
		name          string
		x, z          numeric.Fixed
		width, length float64
	}{
		{"clipped edges", 20 * f, 30 * f, 4, 6},
		{"minimum visible tip", f, f, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{{Name: "base", Parent: -1}, {Name: "leg1", Parent: 0,
				Vertices:   [][3]numeric.Fixed{{0, 0, 0}, {tc.x, 10 * f, 0}, {0, 10 * f, tc.z}},
				Primitives: []compiledmodel.Primitive{{VertexIndices: []uint16{0, 1, 2}}}}}}
			got := measureTrailGeometry(m)
			if math.Abs(got.footWidth-tc.width) > 1e-9 || math.Abs(got.footLength-tc.length) > 1e-9 {
				t.Fatalf("pointed leg %+v: want clipped/minimum dimensions %vx%v", got, tc.width, tc.length)
			}
		})
	}
}

func TestTrailGeometryChangesStrideAndBoundsActualQuads(t *testing.T) {
	c := trailScene(t)
	c.SetEnhanced(true)
	m := c.models["trail_walker"]
	m.compiled = &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{trailSole("base", -1, 0, 0, 0, 44, 40), trailSole("lfoot", 0, -6, 0, 0, 12, 30), trailSole("rfoot", 0, 6, 0, 0, 12, 30)}}
	publishWalker(t, c, 1, 40)
	recordTrails(c)
	publishWalker(t, c, 2, 105)
	sink := recordTrails(c)
	marks := sink.batches[0].Marks
	if len(marks) != 2 || marks[1].X-marks[0].X != 30 || marks[0].AxisX != 15*256 || marks[0].CrossY != 6*256 {
		t.Fatalf("scaled footprints %+v: want 30-pixel stride and 30x12 prints", marks)
	}
	style := c.trailStyleFor(frame.UnitView{Model: "trail_walker", FootX: 100}, trailTracks)
	if math.Abs(style.spread+2*style.halfWidth-22) > 1e-9 || style.stride != trailTrackStride {
		t.Fatalf("vehicle %+v: outer edge must sit half a strip inside body edge, independent of FootX", style)
	}
	// Geometry is immutable and the warm lookup must allocate no scratch.
	if allocs := testing.AllocsPerRun(100, func() { c.trailStyleFor(frame.UnitView{Model: "trail_walker"}, trailFeet) }); allocs != 0 {
		t.Fatalf("warm geometry lookup allocs=%v", allocs)
	}
	// Force a vehicle classification to exercise actual pair placement.
	c.resetTrails()
	c.trails.classes = map[uint16]trailClass{1: trailTracks}
	for tick := uint32(1); tick <= 3; tick++ {
		f := &frame.Frame{Tick: tick, Units: []frame.UnitView{{Slot: 1, DefID: 1, Model: "trail_walker", BMCode: true, MoverMode: 1, X: numeric.Fixed(40+tick*8) * numeric.FixedOne, Z: 50 * numeric.FixedOne}}}
		c.placeTrails(f)
	}
	if len(c.trails.marks) != 4 {
		t.Fatalf("two track steps retained %d quads, want 4", len(c.trails.marks))
	}
	for i := 0; i < trailRingSize+3; i++ {
		c.trails.push(trailMark{live: true, halfWidth: 2, halfLength: 4, class: trailTracks})
	}
	if len(c.trails.marks) != trailRingSize {
		t.Fatalf("ring has %d actual quads", len(c.trails.marks))
	}
}

// Asset dimensions validate the selected geometry, not any claim that retail
// emits footprints. The mark/stride rules are Nanolathe presentation policy.
func TestRetailTrailGeometrySizes(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	got := map[string]trailGeometry{}
	for _, name := range []string{"armpw", "corkrog", "armspid", "armstump", "armbull"} {
		m, err := compiledmodel.Load(fs, "objects3d/"+name+".3do")
		if err != nil {
			t.Fatal(err)
		}
		g := measureTrailGeometry(m)
		got[name] = g
		t.Logf("%s: foot %.3fx%.3f spread %.3f body %.3f", name, g.footWidth, g.footLength, g.footSpread, g.bodyWidth)
	}
	if got["corkrog"].footWidth < 2*got["armpw"].footWidth || got["corkrog"].footLength < 2*got["armpw"].footLength {
		t.Fatal("Krogoth soles must be substantially larger than Peewee feet")
	}
	if got["armspid"].footWidth <= 0 || got["armspid"].footLength <= 0 {
		t.Fatal("single-piece spider legs need usable tips")
	}
	if got["armbull"].bodyWidth <= got["armstump"].bodyWidth {
		t.Fatal("Bulldog body must produce wider track spacing than Stumpy")
	}
}

func TestTrailGeometryUsesNamedBodyWhenRootHasNoSurface(t *testing.T) {
	m := &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{
		{Name: "root", Parent: -1}, trailSole("turret", 0, 0, 10, 0, 200, 20), trailSole("chassis", 0, 0, 0, 0, 48, 30),
	}}
	if got := measureTrailGeometry(m).bodyWidth; got != 48 {
		t.Fatalf("body width=%v, want chassis width without turret", got)
	}
}

func TestTrailGeometryCullsByDimensionsAndKeepsFractionalDimensions(t *testing.T) {
	c := trailScene(t)
	c.SetEnhanced(true)
	c.cam.Zoom = camera.ZoomUnit * 3 / 2
	c.trails.marks = []trailMark{
		{x: -40 * numeric.FixedOne, z: 50 * numeric.FixedOne, dirX: 256, halfLength: 80, halfWidth: 3, class: trailFeet, live: true},
		{x: 50 * numeric.FixedOne, z: 50 * numeric.FixedOne, dirX: 256, halfLength: 3.25, halfWidth: 1.75, class: trailFeet, live: true},
	}
	c.drawTrails()
	sink := &trailCollector{}
	c.list.Replay(sink)
	if len(sink.batches) != 1 || len(sink.batches[0].Marks) != 2 {
		t.Fatalf("large offscreen-centred mark was culled: %+v", sink.batches)
	}
	m := sink.batches[0].Marks[1]
	if m.AxisX != 832 || m.CrossY != 448 {
		t.Fatalf("fractional dimensions %+v, want record axes 832/448 (live zoom belongs to GPU)", m)
	}
}
