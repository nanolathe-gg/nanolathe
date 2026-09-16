package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func otaRND02BModel() *model.Model {
	face := func(name string, parent int) model.Piece {
		return model.Piece{
			Name: name, Parent: parent,
			Vertices: [][3]numeric.Fixed{
				{0, 0, 0}, {numeric.Fixed(1 << 16), 0, 0},
				{numeric.Fixed(1 << 16), 0, numeric.Fixed(1 << 16)}, {0, 0, numeric.Fixed(1 << 16)},
			},
			Primitives: []model.Primitive{{
				ColorIndex: 7, VertexIndices: []uint16{0, 1, 2, 3}, TextureName: "tex",
			}},
		}
	}
	return &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "root", Parent: -1, Children: []int{1, 2}},
			face("left", 0),
			face("right", 0),
		},
	}
}

func otaRND02BTables() palette.Tables {
	var tables palette.Tables
	for i := range tables.Logical {
		tables.Logical[i] = byte(i)
		tables.Base[i] = [4]byte{byte(i), 0, 0, 255}
	}
	return tables
}

func otaRND02BPrimitive(t *testing.T, draw *UnitDraw, piece int) PrimitiveDraw {
	t.Helper()
	if draw == nil || piece >= len(draw.Pieces) || len(draw.Pieces[piece].Primitives) != 1 {
		t.Fatalf("unexpected draw shape: %+v", draw)
	}
	return draw.Pieces[piece].Primitives[0]
}

// C1: BMcode=1 takes the true no-SHD path at every orientation. The
// DONT_SHADE bit is deliberately present to prove the unshaded renderer does
// not consult it [R-RND-02A].
func TestOTARND02BMobileBypassesSHDAtEveryOrientation(t *testing.T) {
	mdl := otaRND02BModel()
	states := []model.PieceState{{}, {DontShade: true}, {}}
	tables := otaRND02BTables()
	const source, remapped = byte(7), byte(41)
	for row := range tables.Shade {
		tables.Shade[row][source] = remapped
	}

	for _, heading := range []uint16{0, 8192, 16384, 32768} {
		draw := BuildUnitDrawInto(mdl, states, heading, 0, 0, frame.UnitView{BMCode: true}, nil, &DrawScratch{})
		for piece := 1; piece <= 2; piece++ {
			prim := otaRND02BPrimitive(t, draw, piece)
			if prim.ShadeRow != NoShadeRow || prim.ShadeRows != nil {
				t.Fatalf("heading %d piece %d emitted shade rows: row=%d rows=%v", heading, piece, prim.ShadeRow, prim.ShadeRows)
			}
			// No SHD row means the unshaded span writer, which writes the
			// byte raw [03 R-REN-03A §5].
			got, _, _, _ := tables.RGBA(byte(prim.ColorIndex & 0xFF))
			if got != source {
				t.Fatalf("heading %d piece %d resolved %d want raw texel %d", heading, piece, got, source)
			}
		}
	}
}

// C2 and C3: BMcode=0 with Shading on uses normal-derived rows, while one
// DONT_SHADE piece is pinned to row 15 without changing its sibling
// [03 §2.4.1][R-RND-02A].
func TestOTARND02BStructureShadingAndPerPieceDontShade(t *testing.T) {
	previous := Shading
	Shading = true
	t.Cleanup(func() { Shading = previous })

	mdl := otaRND02BModel()
	draw := BuildUnitDrawInto(mdl, []model.PieceState{{}, {DontShade: true}, {}}, 0, 0, 0, frame.UnitView{BMCode: false}, nil, &DrawScratch{})
	pinned := otaRND02BPrimitive(t, draw, 1)
	computed := otaRND02BPrimitive(t, draw, 2)
	if pinned.ShadeRow != SHDIdentityRow {
		t.Fatalf("DONT_SHADE row %d want %d", pinned.ShadeRow, SHDIdentityRow)
	}
	if computed.ShadeRow == NoShadeRow || computed.ShadeRow == SHDIdentityRow {
		t.Fatalf("sibling did not retain computed row: %d", computed.ShadeRow)
	}

	tables := otaRND02BTables()
	const source, pinnedIndex, computedIndex = byte(7), byte(41), byte(23)
	tables.Shade[pinned.ShadeRow][source] = pinnedIndex
	tables.Shade[computed.ShadeRow][source] = computedIndex
	// Both carry a real SHD row, so the shaded span writer resolves them
	// through SHD[row*256 + byte] [03 R-REN-03A §5].
	gotPinned, _, _, _ := tables.RGBA(tables.Shade[pinned.ShadeRow][byte(pinned.ColorIndex&0xFF)])
	gotComputed, _, _, _ := tables.RGBA(tables.Shade[computed.ShadeRow][byte(computed.ColorIndex&0xFF)])
	if gotPinned != pinnedIndex || gotComputed != computedIndex || gotPinned == gotComputed {
		t.Fatalf("SHD relationship: pinned=%d computed=%d want %d/%d", gotPinned, gotComputed, pinnedIndex, computedIndex)
	}
}

// C4: disabling the one presentation option sends a BMcode=0 structure down
// the same unshaded path as a BMcode=1 unit [R-RND-02A].
func TestOTARND02BShadingOptionOffMatchesMobile(t *testing.T) {
	previous := Shading
	Shading = false
	t.Cleanup(func() { Shading = previous })

	mdl := otaRND02BModel()
	structure := BuildUnitDrawInto(mdl, nil, 0, 0, 0, frame.UnitView{BMCode: false}, nil, &DrawScratch{})
	mobile := BuildUnitDrawInto(mdl, nil, 0, 0, 0, frame.UnitView{BMCode: true}, nil, &DrawScratch{})
	for piece := 1; piece <= 2; piece++ {
		a := otaRND02BPrimitive(t, structure, piece)
		b := otaRND02BPrimitive(t, mobile, piece)
		if a.ShadeRow != NoShadeRow || b.ShadeRow != NoShadeRow || a.ShadeRows != nil || b.ShadeRows != nil {
			t.Fatalf("piece %d did not share no-SHD representation: structure=%+v mobile=%+v", piece, a, b)
		}
	}
}

// C5 stood here: "flat-colored quads bypass SHD on both piece-renderer
// paths". It exercised the render package's PrimitiveRGBA helper, whose flat
// arm returned the raw palette byte whatever SHD row the primitive carried —
// and [03 R-REN-03A §5] puts the SHD split on the RENDERER, not on the
// authored 3DO flat/textured discriminator, so a flat face under the shaded
// renderer writes SHD[row*256 + color]. The test pinned the helper's
// divergence rather than a retail rule, and the helper is gone. The live
// contract is internal/client's TestShadedFlatWriterResolvesThroughSHD and
// TestUnshadedFlatWriterEmitsTheRawColour, which drive the real span writers.
