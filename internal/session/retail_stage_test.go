package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestReserveRetailUnitsValidatesBeforeAllocation(t *testing.T) {
	def := &content.UnitDef{UnitName: "armcom"}
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
	if _, err := reserveRetailUnits(w, cat, []save.UnitRecord{record}); err == nil {
		t.Fatal("null stable slot accepted")
	}
	if got := w.Used(); got != before {
		t.Fatalf("failed stable identity validation allocated %d units", got)
	}
}
