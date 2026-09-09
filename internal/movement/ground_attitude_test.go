package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
	applyGroundPostMove(terrain, u, true, 1, nil)
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
	applyGroundPostMove(terrain, u, false, 1, nil)
	if u.Move.Pitch != pitch || u.Move.Bank != bank || u.Y != y {
		t.Fatalf("clean next tick rewrote persisted ground pose: y=%v/%v pitch=%d/%d bank=%d/%d", u.Y, y, u.Move.Pitch, pitch, u.Move.Bank, bank)
	}
}

// TestGroundConformRollOnRingOrderedPlate locks the roll run against the way
// stock content actually authors a ground plate. Retail's selection primitive
// runs as a ring — (+x,+z), (-x,+z), (-x,-z), (+x,-z) — so corners 1 and 2
// share an X. Taking the roll run between those two corners is a zero divisor
// for 522 of the 538 stock models that carry a root selection primitive, and
// the arc tangent then answers a quarter circle for any cross-slope at all,
// which lays a vehicle on its side [04 R-MOV-01 §5a]. The run belongs between
// the two corners the numerator differences, corner 0 and corner 1.
//
// The terrain here rises purely along X, so pitch must stay level and roll must
// be the modest angle the slope actually has.
func TestGroundConformRollOnRingOrderedPlate(t *testing.T) {
	terrain := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)}
	for z := int32(0); z < terrain.CellH; z++ {
		for x := int32(0); x < terrain.CellW; x++ {
			terrain.Plot[z*terrain.CellW+x].SetHeight(uint8(x * 12))
		}
	}
	plate := &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Selection: true,
			// Ring order, as [fmt 3do] "Selection primitive" content authors it.
			Vertices: [][3]numeric.Fixed{
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(8)},
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(8)},
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(-8)},
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(-8)},
			},
			Primitives: []model.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}
	u := &units.Unit{
		Def: &content.UnitDef{MaxVelocity: 100000},
		X:   numeric.FixedFromInt(64), Z: numeric.FixedFromInt(64),
		ScriptState: &units.ScriptState{Binding: &cob.Binding{Model: plate}},
	}
	applyGroundPostMove(terrain, u, true, 1, nil)

	if u.Move.Pitch != 0 {
		t.Fatalf("cross-slope only: pitch=%d, want 0", u.Move.Pitch)
	}
	const quarter = 16384
	roll := int16(u.Move.Bank)
	if roll == 0 {
		t.Fatal("roll stayed level on a slope that rises along X")
	}
	if roll == quarter || roll == -quarter {
		t.Fatalf("roll=%d is a quarter circle: the run divisor is degenerate", roll)
	}
	// The plate corners land at world X 72 and 56, whose interpolated height
	// bytes are 54 and 42. A rise of 12 over the 16 world-unit run is
	// atan(12/16) = 36.87 degrees, which is that fraction of the 65536-unit
	// circle.
	if want := int16(6712); roll != want {
		t.Fatalf("roll=%d, want %d for rise 12 over run 16", roll, want)
	}
}
