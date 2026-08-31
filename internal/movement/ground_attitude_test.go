package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestGroundConformFeedsNextTickCapAndPersists(t *testing.T) {
	terrain := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)}
	for z := int32(0); z < terrain.CellH; z++ {
		for x := int32(0); x < terrain.CellW; x++ {
			terrain.Plot[z*terrain.CellW+x].SetHeight(uint8(z * 12))
		}
	}
	plate := &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Selection: true,
			Vertices: [][3]numeric.Fixed{
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(-8)},
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(-8)},
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(8)},
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(8)},
			},
			Primitives: []model.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}
	u := &units.Unit{
		Def: &content.UnitDef{MaxVelocity: 100000},
		X:   numeric.FixedFromInt(32), Z: numeric.FixedFromInt(64),
		Y:           numeric.Fixed(7<<16 | 0x1234),
		ScriptState: &units.ScriptState{Binding: &cob.Binding{Model: plate}},
	}
	steer := &SteerState{MaxVelocity: 100000}
	initialCap := steer.SpeedCapForPitch(int16(u.Move.Pitch))
	applyGroundPostMove(terrain, u, true, 1)
	if u.Move.Pitch == 0 {
		t.Fatal("terrain conform left pitch at zero")
	}
	if got, want := int64(u.Y)>>16, int64(48); got != want {
		t.Fatalf("conformed integer height=%d, want %d", got, want)
	}
	if got := int64(u.Y) & 0xffff; got != 0x1234 {
		t.Fatalf("conform changed Y fraction %#x", got)
	}
	nextCap := steer.SpeedCapForPitch(int16(u.Move.Pitch))
	if nextCap >= initialCap {
		t.Fatalf("next-tick cap=%d, initial cap=%d; pitch was not consumed", nextCap, initialCap)
	}

	pitch, bank, y := u.Move.Pitch, u.Move.Bank, u.Y
	applyGroundPostMove(terrain, u, false, 1)
	if u.Move.Pitch != pitch || u.Move.Bank != bank || u.Y != y {
		t.Fatalf("clean next tick rewrote persisted ground pose: y=%v/%v pitch=%d/%d bank=%d/%d", u.Y, y, u.Move.Pitch, pitch, u.Move.Bank, bank)
	}
}
