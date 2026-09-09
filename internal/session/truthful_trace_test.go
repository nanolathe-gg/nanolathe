package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
)

// Weaponless scripted units must have their COB threads progressed by the
// authoritative loop's exactly-once per-visit drain [04 §4.2][04 §4.6].
func TestRX03_Drain_WeaponlessScriptThreadProgresses(t *testing.T) {
	s := strictNewSessionWithUnits(t, 2, 93, 94)
	def := strictMinimalCatalog().Units["armcom"]
	h, _ := s.Units.Create(def, 0, strictCellToWorld(20), 0, strictCellToWorld(20))
	u := s.Units.Unit(h)
	// SlowReturn: sleep 67ms (=2 ticks); push 7; return.
	code := []uint32{
		0x10021001, 67,
		0x10013000,
		0x10021001, 7,
		0x10065000,
	}
	prog := &cob.Program{Code: code, Scripts: map[string]int{"SlowReturn": 0}, Statics: 0, Pieces: []string{"base"}, ScriptsByID: []int{0}}
	vm := cob.NewVM(prog)
	u.SetScript(vm)
	if !vm.StartByName("SlowReturn", nil) {
		t.Fatalf("could not start SlowReturn thread")
	}
	threadIdx := vm.LastStartedThread()
	publishOne(s, u)
	for tick := 1; tick <= 6; tick++ {
		s.Step(int32(tick))
	}
	val, ok := vm.ConsumeReturn(threadIdx)
	if !ok || val != 7 {
		t.Fatalf("scripted weaponless unit thread never progressed: ok=%v val=%d (want ok=true val=7)", ok, val)
	}
}
