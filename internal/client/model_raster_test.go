package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
		// Flat faces take the generic polygon filler at any arity; only
		// textured faces are quad-gated [R-REN-03A §5]. The stock corpus
		// carries 2,402 flat triangles and 700 flat 5..16-gons.
		{name: "flat untextured triangle renders", pr: presentationrender.PrimitiveDraw{IsColored: 1, VertexIndices: []uint16{0, 1, 2}}, want: modelPrimitiveFlat},
		{name: "flat untextured n-gon renders", pr: presentationrender.PrimitiveDraw{IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3, 4, 5}}, want: modelPrimitiveFlat},
		{name: "resolved texture", pr: presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveTexture},
		{name: "canonical flat override", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 1, ColorIndex: 56, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveFlat},
		// The dispatch reads bit 0 alone and masks the colour to a byte; an
		// out-of-range index does not hand the face back to the texture path
		// [R-REN-03A §5]. This corrects an earlier in-range qualifier that was
		// inferred rather than traced.
		{name: "canonical override ignores out-of-range color", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 1, ColorIndex: 300, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveFlat},
		// Editor garbage that co-occurs with a texture is even in every one of
		// the 43,845 stock textured primitives, so bit 0 stays clear and the
		// texture survives [R-REN-03A §5].
		{name: "even editor garbage retains texture", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 1900572, ColorIndex: 0x1234, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveTexture},
		{name: "odd editor garbage takes the flat filler", pr: presentationrender.PrimitiveDraw{TextureName: "tex", IsColored: 7, ColorIndex: 0x1234, VertexIndices: []uint16{0, 1, 2, 3}}, resolved: true, want: modelPrimitiveFlat},
		{name: "missing texture flat miss", pr: presentationrender.PrimitiveDraw{TextureName: "missing", VertexIndices: []uint16{0, 1, 2, 3}}, want: modelPrimitiveFlat},
		{name: "missing texture n-gon suppressed", pr: presentationrender.PrimitiveDraw{TextureName: "missing", VertexIndices: []uint16{0, 1, 2, 3, 4}}, want: modelPrimitiveSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := modelPrimitiveDispatch(&tt.pr, tt.resolved); got != tt.want {
				t.Fatalf("dispatch = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCollectDrawPolysKeepsAuthoredArityAndCornerOrder locks that one authored
// primitive becomes one face at its authored arity, with the per-corner SHD
// rows in authored index order. The fan triangulation this replaced re-used
// corner 0 in every triangle and would have reported the rows {3,7,11} and
// {3,11,19}; the walk of [R-RAST-01 §1] derives its chains from the index ring
// itself, so a split would change which faces paint.
func TestCollectDrawPolysKeepsAuthoredArityAndCornerOrder(t *testing.T) {
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
	vertices[2] = fixedVertex(0, 0, 0)
	vertices[4] = fixedVertex(1, 0, 0)
	vertices[5] = fixedVertex(1, 0, -1)
	vertices[6] = fixedVertex(0, 0, -1)
	polys := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit)
	if len(polys) != 1 {
		t.Fatalf("face count = %d, want 1 (no fan triangulation)", len(polys))
	}
	if len(polys[0].x) != 4 {
		t.Fatalf("corner count = %d, want the authored 4", len(polys[0].x))
	}
	if !polys[0].useSHD {
		t.Fatal("shaded primitive lost its SHD dispatch")
	}
	for corner, want := range []int32{3, 7, 11, 19} {
		if got := polys[0].attr[spanRow][corner]; got != want {
			t.Fatalf("corner %d row = %d, want %d", corner, got, want)
		}
	}
	// Retail's quad mapper defaults the corners to (0,0) (w-1,0) (w-1,h-1)
	// (0,h-1) in vertex-index order, in texels [R-RAST-01 §1].
	for corner, want := range [][2]int32{{0, 0}, {0, 0}, {0, 0}, {0, 0}} {
		if got := [2]int32{polys[0].attr[spanU][corner], polys[0].attr[spanV][corner]}; got != want {
			t.Fatalf("corner %d uv = %v, want %v on this 1x1 frame", corner, got, want)
		}
	}
}

func TestCollectDrawPolysCarriesNoShadeRowToRaster(t *testing.T) {
	c := testModelTextureClient()
	pr := presentationrender.PrimitiveDraw{
		TextureName: "tex", ShadeRow: presentationrender.NoShadeRow,
		VertexIndices: []uint16{0, 1, 2, 3},
	}
	vertices := [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0),
		fixedVertex(1, 0, -1), fixedVertex(0, 0, -1),
	}
	polys := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit)
	if len(polys) != 1 {
		t.Fatalf("face count = %d, want 1", len(polys))
	}
	if polys[0].useSHD {
		t.Fatal("unshaded face retained SHD dispatch")
	}
	for corner := range polys[0].x {
		if got := polys[0].attr[spanRow][corner]; got != 0 {
			t.Fatalf("unshaded face corner %d emitted row %d", corner, got)
		}
	}
}

