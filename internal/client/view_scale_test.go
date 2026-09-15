package client

// The detail view's two locks (DESIGN_GPU_RENDERER §14.7 items 2 and 3). Both
// encode a NANOLATHE presentation rule, not retail behaviour: retail has one
// view scale and neither the projection nor the art below has a retail
// counterpart at 2x.

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// scaleCapture records one frame's commands in record order, keeping the family
// sequence so the two lists can be compared position by position.
type scaleCapture struct {
	family  []string
	sprites []drawlist.Sprite
	fills   []drawlist.Fill
	lines   []drawlist.Line
	glyphs  []drawlist.Glyphs
	points  []drawlist.Points
	models  []drawlist.Model
	fog     []render.FogOp
	terrain []drawlist.Terrain
}

func (s *scaleCapture) Clear() { s.family = append(s.family, "clear") }
func (s *scaleCapture) Terrain(v drawlist.Terrain) {
	s.family = append(s.family, "terrain")
	s.terrain = append(s.terrain, v)
}
func (s *scaleCapture) Sprite(v drawlist.Sprite) {
	s.family = append(s.family, "sprite")
	s.sprites = append(s.sprites, v)
}
func (s *scaleCapture) Glyphs(v drawlist.Glyphs) {
	s.family = append(s.family, "glyphs")
	s.glyphs = append(s.glyphs, v)
}
func (s *scaleCapture) Fill(v drawlist.Fill) {
	s.family = append(s.family, "fill")
	s.fills = append(s.fills, v)
}
func (s *scaleCapture) Line(v drawlist.Line) {
	s.family = append(s.family, "line")
	s.lines = append(s.lines, v)
}
func (s *scaleCapture) Points(v drawlist.Points) {
	// The batch is a sub-slice of the client's reused point arena, and the
	// second recording writes over it; copy rather than borrow.
	s.family = append(s.family, "points")
	s.points = append(s.points, drawlist.Points{Kind: v.Kind, Points: append([]drawlist.Point(nil), v.Points...)})
}
func (s *scaleCapture) Flash(drawlist.Flash) { s.family = append(s.family, "flash") }
func (s *scaleCapture) Halo(drawlist.Halo)   { s.family = append(s.family, "halo") }
func (s *scaleCapture) Model(v drawlist.Model) {
	// Classic model images come from the list's own reused pool for the same
	// reason; keep the placement values this test reads.
	s.family = append(s.family, "model")
	if v.Classic != nil && v.Classic.Body != nil {
		body := *v.Classic.Body
		v.Classic = &drawlist.ClassicModel{Body: &body}
	} else {
		v.Classic = nil
	}
	s.models = append(s.models, v)
}
func (s *scaleCapture) Fog(v drawlist.Fog) {
	s.family = append(s.family, "fog")
	s.fog = append(s.fog, v.Ops...)
}
func (s *scaleCapture) Surface(v drawlist.Surface) { s.family = append(s.family, "surface") }
func (s *scaleCapture) Cursor(v drawlist.Cursor)   { s.family = append(s.family, "cursor") }
func (s *scaleCapture) Expand()                    { s.family = append(s.family, "expand") }

// pointBounds is a lit-point batch's covering rectangle. A batch's individual
// points are a rasterized disc, so the relationship the scale contract fixes is
// the extent of the batch, not the identity of each point.
func pointBounds(b drawlist.Points) (minX, minY, maxX, maxY int32) {
	if len(b.Points) == 0 {
		return 0, 0, -1, -1
	}
	minX, minY = b.Points[0].X, b.Points[0].Y
	maxX, maxY = minX, minY
	for _, p := range b.Points[1:] {
		minX, maxX = min(minX, p.X), max(maxX, p.X)
		minY, maxY = min(minY, p.Y), max(maxY, p.Y)
	}
	return minX, minY, maxX, maxY
}

