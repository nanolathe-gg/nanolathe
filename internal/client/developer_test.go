package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func developerFixture() *Client {
	c := &Client{width: 200, height: 140, indexed: make([]byte, 200*140), pal: &palette.Tables{}, cam: &camera.Camera{ViewW: 200, ViewH: 140, MapW: 256, MapH: 256}}
	for i := range c.pal.Logical {
		c.pal.Logical[i] = byte(i + 32)
	}
	return c
}

func TestDeveloperContourBoundsOffsetAndTruncation(t *testing.T) {
	c := developerFixture()
	clip := c.developerClip()
	q := [4]Point{{130, 40}, {162, 40}, {162, 72}, {130, 72}}
	h := [4]int32{0, 32, 32, 0}
	c.developer.ContourSpacing = 16 * 256
	c.developerContours(q, h, 0, clip)
	c.replayForTest()
	// Middle equality belongs to the lower edge; minimum equality is excluded.
	if c.indexed[50*c.width+146] != 87 || c.indexed[50*c.width+130] != 0 {
		t.Fatal("contour middle/bottom inclusion changed")
	}
	if c.indexed[50*c.width+162] != 86 {
		t.Fatal("top equality contour missing")
	}
	clear(c.indexed)
	c.list.Reset()
	c.developer.ContourOffset = -24 * 256
	c.developerContours(q, h, 0, clip)
	c.replayForTest()
	// Start is trunc(top/spacing)*spacing+offset; it is never normalized upward.
	if c.indexed[50*c.width+154] != 0 || c.indexed[44*c.width+138] != 88 {
		t.Fatal("negative offset was normalized upward")
	}
	if got := contourIntersection(contourVertex{Point{-2, -5}, 0}, contourVertex{Point{1, 2}, 3}, 1); got != (Point{-1, -2}) {
		t.Fatalf("signed weighted truncation: %v", got)
	}
}

func TestDeveloperCellPaletteAndOrdering(t *testing.T) {
	c := developerFixture()
	clip := c.developerClip()
	q := [4]Point{{130, 40}, {146, 40}, {146, 56}, {130, 56}}
	d := &frame.DeveloperView{Width: 2, Height: 2, SeaLevel: 20}
	v := frame.DeveloperCell{Height: 20, Feature: world.PlotFeatureNone}
	c.developer.Mode = 2
	c.developerCell(d, 0, 0, q, v, clip)
	c.replayForTest()
	if c.indexed[40*c.width+133] != c.paletteIndex(13) {
		t.Fatal("sea equality must use logical 13")
	}
	clear(c.indexed)
	c.list.Reset()
	v.Ground = 259
	v.Air = 261
	v.Building = true
	c.developerCell(d, 0, 0, q, v, clip)
	c.replayForTest()
	if c.indexed[40*c.width+133] != 3 {
		t.Fatal("ground raw fill must overwrite grid")
	}
	if c.indexed[48*c.width+138] != 5 {
		t.Fatal("air raw diagonals must overwrite ground")
	}
	if c.indexed[42*c.width+138] != c.paletteIndex(15) {
		t.Fatal("building diamond must use mapped entry 15")
	}
	clear(c.indexed)
	c.list.Reset()
	v.Ground = 0
	v.Air = 0
	v.Building = false
	v.Feature = 2
	c.developerCell(d, 0, 0, q, v, clip)
	c.replayForTest()
	if c.indexed[44*c.width+135] != 202 {
		t.Fatal("special feature word must take raw modulo fill")
	}
	clear(c.indexed)
	c.list.Reset()
	c.developer.Mode = 4
	d.CoverageWidth = 1
	d.CoverageHeight = 1
	d.Coverage = []byte{1}
	c.developerCell(d, 0, 0, q, v, clip)
	c.replayForTest()
	if c.indexed[35*c.width+128] != c.paletteIndex(15) || c.indexed[35*c.width+127] != 0 || c.indexed[45*c.width+135] != c.paletteIndex(15) || c.indexed[46*c.width+135] != 0 {
		t.Fatal("coverage square or viewport clipping changed")
	}
}