// TestDefaultUVCornersAreTheFrameSizeInTexels locks the default corner table
// against a frame big enough to distinguish it from a normalised one
// [R-RAST-01 §1][R-REN-03A §5].
func TestDefaultUVCornersAreTheFrameSizeInTexels(t *testing.T) {
	c := testModelTextureClient()
	c.texIndex["big"] = texRef{kind: texStatic, key: "big", frame: &formats.GAFFrame{
		Width: 8, Height: 4, Pixels: make([]byte, 32), Transparent: make([]bool, 32),
	}}
	pr := presentationrender.PrimitiveDraw{TextureName: "big", VertexIndices: []uint16{0, 1, 2, 3}}
	vertices := [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0),
		fixedVertex(1, 0, -1), fixedVertex(0, 0, -1),
	}
	polys := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit)
	if len(polys) != 1 {
		t.Fatalf("face count = %d, want 1", len(polys))
	}
	want := [4][2]int32{{0, 0}, {7, 0}, {7, 3}, {0, 3}}
	for corner, w := range want {
		got := [2]int32{polys[0].attr[spanU][corner], polys[0].attr[spanV][corner]}
		if got != w {
			t.Fatalf("corner %d uv = %v, want %v", corner, got, w)
		}
	}
}

func TestTexturedRasterBypassesSHDOnlyForNoShadeRow(t *testing.T) {
	const source, remapped = uint8(7), uint8(41)
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{source}, Transparent: []bool{false}}
	pal := &palette.Tables{}
	pal.Shade[presentationrender.SHDIdentityRow][source] = remapped

	for _, nanoframe := range []bool{false, true} {
		for _, shaded := range []bool{false, true} {
			c := &Client{width: 8, height: 8, indexed: make([]uint8, 64), pal: pal}
			face := walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{70, 70, 70})
			face.useSHD = shaded
			for i := range face.attr[spanRow] {
				face.attr[spanRow][i] = presentationrender.SHDIdentityRow
			}
			target := newModelTarget(c.width, c.height)
			if nanoframe {
				reveal := presentationRevealKeep()
				c.blitTexturedPolyTarget(target, &face, texture)
				c.revealModelImage(target, nil, &reveal, 0)
			} else {
				c.blitTexturedPolyTarget(target, &face, texture)
			}
			target.commit(c.indexed, c.width, c.height)
			want := source
			if shaded {
				want = remapped
			}
			if got := c.indexed[1*c.width+1]; got != want {
				t.Fatalf("nanoframe=%t shaded=%t pixel=%d want %d", nanoframe, shaded, got, want)
			}
		}
	}
}

