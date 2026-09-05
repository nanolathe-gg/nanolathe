package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestLatchToCodeMapping(t *testing.T) {
	// Full latch→code mapping table [GAP T22][07 §9] C11.
	cases := []struct {
		latch input.Latch
		code  int
		name  string
	}{
		{input.LatchNormal, 1, "Normal"},
		{input.LatchMove, 2, "Move"},
		{input.LatchAttack, 3, "Attack"},
		{input.LatchBlast, 4, "Blast"},
		{input.LatchUnload, 5, "Unload"},
		{input.LatchPickup, 6, "Pickup"},
		{input.LatchFollow, 7, "Follow"},
		{input.LatchRepair, 8, "Repair"},
		{input.LatchPatrol, 9, "Patrol"},
		{input.LatchTeleport, 11, "Teleport"},
		{input.LatchReclaim, 12, "Reclaim"},
		{input.LatchCapture, 13, "Capture"},
		{input.LatchMobileBuild, 14, "MobileBuild"},
	}
	for _, tc := range cases {
		got := LatchToCode(tc.latch)
		if got != tc.code {
			t.Errorf("LatchToCode(%s %d) = %d want %d", tc.name, tc.latch, got, tc.code)
		}
		// Also verify via Dispatch path: LatchToCode underlies Dispatch switch key [GAP T22] C11.
		if !tc.latch.IsValid() {
			t.Errorf("latch %s should be valid", tc.name)
		}
		if got != int(tc.latch) {
			t.Errorf("latch byte should equal code for valid latch %s: latch %d code %d", tc.name, tc.latch, got)
		}
	}
	invalid := []input.Latch{0, 10, 0xA, 0x0F, 0xFF, 42}
	for _, l := range invalid {
		if got := LatchToCode(l); got != 0 {
			t.Errorf("LatchToCode invalid %d = %d want 0", l, got)
		}
		if l.IsValid() {
			t.Errorf("latch %d should be invalid", l)
		}
	}
}

func TestParseButtonLatchChain(t *testing.T) {
	// Button parse chain precedence: MOVE → STOP → ATTACK → BLAST → DEFEND →
	// REPAIR → PATROL → RECLAIM → CAPTURE → UNLOAD → LOAD [07 §9 "Corrected
	// and completed"] C11.
	tests := []struct {
		name string
		gate uint32
		want input.Latch
	}{
		{"move", 1, input.LatchMove}, // MOVE is the first test, not a default
		{"STOP", 1, input.LatchNormal},
		{"attack", 1, input.LatchAttack}, // case-insensitive
		{"BLAST", 1, input.LatchBlast},
		{"defend", 1, input.LatchFollow},
		{"Repair", 1, input.LatchRepair},
		{"patrol", 1, input.LatchPatrol},
		{"reclaim", 1, input.LatchReclaim},
		{"CAPTURE", 1, input.LatchCapture},
		{"UNLOAD", 1, input.LatchUnload},
		{"LOAD", 1, input.LatchPickup},
		// Retail has no PICKUP compare, only LOAD: a name that only contains
		// PICKUP is not handled [07 §9].
		{"pickup", 1, NotHandled},
		// No default at all: an unmatched name is not handled — no latch
		// write, no cue [07 §9].
		{"BUILD_ARM", 1, NotHandled},
		{"", 1, NotHandled},
		// Gate zero forces Normal regardless of name [07 §9].
		{"ATTACK", 0, input.LatchNormal},
		{"MOVE", 0, input.LatchNormal},
		{"UNLOAD", 0, input.LatchNormal},
	}
	for _, tc := range tests {
		got := ParseButtonLatch(tc.name, tc.gate)
		if got != tc.want {
			t.Errorf("ParseButtonLatch(%q gate %d) = %d (%s) want %d (%s)", tc.name, tc.gate, got, got.String(), tc.want, tc.want.String())
		}
	}
}