func TestDeveloperSearchAndMetal(t *testing.T) {
	c := developerFixture()
	clip := c.developerClip()
	q := [4]Point{{130, 40}, {146, 40}, {146, 56}, {130, 56}}
	c.SetDeveloperFont(&formats.FNT{})
	d := &frame.DeveloperView{Tick: 1, Width: 2, Height: 2, MovementTiers: []byte{2}, SearchWidth: 1, SearchHeight: 1, Search: []frame.DeveloperSearchCell{{Status: 5, Direction: 0}}}
	c.developer.Mode = 1
	c.developerCell(d, 0, 0, q, frame.DeveloperCell{}, clip)
	spy := &developerSink{}
	c.list.Replay(spy)
	if len(spy.lines) != 2 || spy.lines[0].Index != c.paletteIndex(10) {
		t.Fatal("tier-two cross or unknown status policy changed")
	}
	if len(spy.glyphs) != 1 || spy.glyphs[0].Text != "G" || spy.glyphs[0].Color != 33 {
		t.Fatal("deterministic raw goal color missing")
	}
	c.list.Reset()
	d.MovementTiers = nil
	d.Search[0].Status = 1
	c.developerCell(d, 0, 0, q, frame.DeveloperCell{}, clip)
	spy = &developerSink{}
	c.list.Replay(spy)
	if len(spy.lines) != 3 || spy.lines[0] != (drawlist.Line{X0: 138, Y0: 62, X1: 138, Y1: 48, Index: c.paletteIndex(15)}) {
		t.Fatalf("arrow geometry: %+v", spy.lines)
	}
	c.list.Reset()
	c.developer.Mode = 3
	c.developerCell(d, 0, 0, q, frame.DeveloperCell{Metal: 255}, clip)
	spy = &developerSink{}
	c.list.Replay(spy)
	if len(spy.glyphs) != 1 || spy.glyphs[0].Text != "255" || spy.glyphs[0].X != 132 || spy.glyphs[0].Y != 42 || spy.glyphs[0].Color != c.paletteIndex(15) {
		t.Fatal("metal byte formatting changed")
	}
}

type developerSink struct {
	drawlist.Sink
	lines  []drawlist.Line
	glyphs []drawlist.Glyphs
}

func (s *developerSink) Line(l drawlist.Line)     { s.lines = append(s.lines, l) }
func (s *developerSink) Glyphs(g drawlist.Glyphs) { s.glyphs = append(s.glyphs, g) }

func TestDeveloperPausedInvalidationAndUnitTint(t *testing.T) {
	c := pausedClient(t)
	before, _ := c.PausedWorldDigest()
	pre := c.PresentationDigest()
	c.SetDeveloperOptions(DeveloperOptions{Mode: 1})
	after, _ := c.PausedWorldDigest()
	if before == after || pre == c.PresentationDigest() {
		t.Fatal("developer option reused paused world or prerecord")
	}
	pre = c.PresentationDigest()
	c.SetDeveloperOptions(DeveloperOptions{Mode: 1})
	if pre != c.PresentationDigest() {
		t.Fatal("unchanged options invalidate every frame")
	}
	c, v := cachedLiveRegressionSubject(t)
	c.pal = &palette.Tables{}
	c.pal.Alpha[99*256] = 42
	cachedLiveReplay(t, c, v)
	body := c.cachedModelBodies[v.InstanceID]
	for mode := uint8(1); mode <= 4; mode++ {
		c.SetDeveloperOptions(DeveloperOptions{Mode: mode})
		cachedLiveReplay(t, c, v)
		if c.cachedModelBodies[v.InstanceID] != body || bytes.Count(c.indexed, []byte{42}) == 0 {
			t.Fatalf("mode %d failed tinted retained image commit; cached=%v tinted=%d", mode, c.cachedModelBodies[v.InstanceID] == body, bytes.Count(c.indexed, []byte{42}))
		}
	}
	c.SetDeveloperOptions(DeveloperOptions{})
	cachedLiveReplay(t, c, v)
	if bytes.Count(c.indexed, []byte{99}) == 0 {
		t.Fatal("mode zero did not restore ordinary body")
	}
	modern, mv := cachedLiveRegressionSubject(t)
	modern.geometryOnlyModels = true
	var key drawlist.ModelCacheKey
	for mode := uint8(0); mode <= 4; mode++ {
		modern.SetDeveloperOptions(DeveloperOptions{Mode: mode})
		g := recordKeyedSubject(t, modern, mv)
		if g.Cloaked != (mode != 0) {
			t.Fatal("modern packet omitted developer tint")
		}
		if mode == 0 {
			key = g.Cache
		} else if key != g.Cache {
			t.Fatal("developer tint invalidated immutable geometry")
		}
	}

}

func TestDeveloperMovementStopsAtFirstSelectedAndUsesCommittedFootprint(t *testing.T) {
	c := developerFixture()
	c.developer.Information = true
	f := &frame.Frame{Units: []frame.UnitView{{Slot: 1, InstanceID: 1, Flags: hud.SelectionFlag, FootX: 2, FootZ: 1}, {Slot: 2, InstanceID: 2, Flags: hud.SelectionFlag}}, Developer: &frame.DeveloperView{Units: []frame.DeveloperUnit{{Slot: 1, InstanceID: 1}, {Slot: 2, InstanceID: 2, MovementAvailable: true}}}}
	c.drawDeveloperMovement(f)
	spy := &developerSink{}
	c.list.Replay(spy)
	if len(spy.lines) != 0 {
		t.Fatal("missing first follower must not fall through")
	}
	f.Developer.Width = 16
	f.Developer.Height = 16
	f.Developer.Cells = make([]frame.DeveloperCell, 256)
	d := &f.Developer.Units[0]
	d.MovementAvailable = true
	d.FootprintX = 9
	d.FootprintZ = 3
	d.HasWaypoint = true
	d.Route = []frame.DeveloperRoutePoint{{X: 144, Z: 48}, {X: 160, Z: 64}}
	c.drawDeveloperMovement(f)
	spy = &developerSink{}
	c.list.Replay(spy)
	if len(spy.lines) != 5 || spy.lines[0] != (drawlist.Line{X0: 144, Y0: 48, X1: 176, Y1: 48, Index: c.paletteIndex(15)}) || spy.lines[4].Index != c.paletteIndex(9) {
		t.Fatalf("footprint/route projection: %+v", spy.lines)
	}
}