// viewScaleScene builds the fixture the two recordings share: a terrain, one
// sprite feature, one projectile ground shadow and beam, one effect sprite and
// one lit halo, a fog cell, a health bar and one unit model, plus a HUD fill
// that must not move.
func viewScaleScene(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c, err := New(Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	c.modelFS = vfs.New()
	c.SetPalette(&palette.Tables{})
	c.SetFNT(&formats.FNT{Height: 5, Glyphs: [256]*formats.FNTGlyph{'7': {Width: 3, Height: 5, Bits: []byte{0xE0, 0x20, 0x40, 0x40, 0x40}}}})

	terrain := &world.Terrain{CellW: 8, CellH: 8, TileIndices: make([]uint16, 16), TileSet: make([][1024]byte, 1)}
	c.SetTerrain(terrain)

	sprite := &formats.GAFFrame{
		Width: 4, Height: 3, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: make([]byte, 12), Transparent: make([]bool, 12),
	}
	sprite.PlainPixels, sprite.PlainTransparent = sprite.Pixels, sprite.Transparent
	c.featureGAFs["scale-fixture"] = &formats.GAF{Entries: []formats.GAFEntry{
		{Name: "body", Frames: []formats.GAFFrameRef{{Frame: sprite}}},
	}}
	c.models["scale_unit"] = syntheticModel(
		[]pieceInfo{{name: "root", parent: -1}},
		[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{0, 0, 0}, {6, 0, 0}, {0, 0, 6}}, 17, 0)},
		0,
	)

	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units: []frame.UnitView{{
			Slot: 1, Owner: 0, Model: "scale_unit",
			X: numeric.Fixed(70 << 16), Z: numeric.Fixed(60 << 16),
			Health: 40, MaxHealth: 100, Group: 7,
		}},
	}
	// One explored-but-fogged cell, which records a gray remap over the cell
	// rectangle [03 §3.3].
	cur.Fog.Valid = true
	cur.Fog.W, cur.Fog.H = 4, 4
	cur.Fog.Ch0 = make([]byte, 16)
	cur.Fog.Ch1 = make([]byte, 16)
	cur.Fog.Ch1[2*4+2] = 15
	cur.Fog.Source, cur.Fog.Version = 1, 1
	return c, cur
}

// recordViewScaleScene records the world sites of §14.2 in one order, then one
// HUD fill, and replays the list into a capture.
func recordViewScaleScene(t *testing.T, c *Client, cur *frame.Frame) *scaleCapture {
	t.Helper()
	c.resetListForTest()
	c.fogCache = nil
	c.fogSource, c.fogVersion = 0, 0

	c.drawTerrainPrep()

	c.drawFeature(&frame.FeatureView{
		X: numeric.Fixed(48 << 16), Z: numeric.Fixed(40 << 16),
		Filename: "scale-fixture", SeqName: "body",
	})

	projectile := frame.ProjectileView{
		Handle: 1,
		X:      numeric.Fixed(90 << 16), Z: numeric.Fixed(70 << 16),
		TailX: numeric.Fixed(80 << 16), TailZ: numeric.Fixed(66 << 16),
		FloorHeight: 8, FloorHeightValid: true,
	}
	shadow := &formats.GAFFrame{Width: 2, Height: 2, XOffset: 1, YOffset: 1, ColorKey: 9, Pixels: make([]byte, 4), Transparent: make([]bool, 4)}
	shadow.PlainPixels, shadow.PlainTransparent = shadow.Pixels, shadow.Transparent
	if !c.drawProjectileShadow(shadow, projectile) {
		t.Fatal("the projectile ground shadow recorded nothing")
	}
	c.drawProjectileBeam(render.ProjectileDraw{Color: 11, Color2: 11}, projectile)

	art := &formats.GAFFrame{Width: 3, Height: 3, XOffset: 1, YOffset: 1, ColorKey: 9, Pixels: make([]byte, 9), Transparent: make([]bool, 9)}
	art.PlainPixels, art.PlainTransparent = art.Pixels, art.Transparent
	effects := []frame.EffectView{
		{Graphic: "boom", ActiveA: true, X: numeric.Fixed(100 << 16), Z: numeric.Fixed(80 << 16), Strip: 1},
		{Graphic: "", X: numeric.Fixed(110 << 16), Z: numeric.Fixed(90 << 16), Strip: 1, HasFlashDisc: true, FlashRadius: 5, FlashLevel: 3, Light: true},
	}
	c.DrawEffectViews(effects, EffectDrawOptions{
		ResolveFrame:    func(frame.EffectView, int32) (*formats.GAFFrame, bool) { return art, true },
		TerrainCoverage: func(int, int) bool { return true },
	})

	// The bar is gated on the damagebars interface bit [03 R-FX-01 §6].
	previous := DamageBars()
	SetDamageBars(true)
	c.drawUnitLabels(cur, true)
	SetDamageBars(previous)
	c.drawFog(cur)
	// Pass B walks the bucket array pass A builds, so both run [03 R-RAST-01 §7].
	c.drawWorldPass(cur, true)
	c.drawWorldPassB(cur, true)

	// One HUD command, which the scale must not touch.
	c.UIFillRect(4, 6, 10, 12, 33)

	capture := &scaleCapture{}
	c.list.Replay(capture)
	return capture
}

