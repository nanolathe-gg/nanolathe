package client

// The Enhanced trail layer's locks (DESIGN_GPU_RENDERER §15). Everything here
// is a NANOLATHE presentation rule: retail leaves no marks.

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestClassifyTrail(t *testing.T) {
	cases := []struct {
		ted, move string
		aircraft  bool
		legs      bool
		want      trailClass
	}{
		{"KBOT", "TANKSH2", false, false, trailFeet}, // most kbots ride TANK-prefixed movement classes
		{"COMMANDER", "TANKDS2", false, true, trailFeet},
		{"TANK", "TANKSH3", false, false, trailTracks},
		{"TANK", "TANKHOVER3", false, false, trailNone}, // hovers touch no ground
		{"KBOT", "TANKHOVER3", false, true, trailNone},
		{"SHIP", "BOATS4", false, false, trailNone},
		{"CNSTR", "BOATS4", false, false, trailNone},
		{"CNSTR", "TANKSH2", false, true, trailFeet},    // construction kbot: legs decide
		{"CNSTR", "TANKSH3", false, false, trailTracks}, // construction vehicle
		{"SPECIAL", "SPID3", false, true, trailFeet},
		{"TANK", "SPID3", false, true, trailFeet}, // spider: legs outrank editor TANK
		{"SHIP", "", false, true, trailNone},
		{"PLANT", "", false, true, trailNone},
		{"VTOL", "", false, true, trailNone},
		{"TANK", "TANKHOVER3", false, true, trailNone},
		{"VTOL", "", true, false, trailNone},
		{"CNSTR", "", true, false, trailNone},
		{"", "", false, true, trailFeet},
		{"", "", false, false, trailTracks},
	}
	for _, tc := range cases {
		if got := classifyTrail(tc.ted, tc.move, tc.aircraft, tc.legs); got != tc.want {
			t.Errorf("classifyTrail(%q, %q, aircraft=%v, legs=%v) = %v, want %v", tc.ted, tc.move, tc.aircraft, tc.legs, got, tc.want)
		}
	}
	for _, name := range []string{"lleg", "RFoot", "thigh2", "torso"} {
		want := name != "torso"
		if got := trailLegPiece(name); got != want {
			t.Errorf("trailLegPiece(%q) = %v, want %v", name, got, want)
		}
	}
}

type trailCollector struct {
	batches []drawlist.Trails
	other   int
}

func (s *trailCollector) Clear()                   {}
func (s *trailCollector) Terrain(drawlist.Terrain) { s.other++ }
func (s *trailCollector) Sprite(drawlist.Sprite)   { s.other++ }
func (s *trailCollector) Glyphs(drawlist.Glyphs)   { s.other++ }
func (s *trailCollector) Fill(drawlist.Fill)       { s.other++ }
func (s *trailCollector) Line(drawlist.Line)       { s.other++ }
func (s *trailCollector) Points(drawlist.Points)   { s.other++ }
func (s *trailCollector) Flash(drawlist.Flash)     {}
func (s *trailCollector) Halo(drawlist.Halo)       {}
func (s *trailCollector) Model(drawlist.Model)     { s.other++ }
func (s *trailCollector) Fog(drawlist.Fog)         { s.other++ }
func (s *trailCollector) Surface(drawlist.Surface) { s.other++ }
func (s *trailCollector) Cursor(drawlist.Cursor)   { s.other++ }
func (s *trailCollector) Expand()                  {}
func (s *trailCollector) Trails(v drawlist.Trails) {
	s.batches = append(s.batches, drawlist.Trails{Marks: append([]drawlist.Trail(nil), v.Marks...)})
}

// trailScene is a legged mobile unit on a flat map with a camera at the
// origin, so world x maps onto screen x plus the viewport origin.
func trailScene(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	c.modelFS = vfs.New()
	c.SetPalette(&palette.Tables{})
	c.SetTerrain(&world.Terrain{CellW: 64, CellH: 64, TileIndices: make([]uint16, 32*32), TileSet: make([][1024]byte, 1)})
	c.models["trail_walker"] = syntheticModel(
		[]pieceInfo{{name: "torso", parent: -1}, {name: "lleg", parent: 0}},
		[]syntheticTri{makeTriangle(0, "torso", [3][3]float64{{0, 0, 0}, {6, 0, 0}, {0, 0, 6}}, 17, 0)},
		0,
	)
	c.cam = &camera.Camera{Scale: 1}
	return c
}

func publishWalker(t *testing.T, c *Client, tick uint32, x int32) {
	t.Helper()
	f := c.buffer.BeginWrite()
	f.Tick = tick
	f.Units = append(f.Units[:0], frame.UnitView{
		Slot: 1, Model: "trail_walker", BMCode: true, MoverMode: 1, FootX: 2,
		X: numeric.Fixed(x) << 16, Z: numeric.Fixed(50) << 16,
	})
	if err := c.buffer.Publish(tick); err != nil {
		t.Fatal(err)
	}
}

