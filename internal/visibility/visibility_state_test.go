package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

func oneCellShape() *content.SightShapes {
	return &content.SightShapes{Shapes: []content.SightShape{{
		W: 1, H: 1, Opaque: []bool{true},
	}}}
}

func TestInvalidPlayerDoesNotAliasVisibilityState(t *testing.T) {
	s := New(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetShapes(oneCellShape())
	s.Publish(0, 4, 4, 1, 0)
	word := append([]uint16(nil), s.wordMask...)
	bytes := append([]uint8(nil), s.byteGrids[0]...)
	s.Publish(10, 4, 4, 1, 0)
	if got := s.Unpublish(10, 4, 4, 1, 0); got {
		t.Fatal("invalid owner reported a changed cell")
	}
	s.Refresh(1, Observer{Owner: 10, CX: 4, CZ: 4, HeightByte: 1})
	for i := range word {
		if s.wordMask[i] != word[i] || s.byteGrids[0][i] != bytes[i] {
			t.Fatalf("invalid owner changed slot-zero state at %d", i)
		}
	}
	if s.ByteGrid(10) != nil {
		t.Fatal("invalid owner returned a byte grid")
	}
}

func TestCoverageByteCounterWraps(t *testing.T) {
	s := New(&world.Terrain{CellW: 2, CellH: 2}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.byteGrids[0][0] = 255
	if !s.incByteGrid(0, 0) || s.byteGrids[0][0] != 0 {
		t.Fatalf("increment did not wrap 255 to 0: %d", s.byteGrids[0][0])
	}
	if !s.decByteGrid(0, 0) || s.byteGrids[0][0] != 255 {
		t.Fatalf("decrement did not wrap 0 to 255: %d", s.byteGrids[0][0])
	}
}

func TestObserverCoverageRetiresOnOwnerChange(t *testing.T) {
	s := New(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetShapes(oneCellShape())
	s.SetLocal(0)
	s.Refresh(7, Observer{Owner: 0, CX: 6, CZ: 9, HeightByte: 200, Radius: 0})
	old := int(9*s.W + 6)
	if s.byteGrids[0][old] != 1 {
		t.Fatal("initial stored footprint was not published")
	}
	s.Refresh(7, Observer{Owner: 1, CX: 6, CZ: 9, HeightByte: 200, Radius: 0})
	if s.byteGrids[0][old] != 0 || s.byteGrids[1][old] != 1 {
		t.Fatal("owner change did not retire old coverage before republishing")
	}
	if s.RetireObserver(7) || s.byteGrids[1][old] != 0 {
		t.Fatal("retirement did not remove the stored owner-one footprint")
	}
	if s.RetireObserver(7) {
		t.Fatal("retiring a deleted observer reported a change")
	}
}

func TestSensorVisibilityUsesSinglePoint(t *testing.T) {
	s := New(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	// Coverage is deliberately on a different tile. Owner 0 must not get the
	// gameplay owner bypass in the sensor's single-point final pass.
	s.incByteGrid(int(2*s.W+2), 0)
	var status uint32
	units := []SensorUnit{{Owner: 1, Status: &status, Alive: true, Active: true, X: 0, Y: 0, Z: 0, RadarDistance: 100, SonarDistance: 200}}
	s.SensorTick(1, 2, units)
	if status&SeenBit != 0 {
		t.Fatal("sensor final pass applied owner bypass or hull samples")
	}
	if len(s.SensorInputs()) != 1 || s.SensorInputs()[0].Owner != 1 {
		t.Fatal("sensor contact snapshot missing the active unit")
	}
	beforeWord := append([]uint16(nil), s.wordMask...)
	beforeByte := append([]uint8(nil), s.byteGrids[0]...)
	s.SensorTick(2, 2, units)
	if len(s.SensorInputs()) != 1 || s.SensorInputs()[0].Status != status {
		t.Fatal("sensor snapshot was not refreshed from authoritative status")
	}
	for i := range beforeWord {
		if s.wordMask[i] != beforeWord[i] || s.byteGrids[0][i] != beforeByte[i] {
			t.Fatalf("sensor visibility modified LOS stores at %d", i)
		}
	}
}
