package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// stagingBody builds a carrier image: `w` by `h` at the given anchor, every
// pixel covered with one colour and one key.
func stagingBody(w, h int, anchorX, anchorY int32, color, key uint8) *modelTarget {
	t := newModelImage(w, h, 0, 0, anchorX, anchorY, true, 1)
	for i := range t.color {
		t.color[i], t.covered[i], t.height[i] = color, true, key
	}
	return t
}

// TestStagingBoxIsTheUnionOfCarrierAndChildren locks the staging box of
// [R-REN-03A §4]: the union of the carrier's own box with the boxes of all its
// attached children, offset by each child's position relative to the carrier.
//
// The images already carry that offset in their framebuffer anchors, so the
// union is taken in framebuffer space and the carrier's anchor is kept.
func TestStagingBoxIsTheUnionOfCarrierAndChildren(t *testing.T) {
	body := stagingBody(4, 4, 100, 100, 10, 60)
	// A child four pixels right and three up of the carrier's own origin.
	child := stagingBody(4, 4, 104, 97, 20, 60)

	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	if staging == nil {
		t.Fatal("no staging image")
	}
	// Carrier spans screen x 100..103, y 100..103; child spans 104..107, 97..100.
	if got := staging.screenX(0); got != 100 {
		t.Fatalf("staging left edge = %d, want the carrier's 100", got)
	}
	if got := staging.screenY(0); got != 97 {
		t.Fatalf("staging top edge = %d, want the child's 97", got)
	}
	if staging.width != 8 || staging.heightPx != 7 {
		t.Fatalf("staging box = %dx%d, want the union 8x7", staging.width, staging.heightPx)
	}
	if staging.anchorX != body.anchorX || staging.anchorY != body.anchorY {
		t.Fatal("the staging image must keep the carrier's anchor")
	}
	if staging.height == nil {
		t.Fatal("a carrier with a key plane must stage with one")
	}
	// The cached image is copied in, both planes.
	i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
	if staging.color[i] != 10 || staging.height[i] != 60 || !staging.covered[i] {
		t.Fatalf("carrier pixel not copied into the staging image: colour=%d key=%d covered=%v", staging.color[i], staging.height[i], staging.covered[i])
	}
}

// TestStagingCarrierWithoutAKeyPlaneStagesWithoutOne locks that the staging
// image follows the carrier's own plane: with no key plane there is nothing to
// resolve against, which is the painter path of [R-REN-03A §4].
func TestStagingCarrierWithoutAKeyPlaneStagesWithoutOne(t *testing.T) {
	body := newModelImage(4, 4, 0, 0, 100, 100, false, 1)
	staging := newStagingImage(body, nil)
	if staging.height != nil {
		t.Fatal("a keyless carrier must not stage a key plane")
	}
}

// TestCompositeChildResolvesOnTheKeyTest locks the per-pixel admission of
// [R-REN-03A §4] step 2: a child pixel is written when it is not the child
// image's transparent index and `stagingKey <= childKey + heightDelta`. Equal
// keys admit, so the child — composited later — wins a tie.
func TestCompositeChildResolvesOnTheKeyTest(t *testing.T) {
	cases := []struct {
		name              string
		carrierKey        uint8
		childKey          uint8
		delta             int32
		wantChild         bool
		wantResultingKey  uint8
		wantCarrierColour uint8
	}{
		{name: "child above the carrier", carrierKey: 60, childKey: 90, wantChild: true, wantResultingKey: 90},
		{name: "child below the carrier", carrierKey: 90, childKey: 60, wantChild: false, wantResultingKey: 90, wantCarrierColour: 10},
		{name: "equal keys admit the later child", carrierKey: 70, childKey: 70, wantChild: true, wantResultingKey: 70},
		{name: "the delta lifts a child that would lose", carrierKey: 90, childKey: 60, delta: 40, wantChild: true, wantResultingKey: 100},
		{name: "the delta drops a child that would win", carrierKey: 60, childKey: 90, delta: -40, wantChild: false, wantResultingKey: 60, wantCarrierColour: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := stagingBody(2, 2, 100, 100, 10, tc.carrierKey)
			child := stagingBody(2, 2, 100, 100, 20, tc.childKey)
			staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}, keyDelta: tc.delta}})
			staging.compositeChild(child, tc.delta)

			i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
			if tc.wantChild {
				if staging.color[i] != 20 {
					t.Fatalf("colour = %d, want the child's 20", staging.color[i])
				}
			} else if staging.color[i] != tc.wantCarrierColour {
				t.Fatalf("colour = %d, want the carrier's %d", staging.color[i], tc.wantCarrierColour)
			}
			if staging.height[i] != tc.wantResultingKey {
				t.Fatalf("key = %d, want %d", staging.height[i], tc.wantResultingKey)
			}
		})
	}
}

