package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func fragmentDrawFixture() (*Client, frame.FragmentView, *formats.GAFFrame) {
	c := testModelTextureClient()
	c.width, c.height = 64, 64
	c.indexed = make([]byte, 64*64)
	first := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{31}}
	second := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{77}}
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: first}, {Frame: second}}}
	key := modelTextureLoadKey{kind: modelLoadUnit, id: "fragment"}
	m := &unitModel{compiled: &model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1, Primitives: []model.Primitive{{TextureName: "skin"}}}}}}
	c.modelTextures = &ModelTextureRegistry{
		unitByID: map[uint16]modelTextureLoadKey{1: key},
		loads:    map[modelTextureLoadKey]*unitModel{key: m},
		primary:  map[string]texRef{"skin": {kind: texAnimated, entry: entry}},
	}
	v := frame.FragmentView{Slot: 2, UnitDefID: 1, MaterialValid: true, FrameIndex: 1, Position: fixedVertex(20, 0, 20)}
	v.Vertices = [8][3]numeric.Fixed{fixedVertex(-5, 0, 5), fixedVertex(5, 0, 5), fixedVertex(5, 0, -5), fixedVertex(-5, 0, -5), fixedVertex(-5, -1, -5), fixedVertex(5, -1, -5), fixedVertex(5, -1, 5), fixedVertex(-5, -1, 5)}
	return c, v, second
}

func TestFragmentFrozenMaterialAndFaceProjection(t *testing.T) {
	c, v, want := fragmentDrawFixture()
	for _, kind := range []texKind{texAnimated, texTeam} {
		ref := c.modelTextures.primary["skin"]
		ref.kind = kind
		c.modelTextures.primary["skin"] = ref
		if got := c.fragmentTexture(v); got != want {
			t.Fatalf("kind %d lost frozen frame", kind)
		}
	}
	v.Angles = [3]uint16{2048, 4096, 8192}
	// Compare the fragment's standalone rotation against the existing detached
	// piece entry after mapping fragment Z/Y/X lanes to debris X/Y/Z,
	// including fractional world placement [04 R-COB-04 §3][03 R-COMP-02 §6].
	v.Position[0]++
	polys := c.collectFragmentPolys(v, want)
	m := model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1, Vertices: v.Vertices[:]}}}
	draw := render.BuildDebrisModelPieceInto(&m, frame.DebrisView{X: v.Position[0], Y: v.Position[1], Z: v.Position[2], Angles: [3]uint16{v.Angles[2], v.Angles[1], v.Angles[0]}}, nil)
	// Template order and side-face material are retail contracts, independent
	// of the renderer's index table [04 R-COB-04 §5].
	rings := [6][4]int{{0, 1, 2, 3}, {2, 1, 6, 5}, {0, 3, 4, 7}, {1, 0, 7, 6}, {3, 2, 5, 4}, {4, 5, 6, 7}}
	if len(polys) != len(rings) {
		t.Fatalf("fragment faces = %d, want six template quads", len(polys))
	}
	for face := range polys {
		for corner := 0; corner < 4; corner++ {
			x, y := c.modelDirectVertex(draw.Pieces[0].WorldVertices[rings[face][corner]], v.Position)
			if polys[face].x[corner] != x || polys[face].y[corner] != y || polys[face].useSHD || polys[face].frame != want {
				t.Fatal("fragment changed standalone projection or shading")
			}
		}
	}
	v.MaterialValid = false
	if c.fragmentTexture(v) != nil {
		t.Fatal("unresolved material drew guessed art")
	}
}

func TestFragmentFixedWalkAndBothPackets(t *testing.T) {
	for _, modern := range []bool{false, true} {
		c, v, _ := fragmentDrawFixture()
		c.geometryOnlyModels = modern
		later := v
		later.Slot = 1
		later.FrameIndex = 0
		effects := []frame.EffectView{{FragmentSlot: 2, Strip: -1}, {FragmentSlot: 1, Strip: -1}}
		stats := c.DrawEffectViews(effects, EffectDrawOptions{Fragments: []frame.FragmentView{later, v}})
		if stats.Models != 2 {
			t.Fatalf("modern=%v stats=%+v", modern, stats)
		}
		commands := c.list.ModelCommands()
		if len(commands) != 2 {
			t.Fatalf("got %d models", len(commands))
		}
		if modern {
			geometry := commands[0].Geometry
			if geometry.Width >= int32(c.width) || geometry.Height >= int32(c.height) {
				t.Fatal("small fragment allocated a viewport-sized GPU target")
			}
			if commands[0].Geometry.Faces[0].Texture.Pixels[0] != 77 || commands[1].Geometry.Faces[0].Texture.Pixels[0] != 31 || commands[0].Geometry.KeyPlane {
				t.Fatal("fragment packet lost fixed-effect order or unkeyed path")
			}
		} else {
			c.list.Replay(c.classicSink())
			if got := c.indexed[20*64+20]; got != 31 {
				t.Fatalf("classic painter-order pixel=%d, want 31", got)
			}
		}
	}
}