// TestViewScaleDoublesEveryWorldCommand is §14.7 item 2: the automated form of
// the §14.2 audit. Two recordings of the same scene from the same world camera
// origin, one native and one at the detail scale, and every world-space
// command's screen geometry doubles about the beam origin while the HUD
// commands are identical. This is a Nanolathe presentation rule, not retail
// behaviour.
func TestViewScaleDoublesEveryWorldCommand(t *testing.T) {
	// One client and one committed frame, recorded twice from the same world
	// camera origin: only the view scale differs, so every difference between
	// the two lists is the scale's doing.
	c, cur := viewScaleScene(t)
	cam := &camera.Camera{X: 20, Z: 16, ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096}
	c.SetCamera(cam)
	one := recordViewScaleScene(t, c, cur)
	cam.Scale = camera.ViewScaleDetail
	two := recordViewScaleScene(t, c, cur)

	if len(one.family) != len(two.family) {
		t.Fatalf("command counts differ: native %v, detail %v", one.family, two.family)
	}
	for i := range one.family {
		if one.family[i] != two.family[i] {
			t.Fatalf("command %d family %q at native, %q at the detail scale", i, one.family[i], two.family[i])
		}
	}

	// Every world drawer writes into the surface whose origin is the beam
	// origin, so a world point at native surface x lands at 2x at the detail
	// scale. The HUD fill is the last one recorded and is exempt.
	if len(one.sprites) != 3 || len(one.sprites) != len(two.sprites) {
		t.Fatalf("sprites: native %d, detail %d, want 3 (feature, projectile shadow, effect art)", len(one.sprites), len(two.sprites))
	}
	for i := range one.sprites {
		a, b := one.sprites[i], two.sprites[i]
		if b.X != 2*a.X || b.Y != 2*a.Y {
			t.Fatalf("sprite %d at (%d,%d) native, (%d,%d) detail; want doubled", i, a.X, a.Y, b.X, b.Y)
		}
		if b.Frame == nil || a.Frame == nil {
			t.Fatalf("sprite %d lost its frame", i)
		}
		if b.Frame.Width != 2*a.Frame.Width || b.Frame.Height != 2*a.Frame.Height {
			t.Fatalf("sprite %d frame %dx%d native, %dx%d detail; want the 2x variant", i, a.Frame.Width, a.Frame.Height, b.Frame.Width, b.Frame.Height)
		}
		if b.Frame.XOffset != 2*a.Frame.XOffset || b.Frame.YOffset != 2*a.Frame.YOffset {
			t.Fatalf("sprite %d authored anchor %d,%d native, %d,%d detail; want doubled", i, a.Frame.XOffset, a.Frame.YOffset, b.Frame.XOffset, b.Frame.YOffset)
		}
	}

	for i := range one.lines {
		a, b := one.lines[i], two.lines[i]
		if b.X0 != 2*a.X0 || b.Y0 != 2*a.Y0 || b.X1 != 2*a.X1 || b.Y1 != 2*a.Y1 {
			t.Fatalf("line %d (%d,%d)-(%d,%d) native, (%d,%d)-(%d,%d) detail; want doubled endpoints", i, a.X0, a.Y0, a.X1, a.Y1, b.X0, b.Y0, b.X1, b.Y1)
		}
	}
	if len(one.lines) == 0 {
		t.Fatal("the beam recorded no line")
	}

	if len(one.fills) < 3 {
		t.Fatalf("fills = %d, want the two health-bar rectangles and the HUD fill", len(one.fills))
	}
	for i := range one.fills {
		a, b := one.fills[i], two.fills[i]
		if i == len(one.fills)-1 {
			// The HUD fill: identical, not scaled.
			if a.Rect != b.Rect || a.Index != b.Index || a.Style != b.Style {
				t.Fatalf("the HUD fill moved with the view scale: %+v then %+v", a.Rect, b.Rect)
			}
			continue
		}
		if a.Style != drawlist.FillSolidInclusive {
			t.Fatalf("fill %d style %v, want the health bar's inclusive fill", i, a.Style)
		}
		// The inclusive ENDPOINTS double; the extent form of an inclusive span
		// is right-left+1, so the extent itself is 2W-1 by construction.
		if b.Rect.X != 2*a.Rect.X || b.Rect.Y != 2*a.Rect.Y {
			t.Fatalf("fill %d origin (%d,%d) native, (%d,%d) detail; want doubled", i, a.Rect.X, a.Rect.Y, b.Rect.X, b.Rect.Y)
		}
		if right, want := b.Rect.X+b.Rect.W-1, 2*(a.Rect.X+a.Rect.W-1); right != want {
			t.Fatalf("fill %d inclusive right edge %d, want %d", i, right, want)
		}
		if bottom, want := b.Rect.Y+b.Rect.H-1, 2*(a.Rect.Y+a.Rect.H-1); bottom != want {
			t.Fatalf("fill %d inclusive bottom edge %d, want %d", i, bottom, want)
		}
	}

	if len(one.glyphs) != 1 || len(two.glyphs) != 1 {
		t.Fatalf("glyph runs: native %d, detail %d, want the one control-group digit", len(one.glyphs), len(two.glyphs))
	}
	if a, b := one.glyphs[0], two.glyphs[0]; b.X != 2*a.X || b.Y != 2*a.Y {
		t.Fatalf("the group digit anchor (%d,%d) did not double: (%d,%d)", a.X, a.Y, b.X, b.Y)
	} else if a.Font != b.Font || a.Text != b.Text {
		t.Fatal("the group digit's glyphs changed with the view scale; text is interface")
	}

	if len(one.points) == 0 || len(one.points) != len(two.points) {
		t.Fatalf("lit-point batches: native %d, detail %d", len(one.points), len(two.points))
	}
	for i := range one.points {
		aMinX, aMinY, aMaxX, aMaxY := pointBounds(one.points[i])
		bMinX, bMinY, bMaxX, bMaxY := pointBounds(two.points[i])
		if bMinX != 2*aMinX || bMinY != 2*aMinY {
			t.Fatalf("point batch %d starts at (%d,%d) native, (%d,%d) detail; want doubled", i, aMinX, aMinY, bMinX, bMinY)
		}
		if got, want := bMaxX-bMinX, 2*(aMaxX-aMinX); got < want-1 || got > want+1 {
			t.Fatalf("point batch %d spans %d columns, want about %d", i, got, want)
		}
		if got, want := bMaxY-bMinY, 2*(aMaxY-aMinY); got < want-1 || got > want+1 {
			t.Fatalf("point batch %d spans %d rows, want about %d", i, got, want)
		}
	}

	if len(one.fog) == 0 || len(one.fog) != len(two.fog) {
		t.Fatalf("fog ops: native %d, detail %d", len(one.fog), len(two.fog))
	}
	for i := range one.fog {
		a, b := one.fog[i], two.fog[i]
		ax, ay := a.ScreenX0-camera.OriginX, a.ScreenY0-camera.OriginY
		bx, by := b.ScreenX0-camera.OriginX, b.ScreenY0-camera.OriginY
		if bx != 2*ax || by != 2*ay {
			t.Fatalf("fog op %d at (%d,%d) native, (%d,%d) detail; want doubled", i, ax, ay, bx, by)
		}
		if got, want := b.ScreenX1-b.ScreenX0, 2*(a.ScreenX1-a.ScreenX0); got != want {
			t.Fatalf("fog op %d cell edge %d, want %d", i, got, want)
		}
		if b.ViewScale() != camera.ViewScaleDetail {
			t.Fatalf("fog op %d carries view scale %s, want 2x", i, b.ViewScale())
		}
	}

	if len(one.terrain) != 1 || len(two.terrain) != 1 {
		t.Fatalf("terrain commands: native %d, detail %d", len(one.terrain), len(two.terrain))
	}
	if !one.terrain[0].Scale.Native() || two.terrain[0].Scale != camera.ViewScaleDetail {
		t.Fatalf("terrain record scale %d then %d, want 1 then 2", one.terrain[0].Scale, two.terrain[0].Scale)
	}

	// Original at the detail scale: the model image is the native one, and the
	// blit doubles it about the doubled anchor (§14.2).
	if len(one.models) == 0 || len(one.models) != len(two.models) {
		t.Fatalf("model commands: native %d, detail %d", len(one.models), len(two.models))
	}
	for i := range one.models {
		a, b := one.models[i].Classic, two.models[i].Classic
		if a == nil || b == nil || a.Body == nil || b.Body == nil {
			continue
		}
		if b.Body.AnchorX != 2*a.Body.AnchorX || b.Body.AnchorY != 2*a.Body.AnchorY {
			t.Fatalf("model %d anchor (%d,%d) native, (%d,%d) detail; want doubled", i, a.Body.AnchorX, a.Body.AnchorY, b.Body.AnchorX, b.Body.AnchorY)
		}
		if b.Body.Width != a.Body.Width || b.Body.Height != a.Body.Height || b.Body.OriginX != a.Body.OriginX {
			t.Fatalf("model %d Original image %dx%d must stay native at the detail scale, got %dx%d", i, a.Body.Width, a.Body.Height, b.Body.Width, b.Body.Height)
		}
		if !a.Body.Blit.Native() || b.Body.Blit != camera.ViewScaleDetail {
			t.Fatalf("model %d blit factor %d native, %d detail; want 1 then 2", i, a.Body.Blit, b.Body.Blit)
		}
	}

	// Enhanced at the detail scale: the geometry itself is doubled and the
	// image grows with it, blitted one-to-one.
	c.SetEnhanced(true)
	three := recordViewScaleScene(t, c, cur)
	c.SetEnhanced(false)
	if len(three.models) != len(one.models) {
		t.Fatalf("model commands: native %d, enhanced detail %d", len(one.models), len(three.models))
	}
	for i := range one.models {
		a, b := one.models[i].Classic, three.models[i].Classic
		if a == nil || b == nil || a.Body == nil || b.Body == nil {
			continue
		}
		if b.Body.AnchorX != 2*a.Body.AnchorX || b.Body.AnchorY != 2*a.Body.AnchorY {
			t.Fatalf("model %d anchor (%d,%d) native, (%d,%d) enhanced detail; want doubled", i, a.Body.AnchorX, a.Body.AnchorY, b.Body.AnchorX, b.Body.AnchorY)
		}
		if b.Body.Width <= a.Body.Width || b.Body.Height <= a.Body.Height || !b.Body.Blit.Native() {
			t.Fatalf("model %d Enhanced image %dx%d did not grow with the scale: %dx%d blit %s", i, a.Body.Width, a.Body.Height, b.Body.Width, b.Body.Height, b.Body.Blit)
		}
	}
}

