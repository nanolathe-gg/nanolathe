package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The order port must invoke the signed altitude setter, rather than merely
// copy its flag: terrain-derived Y and vertical arrival depend on it
// [04 R-AIR-01 §4][04 R-ORD-01 §4, §7][04 R-ORD-02 §4].
func TestConstructorAirGoalAltitudeSetter(t *testing.T) {
	for _, offset := range []int16{50, -3, -16384} {
		terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16), SeaLevel: 20}
		for i := range terrain.Plot {
			terrain.Plot[i].SetHeight(30)
		}
		w := units.NewSliced(2, nil)
		h, err := w.Create(&content.UnitDef{CanFly: true, BMCode: 1, MaxDamage: 10, CruiseAlt: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}, Pieces: []string{"base"}}}, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		s := NewSystem(terrain, Profile{}, NewOccupancyGrid())
		s.BindWorld(w)
		setHandleRow(&s.Flights, h, &FlightState{})
		n := &orders.Node{Owner: h}
		if !s.InstallAirGoal(orders.AirGoalRequest{Owner: h, Node: n, X: 3 << 16, Y: numeric.Fixed(int32(offset) << 16), Z: 3 << 16, Flags: 0x08}) {
			t.Fatal("marker refused")
		}
		m := handleRow(s.Flights, h).Command.Payload.(*airMarker)
		wantY := int32(30+int32(offset)) << 16
		if m.altOffset != offset || int32(m.goal.Y) != wantY {
			t.Fatalf("offset %d: marker offset=%d Y=%d, want %d", offset, m.altOffset, m.goal.Y, wantY)
		}
		u := s.unitFor(h)
		u.X, u.Z, u.Y = m.goal.X, m.goal.Z, m.goal.Y+(1<<16)
		if !m.Arrived(u) {
			t.Fatal("inclusive one-unit altitude boundary refused")
		}
		u.Y++
		if m.Arrived(u) {
			t.Fatal("vertical arrival ignored the setter")
		}
	}
}

func TestHalfCruiseAltitudeSignedWordBoundary(t *testing.T) {
	for _, c := range []struct {
		in   int32
		want int16
	}{{-32768, -16384}, {32768, -16384}, {65533, -1}, {-3, -1}, {65539, 1}} {
		if got := HalfCruiseAlt(c.in); got != c.want {
			t.Errorf("HalfCruiseAlt(%d)=%d, want %d", c.in, got, c.want)
		}
	}
}
