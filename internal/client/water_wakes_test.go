package client

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These fixtures lock Enhanced artistic history rules (GPU design §26).
func wakeScene(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c, err := New(Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	c.SetTerrain(&world.Terrain{CellW: 32, CellH: 32, SeaLevel: 0, Plot: make([]world.PlotCell, 32*32)})
	c.enhanced = true
	c.cam = &camera.Camera{Scale: 1}
	f := &frame.Frame{Tick: 1, Units: []frame.UnitView{{
		InstanceID: 1, Slot: 1, CanHover: true, MoverMode: moverModeGrounded,
		FootX: 2, FootZ: 3, Waterline: 4, HullYExtent: 12 * numeric.FixedOne,
		X: 32 * numeric.FixedOne, Y: 0, Z: 32 * numeric.FixedOne,
	}}}
	return c, f
}

func stepWake(c *Client, f *frame.Frame, x, z int32) {
	f.Tick++
	f.Units[0].X, f.Units[0].Z = numeric.Fixed(x)*numeric.FixedOne, numeric.Fixed(z)*numeric.FixedOne
	c.placeSurfaceWakes(f)
}

type wakeCollector struct {
	trailCollector
	marks []drawlist.SurfaceWake
}

func (s *wakeCollector) SurfaceWakes(v drawlist.SurfaceWakes) {
	s.marks = append(s.marks, v.Marks...)
}

func recordWakes(c *Client) []drawlist.SurfaceWake {
	c.list.Reset()
	c.drawSurfaceWakes()
	s := &wakeCollector{}
	c.list.Replay(s)
	return s.marks
}

func TestSurfaceWakesRetainTurnsAndFade(t *testing.T) {
	c, f := wakeScene(t)
	c.placeSurfaceWakes(f)
	if len(recordWakes(c)) != 0 {
		t.Fatal("first sighting emitted dust")
	}
	stepWake(c, f, 38, 32)
	stepWake(c, f, 38, 38)
	marks := recordWakes(c)
	if len(marks) != 2 {
		t.Fatalf("turn recorded %d puffs", len(marks))
	}
	a, b := marks[0], marks[1]
	if a.AxisX <= 0 || a.AxisY != 0 || b.AxisX != 0 || b.AxisY <= 0 {
		t.Fatalf("turn lost the puffs' birth directions: %+v", marks)
	}
	if !a.Dust || !b.Dust || a.Y == 32 || b.X == 38 {
		t.Fatalf("puffs did not alternate off the centre path: %+v", marks)
	}
	c.placeSurfaceWakes(f)
	stepWake(c, f, 38, 38)
	if len(c.wakes.marks) != 2 {
		t.Fatal("same tick or stationary unit emitted dust")
	}
	for range wakeDustLife / 2 {
		stepWake(c, f, 38, 38)
	}
	aged := recordWakes(c)
	if len(aged) != 2 || aged[0].Alpha >= a.Alpha || aged[0].Alpha <= 0 || aged[0].CrossY <= a.CrossY || aged[0].Age <= a.Age || aged[0].Y == a.Y {
		t.Fatalf("dust did not spread, drift and fade: fresh %+v aged %+v", a, aged)
	}
	for range wakeDustLife {
		stepWake(c, f, 38, 38)
	}
	if len(recordWakes(c)) != 0 {
		t.Fatal("expired dust still recorded")
	}
}

// The recorded age carries the presentation fraction, so the puffs spread and
// fade at display rate instead of stepping at 30 Hz. The lifetime in ticks and
// the zero-fraction recording are unchanged.
func TestSurfaceDustAgeUsesPresentationFraction(t *testing.T) {
	c, f := wakeScene(t)
	c.placeSurfaceWakes(f)
	stepWake(c, f, 38, 32)
	base := recordWakes(c)
	if len(base) != 1 {
		t.Fatalf("fixture recorded %d puffs", len(base))
	}
	c.interpolation = true
	c.SetTickFraction(0)
	if zero := recordWakes(c); len(zero) != 1 || zero[0] != base[0] {
		t.Fatalf("zero fraction changed the recording: %+v, want %+v", zero, base)
	}
	c.SetTickFraction(0.5)
	half := recordWakes(c)
	if len(half) != 1 || half[0].Age <= base[0].Age || half[0].Alpha >= base[0].Alpha || half[0].CrossY <= base[0].CrossY {
		t.Fatalf("dust did not age with the presentation fraction: %+v after %+v", half, base)
	}
	for range wakeDustLife - 1 {
		stepWake(c, f, 38, 32)
	}
	if len(recordWakes(c)) != 1 {
		t.Fatal("the fraction shortened the dust lifetime")
	}
	stepWake(c, f, 38, 32)
	if len(recordWakes(c)) != 0 {
		t.Fatal("expired dust still recorded")
	}
}

func TestSurfaceDustUsesDistanceCadence(t *testing.T) {
	slow, sf := wakeScene(t)
	fast, ff := wakeScene(t)
	slow.placeSurfaceWakes(sf)
	fast.placeSurfaceWakes(ff)
	for x := int32(33); x <= 56; x++ {
		stepWake(slow, sf, x, 32)
	}
	stepWake(fast, ff, 56, 32)
	if len(slow.wakes.marks) == 0 || len(slow.wakes.marks) != len(fast.wakes.marks) {
		t.Fatal("equal travelled distance produced different dust density")
	}
	for i, a := range slow.wakes.marks {
		b := fast.wakes.marks[i]
		if math.Abs(float64(a.x-b.x)) > 1 || math.Abs(float64(a.z-b.z)) > 1 || a.half != b.half || a.width != b.width {
			t.Fatalf("distance subdivision changed puff geometry: %+v %+v", a, b)
		}
	}
}

// Established height semantics [04 R-MOV-01 §5]: a four-corner average
// can differ from the centre height by more than the bob amplitude. The
// artistic dust admission must use the committed grounded mode instead.
func TestSurfaceDustAcceptsGroundedPlateHeight(t *testing.T) {
	c, f := wakeScene(t)
	for i := range c.terrain.Plot {
		c.terrain.Plot[i].SetHeight(20)
	}
	c.terrain.PlotAt(2, 2).SetHeight(4)
	f.Units[0].Y = 18 * numeric.FixedOne
	if f.Units[0].Y-c.terrain.HeightAt(f.Units[0].X, f.Units[0].Z) <= 2*numeric.FixedOne {
		t.Fatal("fixture does not separate plate and centre height")
	}
	c.placeSurfaceWakes(f)
	stepWake(c, f, 38, 32)
	if len(c.wakes.marks) == 0 {
		t.Fatal("grounded hovercraft lost dust because model Y differs from centre terrain")
	}
	for _, m := range c.wakes.marks {
		if m.y != c.terrain.HeightAt(m.x, m.z) {
			t.Fatal("dust did not use its own ground height")
		}
	}
}

func TestSurfaceWakesBreakHiddenAndDiscontinuousHistory(t *testing.T) {
	for _, reason := range []string{"hidden", "carried", "airborne", "unfinished", "reused", "missing", "teleport"} {
		t.Run(reason, func(t *testing.T) {
			c, f := wakeScene(t)
			f.Units[0].Owner = 1
			f.Visibility = frame.VisibilityView{W: 16, H: 16, Valid: true, CoverageBytes: true, Visible: make([]uint8, 256)}
			for i := range f.Visibility.Visible {
				f.Visibility.Visible[i] = 1
			}
			f.Fog = frame.FogView{W: 16, H: 16, Valid: true, Ch0: make([]uint8, 256), Ch1: make([]uint8, 256)}
			c.placeSurfaceWakes(f)
			stepWake(c, f, 38, 32)
			if len(c.wakes.marks) != 1 {
				t.Fatal("visible enemy failed to emit initial mark")
			}
			saved := f.Units[0]
			switch reason {
			case "hidden":
				f.Units[0].Cloaked = true
			case "carried":
				f.Units[0].Carrier = 2
			case "airborne":
				f.Units[0].MoverMode = 2
			case "unfinished":
				f.Units[0].BuildRemaining = 0.5
			case "reused":
				f.Units[0].InstanceID = 2
			case "missing":
				f.Units = nil
				f.Tick++
				c.placeSurfaceWakes(f)
			case "teleport":
				stepWake(c, f, 200, 32)
			}
			if reason != "missing" && reason != "teleport" {
				stepWake(c, f, 44, 32)
			}
			if len(c.wakes.marks) != 1 {
				t.Fatal("rejected movement bridged the previous visible history")
			}
			f.Units = []frame.UnitView{saved}
			stepWake(c, f, 50, 32)
			if len(c.wakes.marks) != 1 {
				t.Fatal("reappearance bridged a hidden interval or reused identity")
			}
			stepWake(c, f, 56, 32)
			if len(c.wakes.marks) != 2 {
				t.Fatal("consecutive visible movement did not resume")
			}
		})
	}
	for _, tick := range []uint32{0, 20} {
		c, f := wakeScene(t)
		c.placeSurfaceWakes(f)
		stepWake(c, f, 38, 32)
		f.Tick = tick
		f.Units[0].X += 8 * numeric.FixedOne
		c.placeSurfaceWakes(f)
		if len(c.wakes.marks) != 0 {
			t.Fatal("tick discontinuity retained old history")
		}
	}
}

func TestSurfaceDustStopsAtWaterAndRestartsOnLand(t *testing.T) {
	c, f := wakeScene(t)
	c.terrain.SeaLevel = 10
	for z := int32(0); z < c.terrain.CellH; z++ {
		for x := int32(4); x < c.terrain.CellW; x++ {
			c.terrain.PlotAt(x, z).SetHeight(20)
		}
	}
	c.placeSurfaceWakes(f)
	stepWake(c, f, 38, 32)
	if len(c.wakes.marks) != 0 {
		t.Fatal("hover water movement emitted added wakes")
	}
	stepWake(c, f, 80, 32)
	stepWake(c, f, 86, 32)
	if len(c.wakes.marks) == 0 {
		t.Fatal("dry hover movement emitted no dust")
	}
	count := len(c.wakes.marks)
	stepWake(c, f, 40, 32)
	stepWake(c, f, 46, 32)
	if len(c.wakes.marks) != count {
		t.Fatal("water movement emitted new dust")
	}
	for _, m := range c.wakes.marks {
		if m.y < c.terrain.SeaLevelWorld() {
			t.Fatal("dust born in water")
		}
	}
	stepWake(c, f, 86, 32)
	if len(c.wakes.marks) != count {
		t.Fatal("land reappearance bridged water history")
	}
	for range wakeDustLife {
		stepWake(c, f, 86, 32)
	}
	if len(recordWakes(c)) != 0 {
		t.Fatal("dry dust retained after expiry")
	}
}

func TestSurfaceWakesObserveCaptureTicksAndViewerReset(t *testing.T) {
	c, f := wakeScene(t)
	for tick := uint32(1); tick <= 3; tick++ {
		published := c.buffer.BeginWrite()
		published.Units = append(published.Units[:0], f.Units[0])
		published.Units[0].X += numeric.Fixed(tick*6) * numeric.FixedOne
		if err := c.buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
		c.ObserveCommittedTick()
	}
	if len(recordWakes(c)) != 2 {
		t.Fatal("capture tick observation omitted intermediate movement")
	}
	cur := c.buffer.Current()
	changed := *cur
	changed.ViewingPlayer = 1
	c.placeSurfaceWakes(&changed)
	if len(c.wakes.marks) != 0 {
		t.Fatal("viewer change retained another viewer's history")
	}
}

func TestSurfaceWakesRejectIneligibleSurfacesAndUnits(t *testing.T) {
	for _, reason := range []string{"ordinary", "no identity", "water hover", "water ship", "lava water", "acid water", "off map", "land ship", "building", "Original"} {
		t.Run(reason, func(t *testing.T) {
			c, f := wakeScene(t)
			switch reason {
			case "ordinary":
				f.Units[0].CanHover = false
			case "no identity":
				f.Units[0].InstanceID = 0
			case "water hover":
				c.terrain.SeaLevel = 10
			case "water ship":
				c.terrain.SeaLevel = 10
				f.Units[0].CanHover = false
				f.Units[0].Floater = true
			case "lava water":
				c.terrain.SeaLevel = 10
				c.terrain.LavaWorld = true
			case "acid water":
				c.terrain.SeaLevel = 10
				c.terrain.WaterDoesDamage = 1
				c.terrain.WaterDamage = 1
			case "off map":
				f.Units[0].Z = -numeric.FixedOne
			case "land ship":
				f.Units[0].CanHover = false
				f.Units[0].Floater = true
			case "building":
				f.Units[0].IsBuilding = true
			case "Original":
				c.enhanced = false
			}
			c.placeSurfaceWakes(f)
			f.Tick++
			f.Units[0].X += 6 * numeric.FixedOne
			c.placeSurfaceWakes(f)
			if len(c.wakes.marks) != 0 {
				t.Fatal("ineligible unit or surface emitted dust")
			}
		})
	}
}

func TestSurfaceWakeStorageBoundAndSourceReset(t *testing.T) {
	c, f := wakeScene(t)
	for i := range wakeRingSize * 2 {
		c.wakes.push(surfaceWakeMark{born: uint32(i)})
	}
	if len(c.wakes.marks) != wakeRingSize || cap(c.wakes.marks) > wakeRingSize*2 {
		t.Fatal("wake ring grew beyond its bounded storage")
	}
	u := f.Units[0]
	f.Units = make([]frame.UnitView, wakeTrackerLimit+10)
	for i := range f.Units {
		f.Units[i] = u
		f.Units[i].InstanceID = uint64(i + 1)
	}
	c.placeSurfaceWakes(f)
	if len(c.wakes.units) != wakeTrackerLimit {
		t.Fatal("tracker storage is not bounded")
	}
	f.Tick++
	f.Units = nil
	c.placeSurfaceWakes(f)
	if len(c.wakes.units) != 0 {
		t.Fatal("absent unit trackers retained")
	}
	c.SetSnapshot(frame.NewBuffer())
	if len(c.wakes.marks) != 0 || c.wakes.valid {
		t.Fatal("source replacement retained wake history")
	}
}
