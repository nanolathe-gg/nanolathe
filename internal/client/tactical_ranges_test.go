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

// Enhanced guide geometry preserves the icon projection at live zoom and pan,
// while the terrain-height floor is the established [07 R-P0-11 §3] contract.
func TestStrategicRangeProjectionAndTerrain(t *testing.T) {
	c, _ := iconLayoutFixture(t)
	c.terrain = &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	for i := range c.terrain.Plot {
		c.terrain.Plot[i].SetHeight(100)
	}
	for _, zoom := range []camera.Zoom{camera.ZoomUnit * 3 / 8, camera.ZoomUnit / 2, camera.ZoomUnit, camera.ZoomUnit * 3 / 2, camera.ZoomUnit * 2} {
		for _, centerY := range []int32{80, 120} {
			c.cam.Zoom = zoom
			c.cam.X = 800 - int32(int64(360)*int64(camera.ZoomUnit)/int64(zoom))
			c.cam.Z = 600 - int32(int64(240)*int64(camera.ZoomUnit)/int64(zoom))
			c.list.Reset()
			c.DrawTacticalRange(numeric.FixedFromInt(800), numeric.FixedFromInt(int64(centerY)), numeric.FixedFromInt(650), 80, 19, false)
			lines := recordedLines(&c.list)
			if len(lines) == 0 || len(lines) > 512 {
				t.Fatalf("unbounded or missing guide: %d", len(lines))
			}
			minX, minY, maxX, maxY := int32(math.MaxInt32), int32(math.MaxInt32), int32(math.MinInt32), int32(math.MinInt32)
			for _, line := range lines {
				if line.Index != 19 || line.Emissive {
					t.Fatal("range ink changed or became a glow source")
				}
				minX, minY = min(minX, line.X0, line.X1), min(minY, line.Y0, line.Y1)
				maxX, maxY = max(maxX, line.X0, line.X1), max(maxY, line.Y0, line.Y1)
			}
			groundY := max(centerY, 100)
			if minX != zoom.Project(800-80-c.cam.X) || maxX != zoom.Project(800+80-c.cam.X) || minY != zoom.Project(650-80-groundY/2-c.cam.Z) || maxY != zoom.Project(650+80-groundY/2-c.cam.Z) {
				t.Fatalf("zoom %v height %d: bounds (%d,%d)..(%d,%d) disagree with icon projection", zoom, centerY, minX, minY, maxX, maxY)
			}
		}
	}
}

func TestStrategicRangeClippingAndBoundedWork(t *testing.T) {
	c, _ := iconLayoutFixture(t)
	clip := c.battleViewportRect()
	for _, radius := range []int32{1, 300, 2000, math.MaxInt32} {
		for _, dashed := range []bool{false, true} {
			c.list.Reset()
			c.DrawTacticalRange(numeric.FixedFromInt(600), 0, numeric.FixedFromInt(400), radius, 3, dashed)
			lines := recordedLines(&c.list)
			if len(lines) > 512 || (dashed && len(lines) > 256) {
				t.Fatal("range escaped bounded tessellation")
			}
			for _, line := range lines {
				for _, p := range [2][2]int32{{line.X0, line.Y0}, {line.X1, line.Y1}} {
					if p[0] < clip.X || p[0] >= clip.X+clip.W || p[1] < clip.Y || p[1] >= clip.Y+clip.H {
						t.Fatalf("range escaped viewport: %+v", line)
					}
				}
			}
		}
	}
	// A huge segment crossing the viewport is clipped before int32 narrowing.
	line, ok := clipStrategicRange(-1<<34, 200, 1<<34, 200, clip)
	if !ok || line.X0 != clip.X || line.X1 != clip.X+clip.W-1 || line.Y0 != 200 || line.Y1 != 200 {
		t.Fatalf("wide clipping: %+v, %v", line, ok)
	}
	c.list.Reset()
	c.DrawTacticalRange(numeric.FixedFromInt(65536+600), 0, numeric.FixedFromInt(400), 65536, 3, false)
	if len(recordedLines(&c.list)) == 0 {
		t.Fatal("long range wrapped to zero instead of crossing the viewport")
	}
	c.list.Reset()
	c.DrawTacticalRange(0, 0, 0, -1, 3, false)
	c.DrawTacticalRange(0, 0, 0, 0, 3, false)
	c.DrawTacticalRange(numeric.Fixed(math.MaxInt64), 0, 0, 40, 3, false)
	if len(recordedLines(&c.list)) != 0 {
		t.Fatal("invalid range geometry was recorded")
	}
}

