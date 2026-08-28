package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestOTARND02BPieceShadeAndFlatPolicy locks the effective presentation
// contract: the per-piece state selects identity SHD row 15 for textured
// primitives, while flat-colored primitives bypass SHD regardless of that
// state [03 §2.4.1] [03 §4.3.2] [R-RND-02A].
func TestOTARND02BPieceShadeAndFlatPolicy(t *testing.T) {
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Name:   "base",
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{0, 0, 0}, {numeric.Fixed(1 << 16), 0, 0},
				{numeric.Fixed(1 << 16), 0, numeric.Fixed(1 << 16)}, {0, 0, numeric.Fixed(1 << 16)},
			},
			Primitives: []model.Primitive{{
				ColorIndex:    7,
				VertexIndices: []uint16{0, 1, 2, 3},
				TextureName:   "tex",
			}},
		}},
	}

	regular := BuildPieceDraws(mdl, []model.PieceState{{}}, [3]numeric.Fixed{}, false)
	identity := BuildPieceDraws(mdl, []model.PieceState{{DontShade: true}}, [3]numeric.Fixed{}, false)
	if len(regular) != 1 || len(identity) != 1 || len(regular[0].Primitives) != 1 || len(identity[0].Primitives) != 1 {
		t.Fatalf("unexpected draw shape: regular=%+v identity=%+v", regular, identity)
	}
	if regular[0].Primitives[0].ShadeRow == SHDIdentityRow {
		t.Fatalf("regular textured piece selected identity row %d", SHDIdentityRow)
	}
	if identity[0].Primitives[0].ShadeRow != SHDIdentityRow {
		t.Fatalf("dont-shade textured piece row %d want %d", identity[0].Primitives[0].ShadeRow, SHDIdentityRow)
	}

	var tables palette.Tables
	for i := range tables.Logical {
		tables.Logical[i] = byte(i)
		tables.Base[i] = [4]byte{byte(i), 0, 0, 255}
	}
	const source, computed, identityIndex = byte(7), byte(23), byte(41)
	tables.Shade[regular[0].Primitives[0].ShadeRow][source] = computed
	tables.Shade[SHDIdentityRow][source] = identityIndex

	gotRegular, _, _, _ := PrimitiveRGBA(&tables, regular[0].Primitives[0])
	if gotRegular != computed {
		t.Fatalf("regular textured pixel used row result %d want %d", gotRegular, computed)
	}
	gotIdentity, _, _, _ := PrimitiveRGBA(&tables, identity[0].Primitives[0])
	if gotIdentity != identityIndex {
		t.Fatalf("dont-shade textured pixel used row result %d want %d", gotIdentity, identityIndex)
	}

	flat := identity[0].Primitives[0]
	flat.IsColored = 1
	flat.ColorIndex = uint32(source)
	flat.ShadeRow = regular[0].Primitives[0].ShadeRow
	gotFlat, _, _, _ := PrimitiveRGBA(&tables, flat)
	if gotFlat != source {
		t.Fatalf("flat-colored pixel used SHD result %d want source %d", gotFlat, source)
	}
}
