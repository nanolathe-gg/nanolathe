package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
)

// Ports read the live two-bit fields, not a bind-time copy or the interface's
// three-bit cycle state [04 R-COB-03 §3]. Writes remain marker-only §4.
func TestStandingPortsReadLiveFieldsAndDoNotWriteThem(t *testing.T) {
	u := &Unit{}
	for _, tc := range []struct {
		port  uint32
		shift uint
	}{{2, StandingMoveShift}, {3, StandingFireShift}} {
		code := []uint32{0x10021001, tc.port}
		for range 4 {
			code = append(code, 0x10021001, 0)
		}
		code = append(code, 0x10043000, 0x10065000)
		vm := cob.NewVM(&cob.Program{Code: code, Scripts: map[string]int{"Probe": 0}, ScriptsByID: []int{0}})
		bindUnitPortHandlers(vm, u)
		for value := uint32(0); value < 4; value++ {
			u.Flags = ^uint32(0)&^(StandingFieldMask<<tc.shift) | value<<tc.shift
			u.Pending = 0
			if !vm.Start(0, nil) {
				t.Fatal("start stance query")
			}
			vm.Drain(1)
			got, ok := vm.ConsumeReturn(0)
			if !ok || got != int32(value) || u.Pending != 0 {
				t.Fatalf("port %d: got %d/%v pending=%d, want pure read %d", tc.port, got, ok, u.Pending, value)
			}
			flags := u.Flags
			drainWrites(t, u, [][2]int32{{int32(tc.port), int32(value ^ 3)}})
			if u.Flags != flags || u.Pending != PendingScriptTouched {
				t.Fatalf("port %d write changed stance or lost marker", tc.port)
			}
		}
	}
}