// TestViewScalePickingRoundTrip is §14.7 item 3. Every screen pixel of a
// viewport at the detail scale round trips to a world pixel whose own
// projection lands inside that pixel's 2x2 block, including the negative beam
// offsets left of and above the viewport origin, and a drag rectangle at the
// detail scale converts to the same world rectangle as the native rectangle
// over the same world. Nanolathe presentation rule.
func TestViewScalePickingRoundTrip(t *testing.T) {
	cam := &camera.Camera{X: 40, Z: 30, ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096, Scale: camera.ViewScaleDetail}
	// The sweep deliberately includes screen coordinates below the beam origin,
	// where the inverse must FLOOR: truncation toward zero would fold (-1, 0)
	// onto the same world pixel and leave one unreachable [I3].
	for sy := int32(0); sy < 120; sy++ {
		for sx := int32(0); sx < 200; sx++ {
			wx, wz := cam.ScreenToWorld(sx, sy)
			back, backY := cam.WorldToScreen(wx, 0, wz)
			if sx-back < 0 || sx-back > 1 || sy-backY < 0 || sy-backY > 1 {
				t.Fatalf("screen (%d,%d) round tripped to world (%d,%d), which projects to (%d,%d)", sx, sy, wx>>16, wz>>16, back, backY)
			}
		}
	}

	// A drag rectangle over the same world converts to the same world
	// rectangle at either scale. The native rectangle covering world pixels
	// [x0,x1] is the detail rectangle's corners halved about the beam origin.
	native := &camera.Camera{X: 40, Z: 30, ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096}
	for _, r := range [][4]int32{{0, 0, 40, 30}, {17, 5, 91, 63}, {129, 32, 200, 140}} {
		ax, az := native.ScreenToWorld(r[0]+camera.OriginX, r[1]+camera.OriginY)
		bx, bz := native.ScreenToWorld(r[2]+camera.OriginX, r[3]+camera.OriginY)
		dax, daz := cam.ScreenToWorld(2*r[0]+camera.OriginX, 2*r[1]+camera.OriginY)
		dbx, dbz := cam.ScreenToWorld(2*r[2]+camera.OriginX, 2*r[3]+camera.OriginY)
		if ax != dax || az != daz || bx != dbx || bz != dbz {
			t.Fatalf("drag rect %v: native world (%d,%d)-(%d,%d), detail (%d,%d)-(%d,%d)",
				r, ax>>16, az>>16, bx>>16, bz>>16, dax>>16, daz>>16, dbx>>16, dbz>>16)
		}
	}
}
