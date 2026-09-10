package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// zoomRecorderClient is a client with a camera large enough that the strategic
// gates and the record extent are the only things deciding what is recorded.
func zoomRecorderClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	c.SetCamera(&camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 16384, MapH: 16384})
	return c
}

// The record extent is the framebuffer measured in RECORD pixels: exactly the
// framebuffer at a rest factor — which is what keeps a 1x or 2x frame recording
// the commands it always recorded — and larger below the step
// (DESIGN_GPU_RENDERER §16.3).
func TestRecordExtentIsTheFramebufferAtRestAndWiderBelowIt(t *testing.T) {
	c := zoomRecorderClient(t)
	for _, s := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleMid, camera.ViewScaleDetail} {
		c.cam.Scale, c.cam.Zoom = s, camera.ZoomOf(s)
		c.refreshRecordExtent()
		if w, h := c.recordExtent(); w != c.width || h != c.height {
			t.Fatalf("%s: record extent %dx%d, want the framebuffer %dx%d", s, w, h, c.width, c.height)
		}
	}
	// Half the native factor doubles the world one frame has to cover.
	c.cam.Zoom, c.cam.Scale = camera.ZoomUnit/2, camera.ViewScaleNative
	c.refreshRecordExtent()
	w, h := c.recordExtent()
	if w < 2*c.width || h < 2*c.height {
		t.Fatalf("at 0.5x the record extent is %dx%d, want at least twice the %dx%d framebuffer", w, h, c.width, c.height)
	}
	// A world clip site takes the record extent; an interface site does not.
	if got := c.selectionClip(); got.MaxX != int32(w)-1 || got.MaxY != int32(h)-33 {
		t.Fatalf("the world selection clip is %+v, want the record extent %dx%d", got, w, h)
	}
}

// The world region is bracketed by exactly two boundary markers, and the marker
// carries the factor, the step and the record extent the executor needs
// (§16.3).
func TestOneWorldRegionPerRecordedFrame(t *testing.T) {
	c := zoomRecorderClient(t)
	c.cam.Zoom, c.cam.Scale = camera.ZoomUnit*7/10, camera.ViewScaleNative
	c.drawCommittedFrame(&frame.Frame{}, true)
	spaces := c.list.WorldSpaces()
	if len(spaces) != 2 || !spaces[0].Begin || spaces[1].Begin {
		t.Fatalf("recorded world markers = %+v, want one open and one close", spaces)
	}
	open := spaces[0]
	if open.Zoom != camera.ZoomUnit*7/10 || open.Step != camera.ViewScaleNative {
		t.Fatalf("the open marker carries %s at %s", open.Zoom, open.Step)
	}
	if open.Identity() {
		t.Fatal("0.7x is reported as an identity region")
	}
	if int(open.RecordW) != c.recordW || int(open.RecordH) != c.recordH {
		t.Fatalf("the open marker carries extent %dx%d, want %dx%d", open.RecordW, open.RecordH, c.recordW, c.recordH)
	}
	// The viewport it carries is the chrome's, in framebuffer pixels, and does
	// not move with the factor.
	if open.Viewport != (drawlist.Rect{X: camera.OriginX, Y: camera.OriginY, W: 640 - camera.OriginX, H: 480 - 2*camera.OriginY}) {
		t.Fatalf("the open marker's viewport is %+v", open.Viewport)
	}
}

// Below the strategic cut the recorder emits no model, effect, projectile,
// sprite-feature or unit-label command; terrain, fog, the selection fills and
// the drag rectangle still record (§16.10).
func TestStrategicViewDropsTheUnitLayers(t *testing.T) {
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units: []frame.UnitView{
			{Slot: 1, Owner: 0, X: numeric.FixedFromInt(320), Z: numeric.FixedFromInt(240),
				Model: "zoom_marker", Flags: hud.SelectionFlag, MoverMode: 1},
		},
		Projectiles: []frame.ProjectileView{{X: numeric.FixedFromInt(300), Z: numeric.FixedFromInt(200)}},
	}

	full := zoomRecorderClient(t)
	full.models["zoom_marker"] = syntheticModel(
		[]pieceInfo{{name: "root", parent: -1}},
		[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{0, 0, 0}, {8, 0, 0}, {0, 0, 8}}, 17, 0)},
		0,
	)
	full.drawCommittedFrame(cur, true)
	if len(full.list.ModelCommands()) == 0 {
		t.Fatal("the ordinary view recorded no model; the fixture proves nothing")
	}
	fullLines := len(recordedLines(&full.list))

	strategic := zoomRecorderClient(t)
	strategic.models["zoom_marker"] = full.models["zoom_marker"]
	strategic.cam.Zoom, strategic.cam.Scale = strategicModelCut-1, camera.ViewScaleNative
	strategic.drawCommittedFrame(cur, true)
	if got := len(strategic.list.ModelCommands()); got != 0 {
		t.Fatalf("the strategic view recorded %d model commands, want none", got)
	}
	if !strategic.strategicView() {
		t.Fatal("the fixture factor is not below the strategic cut")
	}
	// The selection quad is a world FILL and survives: it is drawn as four
	// lines, exactly as it is at every other factor.
	if got := len(recordedLines(&strategic.list)); got != fullLines {
		t.Fatalf("the strategic view recorded %d selection lines, want the ordinary view's %d", got, fullLines)
	}
}