// TestCompositeChildWritesWhereTheCarrierIsTransparent locks that the child
// still lands where the carrier's image never covered anything: the staging
// image's own background carries key zero, so the child's key admits.
func TestCompositeChildWritesWhereTheCarrierIsTransparent(t *testing.T) {
	body := stagingBody(2, 2, 100, 100, 10, 200)
	child := stagingBody(2, 2, 104, 100, 20, 30) // no overlap with the carrier
	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	staging.compositeChild(child, 0)

	i := int(staging.imageY(100))*staging.width + int(staging.imageX(104))
	if staging.color[i] != 20 || !staging.covered[i] {
		t.Fatalf("child pixel outside the carrier was not written: colour=%d covered=%v", staging.color[i], staging.covered[i])
	}
}

// TestCompositeChildSkipsTheChildBackground locks the other half of the
// admission: a pixel the child image never covered is its transparent index and
// contributes nothing, so the carrier shows through [R-REN-03A §4].
func TestCompositeChildSkipsTheChildBackground(t *testing.T) {
	body := stagingBody(2, 2, 100, 100, 10, 60)
	child := newModelImage(2, 2, 0, 0, 100, 100, true, 1) // all background
	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	staging.compositeChild(child, 0)

	i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
	if staging.color[i] != 10 {
		t.Fatalf("colour = %d, want the carrier's 10 to show through", staging.color[i])
	}
	if staging.height[i] != 60 {
		t.Fatalf("key = %d, want the carrier's 60 undisturbed", staging.height[i])
	}
}

// TestCompositeChildKeyStoreWraps locks the store width of [R-REN-03A §4]: the
// comparison is made at full width, so a child lifted past the byte range still
// wins against the carrier, but the plane stores only the low byte of the
// shifted key — a wrap, not a saturation. The stored 44 is what a later child
// or the digger and waterline passes then see.
func TestCompositeChildKeyStoreWraps(t *testing.T) {
	body := stagingBody(2, 2, 100, 100, 10, 100)
	child := stagingBody(2, 2, 100, 100, 20, 200)
	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	staging.compositeChild(child, 100) // shifted key 300: admitted over 100, stored as 300 mod 256

	i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
	if staging.color[i] != 20 {
		t.Fatalf("colour = %d, want the child's 20: a full-width comparison admits 300 over 100", staging.color[i])
	}
	if staging.height[i] != 44 {
		t.Fatalf("key = %d, want 44: the store keeps the low byte of 300", staging.height[i])
	}
	for _, tc := range []struct {
		in   int32
		want uint8
	}{{-1, 255}, {0, 0}, {255, 255}, {256, 0}, {300, 44}} {
		if got := wrapKeyByte(tc.in); got != tc.want {
			t.Fatalf("wrapKeyByte(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestCarriedChildUsesItsOwnPublishedTeamColour locks the staging input rather
// than only the primitive helper: cargo with owner slot 7 and colour 0 must use
// its own frame even when its carrier is slot 0 with colour 7 [R-REN-03A §4]
// [R-RAST-01 §3].
func TestCarriedChildUsesItsOwnPublishedTeamColour(t *testing.T) {
	c := newPieceFixtureClient(t)
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(100 + i)}}
	}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}
	c.models = map[string]*unitModel{"cargo": teamLogoTestModel()}

	carrier := frame.UnitView{
		InstanceID: 1, Model: "cargo", Owner: 0, OwnerColor: 7, OwnerColorKnown: true, ZBuffer: true,
	}
	child := frame.UnitView{
		InstanceID: 2, Model: "cargo", Owner: 7, OwnerColor: 0, OwnerColorKnown: true, ZBuffer: true,
	}
	if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("carrier with carried child did not compose")
	}
	c.list.Replay(c.classicSink())
	var childPixels, carrierPixels int
	for _, color := range c.indexed {
		switch color {
		case 100:
			childPixels++
		case 107:
			carrierPixels++
		}
	}
	if childPixels == 0 {
		t.Fatal("carried child wrote no colour-zero pixels")
	}
	if carrierPixels != 0 {
		t.Fatalf("carrier colour-seven frame remained in %d pixels after equal-key child composite", carrierPixels)
	}
}