func TestCollectDrawPolysUsesPublishedTeamColour(t *testing.T) {
	c := testModelTextureClient()
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{
			Width:  uint16(i + 1),
			Height: uint16(i + 2),
			Pixels: make([]byte, (i+1)*(i+2)),
		}
	}
	entry := &formats.GAFEntry{Name: "logo", Frames: frames}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: entry}
	pr := presentationrender.PrimitiveDraw{TextureName: "logo", VertexIndices: []uint16{0, 1, 2, 3}}
	draw := testPrimitiveDraw(pr, [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, -1), fixedVertex(0, 0, -1),
	})

	for _, tt := range []struct {
		name      string
		selector  teamColor
		wantFrame *formats.GAFFrame
		wantUV    [4][2]int32
	}{
		// The player slot is deliberately absent here: both crossed pairs prove
		// that the published colour byte, rather than the owner value, selects
		// the LOGOS frame [R-RAST-01 §3].
		{name: "slot zero colour seven", selector: teamColor{index: 7, known: true}, wantFrame: frames[7].Frame, wantUV: [4][2]int32{{0, 0}, {7, 0}, {7, 8}, {0, 8}}},
		{name: "slot seven colour zero", selector: teamColor{index: 0, known: true}, wantFrame: frames[0].Frame, wantUV: [4][2]int32{{0, 0}, {0, 0}, {0, 1}, {0, 1}}},
		{name: "unknown selector", selector: teamColor{}},
		{name: "out of range selector", selector: teamColor{index: 10, known: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			polys := c.collectDrawPolys(draw, tt.selector, 1, modelCursorUnit)
			if tt.wantFrame == nil {
				if len(polys) != 0 {
					t.Fatalf("team face count = %d, want no frame", len(polys))
				}
				return
			}
			if len(polys) != 1 {
				t.Fatalf("team face count = %d, want 1", len(polys))
			}
			if polys[0].frame != tt.wantFrame {
				t.Fatalf("team frame = %p, want %p", polys[0].frame, tt.wantFrame)
			}
			for corner, want := range tt.wantUV {
				got := [2]int32{polys[0].attr[spanU][corner], polys[0].attr[spanV][corner]}
				if got != want {
					t.Fatalf("corner %d uv = %v, want %v", corner, got, want)
				}
			}
		})
	}

	// An entry shorter than a selected player colour is likewise a hole. It
	// must not wrap into a valid frame [R-RAST-01 §3].
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames[:7]}}
	if polys := c.collectDrawPolys(draw, teamColor{index: 7, known: true}, 1, modelCursorUnit); len(polys) != 0 {
		t.Fatalf("short team entry produced %d faces, want none", len(polys))
	}
}

func TestProjectileLogoUsesOrdinaryFrameZero(t *testing.T) {
	c := testModelTextureClient()
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(100 + i)}}
	}
	c.texIndex["logo"] = texRef{kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}
	pr := presentationrender.PrimitiveDraw{TextureName: "logo", VertexIndices: []uint16{0, 1, 2, 3}}
	draw := testPrimitiveDraw(pr, [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, -1), fixedVertex(0, 0, -1),
	})
	polys := c.collectDrawPolys(draw, teamColor{}, 1, modelCursorProjectile)
	if len(polys) != 1 {
		t.Fatalf("projectile LOGOS face count = %d, want 1", len(polys))
	}
	if polys[0].frame != frames[0].Frame {
		t.Fatalf("projectile LOGOS frame = %p, want ordinary frame zero %p", polys[0].frame, frames[0].Frame)
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
	polys := c.collectDrawPolys(testPrimitiveDraw(pr, [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, -1), fixedVertex(0, 0, -1),
	}), teamColor{index: 7, known: true}, 1, modelCursorUnit)
	if len(polys) != 1 {
		t.Fatalf("flat team override emitted %d faces, want 1", len(polys))
	}
	if polys[0].frame != nil || polys[0].color != 56 {
		t.Fatalf("flat team override = frame %v color %d, want direct color 56", polys[0].frame, polys[0].color)
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
			// A root piece has no parent; the compiler stores -1 [03 §2.4]. A
			// zero Parent names piece 0 itself, which the hidden-piece walk
			// treats as a malformed cycle.
			compiled: &compiledmodel.Model{Pieces: []compiledmodel.Piece{{
				Parent:     -1,
				Primitives: []compiledmodel.Primitive{{ColorIndex: 56, IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3}}},
				Vertices:   [][3]numeric.Fixed{{0, 0, 0}, {1 << 16, 0, 0}, {1 << 16, 0, -1 << 16}, {0, 0, -1 << 16}},
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
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, -1), fixedVertex(0, 0, -1)}
	if got := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{}, 0, modelCursorFeature); len(got) != 0 {
		t.Fatalf("animated feature with missing identity emitted %d faces", len(got))
	}
	if got := c.animatedGAFFrame("anim", 0, c.texIndex["anim"].entry); got != nil {
		t.Fatal("animated feature sprite with missing identity returned a frame")
	}
}