// The marker layer replaces the models: one marker per admitted contact, fading
// in linearly from nothing at 0.625x to full at 0.5x (§16.11).
func TestStrategicMarkerCountAndAlphaRamp(t *testing.T) {
	c := zoomRecorderClient(t)
	c.SetStrategicBlipArt(testBlipArt())
	cur := &frame.Frame{
		ViewingPlayer: 0,
		Radar: frame.RadarView{Contacts: []frame.RadarContactView{
			{Kind: frame.RadarContactUnit, Owner: 0, PaletteKnown: true, Palette: 0, Visible: true,
				X: numeric.FixedFromInt(600), Z: numeric.FixedFromInt(400), Selected: true},
			{Kind: frame.RadarContactUnit, Owner: 0, PaletteKnown: true, Palette: 1, Visible: true,
				X: numeric.FixedFromInt(760), Z: numeric.FixedFromInt(520)},
			// A projectile contact is never a marker.
			{Kind: frame.RadarContactProjectile, Owner: 0, PaletteKnown: true, Visible: true,
				X: numeric.FixedFromInt(680), Z: numeric.FixedFromInt(460)},
		}},
	}

	for _, tc := range []struct {
		zoom  camera.Zoom
		alpha uint8
		marks int
	}{
		// 0.625x, 0.5625x and 0.5x: the two ends of the ramp and its midpoint.
		{strategicMarkerOn, 0, 0},
		{(strategicMarkerOn + strategicModelCut) / 2, 128, 2},
		{strategicModelCut, 255, 2},
	} {
		c.cam.Zoom, c.cam.Scale = tc.zoom, tc.zoom.Step()
		c.list.Reset()
		c.refreshRecordExtent()
		if got := c.markerAlpha(); got != tc.alpha {
			t.Errorf("%s: marker alpha %d, want %d", tc.zoom, got, tc.alpha)
		}
		c.drawStrategicMarkers(cur)
		batches := c.list.MarkerBatches()
		got := 0
		for _, b := range batches {
			got += len(b.Marks)
		}
		if got != tc.marks {
			t.Errorf("%s: recorded %d markers, want %d", tc.zoom, got, tc.marks)
		}
		if tc.marks == 0 {
			continue
		}
		m := batches[0].Marks[0]
		if m.Size != strategicMarkerSize || m.Alpha != tc.alpha || !m.Selected {
			t.Errorf("%s: first marker %+v", tc.zoom, m)
		}
		// The marker is positioned through the LIVE factor, in framebuffer
		// pixels, not through the record step.
		wantX := tc.zoom.Project(600 - c.cam.X)
		if m.X != wantX {
			t.Errorf("%s: marker X %d, want %d", tc.zoom, m.X, wantX)
		}
	}
}

// A contact the minimap would not draw a dot for has no marker either: the gate
// is the minimap's own, not a second one (§16.11).
func TestStrategicMarkersReuseTheMinimapGate(t *testing.T) {
	c := zoomRecorderClient(t)
	c.SetStrategicBlipArt(testBlipArt())
	c.cam.Zoom, c.cam.Scale = strategicModelCut, camera.ViewScaleNative
	c.refreshRecordExtent()
	hidden := frame.RadarContactView{
		Kind: frame.RadarContactUnit, Owner: 3, PaletteKnown: true, Visible: false,
		X: numeric.FixedFromInt(320), Z: numeric.FixedFromInt(240),
	}
	cur := &frame.Frame{ViewingPlayer: 0, Radar: frame.RadarView{
		MappingLOS: 3, // mapping and LOS both set, so the "both clear" disjunct fails
		Contacts:   []frame.RadarContactView{hidden},
	}}
	if render.MinimapContactGate(render.MinimapContact{
		Owner: hidden.Owner, Status: hidden.Status, Visible: hidden.Visible,
		LocalPlayer: 0, MinimapMode: 3,
	}) {
		t.Fatal("the fixture contact passes the minimap gate; it proves nothing")
	}
	c.drawStrategicMarkers(cur)
	if n := len(c.list.MarkerBatches()); n != 0 {
		t.Fatalf("a contact the minimap rejects produced %d marker batches", n)
	}
}

// testBlipArt is a two-frame stand-in for `radlogo`: one solid frame per player
// colour, so the marker colour resolves to that frame's own dominant index.
func testBlipArt() *formats.GAFEntry {
	frameFor := func(index uint8) *formats.GAFFrame {
		pixels := make([]byte, 16)
		for i := range pixels {
			pixels[i] = index
		}
		trans := make([]bool, len(pixels))
		return &formats.GAFFrame{Width: 4, Height: 4, Pixels: pixels, Transparent: trans, ColorKey: 9}
	}
	return &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Frame: frameFor(11)}, {Frame: frameFor(22)},
	}}
}

// recordedLines counts the recorded line commands, which is how the selection
// quad shows up in a list.
func recordedLines(l *drawlist.List) []drawlist.Line {
	var out []drawlist.Line
	l.Replay(lineCounter{&out})
	return out
}

type lineCounter struct{ out *[]drawlist.Line }

func (s lineCounter) Clear()                   {}
func (s lineCounter) Terrain(drawlist.Terrain) {}
func (s lineCounter) Sprite(drawlist.Sprite)   {}
func (s lineCounter) Glyphs(drawlist.Glyphs)   {}
func (s lineCounter) Fill(drawlist.Fill)       {}
func (s lineCounter) Line(l drawlist.Line)     { *s.out = append(*s.out, l) }
func (s lineCounter) Points(drawlist.Points)   {}
func (s lineCounter) Model(drawlist.Model)     {}
func (s lineCounter) Fog(drawlist.Fog)         {}
func (s lineCounter) Surface(drawlist.Surface) {}
func (s lineCounter) Cursor(drawlist.Cursor)   {}
func (s lineCounter) Expand()                  {}