func TestGeometryOnlyCarrierRecordsChildComposition(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.geometryOnlyModels = true
	c.models = map[string]*unitModel{"cargo": teamLogoTestModel()}

	carrier := frame.UnitView{InstanceID: 1, Model: "cargo", Owner: 0, OwnerColor: 7, OwnerColorKnown: true, ZBuffer: true}
	child := frame.UnitView{InstanceID: 2, Model: "cargo", Owner: 7, OwnerColor: 0, OwnerColorKnown: true, ZBuffer: true}
	if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("valid staged carrier did not retain selection-chrome eligibility")
	}
	if got := len(c.modelCommits); got != 0 {
		t.Fatalf("geometry-only staged carrier retained %d CPU model commits", got)
	}
	models := c.list.ModelCommands()
	if len(models) != 2 || !models[0].ShadowOnly || models[1].Geometry == nil || !models[1].Geometry.Eligible || len(models[1].Geometry.Children) != 1 {
		t.Fatalf("geometry-only staged carrier = %#v, want child shadow then carrier group", models)
	}
	g := models[1].Geometry
	if !g.Children[0].Geometry.KeyPlane {
		t.Fatal("staged child has no key plane")
	}
	clone := g.Clone()
	clone.Children[0].Geometry.Faces[0].Vertices[0].X++
	if clone.Children[0].Geometry.Faces[0].Vertices[0].X == g.Children[0].Geometry.Faces[0].Vertices[0].X {
		t.Fatal("staged clone aliases child geometry")
	}
}

func TestGeometryOnlyCarrierRegistersChildTextureCursors(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.geometryOnlyModels = true
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{31}}},
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{32}}},
	}}
	c.texIndex["logo"] = texRef{kind: texAnimated, key: "fixture|logo", entry: entry}
	c.models = map[string]*unitModel{"cargo": teamLogoTestModel()}

	carrier := frame.UnitView{InstanceID: 1, Model: "cargo", Owner: 0, ZBuffer: true}
	child := frame.UnitView{InstanceID: 2, Model: "cargo", Owner: 0, ZBuffer: true}
	if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("valid staged carrier did not resolve")
	}
	if got := len(c.modelPresentation); got != 2 {
		t.Fatalf("registered model texture cursors = %d, want carrier and child", got)
	}
	if got := len(c.modelPlayers); got != 2 {
		t.Fatalf("phase-7 model texture players = %d, want carrier and child", got)
	}
}

