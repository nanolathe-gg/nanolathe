package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
)

// Generic engine reads carry four argument cells. They must use the port read
// switch, never the six write arms [04 R-COB-03 §1][04 R-COB-03 §4].
func TestGenericCOBReadsOfWritablePortsArePure(t *testing.T) {
	u := &Unit{
		Activated:     true,
		InBuildStance: true,
		Busy:          true,
		YardOpen:      true,
		BuggerOff:     true,
		Armored:       true,
	}
	yardCalls := 0
	u.SetYardOpenTransaction(func(bool) { yardCalls++ })

	for _, port := range []int32{1, 5, 6, 18, 19, 20} {
		code := make([]uint32, 0, 12)
		for _, value := range []int32{port, 0, 0, 0, 0} {
			code = append(code, 0x10021001, uint32(value))
		}
		code = append(code, 0x10043000, 0x10065000)
		prog := &cob.Program{Code: code, Pieces: []string{"base"}, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}}
		vm := cob.NewVM(prog)
		bindUnitPortHandlers(vm, u)
		if !vm.Start(0, nil) {
			t.Fatal("start")
		}
		vm.Drain(1)
		got, ok := vm.ConsumeReturn(0)
		if !ok || got != 1 {
			t.Fatalf("generic read port %d = %d, want 1", port, got)
		}
	}
	if !u.Activated || !u.InBuildStance || !u.Busy || !u.YardOpen || !u.BuggerOff || !u.Armored {
		t.Fatalf("generic read mutated writable unit state: %+v", u)
	}
	if yardCalls != 0 {
		t.Fatalf("generic yard read invoked transaction %d times", yardCalls)
	}
}