func recordTrails(c *Client) *trailCollector {
	c.list.Reset()
	c.placeTrails(c.buffer.Current())
	c.drawTrails()
	sink := &trailCollector{}
	c.list.Replay(sink)
	return sink
}

func TestTrailsFollowCommittedMotionOnlyWhileEnhanced(t *testing.T) {
	c := trailScene(t)
	c.SetEnhanced(true)

	// The first sighting lays nothing: there is no stride to measure yet.
	publishWalker(t, c, 1, 40)
	if sink := recordTrails(c); len(sink.batches) != 0 {
		t.Fatalf("first sighting recorded %d trail batches, want none", len(sink.batches))
	}
	// A 25-pixel step at the 10-pixel foot stride lays two footprints, on
	// alternating sides, both pointing along +x.
	publishWalker(t, c, 2, 65)
	sink := recordTrails(c)
	if len(sink.batches) != 1 || len(sink.batches[0].Marks) != 2 {
		t.Fatalf("after the step: batches %d, want one batch of two marks (%+v)", len(sink.batches), sink.batches)
	}
	a, b := sink.batches[0].Marks[0], sink.batches[0].Marks[1]
	if a.Shape != drawlist.TrailFootprint || a.AxisX <= 0 || a.AxisY != 0 {
		t.Fatalf("first footprint %+v: want an oval pointing along +x", a)
	}
	if b.X-a.X != 10 || a.Y == b.Y {
		t.Fatalf("footprints %+v and %+v: want one stride apart along x on alternating sides", a, b)
	}
	// The camera sits at the world origin, so the mark's screen x is its world
	// x rebased to the shell origin, exactly as the model anchor is.
	if a.X != 40+10 {
		t.Fatalf("first footprint at screen x %d, want the stride point %d", a.X, 40+10)
	}
	// Re-recording the same committed tick lays nothing new and keeps both.
	if again := recordTrails(c); len(again.batches) != 1 || len(again.batches[0].Marks) != 2 {
		t.Fatalf("re-recording the same tick changed the batch: %+v", again.batches)
	}
	// Age fades the strength and the marks expire after their life.
	publishWalker(t, c, 2+trailLifeTicks/2, 65)
	mid := recordTrails(c)
	if len(mid.batches) != 1 || mid.batches[0].Marks[0].Strength >= a.Strength || mid.batches[0].Marks[0].Strength == 0 {
		t.Fatalf("half-life strength %+v, want below the fresh %d and above zero", mid.batches, a.Strength)
	}
	publishWalker(t, c, 2+trailLifeTicks, 65)
	if old := recordTrails(c); len(old.batches) != 0 {
		t.Fatalf("expired marks still recorded: %+v", old.batches)
	}

	// Original presents no trail layer at all, and lays none either.
	c.SetEnhanced(false)
	publishWalker(t, c, 400, 100)
	publishWalker(t, c, 401, 130)
	if sink := recordTrails(c); len(sink.batches) != 0 {
		t.Fatalf("Original recorded trail batches: %+v", sink.batches)
	}
}

func TestTrailsSkipAirborneWadingAndTeleported(t *testing.T) {
	c := trailScene(t)
	c.SetEnhanced(true)
	publish := func(tick uint32, x int32, mode uint8, y numeric.Fixed) {
		f := c.buffer.BeginWrite()
		f.Tick = tick
		f.Units = append(f.Units[:0], frame.UnitView{
			Slot: 1, Model: "trail_walker", BMCode: true, MoverMode: mode, FootX: 2,
			X: numeric.Fixed(x) << 16, Y: y, Z: numeric.Fixed(50) << 16,
		})
		if err := c.buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	publish(1, 40, 1, 0)
	// Airborne: the stride restarts at the landing point.
	publish(2, 70, 2, 0)
	if sink := recordTrails(c); len(sink.batches) != 0 {
		t.Fatalf("airborne step laid marks: %+v", sink.batches)
	}
	// Hovering above the ground.
	publish(3, 100, 1, numeric.Fixed(8)<<16)
	if sink := recordTrails(c); len(sink.batches) != 0 {
		t.Fatalf("elevated step laid marks: %+v", sink.batches)
	}
	// A step far beyond the snap bound is a move, not a walk.
	publish(4, 100+trailFeetStride*trailSnap+5, 1, 0)
	if sink := recordTrails(c); len(sink.batches) != 0 {
		t.Fatalf("teleport laid marks: %+v", sink.batches)
	}
	// Walking again from the new place lays marks.
	publish(5, 100+trailFeetStride*trailSnap+5+12, 1, 0)
	if sink := recordTrails(c); len(sink.batches) != 1 || len(sink.batches[0].Marks) != 1 {
		t.Fatalf("walk after the move: %+v, want one footprint", sink.batches)
	}
}
