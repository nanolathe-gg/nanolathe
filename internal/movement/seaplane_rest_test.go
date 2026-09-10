package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestLandedSeaplaneRestsOnTheSeabed locks where a landed amphibious aircraft
// comes to rest over deep water [04 R-AIR-01 §6 "Touchdown"].
//
// The water-landing leg commands the terrain height, the flight integrator
// descends to it with no sea-level term [04 §10.1], and on the touchdown tick
// the post-move correction's four-corner conform writes the integer height from
// the raw terrain bytes under the ground plate [04 R-MOV-01 §5]. Over water
// those bytes are the seabed, and sea level is read on that path only inside
// the `canhover` arm, which no aircraft takes. The regression this guards is a
// well-meant surface floor: the play-test expectation that a seaplane floats
// has no producer in retail, whose renderer draws the landed seaplane
// submerged and blue-tinted like a submarine [03 R-REN-03A §8].
//
// The fixture is deep water over a seabed that slopes along Z — four height
// units per cell under a sea level of 120, so the aircraft's cell lies some
// 88 units down, the depth every stock naval map has under open water. The
// slope is what makes the touchdown conform observable: the descent snaps
// exactly onto the commanded Y, so on a flat bed the integer height alone
// cannot tell "conformed" from "never corrected", but the conform also
// writes pitch from the plate's rise over run, and this definition's
// `pitchscale` is zero, so the levelled pitch the mode setter leaves is
// exactly zero without it.
func TestLandedSeaplaneRestsOnTheSeabed(t *testing.T) {
	const retailAircraftWaterDepth = 255 // [fmt tdf "Duplicate keys"]
	sys, w, u := waterAirFixtureFor(t, true, retailAircraftWaterDepth)
	ter := sys.Terrain
	ter.SeaLevel = 120
	for cz := int32(0); cz < ter.CellH; cz++ {
		for cx := int32(0); cx < ter.CellW; cx++ {
			h := uint8(cz * 4)
			p := &ter.Plot[cz*ter.CellW+cx]
			p.SetHeight(h)
			p.SetMinHeight(h)
			p.SetMaxHeight(h)
		}
	}
	// The unit was created on the fixture's own bed; start it on this one so
	// the climb and the descent both measure against it.
	u.Y = ter.HeightAt(u.X, u.Z)
	if fl := handleRow(sys.Flights, u.Handle); fl != nil {
		fl.Y = int32(u.Y.Raw())
	}
	// A root selection plate is what the conform samples; without one the
	// correction abandons and writes nothing [04 R-MOV-01 §5].
	plate := &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Selection: true,
			Vertices: [][3]numeric.Fixed{
				{numeric.FixedFromInt(4), 0, numeric.FixedFromInt(4)},
				{numeric.FixedFromInt(-4), 0, numeric.FixedFromInt(4)},
				{numeric.FixedFromInt(-4), 0, numeric.FixedFromInt(-4)},
				{numeric.FixedFromInt(4), 0, numeric.FixedFromInt(-4)},
			},
			Primitives: []model.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}
	u.ScriptState = &units.ScriptState{Binding: &cob.Binding{Model: plate}}

	if !flyThenLand(t, sys, w, u) {
		t.Fatalf("the seaplane never returned to grounded mode 1 (mode=%d)", u.Move.Mode&0x3)
	}
	sea := int64(ter.SeaLevel)
	got := int64(u.Y) >> 16
	if got >= sea {
		t.Fatalf("landed height %d is at or above the sea level %d: a surface floor was invented; the touchdown conform "+
			"writes the raw seabed bytes and nothing in retail floors them [04 R-AIR-01 §6 \"Touchdown\"]", got, sea)
	}
	// The seabed under the aircraft: the bilinear byte at its position, which on
	// a linear slope is the plate's pairwise corner average to within the
	// conform's truncation.
	bed := int64(ter.HeightAt(u.X, u.Z)) >> 16
	if got < bed-1 || got > bed+1 {
		t.Fatalf("landed height %d, want the seabed %d under sea level %d", got, bed, sea)
	}
	if u.Move.Pitch == 0 {
		t.Fatal("pitch is zero on a sloping seabed: the touchdown tick's post-move correction did not run, so the " +
			"four-corner conform never wrote the resting height and attitude [04 R-MOV-01 §5][04 R-AIR-01 §6 \"Touchdown\"]")
	}
	fl := handleRow(sys.Flights, u.Handle)
	if fl == nil || fl.Y != int32(u.Y.Raw()) {
		t.Fatalf("the integrator's Y copy does not follow the resting height")
	}

	// Nothing writes Y again while the aircraft sits: the velocity triple is
	// zero and the mode mirror agrees, so the commit never enters.
	rest := u.Y
	tick := uint32(2000)
	for i := 0; i < 60; i++ {
		tick++
		runMovementTick(sys, tick, w)
	}
	if u.Y != rest || u.Move.Mode&0x3 != 1 {
		t.Fatalf("a parked seaplane moved: y %v -> %v, mode %d", rest, u.Y, u.Move.Mode&0x3)
	}
}
