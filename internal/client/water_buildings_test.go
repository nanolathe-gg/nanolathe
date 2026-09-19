package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"testing"
)

// The ring phase carries the presentation fraction, so the foam advances at
// display rate. It still wraps with the tick and is unchanged at fraction zero.
func TestBuildingFoamPhaseUsesPresentationFraction(t *testing.T) {
	c, f := buildingFoamScene(t)
	c.terrain.SeaLevel = 10
	u := &f.Units[0]
	u.IsBuilding, u.Floater, u.CanHover = true, false, false
	u.Y = c.terrain.SeaLevelWorld() - numeric.Fixed(u.Waterline)*numeric.FixedOne
	read := func() drawlist.SurfaceWake {
		t.Helper()
		c.list.Reset()
		c.drawBuildingFoam(f)
		var s wakeCollector
		c.list.Replay(&s)
		if len(s.marks) != 1 {
			t.Fatalf("floating building recorded %d foam rings", len(s.marks))
		}
		return s.marks[0]
	}
	f.Tick = 7
	base := read()
	c.interpolation = true
	c.SetTickFraction(0)
	if zero := read(); zero != base {
		t.Fatalf("zero fraction changed the foam recording: %+v, want %+v", zero, base)
	}
	c.SetTickFraction(0.5)
	half := read()
	f.Tick = 8
	next := read()
	if half.Age <= base.Age || next.Age <= half.Age {
		t.Fatalf("half-tick foam phase is not between the two ticks: %v, %v, %v", base.Age, half.Age, next.Age)
	}
	// The wrapped tick keeps the phase inside one cycle at any fraction.
	f.Tick = 239
	c.SetTickFraction(0.75)
	if wrapped := read(); wrapped.Age >= 1 || wrapped.Age <= next.Age {
		t.Fatalf("foam phase left its cycle: %v", wrapped.Age)
	}
	f.Tick = 240
	if rolled := read(); rolled.Age != 0.75/240 {
		t.Fatalf("foam phase did not wrap with the tick: %v", rolled.Age)
	}
}

func TestBuildingFoamRequiresVisibleCompletedFloater(t *testing.T) {
	c, f := buildingFoamScene(t)
	c.terrain.SeaLevel = 10
	u := &f.Units[0]
	u.IsBuilding, u.Floater, u.CanHover = true, false, false
	u.Y = c.terrain.SeaLevelWorld() - numeric.Fixed(u.Waterline)*numeric.FixedOne
	read := func() int {
		c.list.Reset()
		c.drawBuildingFoam(f)
		var s wakeCollector
		c.list.Replay(&s)
		for _, m := range s.marks {
			if !m.Foam || m.Dust {
				t.Fatal("building emitted movement particles")
			}
		}
		return len(s.marks)
	}
	if read() != 1 {
		t.Fatal("visible floating building lost foam")
	}
	u.BuildRemaining = 1
	if read() != 0 {
		t.Fatal("unfinished building emitted foam")
	}
	u.BuildRemaining = 0
	u.Owner = 1
	if read() != 0 {
		t.Fatal("hidden enemy building revealed by foam")
	}
	u.Owner = 0
	c.terrain.LavaWorld = true
	if read() != 0 {
		t.Fatal("building foam appeared on lava")
	}
}

func buildingFoamScene(t *testing.T) (*Client, *frame.Frame) {
	c, f := wakeScene(t)
	piece := &c.models["wake-fixture"].compiled.Pieces[0]
	piece.Vertices = [][3]numeric.Fixed{{0, 0, 0}, {0, 12 * numeric.FixedOne, 0}, {8 * numeric.FixedOne, 0, 0}}
	piece.Primitives = []model.Primitive{{IsColored: 1, VertexIndices: []uint16{0, 1, 2}}}
	return c, f
}

func TestBuildingFoamRequiresVisibleSurfaceIntersection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Client, *frame.UnitView)
		want   int
	}{
		{"surface", func(*Client, *frame.UnitView) {}, 1},
		{"submerged pose", func(_ *Client, u *frame.UnitView) {
			u.Pieces = []frame.PieceView{{Index: 0, Ty: -9 * numeric.FixedOne}}
		}, 0},
		{"touching surface", func(_ *Client, u *frame.UnitView) {
			u.Pieces = []frame.PieceView{{Index: 0, Ty: -8 * numeric.FixedOne}}
		}, 1},
		{"above surface", func(_ *Client, u *frame.UnitView) { u.Pieces = []frame.PieceView{{Index: 0, Ty: 5 * numeric.FixedOne}} }, 0},
		{"hidden piece", func(_ *Client, u *frame.UnitView) { u.Pieces = []frame.PieceView{{Index: 0, Hidden: true}} }, 0},
		{"cloaked", func(_ *Client, u *frame.UnitView) { u.Cloaked = true }, 0},
		{"selection only", func(c *Client, _ *frame.UnitView) { c.models["wake-fixture"].compiled.Pieces[0].Selection = true }, 0},
		{"unused tall vertex", func(c *Client, _ *frame.UnitView) {
			p := &c.models["wake-fixture"].compiled.Pieces[0]
			p.Vertices[1][1] = 3 * numeric.FixedOne
			p.Vertices = append(p.Vertices, [3]numeric.Fixed{0, 100 * numeric.FixedOne, 0})
		}, 0},
		{"missing model", func(_ *Client, u *frame.UnitView) { u.Model = "" }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := buildingFoamScene(t)
			c.terrain.SeaLevel = 10
			u := &f.Units[0]
			u.IsBuilding, u.CanHover = true, false
			u.Y = c.terrain.SeaLevelWorld() - numeric.Fixed(u.Waterline)*numeric.FixedOne
			tc.change(c, u)
			c.drawBuildingFoam(f)
			var sink wakeCollector
			c.list.Replay(&sink)
			if len(sink.marks) != tc.want {
				t.Fatalf("recorded %d foam rings, want %d", len(sink.marks), tc.want)
			}
		})
	}
}

func TestSubmergedMobileUnitsDoNotProduceEnhancedSurfaceMarks(t *testing.T) {
	for _, hover := range []bool{false, true} {
		c, f := buildingFoamScene(t)
		c.terrain.SeaLevel = 40
		f.Units[0].CanHover = hover
		startWake(c, f)
		c.drawSurfaceWakes()
		c.drawBuildingFoam(f)
		var sink wakeCollector
		c.list.Replay(&sink)
		if len(sink.marks) != 0 {
			t.Fatalf("submerged mobile unit (hover=%v) emitted %d surface marks", hover, len(sink.marks))
		}
	}
}