func TestGeometryOnlyMissingCarrierAccountsForValidChild(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.geometryOnlyModels = true
	c.shadows = true
	c.pal = &palette.Tables{}
	c.models = map[string]*unitModel{"cargo": teamLogoTestModel()}

	carrier := frame.UnitView{InstanceID: 1, Model: "missing"}
	child := frame.UnitView{InstanceID: 2, Model: "cargo", Owner: 0, ZBuffer: true}
	if c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("missing carrier unexpectedly retained selection-chrome eligibility")
	}
	models := c.list.ModelCommands()
	if len(models) != 1 || models[0].Geometry == nil || !models[0].Geometry.Eligible || models[0].ShadowOnly {
		t.Fatalf("missing-carrier geometry = %#v, want independent child body", models)
	}
	if models[0].Geometry.Shadow == nil || models[0].ShadowOmissions != 0 {
		t.Fatal("valid child's shadow was omitted")
	}
}

func teamLogoTestModel() *unitModel {
	return &unitModel{compiled: &compiledmodel.Model{
		Root: 0,
		Pieces: []compiledmodel.Piece{{
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{0, 0, 0}, {8 << 16, 0, 0}, {8 << 16, 0, -8 << 16}, {0, 0, -8 << 16},
			},
			Primitives: []compiledmodel.Primitive{{TextureName: "logo", VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}}
}

// TestTeamColourDoesNotChangeWaterlineOwnership keeps the visual selector out
// of the gameplay visibility predicate: player zero's submerged unit remains
// tinted/present even when its LOGOS frame is colour seven [R-RAST-01 §3, §4].
func TestTeamColourDoesNotChangeWaterlineOwnership(t *testing.T) {
	c := newPieceFixtureClient(t)
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(100 + i)}}
	}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}
	c.models = map[string]*unitModel{"sub": teamLogoTestModel()}
	c.buffer = frame.NewBuffer()
	w := c.buffer.BeginWrite()
	w.Selection.LocalPlayer = 0
	w.Visibility.SeaLevel = 1 << 16
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}

	composed, ok := c.composeUnitModel(frame.UnitView{
		InstanceID: 3, Model: "sub", Owner: 0, OwnerColor: 7, OwnerColorKnown: true, ZBuffer: true,
	})
	if !ok || composed.image == nil {
		t.Fatal("submerged owned unit did not compose")
	}
	covered := 0
	for _, present := range composed.image.covered {
		if present {
			covered++
		}
	}
	if covered == 0 {
		t.Fatal("owner-colour selector changed waterline ownership and erased the owned unit")
	}
}

func TestCarrierRecordingRoutesPreserveChildHeightAndPainterOrder(t *testing.T) {
	for _, geometryOnly := range []bool{false, true} {
		for _, keyed := range []bool{false, true} {
			c := newPieceFixtureClient(t)
			c.geometryOnlyModels, c.recordModelGeometry = geometryOnly, true
			c.models = map[string]*unitModel{"cargo": teamLogoTestModel()}
			carrier := frame.UnitView{InstanceID: 1, Model: "cargo", ZBuffer: keyed, Y: numeric.Fixed(3 << 16)}
			child := frame.UnitView{InstanceID: 2, Model: "cargo", ZBuffer: false, Y: numeric.Fixed(-1)}
			if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
				t.Fatal("carrier did not record")
			}
			models := c.list.ModelCommands()
			if len(models) != 2 {
				t.Fatalf("geometry=%v keyed=%v commands=%d", geometryOnly, keyed, len(models))
			}
			if keyed {
				if !models[0].ShadowOnly || !models[1].Geometry.Eligible || len(models[1].Geometry.Children) != 1 {
					t.Fatal("keyed child order/group was lost")
				}
				child := models[1].Geometry.Children[0]
				if child.KeyDelta != -4 || !child.Geometry.KeyPlane {
					t.Fatalf("child delta/key plane=%d/%v, want -4/true", child.KeyDelta, child.Geometry.KeyPlane)
				}
			} else if models[0].ShadowOnly || models[1].ShadowOnly || len(models[0].Geometry.Children) != 0 || models[1].Geometry.KeyPlane {
				t.Fatal("keyless group did not preserve independent painter order")
			}
		}
	}
}
