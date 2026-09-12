package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Fixtures lock the authored Enhanced history rules, not retail decals (§29).
func scorchScene(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c := stripTestClient(t)
	c.enhanced = true
	c.SetTerrain(&world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)})
	c.effectBanks["fx"] = &formats.GAF{Entries: []formats.GAFEntry{{Name: "blast", Frames: []formats.GAFFrameRef{
		{Frame: &formats.GAFFrame{Width: 4, Height: 4}},
		{Frame: &formats.GAFFrame{Width: 80, Height: 64}},
	}}}}
	f := stripTestFrame(true)
	f.Tick = 10
	f.Effects = []frame.EffectView{{ID: 1, StartTick: f.Tick, Kind: frame.KindImpact.String(), ActiveA: true,
		Graphic: "blast", AssetID: "fx", X: 32 * numeric.FixedOne, Z: 32 * numeric.FixedOne}}
	return c, f
}

type scorchCollector struct {
	trailCollector
	marks []drawlist.ScorchMark
}

func (s *scorchCollector) ScorchMarks(v drawlist.ScorchMarks) {
	s.marks = append(s.marks, v.Marks...)
}

func recordScorch(c *Client, f *frame.Frame) []drawlist.ScorchMark {
	c.list.Reset()
	c.drawScorchMarks(f)
	s := &scorchCollector{}
	c.list.Replay(s)
	return s.marks
}

func TestScorchAdmissionAndNoLateResurrection(t *testing.T) {
	for _, reason := range []string{"visible", "hidden", "missing visibility", "missing art", "finished primary", "flash only", "smoke", "airburst", "underground", "old", "missing identity", "invalid terrain", "missing terrain grid", "classic"} {
		t.Run(reason, func(t *testing.T) {
			c, f := scorchScene(t)
			saved := f.Effects[0]
			switch reason {
			case "hidden":
				clear(f.Visibility.Visible)
			case "missing visibility":
				f.Visibility.Valid = false
			case "missing art":
				f.Effects[0].Graphic = "absent"
			case "finished primary":
				f.Effects[0].ActiveA = false
			case "flash only":
				f.Effects[0].ActiveA = false
				f.Effects[0].ActiveB, f.Effects[0].HasCalculatedFlash = true, true
			case "smoke":
				f.Effects[0].Kind = frame.KindSmokeStart.String()
			case "airburst":
				f.Effects[0].Y = scorchHeightTolerance + 1
			case "underground":
				f.Effects[0].Y = -scorchHeightTolerance - 1
			case "old":
				f.Effects[0].StartTick--
			case "missing identity":
				f.Effects[0].ID = 0
			case "invalid terrain":
				f.Effects[0].X = -1
			case "classic":
				c.enhanced = false
			case "missing terrain grid":
				c.terrain.Plot = nil
			}
			c.observeScorchMarks(f)
			want := 0
			if reason == "visible" {
				want = 1
			}
			if got := len(recordScorch(c, f)); got != want {
				t.Fatalf("admitted %d marks, want %d", got, want)
			}
			// Even restoring all missing prerequisites cannot create an old mark.
			f.Tick++
			f.Effects[0] = saved
			f.Visibility = stripTestFrame(true).Visibility
			c.terrain.Plot = make([]world.PlotCell, 64)
			if got := len(recordScorch(c, f)); got != want {
				t.Fatalf("late eligibility changed history to %d", got)
			}
		})
	}
}

func TestScorchDryWaterAndHeightBoundary(t *testing.T) {
	for _, medium := range []string{"dry", "water", "lava", "acid", "inactive acid", "water airburst"} {
		t.Run(medium, func(t *testing.T) {
			c, f := scorchScene(t)
			if medium != "dry" {
				c.terrain.SeaLevel = 20
				f.Effects[0].Kind = frame.KindWaterImpact.String()
				f.Effects[0].Y = 20 * numeric.FixedOne
			}
			switch medium {
			case "dry":
				f.Effects[0].Y = scorchHeightTolerance
			case "lava":
				c.terrain.LavaWorld = true
			case "acid":
				c.terrain.WaterDoesDamage, c.terrain.WaterDamage = 1, 5
			case "inactive acid":
				c.terrain.WaterDamage = 5
			case "water airburst":
				f.Effects[0].Y += scorchHeightTolerance + 1
			}
			marks := recordScorch(c, f)
			if medium != "dry" {
				if len(marks) != 0 {
					t.Fatal("excluded medium or height admitted a mark")
				}
				return
			}
			if len(marks) != 1 || marks[0].Radius != 44 {
				t.Fatalf("surface classification = %+v", marks)
			}
			wantY := float32(32)
			if marks[0].Y != wantY {
				t.Fatalf("anchor Y = %v, want sampled surface %v", marks[0].Y, wantY)
			}
		})
	}
}

func TestScorchRequiresVisibleSurfaceAnchor(t *testing.T) {
	c, f := scorchScene(t)
	f.Effects[0].Y = scorchHeightTolerance
	// The elevated event projects into row zero; its dry anchor projects into
	// row one. Seeing the event must not expose hidden ground at the fog edge.
	f.Visibility.Visible[5] = 0
	if !visibleExplosionSource(f.Effects[0], f) {
		t.Fatal("fixture hid the elevated effect")
	}
	if len(recordScorch(c, f)) != 0 {
		t.Fatal("visible event revealed its hidden surface anchor")
	}
}