func TestVisitTacticalUnitsUsesCurrentIdentifiedIcons(t *testing.T) {
	c, f := iconLayoutFixture(t)
	visible := f.Units[0]
	hidden := visible
	hidden.Slot, hidden.Owner, hidden.Cloaked = 8, 1, true
	offscreen := visible
	offscreen.Slot, offscreen.X = 9, numeric.FixedFromInt(3000)
	f.Units = append(f.Units, hidden, offscreen)
	contact := f.Radar.Contacts[0]
	contact.Handle, contact.Owner = hidden.Slot, hidden.Owner
	f.Radar.Contacts = append(f.Radar.Contacts, contact)
	buffer := frame.NewBuffer()
	*buffer.BeginWrite() = *f
	if err := buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	c.SetSnapshot(buffer)
	// Deliberately stale incoming identities cannot bypass committed admission.
	stale := &frame.Frame{Units: []frame.UnitView{visible, visible, visible}}
	for _, zoom := range []camera.Zoom{camera.ZoomUnit / 2, camera.ZoomUnit, camera.ZoomUnit * 3 / 2, camera.ZoomUnit * 2} {
		c.cam.Zoom = zoom
		c.cam.X = 600 - int32(int64(300)*int64(camera.ZoomUnit)/int64(zoom))
		c.cam.Z = 400 - int32(int64(200)*int64(camera.ZoomUnit)/int64(zoom))
		if zoom >= camera.ZoomUnit {
			c.SetStrategicIconCatalog(nil)
		}
		seen := 0
		c.VisitTacticalUnits(stale, func(u frame.UnitView) {
			seen++
			if u.Slot != visible.Slot {
				t.Fatal("hidden, radar-only, or off-screen unit exposed")
			}
		})
		if seen != 1 {
			t.Fatalf("zoom %v: wanted one identified unit, visited %d", zoom, seen)
		}
	}
	c.SetEnhanced(false)
	c.VisitTacticalUnits(f, func(frame.UnitView) { t.Fatal("classic mode exposed strategic targets") })
}

type tacticalOverlayFixture struct {
	frame *frame.Frame
	calls int
	held  bool
}

func (s *tacticalOverlayFixture) DrawUI(*Client, UIFrame) {}
func (s *tacticalOverlayFixture) DrawTacticalOverlay(c *Client, f *frame.Frame) {
	s.frame, s.calls = f, s.calls+1
	if s.held {
		c.DrawTacticalRange(numeric.FixedFromInt(600), 0, numeric.FixedFromInt(400), 80, 3, false)
	}
}

type tacticalOrderTrace struct {
	regionTrace
	seenMarker, lineAfterMarker bool
}

func (s *tacticalOrderTrace) Markers(drawlist.Markers) { s.seenMarker = true }
func (s *tacticalOrderTrace) Line(drawlist.Line) {
	if s.seenMarker {
		s.lineAfterMarker = true
	}
}

func TestStrategicOverlayCurrentFrameOrderingAndRelease(t *testing.T) {
	c, f := iconLayoutFixture(t)
	buffer := frame.NewBuffer()
	*buffer.BeginWrite() = *f
	if err := buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	c.SetSnapshot(buffer)
	stage := &tacticalOverlayFixture{held: true}
	c.SetUIStage(stage)
	c.drawCommittedForeground(&frame.Frame{})
	if stage.frame != buffer.Current() || stage.calls != 1 || len(recordedLines(&c.list)) == 0 {
		t.Fatal("overlay missed current committed frame")
	}
	trace := &tacticalOrderTrace{}
	c.list.Replay(trace)
	if !trace.seenMarker || trace.lineAfterMarker {
		t.Fatal("guides did not precede icons")
	}
	stage.held = false
	c.list.Reset()
	c.drawCommittedForeground(f)
	if stage.calls != 2 || len(recordedLines(&c.list)) != 0 {
		t.Fatal("released overlay retained guide records")
	}
	c.cam.Zoom = camera.ZoomUnit
	c.drawCommittedForeground(f)
	if stage.calls != 3 {
		t.Fatal("normal zoom omitted tactical overlay")
	}
	c.cam.Zoom = strategicModelCut
	c.SetEnhanced(false)
	c.drawCommittedForeground(f)
	if stage.calls != 3 {
		t.Fatal("classic mode invoked tactical overlay")
	}
}
