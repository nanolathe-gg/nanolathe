package orders

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestQueueOverflowCap_Primary(t *testing.T) {
	// [P1-I09] queue is now dynamic with OOM guard far outside stock (105 << 10000).
	// The old 64 cap was inside stock and has been replaced; the guard is now OOMGuardQueue.
	rng.SeedGlobal(1, 0)
	q := &Queue{}
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("lookup Move_Ground failed")
	}
	for i := 0; i < OOMGuardQueue+5; i++ {
		q.Push(id, Node{Param1: uint32(i)})
	}
	if len(q.primary) != OOMGuardQueue {
		t.Fatalf("primary OOM guard cap = %d, want %d", len(q.primary), OOMGuardQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for OOM guard overflow")
	}
	found := false
	for _, d := range q.Diagnostics() {
		if len(d) > 0 && (d[0] == 'o') {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("diagnostics missing OOM guard text: %v", q.Diagnostics())
	}
	// Stock-reachable length 105 must not trigger guard
	q2 := &Queue{}
	for i := 0; i < 105; i++ {
		q2.Push(id, Node{Param1: uint32(i)})
	}
	if len(q2.primary) != 105 {
		t.Fatalf("stock queue 105 should not be capped, got %d", len(q2.primary))
	}
	if len(q2.Diagnostics()) != 0 {
		t.Fatalf("stock queue should not diagnostic, got %v", q2.Diagnostics())
	}
}

func TestQueueOverflowCap_Secondary(t *testing.T) {
	// Secondary is also dynamic with OOM guard [P1-I09]; maxSecondary in corpus is 1 << 10000.
	q := &Queue{}
	id := Lookup("BuildWeapon")
	if id == 0 {
		t.Fatalf("lookup BuildWeapon failed")
	}
	for i := 0; i < OOMGuardQueue+3; i++ {
		q.PushSecondary(id, Node{Param1: uint32(i)})
	}
	if len(q.secondary) != OOMGuardQueue {
		t.Fatalf("secondary OOM guard cap = %d, want %d", len(q.secondary), OOMGuardQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for secondary OOM guard overflow")
	}
	// Stock secondary max 1 should not hit guard
	q2 := &Queue{}
	q2.PushSecondary(id, Node{Param1: 1})
	if len(q2.secondary) != 1 || len(q2.Diagnostics()) != 0 {
		t.Fatalf("stock secondary should not guard")
	}
}

// TestPumpWedgeIsNotRescued locks ORD-02: there is NO pump iteration guard.
// Retail has none [04 §3.3][P2-03], and a handler that keeps returning a
// same-record continue code wedges the walk forever — reproducing that wedge
// is the contract, because a defensive cap would alter queue state, RNG use,
// and later updates.
//
// The wedging code is 0 ("reset the phase to zero and continue walking",
// [04 §3.3]), which re-dispatches the record the walk is standing on — the
// same arm that lets one pump call cascade a record through several phases in
// a tick. It is NOT 2 or 4: those advance to the next record in the same pass
// ([04 R-FAC-02 §4]), so a queue of holds runs out instead of spinning. The wedging case therefore runs in a SUBPROCESS (this
// test binary re-exec'd with -test.run and a short -test.timeout) so the
// suite waits seconds, not forever; the child only exits cleanly if some cap
// rescued the walk, which is a failure. Skipped in -short mode.
func TestPumpWedgeIsNotRescued(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: wedge subprocess test skipped")
	}
	if os.Getenv("NANOLATHE_PUMP_WEDGE_CHILD") == "1" {
		sim := rng.NewSimulation(2)
		q := &Queue{binding: &QueueBinding{SimRNG: &sim}}
		u := &units.Unit{Handle: 1, Pending: 0}
		id := Lookup("Move_Ground")
		if id == 0 {
			t.Fatal("lookup")
		}
		// Handler that always returns 0 (reset the phase and continue walking)
		// forces an endless cascade over the same head [04 §3.3] — retail
		// wedges here, so must we.
		restore := setHandler(id, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(0) })
		defer restore()
		q.Push(id, Node{})
		clearGates(q)
		q.Pump(u, 10)
		// Only reachable if a cap rescued the walk.
		fmt.Println("wedge pump returned; engine rescued a tight loop")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPumpWedgeIsNotRescued$", "-test.timeout=5s")
	cmd.Env = append(os.Environ(), "NANOLATHE_PUMP_WEDGE_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("wedge child exited cleanly — a pump cap rescued the tight loop (ORD-02 violated). output:\n%s", out)
	}
	if !strings.Contains(string(out), "test timed out") {
		t.Fatalf("wedge child failed for an unexpected reason (want still running at -test.timeout):\n%s", out)
	}
}

func TestCoalesceTailWrapAndOverflow(t *testing.T) {
	q := &Queue{}
	id := Lookup("MobileBuild")
	if id == 0 {
		t.Fatalf("lookup MobileBuild")
	}
	q.CoalesceTail(id, Node{Param1: 42, Param2: 0xffffffff})
	if q.primary[0].Param2 != 0xffffffff {
		t.Fatalf("initial coalesce param %d", q.primary[0].Param2)
	}
	q.CoalesceTail(id, Node{Param1: 42, Param2: 2})
	// wrap low32 like retail add
	if q.primary[0].Param2 != 1 { // 0xffffffff +2 = 1 wrap uint32
		t.Fatalf("wrap add got %d want 1", q.primary[0].Param2)
	}
}