func TestCollectDrawPolysUsesCameraScale(t *testing.T) {
	c := testModelTextureClient()
	c.cam.Scale = camera.ViewScaleDetail
	// Only Enhanced projects the geometry at the view scale; Original keeps
	// the native projection and doubles the finished image on the blit
	// (DESIGN_GPU_RENDERER §14.2). A Nanolathe presentation rule, not retail.
	c.SetEnhanced(true)
	vertices := [][3]numeric.Fixed{fixedVertex(2, 4, 6), fixedVertex(4, 4, 6), fixedVertex(2, 4, 4), fixedVertex(2, 4, 6)}
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3}}
	polys := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit)
	if len(polys) == 0 {
		t.Fatal("expected projected faces")
	}
	face := &polys[0]
	// The model path is deliberately NOT the world projection: a unit's own
	// position enters the blit as +worldZ, while a model-relative vertex
	// narrows as `Zn = hi16(-vz)` [R-RAST-01 §2]. Zoom then scales the
	// model-relative offset. WorldPos is zero here, so the model-relative
	// value is the vertex itself.
	for i := range vertices {
		zn := int32(-vertices[i][2] >> 16)
		wantX, wantY := c.scaleModelLocal(int32(vertices[i][0]>>16), zn-(int32(vertices[i][1]>>16)>>1))
		if face.x[i] != wantX || face.y[i] != wantY {
			t.Fatalf("corner %d = (%d,%d), want scaled model-local (%d,%d)", i, face.x[i], face.y[i], wantX, wantY)
		}
	}
	// Spelled out for corner 0: x = 2*2, y = 2*(-6 - (4>>1)).
	if face.x[0] != 4 || face.y[0] != -16 {
		t.Fatalf("corner 0 = (%d,%d), want (4,-16)", face.x[0], face.y[0])
	}
	c.SetEnhanced(false)
	polys = c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit)
	if len(polys) == 0 || polys[0].x[0] != 2 || polys[0].y[0] != -8 {
		t.Fatalf("Original at scale 2 must project natively: corner 0 = (%d,%d), want (2,-8)", polys[0].x[0], polys[0].y[0])
	}
}

func TestCollectDrawPolysSuppressesInvalidPrimitive(t *testing.T) {
	c := testModelTextureClient()
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 9, 2}, ShadeRows: []int{1, 2, 3, 4}}
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(0, 0, 1)}
	if got := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit); len(got) != 0 {
		t.Fatalf("invalid primitive emitted %d faces", len(got))
	}
}

func TestCollectDrawPolysSuppressesUnsupportedTexturedNGon(t *testing.T) {
	c := testModelTextureClient()
	vertices := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(2, 0, 1), fixedVertex(1, 0, 2), fixedVertex(0, 0, 1)}
	pr := presentationrender.PrimitiveDraw{TextureName: "tex", VertexIndices: []uint16{0, 1, 2, 3, 4}, ShadeRows: []int{1, 2, 3, 4, 5}}
	if got := c.collectDrawPolys(testPrimitiveDraw(pr, vertices), teamColor{index: 0, known: true}, 1, modelCursorUnit); len(got) != 0 {
		t.Fatalf("unsupported textured n-gon emitted %d faces", len(got))
	}
}
