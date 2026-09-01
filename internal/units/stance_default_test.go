package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestCreationSeedsBothStandingFieldsFromTheDefinition locks the creation half
// of [04 R-STANCE-01 §6]: both standing fields are seeded at unit creation from
// the definition's packed standing byte, whose two keys each parse with a
// default of 2.
//
// Before this landed nothing seeded them, so every unit in every battle was
// created at move 0 and fire 0 — hold position and hold fire — and the
// auto-engage issuer of [04 R-STANCE-01 §3], which refuses on either zero,
// could never fire. The relationship this asserts is exactly that: the field a
// definition authors is the field the unit carries.
func TestCreationSeedsBothStandingFieldsFromTheDefinition(t *testing.T) {
	rows := []struct {
		name             string
		move, fire       int32
		wantMove, wantFi uint32
	}{
		// The parsed defaults: roam + fire at will [04 R-STANCE-01 §6].
		{name: "parsed defaults", move: 2, fire: 2, wantMove: 2, wantFi: 2},
		// What the stock census actually authors on a mobile unit.
		{name: "stock mobile", move: 1, fire: 2, wantMove: 1, wantFi: 2},
		// A definition that authors hold on both, as fourteen stock ones do.
		{name: "authored hold", move: 0, fire: 0, wantMove: 0, wantFi: 0},
		// The deposit masks to two bits, as the handler's own deposit does.
		{name: "masked to two bits", move: 5, fire: 6, wantMove: 1, wantFi: 2},
	}
	for _, row := range rows {
		flags := initialStatusFlags(&content.UnitDef{StandingMoveOrder: row.move, StandingFireOrder: row.fire})
		gotMove := (flags >> StandingMoveShift) & StandingFieldMask
		gotFire := (flags >> StandingFireShift) & StandingFieldMask
		if gotMove != row.wantMove || gotFire != row.wantFi {
			t.Fatalf("%s: move/fire = %d/%d, want %d/%d", row.name, gotMove, gotFire, row.wantMove, row.wantFi)
		}
	}
}
