package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func testModelTextureClient() *Client {
	return &Client{
		cam:      &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096},
		texIndex: map[string]texRef{"tex": {kind: texStatic, key: "tex", frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}}}},
	}
}

func testPrimitiveDraw(pr presentationrender.PrimitiveDraw, vertices [][3]numeric.Fixed) *presentationrender.UnitDraw {
	return &presentationrender.UnitDraw{
		Model:  &compiledmodel.Model{Pieces: []compiledmodel.Piece{{Name: "root"}}},
		Pieces: []presentationrender.PieceDraw{{WorldVertices: vertices, Primitives: []presentationrender.PrimitiveDraw{pr}}},
	}
}

func fixedVertex(x, y, z int64) [3]numeric.Fixed {
	return [3]numeric.Fixed{numeric.Fixed(x << 16), numeric.Fixed(y << 16), numeric.Fixed(z << 16)}
}

func TestModelPrimitiveDispatch(t *testing.T) {
	tests := []struct {
		name     string
		pr       presentationrender.PrimitiveDraw
		resolved bool
		want     modelPrimitiveMode
	}{
		{name: "clear untextured", pr: presentationrender.PrimitiveDraw{VertexIndices: []uint16{0, 1, 2, 3}}, want: modelPrimitiveSkip},
		{name: "flat untextured quad", pr: presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 56, VertexIndices: []uint16{0, 1, 2, 3}}, want: modelPrimitiveFlat},
		{name: "flat untextured triangle suppressed", pr: presentationrender.PrimitiveDraw{IsColored: 1, VertexIndices: []uint16{0, 1, 2}}, want: modelPrimitiveSkip},
		{name: "resolved texture", pr: presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveTexture},
		{name: "canonical flat override", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 1, ColorIndex: 56, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveFlat},
		{name: "canonical override requires in-range color", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 1, ColorIndex: 300, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveTexture},
		{name: "editor garbage retains texture", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 7, ColorIndex: 0x1234, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveTexture},
		{name: "missing texture flat miss", pr: presentationrender.PrimitiveDraw{TextureName: "missing", VertexIndices: []uint16{0, 1, 2, 3}}, want: modelPrimitiveFlat},
		{name: "missing texture n-gon suppressed", pr: presentationrender.PrimitiveDraw{TextureName: "missing", VertexIndices: []uint16{0, 1, 2, 3, 4}}, want: modelPrimitiveSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := modelPrimitiveDispatch(tt.pr, tt.resolved); got != tt.want {
				t.Fatalf("dispatch = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCollectDrawTrisUsesPrimitiveCornerShadeRows(t *testing.T) {
	c := testModelTextureClient()
	pr := presentationrender.PrimitiveDraw{
		TextureName:   "tex",
		VertexIndices: []uint16{2, 4, 5, 6},
		ShadeRows:     []int{3, 7, 11, 19},
	}
	vertices := make([][3]numeric.Fixed, 7)
	for i := range vertices {
		vertices[i] = fixedVertex(int64(i), 0, int64(i))
	}
	tris := c.collectDrawTris(testPrimitiveDraw(pr, vertices), 0, 1, modelCursorUnit)
	if len(tris) != 2 {
		t.Fatalf("triangle count = %d, want 2", len(tris))
	}
	if got, want := tris[0].row, [3]float64{3, 7, 11}; got != want {
		t.Fatalf("first corner rows = %v, want %v", got, want)
	}
	if got, want := tris[1].row, [3]float64{3, 11, 19}; got != want {
		t.Fatalf("second corner rows = %v, want %v", got, want)
	}
}

func TestCollectDrawTrisSelectsTeamLogoFrame(t *testing.T) {
	c := testModelTextureClient()
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(i)}}
	}
	entry := &formats.GAFEntry{Name: "logo", Frames: frames}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: entry}
	pr := presentationrender.PrimitiveDraw{TextureName: "logo", VertexIndices: []uint16{0, 1, 2, 3}}
	tris := c.collectDrawTris(testPrimitiveDraw(pr, [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, 1), fixedVertex(0, 0, 1),
	}), 7, 1, modelCursorUnit)
	if len(tris) != 2 || tris[0].frame != frames[7].Frame || tris[1].frame != frames[7].Frame {
		t.Fatalf("team texture did not select owner frame")
	}
}