func TestScorchQueuedAndDirectIdentityDoNotAlias(t *testing.T) {
	c, f := scorchScene(t)
	f.Effects = append(f.Effects, f.Effects[0])
	f.Effects[1].EventSeq = 7
	if len(recordScorch(c, f)) != 2 {
		t.Fatal("queued cue ID aliased a direct admission")
	}
}

func TestScorchClipsAgainstDryEnvelope(t *testing.T) {
	c, f := scorchScene(t)
	c.observeScorchMarks(f)
	for _, cameraX := range []int32{72, -72} {
		c.cam.X = cameraX
		// The anchor is 40 pixels outside either viewport edge, within the
		// approved 44-pixel scorch radius.
		marks := recordScorch(c, f)
		if len(marks) != 1 || marks[0].Radius != 44 || marks[0].Age != 0 {
			t.Fatalf("intersecting accent at camera X %d was lost or changed: %+v", cameraX, marks)
		}
	}
	for _, cameraX := range []int32{80, -80} {
		c.cam.X = cameraX
		if len(recordScorch(c, f)) != 0 {
			t.Fatalf("accent outside its conservative envelope survived culling at camera X %d", cameraX)
		}
	}
}

func TestScorchDedupCatchUpAgeAndSourceReset(t *testing.T) {
	c, f := scorchScene(t)
	f.Effects = append(f.Effects, f.Effects[0])
	c.observeScorchMarks(f)
	c.observeScorchMarks(f)
	if c.scorch.count != 1 {
		t.Fatal("duplicate records or repeated publication duplicated an impact")
	}
	// Two further committed ticks arrive before the next draw. The first event
	// disappears, but its independently owned mark survives the catch-up pump.
	f.Tick++
	f.Effects = f.Effects[:1]
	f.Effects[0].ID, f.Effects[0].StartTick = 2, f.Tick
	c.observeScorchMarks(f)
	f.Tick++
	f.Effects = nil
	c.observeScorchMarks(f)
	c.interpolation = true
	c.SetTickFraction(0.5)
	c.cam.Scale = camera.ViewScaleDetail
	marks := recordScorch(c, f)
	if len(marks) != 2 || marks[0].Age != 2.5 || marks[1].Age != 1.5 || marks[0].Radius != 88 || marks[0].X != 64 {
		t.Fatalf("catch-up age/art extent/record scale = %+v", marks)
	}
	if again := recordScorch(c, f); len(again) != len(marks) || again[0] != marks[0] {
		t.Fatal("paused repeated draw changed history")
	}
	c.SetTerrain(&world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)})
	if len(recordScorch(c, f)) != 0 {
		t.Fatal("terrain source change retained impact history")
	}
}

func TestScorchRewindViewerAndBoundedHistory(t *testing.T) {
	for _, reset := range []string{"rewind", "viewer"} {
		t.Run(reset, func(t *testing.T) {
			c, f := scorchScene(t)
			c.observeScorchMarks(f)
			f.Effects = nil
			if reset == "rewind" {
				f.Tick--
			} else {
				f.ViewingPlayer++
			}
			if len(recordScorch(c, f)) != 0 {
				t.Fatal("old history crossed its age/viewer domain")
			}
		})
	}
	c, f := scorchScene(t)
	for n := uint32(1); n <= drawlist.ScorchMarkLimit+20; n++ {
		f.Tick++
		f.Effects[0].ID, f.Effects[0].StartTick = n, f.Tick
		c.observeScorchMarks(f)
	}
	marks := recordScorch(c, f)
	if len(marks) != drawlist.ScorchMarkLimit || marks[0].Age != drawlist.ScorchMarkLimit-1 || marks[len(marks)-1].Age != 0 {
		t.Fatal("ring did not evict oldest first and retain stable birth ordering")
	}
	f.Effects = nil
	f.Tick += drawlist.ScorchLifeTicks - 1
	if got := recordScorch(c, f); len(got) != 1 || got[0].Age != drawlist.ScorchLifeTicks-1 {
		t.Fatalf("expiry boundary = %+v", got)
	}
	f.Tick++
	if len(recordScorch(c, f)) != 0 || c.scorch.count != 0 {
		t.Fatal("history survived its maximum lifetime")
	}
}

func TestScorchSameTickFIFOAndTickZero(t *testing.T) {
	c, f := scorchScene(t)
	f.Tick = 0
	first := f.Effects[0]
	first.StartTick = 0
	f.Effects = nil
	for id := uint32(1); id <= drawlist.ScorchMarkLimit+20; id++ {
		v := first
		v.ID = id
		f.Effects = append(f.Effects, v)
	}
	marks := recordScorch(c, f)
	if len(marks) != drawlist.ScorchMarkLimit || marks[0].Variant != 21 || marks[len(marks)-1].Variant != drawlist.ScorchMarkLimit+20 || marks[0].Age != 0 {
		t.Fatal("same-tick births did not evict oldest first, or tick zero was rejected")
	}
}
