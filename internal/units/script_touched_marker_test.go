package units

// Contract tests for the unit side of the SCRIPT-TOUCHED MARKER [04 R-COB-06]:
// the COB engine-write opcode raises it, and the raise lands as bit 2 of this
// unit's order-event word (Unit.Pending), which is order gate bit 0x4. The VM
// half — that every arm and the fall-through raise it — is locked in
// internal/cob; these tests pin the hop.

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
)

// drainWrites runs one script of engine writes against a bound unit and
// returns the unit's order-event word afterwards.
func drainWrites(t *testing.T, u *Unit, writes [][2]int32) {
	t.Helper()
	var code []uint32
	for _, w := range writes {
		code = append(code,
			0x10021001, uint32(w[0]), // push identifier [R-P0-10]
			0x10021001, uint32(w[1]), // push value
			0x10082000, // set
		)
	}
	vm := cob.NewVM(&cob.Program{Code: code})
	bindUnitPortHandlers(vm, u)
	vm.Threads[0].Status = cob.ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
}

// TestEngineWriteSetsTheScriptTouchedMarkerOnTheUnit locks the hop for a write
// arm: `set INBUILDSTANCE to 1` performs the port's own effect AND leaves gate
// bit 0x4 standing on the unit's order-event word [04 R-COB-06].
func TestEngineWriteSetsTheScriptTouchedMarkerOnTheUnit(t *testing.T) {
	u := &Unit{}
	drainWrites(t, u, [][2]int32{{5, 1}})
	if !u.InBuildStance {
		t.Fatal("port 5's own effect did not run")
	}
	if u.Pending&PendingScriptTouched == 0 {
		t.Fatalf("order-event word = %#x, want bit 0x4 raised beside the port's effect [04 R-COB-06]", u.Pending)
	}
}

// TestEveryPortRaisesTheMarkerOnTheUnit is the trap: the marker is NOT port 5's
// signal. A write to port 6 (BUSY) raises exactly the same bit, which is what
// makes the ground transport's BUSY wait and the INBUILDSTANCE wait share one
// gate bit [04 R-COB-06][04 R-AIR-01 §9]. So does a write to an identifier with
// no write arm at all, and so does one outside 1..20.
func TestEveryPortRaisesTheMarkerOnTheUnit(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   int32
	}{
		{"activation", 1},
		{"in-build-stance", 5},
		{"busy", 6},
		{"yard open", 18},
		{"bugger off", 19},
		{"armored", 20},
		{"valid port with no write arm", 4},
		{"identifier outside 1..20", 77},
	} {
		u := &Unit{}
		drainWrites(t, u, [][2]int32{{tc.id, 0}})
		if u.Pending&PendingScriptTouched == 0 {
			t.Fatalf("%s (identifier %d): order-event word = %#x, want bit 0x4 [04 R-COB-06]", tc.name, tc.id, u.Pending)
		}
	}
}

// TestMarkerLeavesTheRestOfTheOrderEventWordAlone: the raise is one OR of bit
// 2. The weapon layer's bits in the same word [06 R-WPN-05 §6] survive it, and
// the marker is idempotent while nothing consumes it — it is a level the pump
// clears, not a counter.
func TestMarkerLeavesTheRestOfTheOrderEventWordAlone(t *testing.T) {
	u := &Unit{Pending: PendingCouldNotFire}
	drainWrites(t, u, [][2]int32{{5, 1}, {6, 1}, {19, 0}})
	if u.Pending&PendingCouldNotFire == 0 {
		t.Fatalf("order-event word = %#x, want the weapon layer's 0x1000 preserved [06 R-WPN-05 §6]", u.Pending)
	}
	if u.Pending != PendingCouldNotFire|PendingScriptTouched {
		t.Fatalf("order-event word = %#x, want exactly 0x1000|0x4 — the raise ORs one bit [04 R-COB-06]", u.Pending)
	}
}