func TestCollectDrawTrisFlatOverrideWinsOverTeamLogo(t *testing.T) {
	c := testModelTextureClient()
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(i)}}
	}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}
	pr := presentationrender.PrimitiveDraw{TextureName: "logo", IsColored: 1, ColorIndex: 56, VertexIndices: []uint16{0, 1, 2, 3}}
	tris := c.collectDrawTris(testPrimitiveDraw(pr, [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, 1), fixedVertex(0, 0, 1),
	}), 7, 1, modelCursorUnit)
	if len(tris) != 2 {
		t.Fatalf("flat team override emitted %d triangles, want 2", len(tris))
	}
	for _, tri := range tris {
		if tri.frame != nil || tri.color != 56 {
			t.Fatalf("flat team override = frame %v color %d, want direct color 56", tri.frame, tri.color)
		}
	}
}

func TestFeaturePresentationIDRequiresPublishedIdentity(t *testing.T) {
	if got := featurePresentationID(frame.FeatureView{CX: 12, CZ: -4}); got != 0 {
		t.Fatalf("missing feature identity = %d, want zero", got)
	}
	if got := featurePresentationID(frame.FeatureView{InstanceID: 17, CX: 12, CZ: -4}); got != 17 {
		t.Fatalf("published feature identity = %d, want 17", got)
	}
}

func TestFeatureStaticModelAllowsMissingIdentity(t *testing.T) {
	c := testModelTextureClient()
	c.models = map[string]*unitModel{
		"feature": {
			compiled: &compiledmodel.Model{Pieces: []compiledmodel.Piece{{
				Primitives: []compiledmodel.Primitive{{ColorIndex: 56, IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3}}},
				Vertices:   [][3]numeric.Fixed{{0, 0, 0}, {1 << 16, 0, 0}, {1 << 16, 0, 1 << 16}, {0, 0, 1 << 16}},
			}}},
		},
	}
	if !c.drawFeatureModel(frame.FeatureView{Model: "feature"}) {
		t.Fatal("static feature model was suppressed without InstanceID")
	}
}

func TestFeatureAnimatedModelSuppressesMissingIdentity(t *testing.T) {
	c := testModelTextureClient()
	c.texIndex["anim"] = texRef{kind: texAnimated, key: "anim", entry: &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{1}}},
		{Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{2}}},
	}}}
	pr := presentationrender.PrimitiveDraw{TextureName: "anim", VertexIndices: []uint16{0, 1, 2, 3}}
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, 1), fixedVertex(0, 0, 1)}
	if got := c.collectDrawTris(testPrimitiveDraw(pr, vertices), 0, 0, modelCursorFeature); len(got) != 0 {
		t.Fatalf("animated feature with missing identity emitted %d triangles", len(got))
	}
	if got := c.animatedGAFFrame("anim", 0, c.texIndex["anim"].entry); got != nil {
		t.Fatal("animated feature sprite with missing identity returned a frame")
	}
}

func TestCollectDrawTrisUsesCameraScale(t *testing.T) {
	c := testModelTextureClient()
	c.cam.Scale = 2
	vertices := [][3]numeric.Fixed{fixedVertex(2, 4, 6), fixedVertex(4, 4, 6), fixedVertex(2, 4, 8), fixedVertex(2, 4, 6)}
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3}}
	tris := c.collectDrawTris(testPrimitiveDraw(pr, vertices), 0, 1, modelCursorUnit)
	if len(tris) == 0 {
		t.Fatal("expected projected triangles")
	}
	for i, vi := range []int{0, 1, 2} {
		wantX, wantY := c.cam.WorldToScreen(vertices[vi][0], vertices[vi][1], vertices[vi][2])
		wantX -= camera.OriginX
		wantY -= camera.OriginY
		if tris[0].x[i] != wantX || tris[0].y[i] != wantY {
			t.Fatalf("corner %d = (%d,%d), want camera projection (%d,%d)", i, tris[0].x[i], tris[0].y[i], wantX, wantY)
		}
	}
}

func TestCollectDrawTrisSuppressesInvalidPrimitive(t *testing.T) {
	c := testModelTextureClient()
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 9, 2}, ShadeRows: []int{1, 2, 3, 4}}
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(0, 0, 1)}
	if got := c.collectDrawTris(testPrimitiveDraw(pr, vertices), 0, 1, modelCursorUnit); len(got) != 0 {
		t.Fatalf("invalid primitive emitted %d triangles", len(got))
	}
}

func TestCollectDrawTrisSuppressesUnsupportedTexturedNGon(t *testing.T) {
	c := testModelTextureClient()
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(2, 0, 1), fixedVertex(1, 0, 2), fixedVertex(0, 0, 1)}
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3, 4}, ShadeRows: []int{1, 2, 3, 4, 5}}
	if got := c.collectDrawTris(testPrimitiveDraw(pr, vertices), 0, 1, modelCursorUnit); len(got) != 0 {
		t.Fatalf("unsupported textured n-gon emitted %d triangles", len(got))
	}
}
