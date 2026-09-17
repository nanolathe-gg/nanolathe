package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Stop's landing child must wake EndTransport before it lowers activation
// [04 R-ORD-01 §2][04 R-AIR-01 §6]. The authored callback returns immediately,
// so its finish must already be observed when Deactivate starts.
func TestLandIfCanWakesEndTransportBeforeDeactivate(t *testing.T) {
	sys, w, u := airFixture(t)
	sys.SetMoverMode(u, 2)
	// The setter writes the mover's request byte; the unit-side mirror changes
	// only at the ordinary position commit [04 R-AIR-01 §3][04 R-COLL-01 §1],
	// and runLandingTick pumps orders BEFORE the movement tick, so no commit
	// has run when `Stop` is dispatched. `Stop` spawns its landing child on the
	// COMMITTED mover mode [04 §3.4], and an aircraft that is already flying —
	// which is the subject of this test — carries 2 in both words.
	u.Move.ModeMirror = 2
	u.Y += numeric.Fixed(100 << 16)
	u.Activated = true
	vm := cob.NewVM(&cob.Program{
		Code:        []uint32{0x10021001, 0, 0x10065000},
		Scripts:     map[string]int{"EndTransport": 0, "Deactivate": 0},
		ScriptsByID: []int{0, 0},
	})
	bridge := cob.NewCallbackBridge(vm)
	u.ScriptState = &units.ScriptState{VM: vm, Bridge: bridge, Binding: &cob.Binding{VM: vm, Callbacks: bridge}}
	var events []string
	bridge.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Name == "EndTransport" || e.Name == "Deactivate" {
			events = append(events, e.Name+":"+e.Phase)
		}
	})
	pushAirOrder(t, u, "Stop", 0, 0)
	for tick := uint32(1); tick <= 5 && u.Activated; tick++ {
		runLandingTick(sys, tick, w)
	}
	if u.Activated {
		t.Fatal("Stop did not begin terrain descent")
	}
	finish, deactivate := -1, -1
	for i, e := range events {
		if e == "EndTransport:finish" {
			finish = i
		}
		if e == "Deactivate:start" {
			deactivate = i
		}
	}
	if finish < 0 || deactivate <= finish {
		t.Fatalf("callback events = %q, want EndTransport finished before Deactivate starts [04 R-AIR-01 §6]", events)
	}
}

// A paralyzer head blocks the landing record, then resumes its existing
// descent. It must not re-run the random-bearing initialization or replace
// that record's marker [04 §2.4][04 R-ORD-01 §2][04 R-AIR-01 §6].
func TestLandIfCanResumesDescentAfterParalyze(t *testing.T) {
	sys, w, u := airFixture(t)
	sys.SetMoverMode(u, 2)
	u.Y += numeric.Fixed(100 << 16)
	landing := pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
	for tick := uint32(1); tick <= 2; tick++ {
		runLandingTick(sys, tick, w)
	}
	marker := sys.AirGoalPayload(u.Handle)
	if marker == nil || landing.Phase != 2 {
		t.Fatal("landing did not install its descent marker")
	}
	q := orders.QueueForUnit(u)
	randomBefore := *q.Binding().SimRNG
	orders.PushParalyzeCredit(u, 2, 3)
	for tick := uint32(3); tick <= 6; tick++ {
		runLandingTick(sys, tick, w)
	}
	if q.Head() != landing {
		t.Fatal("stun did not return to the waiting landing record")
	}
	if landing.Phase != 2 || sys.AirGoalPayload(u.Handle) != marker || *q.Binding().SimRNG != randomBefore {
		t.Fatal("landing restarted after paralyze; its existing descent and random state must survive the temporary head [04 R-AIR-01 §6]")
	}
}

// The twelve-failure orbit turns on any delivered movement outcome, including
// ordinary arrival alone. The scratch arithmetic wraps at 32 bits and only
// its bearing input narrows [04 R-AIR-01 §6].
func TestLandIfCanSearchTurnsOnArrivalAlone(t *testing.T) {
	sys, _, u := waterAirFixture(t)
	sys.SetMoverMode(u, 2)
	n := pushAirOrder(t, u, "VTOL_LandIfCan", u.X, u.Z)
	n.Phase = 1
	n.Param1 = 1
	if code := sys.legVTOLLandIfCan(u, n, 0x20, 1); code != 2 {
		t.Fatalf("search result = %d, want hold", code)
	}
	if want := uint32(0xffffaaac); n.Param1 != want {
		t.Fatalf("search bearing scratch = %#x, want %#x after arrival alone [04 R-AIR-01 §6]", n.Param1, want)
	}
	if n.DynamicGate != 0xE0 {
		t.Fatalf("search gate = %#x, want three movement outcomes", n.DynamicGate)
	}
}
