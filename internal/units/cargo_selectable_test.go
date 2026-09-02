package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestCreationSeedsCargoSelectableFromIsAirBase locks [04 R-UNIT-06 §3]: the
// cargo-selectable mirror bit (bit 30 of the status word) is set at creation
// exactly when the definition has `isairbase`, and never otherwise. This is
// the write side of the carrier clause WU-19-77 documented and WU-19-81
// closes — internal/triggers reads this same bit off a carrier's Flags word.
func TestCreationSeedsCargoSelectableFromIsAirBase(t *testing.T) {
	rows := []struct {
		name      string
		isAirBase bool
		want      bool
	}{
		{name: "airbase definition", isAirBase: true, want: true},
		{name: "ordinary definition", isAirBase: false, want: false},
	}
	for _, row := range rows {
		flags := initialStatusFlags(&content.UnitDef{IsAirBase: row.isAirBase})
		got := flags&CargoSelectableStatus != 0
		if got != row.want {
			t.Fatalf("%s: CargoSelectableStatus set = %v, want %v", row.name, got, row.want)
		}
	}
}
