package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
)

// Admission copies the selected frame, even though the source animation and
// owning player's colour remain mutable [04 R-COB-04 §3].
func TestFragmentMaterialFreezesAdmissionFrame(t *testing.T) {
	r := newModelTextureRegistry(nil, false)
	load := modelTextureLoadKey{kind: modelLoadUnit, id: "7"}
	m := &compiledmodel.Model{Pieces: []compiledmodel.Piece{{Primitives: []compiledmodel.Primitive{
		{TextureName: "animated"}, {TextureName: "logo"}, {TextureName: "static"}, {TextureName: "missing"},
	}}}}
	r.unitByID[7] = load
	r.loads[load] = &unitModel{compiled: m}
	animated := &formats.GAFEntry{Unknown1: 1, Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{1}}},
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{2}}},
	}}
	r.primary["animated"] = texRef{kind: texAnimated, key: "authored|animated", entry: animated}
	logo := &formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 10)}
	for i := range logo.Frames {
		logo.Frames[i].Frame = &formats.GAFFrame{Pixels: []byte{byte(i)}}
	}
	r.logos["logo"] = texRef{kind: texTeam, entry: logo}
	r.primary["static"] = texRef{kind: texStatic, frame: &formats.GAFFrame{Pixels: []byte{3}}}
	r.bindPiece(m, 0, load)
	r.StepPhase7()
	frozen := r.FreezeFragmentMaterial(7, 0, 0, 3)
	team := r.FreezeFragmentMaterial(7, 0, 1, 3)
	static := r.FreezeFragmentMaterial(7, 0, 2, 3)
	if !frozen.Valid || frozen.FrameIndex != 1 || !team.Valid || team.FrameIndex != 3 || !static.Valid || static.FrameIndex != 0 {
		t.Fatalf("admission materials: animated=%+v logo=%+v static=%+v", frozen, team, static)
	}
	r.StepPhase7()
	next := r.FreezeFragmentMaterial(7, 0, 0, 8)
	recoloured := r.FreezeFragmentMaterial(7, 0, 1, 8)
	if next.FrameIndex != 0 || recoloured.FrameIndex != 8 || frozen.FrameIndex != 1 || team.FrameIndex != 3 {
		t.Fatalf("source advance changed frozen frame: frozen=%+v logo=%+v next=%+v recoloured=%+v", frozen, team, next, recoloured)
	}
	if r.FreezeFragmentMaterial(7, 0, 1, 255).Valid || r.FreezeFragmentMaterial(7, 0, 3, 0).Valid {
		t.Fatal("unassigned logo or missing art resolved a frame")
	}
	if frozen.UnitDefID != 7 || frozen.PieceIndex != 0 || frozen.PrimitiveIndex != 0 {
		t.Fatalf("lost source material identity: %+v", frozen)
	}
}