func TestDeveloperClippedLinePreservesRasterAndSmoothZoom(t *testing.T) {
	c := developerFixture()
	a, b := Point{110, 35}, Point{160, 71}
	clip := c.developerClip()
	c.drawIndexedLine(a.X, a.Y, b.X, b.Y, 77)
	want := append([]byte(nil), c.indexed...)
	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			if int32(x) < clip.X || int32(x) >= clip.X+clip.W || int32(y) < clip.Y || int32(y) >= clip.Y+clip.H {
				want[y*c.width+x] = 0
			}
		}
	}
	clear(c.indexed)
	c.developerLine(a, b, 77, clip)
	c.replayForTest()
	if !bytes.Equal(want, c.indexed) {
		t.Fatal("clipping changed Bresenham phase")
	}
	c.cam.Zoom = camera.ZoomUnit * 3 / 4
	got := c.developerClip()
	if got.X != 170 || got.Y != 42 || got.W != 97 || got.H != 102 {
		t.Fatalf("smooth zoom clip: %+v", got)
	}
}

func TestDeveloperPassOrderAndModeZeroContours(t *testing.T) {
	c := newTestClient(t)
	c.cam.X = -128
	c.cam.Z = -32
	c.terrain = &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	f := c.buffer.BeginWrite()
	f.Developer = &frame.DeveloperView{Width: 4, Height: 4, Cells: make([]frame.DeveloperCell, 16), Units: []frame.DeveloperUnit{{Slot: 1, InstanceID: 1, MovementAvailable: true}}}
	for i := range f.Developer.Cells {
		f.Developer.Cells[i] = frame.DeveloperCell{Height: uint8((i % 4) * 16), Feature: world.PlotFeatureNone}
	}
	f.Units = []frame.UnitView{{Slot: 1, InstanceID: 1, Flags: hud.SelectionFlag, FootX: 1, FootZ: 1}}
	f.Fog = frame.FogView{Valid: true, W: 2, H: 2, Ch0: make([]byte, 4), Ch1: make([]byte, 4)}
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	c.SetDeveloperOptions(DeveloperOptions{Information: true, ContourSpacing: 16 * 256})
	c.recordFrameNoAudio()
	spy := &scaleCapture{}
	c.list.Replay(spy)
	terrain, firstLine, lastLine, fog := -1, -1, -1, -1
	for i, k := range spy.family {
		switch k {
		case "terrain":
			terrain = i
		case "line", "points":
			if firstLine < 0 {
				firstLine = i
			}
			lastLine = i
		case "fog":
			fog = i
		}
	}
	if terrain < 0 || firstLine <= terrain || fog <= lastLine || len(spy.lines) < 5 {
		t.Fatalf("contours/route order: %v", spy.family)
	}
}

func TestDeveloperDetachedHeightTruncatesEachAxis(t *testing.T) {
	d := &frame.DeveloperView{Width: 2, Height: 2, Cells: []frame.DeveloperCell{{Height: 16}, {Height: 0}, {Height: 1}, {Height: 33}}}
	if h, ok := developerGroundHeight(d, 1, 1); !ok || h != 15 {
		t.Fatalf("two-stage signed height: %d %v", h, ok)
	}
	if h, ok := developerGroundHeight(d, 16, 0); !ok || h != -1 {
		t.Fatal("edge query lost sentinel")
	}
	if _, ok := developerGroundHeight(nil, 0, 0); ok {
		t.Fatal("missing observation became zero height")
	}
}

func TestDeveloperPickCrossUsesDetachedHeightAndRingInk(t *testing.T) {
	c := developerFixture()
	d := &frame.DeveloperView{Width: 12, Height: 8, Cells: make([]frame.DeveloperCell, 96)}
	for i := range d.Cells {
		d.Cells[i] = frame.DeveloperCell{Height: 40, Feature: world.PlotFeatureNone}
	}
	c.SetDeveloperOptions(DeveloperOptions{Mode: 2, PickValid: true, PickX: 145, PickZ: 81})
	c.drawDeveloperTerrain(&frame.Frame{Developer: d})
	spy := &scaleCapture{}
	c.list.Replay(spy)
	lines := spy.lines[len(spy.lines)-2:]
	if lines[0] != (drawlist.Line{X0: 143, Y0: 61, X1: 147, Y1: 61, Index: c.paletteIndex(15)}) || lines[1] != (drawlist.Line{X0: 145, Y0: 59, X1: 145, Y1: 63, Index: c.paletteIndex(15)}) {
		t.Fatalf("five-pixel detached pick cross: %v", lines)
	}
}