// TestParseButtonLatchNotHandled locks the distinct not-handled result: it is
// invalid, distinct from every armed latch (including Normal), and is what a
// button whose name matches no chain predicate produces [07 §9 "Corrected and
// completed"].
func TestParseButtonLatchNotHandled(t *testing.T) {
	if NotHandled.IsValid() {
		t.Fatalf("NotHandled must not be a valid latch, got %v", NotHandled)
	}
	if NotHandled == input.LatchNormal {
		t.Fatalf("NotHandled must be distinct from LatchNormal")
	}
	for _, name := range []string{"PICKUP", "FOO", "GADGET1", ""} {
		if got := ParseButtonLatch(name, 1); got != NotHandled {
			t.Errorf("ParseButtonLatch(%q, 1) = %v, want NotHandled", name, got)
		}
	}
}

func TestParseButtonLatchPrecedence(t *testing.T) {
	// A button satisfying multiple predicates picks per chain order [07 §9] C11.
	precedence := []struct {
		name string
		want input.Latch
		desc string
	}{
		{"MOVESTOPATTACKBLASTDEFENDREPAIRPATROLRECLAIMCAPTUREUNLOADLOAD", input.LatchMove, "MOVE first"},
		{"STOPATTACKBLASTDEFENDREPAIRPATROLRECLAIMCAPTUREUNLOADLOAD", input.LatchNormal, "STOP before the rest once MOVE is absent"},
		{"ATTACKBLAST", input.LatchAttack, "ATTACK before BLAST"},
		{"ATTACKMOVE", input.LatchMove, "MOVE is tested before ATTACK"},
		{"BLASTMOVE", input.LatchMove, "MOVE is tested before BLAST"},
		{"DEFENDREPAIR", input.LatchFollow, "DEFEND before REPAIR"},
		{"REPAIRPATROL", input.LatchRepair, "REPAIR before PATROL"},
		{"RECLAIMCAPTURE", input.LatchReclaim, "RECLAIM before CAPTURE"},
		{"UNLOAD", input.LatchUnload, "UNLOAD before LOAD substring"},
		{"UNLOAD_LOAD", input.LatchUnload, "UNLOAD vs LOAD contains"},
		{"RECLAIM_UNLOAD", input.LatchReclaim, "RECLAIM before UNLOAD"},
		{"CAPTURE_UNLOAD", input.LatchCapture, "CAPTURE before UNLOAD"},
		{"PICKUP", NotHandled, "retail has no PICKUP compare"},
		{"LOADPICKUP", input.LatchPickup, "LOAD substring, PICKUP irrelevant"},
		{"MOVE", input.LatchMove, "MOVE is a first-class test, not a default"},
	}
	for _, tc := range precedence {
		got := ParseButtonLatch(tc.name, 1)
		if got != tc.want {
			t.Errorf("ParseButtonLatch precedence %q (%s) = %s want %s", tc.name, tc.desc, got.String(), tc.want.String())
		}
	}
	// Extra: button containing both ATTACK and BLAST with earlier STOP should still pick STOP.
	if got := ParseButtonLatch("STOP_ATTACK_BLAST", 1); got != input.LatchNormal {
		t.Errorf("STOP should outrank ATTACK/BLAST: got %s", got.String())
	}
	// UNLOAD contains LOAD — must not be mistaken for LOAD/PICKUP.
	if got := ParseButtonLatch("UNLOAD", 1); got != input.LatchUnload {
		t.Errorf("UNLOAD substring test: got %s want Unload", got.String())
	}
	if got := ParseButtonLatch("LOAD", 1); got != input.LatchPickup {
		t.Errorf("LOAD -> Pickup: got %s", got.String())
	}
}

func TestParseButtonLatchGateForcesNormal(t *testing.T) {
	// Gate zero forces normal regardless of parse chain [07 §9].
	names := []string{"ATTACK", "BLAST", "MOVE", "UNLOAD", "LOAD", "REPAIR", "STOP"}
	for _, n := range names {
		if got := ParseButtonLatch(n, 0); got != input.LatchNormal {
			t.Errorf("ParseButtonLatch(%q, 0) = %s want Normal", n, got.String())
		}
	}
}
