package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestReserveRetailUnitsValidatesBeforeAllocation(t *testing.T) {
	def := &content.UnitDef{UnitName: "armcom", Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"armcom": def}}
	w := units.NewSliced(1, cat)
	record := save.UnitRecord{Number: 0, StableID: 1, Data: make([]byte, save.UnitBoxSize)}
	copy(record.Data, []byte("armcom"))
	record.Data[0x20] = 10 // outside the ten fixed player slices
	before := w.Used()
	if _, err := reserveRetailUnits(w, cat, []save.UnitRecord{record}); err == nil {
		t.Fatal("invalid owner accepted")
	}
	if got := w.Used(); got != before {
		t.Fatalf("failed identity validation allocated %d units", got)
	}
	// A malformed stable identity must be rejected before any later field is
	// inspected; keep this explicit to lock the atomic staging boundary.
	record.Data[0x20] = 0
	binary.LittleEndian.PutUint16(record.Data[0x21:], 0)
	record.StableID = 0
	if _, err := reserveRetailUnits(w, cat, []save.UnitRecord{record}); err == nil {
		t.Fatal("null stable slot accepted")
	}
	if got := w.Used(); got != before {
		t.Fatalf("failed stable identity validation allocated %d units", got)
	}
}

func TestReserveRetailUnitsSkipsCompatibilityNullRecords(t *testing.T) {
	def := &content.UnitDef{UnitName: "armcom", Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"armcom": def}}
	w := units.NewSliced(4, cat)
	standard := func(number int, id uint16) save.UnitRecord {
		data := make([]byte, save.UnitBoxSize)
		copy(data, []byte("armcom"))
		binary.LittleEndian.PutUint16(data[0x21:], id)
		return save.UnitRecord{Number: number, StableID: id, Data: data}
	}
	stable, err := reserveRetailUnits(w, cat, []save.UnitRecord{
		standard(0, 1),
		{Number: 1, Compat: true, Data: make([]byte, save.UnitBoxCompatSize)},
		standard(2, 3),
	})
	if err != nil {
		t.Fatalf("mixed standard/null records: %v", err)
	}
	if w.Used() != 2 || stable[1] == 0 || stable[3] == 0 {
		t.Fatalf("null record was allocated or standard slots lost: used=%d stable=%v", w.Used(), stable)
	}
}
