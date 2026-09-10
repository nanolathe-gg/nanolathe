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
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	// A world clip site takes the record extent; the fog window is one.
	if fw, fh := c.recordExtent(); fw != w || fh != h {
		t.Fatalf("the record extent is not stable across reads: %dx%d then %dx%d", w, h, fw, fh)
	}
	// The drag rectangle is NOT a world site: it is drawn from pointer
	// coordinates outside the region, so its clip stays the framebuffer's.
	if got := c.selectionClip(); got.MaxX != int32(c.width)-1 || got.MaxY != int32(c.height)-33 {
		t.Fatalf("the drag-rectangle clip is %+v, want the framebuffer %dx%d", got, c.width, c.height)
	}
}

// The drag-selection rectangle is drawn from POINTER coordinates, so it must be
// recorded OUTSIDE the world region: inside it the executor would scale it by
// the live factor and the rubber band would come away from the cursor
// (DESIGN_GPU_RENDERER §16.3).
func TestDragRectangleIsRecordedOutsideTheWorldRegion(t *testing.T) {
	c := zoomRecorderClient(t)
	c.cam.Zoom, c.cam.Scale = camera.ZoomUnit/2, camera.ViewScaleNative
	c.SetSelectionDrag(SelectionDrag{Active: true, StartX: 200, StartY: 120, EndX: 260, EndY: 180})
	c.drawCommittedFrame(&frame.Frame{}, true)

	trace := &regionTrace{}
	c.list.Replay(trace)
	if trace.regions != 2 {
		t.Fatalf("recorded %d world markers, want one open and one close", trace.regions)
	}
	found := false
	for _, f := range trace.fills {
		if f.rect.X != 200 || f.rect.Y != 120 {
			continue
		}
		found = true
		if f.inWorld {
			t.Fatalf("the drag rectangle %+v was recorded inside a world region", f.rect)
		}
	}
	if !found {
		t.Fatalf("no drag rectangle at the pointer's own (200,120) in %d fills", len(trace.fills))
	}
}

// regionTrace records each Fill together with whether a world region was open
// when it was replayed.
type regionTrace struct {
	open    bool
	regions int
	fills   []struct {
		rect    drawlist.Rect
		inWorld bool
	}
}

func (s *regionTrace) World(w drawlist.WorldSpace) {
	s.regions++
	s.open = w.Begin
}

func (s *regionTrace) Fill(v drawlist.Fill) {
	s.fills = append(s.fills, struct {
		rect    drawlist.Rect
		inWorld bool
	}{v.Rect, s.open})
}

func (s *regionTrace) Clear()                   {}
func (s *regionTrace) Terrain(drawlist.Terrain) {}
func (s *regionTrace) Sprite(drawlist.Sprite)   {}
func (s *regionTrace) Glyphs(drawlist.Glyphs)   {}
func (s *regionTrace) Line(drawlist.Line)       {}
func (s *regionTrace) Points(drawlist.Points)   {}
func (s *regionTrace) Model(drawlist.Model)     {}
func (s *regionTrace) Fog(drawlist.Fog)         {}
func (s *regionTrace) Surface(drawlist.Surface) {}
func (s *regionTrace) Cursor(drawlist.Cursor)   {}
func (s *regionTrace) Expand()                  {}
func (s *regionTrace) Flash(drawlist.Flash)     {}
func (s *regionTrace) Halo(drawlist.Halo)       {}

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

// Below the strategic cut the recorder emits no unit-model, effect, projectile
// or unit-label command; terrain, fog, the FEATURES, the selection fills and the
// drag rectangle still record (§16.10).
func TestStrategicViewDropsTheUnitLayers(t *testing.T) {
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units: []frame.UnitView{
			{Slot: 1, Owner: 0, X: numeric.FixedFromInt(320), Z: numeric.FixedFromInt(240),
				Model: "zoom_marker", Flags: hud.SelectionFlag, MoverMode: 1},
		},
		Features: []frame.FeatureView{{
			CX: 20, CZ: 15, X: numeric.FixedFromInt(320), Z: numeric.FixedFromInt(240),
			Filename: "zoom-fixture", SeqName: "body",
		}},
		Projectiles: []frame.ProjectileView{{X: numeric.FixedFromInt(300), Z: numeric.FixedFromInt(200)}},
	}

	full := zoomRecorderClient(t)
	installZoomFeatureArt(full)
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
	fullFeatures := len(recordedFeatureSprites(&full.list))
	if fullFeatures == 0 {
		t.Fatal("the ordinary view recorded no feature sprite; the fixture proves nothing")
	}

	strategic := zoomRecorderClient(t)
	installZoomFeatureArt(strategic)
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
	// The features survive too: nothing stands in for a rock or a tree, so
	// dropping them takes the map's landmarks away at exactly the factor a
	// player pulls out to read them by (§16.10).
	if got := len(recordedFeatureSprites(&strategic.list)); got != fullFeatures {
		t.Fatalf("the strategic view recorded %d feature sprites, want the ordinary view's %d", got, fullFeatures)
	}
}

// installZoomFeatureArt gives the client one four-by-three sprite feature entry,
// the same shape internal/client's other feature fixtures use.
func installZoomFeatureArt(c *Client) {
	c.modelFS = vfs.New()
	sprite := &formats.GAFFrame{
		Width: 4, Height: 3, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: make([]byte, 12), Transparent: make([]bool, 12),
	}
	sprite.PlainPixels, sprite.PlainTransparent = sprite.Pixels, sprite.Transparent
	c.featureGAFs["zoom-fixture"] = &formats.GAF{Entries: []formats.GAFEntry{
		{Name: "body", Frames: []formats.GAFFrameRef{{Frame: sprite}}},
	}}
}

// recordedFeatureSprites collects the recorded feature sprite commands, which is
// how a tree, a rock or a wreck shows up in a list.
func recordedFeatureSprites(l *drawlist.List) []drawlist.Sprite {
	var out []drawlist.Sprite
	l.Replay(spriteCounter{&out})
	return out
}

type spriteCounter struct{ out *[]drawlist.Sprite }

func (s spriteCounter) Clear()                   {}
func (s spriteCounter) Terrain(drawlist.Terrain) {}
func (s spriteCounter) Sprite(v drawlist.Sprite) {
	if v.Kind == drawlist.BlitFeatureNormal || v.Kind == drawlist.BlitFeatureShadow {
		*s.out = append(*s.out, v)
	}
}
func (s spriteCounter) Glyphs(drawlist.Glyphs)   {}
func (s spriteCounter) Fill(drawlist.Fill)       {}
func (s spriteCounter) Line(drawlist.Line)       {}
func (s spriteCounter) Points(drawlist.Points)   {}
func (s spriteCounter) Model(drawlist.Model)     {}
func (s spriteCounter) Fog(drawlist.Fog)         {}
func (s spriteCounter) Surface(drawlist.Surface) {}
func (s spriteCounter) Cursor(drawlist.Cursor)   {}
func (s spriteCounter) Expand()                  {}
func (s spriteCounter) Flash(drawlist.Flash)     {}
func (s spriteCounter) Halo(drawlist.Halo)       {}

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
func (s lineCounter) Flash(drawlist.Flash)     {}
func (s lineCounter) Halo(drawlist.Halo)       {}
