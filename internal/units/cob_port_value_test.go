package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
)

// TestActivationAndArmoredPortsTreatAnyNonzeroValueAsOn locks how the port-1
// and port-20 write arms read the value [04 R-COB-03 §4]. Neither arm masks
// it: both hand the whole word to the shared edge machine, which sets its bit
// for any nonzero value and clears it only for zero. A write that leaves the
// bit where it was is not an edge and raises nothing [04 R-UNIT-06 §2].
//
// The values are the ones that matter: 2 and 100 are even, so a low-bit
// reading would clear the bit. UberHack 4.0's wind generators write 100 to
// port 1 in SetSpeed (issue 195), and -1 has every bit set.
func TestActivationAndArmoredPortsTreatAnyNonzeroValueAsOn(t *testing.T) {
	u := &Unit{Handle: 1}
	var cues []uint8
	u.SetStatusCueSink(func(_ *Unit, code uint8) { cues = append(cues, code) })
	ports := unitPortHandlers(cob.NewVM(&cob.Program{}), u)

	for _, tc := range []struct {
		port cob.Port
		get  func() bool
	}{
		{cob.Port(1), func() bool { return u.Activated }},
		{cob.Port(20), func() bool { return u.Armored }},
	} {
		for _, value := range []int32{2, 100, -1, 18200, 1} {
			ports[tc.port]([]int32{int32(tc.port), 0})
			ports[tc.port]([]int32{int32(tc.port), value})
			if !tc.get() || ports[tc.port]([]int32{int32(tc.port)}) != 1 {
				t.Fatalf("port %d write %d left the bit clear; any nonzero value sets it [04 R-COB-03 §4]", tc.port, value)
			}
			ports[tc.port]([]int32{int32(tc.port), 0})
			if tc.get() {
				t.Fatalf("port %d write 0 after %d left the bit set; zero clears it [04 R-COB-03 §4]", tc.port, value)
			}
		}
	}

	// An even write to an active unit is not an edge: it raises no Deactivate
	// cue, and the unit stays on — the wind generator of issue 195.
	u.SetActivationEdge(true)
	cues = cues[:0]
	ports[cob.Port(1)]([]int32{1, 100})
	if !u.Activated || len(cues) != 0 {
		t.Fatalf("SET ACTIVATION 100 on an active unit: activated=%v cues=%v, want on and no edge [04 R-UNIT-06 §2]", u.Activated, cues)
	}
}

// TestStanceBitPortsTakeTheLowBit locks the other three bit ports, which do
// mask: ports 5, 6 and 19 write their bit from the value's low bit, so a write
// of 2 clears it and 3 sets it [04 R-COB-03 §4].
func TestStanceBitPortsTakeTheLowBit(t *testing.T) {
	u := &Unit{}
	ports := unitPortHandlers(cob.NewVM(&cob.Program{}), u)
	for _, tc := range []struct {
		port cob.Port
		get  func() bool
	}{
		{cob.Port(5), func() bool { return u.InBuildStance }},
		{cob.Port(6), func() bool { return u.Busy }},
		{cob.Port(19), func() bool { return u.BuggerOff }},
	} {
		ports[tc.port]([]int32{int32(tc.port), 1})
		ports[tc.port]([]int32{int32(tc.port), 2})
		if tc.get() {
			t.Fatalf("port %d write 2 left the bit set; the arm takes the low bit [04 R-COB-03 §4]", tc.port)
		}
		ports[tc.port]([]int32{int32(tc.port), 3})
		if !tc.get() {
			t.Fatalf("port %d write 3 left the bit clear; the arm takes the low bit [04 R-COB-03 §4]", tc.port)
		}
	}
}
