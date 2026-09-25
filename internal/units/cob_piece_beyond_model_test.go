package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// A script piece declared beyond its model has no render record. Retail's
// flag adapter has no bound test and the thread runs on [04 R-COB-01 §4];
// Nanolathe drops the write and keeps the thread, so Create's following `show`
// of the real piece still lands.
func TestFlagWriteToPieceBeyondModelKeepsThread(t *testing.T) {
	program := &cob.Program{
		Code: []uint32{
			0x10006000, 1, // hide extra (no render record)
			0x10005000, 0, // show base
			0x10065000,
		},
		Scripts:     map[string]int{"Create": 0},
		ScriptsByID: []int{0},
		Pieces:      []string{"base", "extra"},
	}
	def := &content.UnitDef{UnitName: "testunit", Script: program, DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("testunit")}}
	mdl := &model.Model{Name: "testunit", Root: 0, Pieces: []model.Piece{{Name: "base", Parent: -1}}}
	u := &Unit{Def: def}
	sim := rng.NewSimulation(1)
	binding, err := BindCOBWithPortsAndVisibilityForUnit(nil, u, mdl, &sim, nil, nil)
	if err != nil {
		t.Fatalf("bind with a piece beyond the model: %v", err)
	}
	if got := binding.PieceMap; len(got) != 2 || got[0] != 0 || got[1] != -1 {
		t.Fatalf("piece map = %v, want [0 -1]", got)
	}
	if len(u.RenderPieceFlags) != 1 || u.RenderPieceFlags[0]&0x01 == 0 {
		t.Fatalf("model flags = %v: Create stopped at the unbacked hide instead of showing base", u.RenderPieceFlags)
	}
}
