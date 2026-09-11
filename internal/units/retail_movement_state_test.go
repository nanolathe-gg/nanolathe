package units

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// ModeMirror and MoveTier own the unit-side movement nibble; Flags can retain an
// older packed copy between loads. Preserve the cached tier rather than
// recomputing it from speed [08 R-SAVE-02 §6][04 R-MOV-01 §6].
func TestRetailSaveProjectsCurrentMovementState(t *testing.T) {
	for _, tc := range []struct{ mode, tier uint8 }{{1, 2}, {2, 3}} {
		u := &Unit{Handle: 1, Def: &content.UnitDef{UnitName: "movementstate"}, Alive: true, Flags: 0xf, MoveTier: tc.tier}
		u.Move.Mode = (tc.mode + 1) & 3
		u.Move.ModeMirror = tc.mode
		resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == 1 }
		data, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(tc.mode) | uint32(tc.tier)<<2
		if got := (binary.LittleEndian.Uint32(data[0xb4:]) >> 4) & 0xf; got != want {
			t.Errorf("saved movement nibble = %x, want %x", got, want)
		}
		restored := &Unit{}
		if err := RetailUnitBase(restored, data); err != nil {
			t.Fatal(err)
		}
		if restored.Move.ModeMirror != tc.mode || restored.Move.Mode != tc.mode || restored.MoveTier != tc.tier {
			t.Errorf("restored mode/tier = %d/%d, want %d/%d", restored.Move.Mode, restored.MoveTier, tc.mode, tc.tier)
		}
		if u.Flags != 0xf {
			t.Fatal("save projection mutated live flags")
		}
	}
}
