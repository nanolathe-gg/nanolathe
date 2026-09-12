package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestGAFGlyphRawModeReplacesKeyAndReadsDestination(t *testing.T) {
	pal := &palette.Tables{}
	pal.Light[9*256+4] = 21
	pal.Light[7*256+4] = 22
	c := &Client{width: 5, height: 1, indexed: []byte{4, 4, 4, 4, 4}}
	f := &formats.GAFFrame{Width: 5, Height: 1, ColorKey: 9, Pixels: []byte{9, 2, 7, 32, 7}, Transparent: []bool{true, false, false, false, false}}
	c.emitSprite(drawlist.Sprite{Frame: f, Kind: drawlist.BlitLit, LightRow: 2, Pal: pal, HasClip: true, Clip: drawlist.Rect{W: 4, H: 1}})
	c.replayForTest()
	for i, want := range []byte{21, 4, 22, 4, 4} {
		if c.indexed[i] != want {
			t.Fatalf("pixel %d=%d, want %d", i, c.indexed[i], want)
		}
	}
}

func TestGAFGlyphRLEUsesModeRowAndRunTransparency(t *testing.T) {
	pal := &palette.Tables{}
	pal.Light[2*256+9] = 21
	pal.Light[2*256+2] = 22
	c := &Client{width: 3, height: 1, indexed: []byte{4, 4, 4}}
	f := &formats.GAFFrame{Width: 3, Height: 1, Compressed: 1, ColorKey: 9, Pixels: []byte{9, 2, 7}, Transparent: []bool{false, false, true}}
	c.UIBlitLit(f, 0, 0, pal, 2)
	c.replayForTest()
	for i, want := range []byte{21, 22, 4} {
		if c.indexed[i] != want {
			t.Fatalf("pixel %d=%d, want %d", i, c.indexed[i], want)
		}
	}
}

func TestGAFGlyphCompositeSendsEveryChildThroughALP(t *testing.T) {
	pal := &palette.Tables{}
	pal.Alpha[5*256+4] = 6
	pal.Alpha[7*256+6] = 8
	c := &Client{width: 4, height: 1, indexed: []byte{4, 4, 4, 4}, pal: pal}
	f := &formats.GAFFrame{Width: 1, Height: 1, XOffset: 2, Subframes: []*formats.GAFFrame{gafLeaf(5, 0, 0), gafLeaf(7, 0, 0)}}
	c.UIBlitLit(f, 0, 0, pal, 2)
	c.replayForTest()
	if c.indexed[2] != 8 || c.indexed[0] != 4 {
		t.Fatalf("composite pixels %v, want child ALP chain outside parent canvas", c.indexed)
	}
	c.indexed[2] = 4
	c.resetListForTest()
	c.UIBlitLit(f, 0, 0, nil, 2)
	c.replayForTest()
	if c.indexed[2] != 4 {
		t.Fatal("missing light table admitted composite")
	}
}

func TestGAFFogChildrenRetainOperationOrderAndRawGate(t *testing.T) {
	pal := &palette.Tables{}
	pal.Gray[4], pal.Gray[5] = 5, 6
	f := &formats.GAFFrame{Width: 1, Height: 1, XOffset: 12, Subframes: []*formats.GAFFrame{gafLeaf(7, 0, 0), gafLeaf(8, 0, 0)}}
	f.Subframes[1].AlternateBlitter = 1
	for _, mode := range []fogBlitMode{fogBlitGray, fogBlitPatterned} {
		c := &Client{width: 3, height: 1, indexed: []byte{4, 4, 4}, pal: pal}
		c.blitFogGAF(f, 1, 0, mode)
		want := byte(6)
		if mode == fogBlitPatterned {
			want = 0
		}
		if c.indexed[1] != want {
			t.Fatalf("mode %v pixels %v, want both children under own operation", mode, c.indexed)
		}
		f.Subframes[1].Compressed = 1
		c.indexed[1] = 4
		c.blitFogGAF(f, 1, 0, mode)
		want = 5
		if mode == fogBlitPatterned {
			want = 0
		}
		if c.indexed[1] != want {
			t.Fatal("compressed child bypassed raw gate")
		}
		f.Compressed = 1
		c.indexed[1] = 4
		c.blitFogGAF(f, 1, 0, mode)
		if c.indexed[1] != 4 {
			t.Fatal("compressed parent recursed before raw gate")
		}
		f.Compressed = 0
		f.Subframes[1].Compressed = 0
	}
}

func TestProjectileTextureConsumerRejectsCompositeInsteadOfFlattening(t *testing.T) {
	c := testModelTextureClient()
	pr := presentationrender.PrimitiveDraw{TextureName: "direct", VertexIndices: []uint16{0, 1, 2, 3}}
	draw := testPrimitiveDraw(pr, [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(1, 0, 0), fixedVertex(1, 0, -1), fixedVertex(0, 0, -1)})
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "direct", Frames: []formats.GAFWriteFrame{{Width: 1, Height: 1, Subframes: []formats.GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{7}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	bank, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	c.texIndex["direct"] = texRef{kind: texStatic, key: "direct", frame: bank.Entries[0].Frames[0].Frame}
	if polys := c.collectDrawPolys(draw, teamColor{}, 1, modelCursorProjectile); len(polys) != 0 {
		t.Fatal("projectile consumer flattened composite")
	}
	if polys := c.collectDrawPolys(draw, teamColor{}, 1, modelCursorUnit); len(polys) != 1 {
		t.Fatal("untraced structure consumer was changed")
	}
}
