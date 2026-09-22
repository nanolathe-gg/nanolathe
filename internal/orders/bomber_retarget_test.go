package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A Replace issued while a bomber waits on its phase-6 break marker removes
// the old AirStrike before delivering cancel-current. That callback appends a
// seek because the removed attack was last, and the live purge then visits and
// removes the appended seek before the new explicit attack is inserted
// [04 R-MOV-03 §6][04 R-AIR-01 §16].
func TestBomberPointAttackReplacementPurgesCancelSeek(t *testing.T) {
	q, u := gateFixture()
	u.Flags |= 2 << units.StandingFireShift
	q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(h pool.Handle) *units.Unit {
		if h == u.Handle {
			return u
		}
		return nil
	}})

	oldGoal := numeric.FixedFromInt(100)
	q.Push(Lookup("AirStrike"), NewNodeForOrder(Lookup("AirStrike"), 0, oldGoal, 0, oldGoal, 1, u.Handle, false))
	old := q.Head()
	old.Phase = 6
	old.DynamicGate = 0xE2

	q.PurgeUnprotected()
	if got := len(q.Primary()); got != 0 {
		t.Fatalf("replacement purge retained %d records; cancel-appended seek must be visited by the live purge", got)
	}

	newGoal := numeric.FixedFromInt(200)
	replacementID := Lookup("AirStrike")
	q.Push(replacementID, NewNodeForOrder(replacementID, 0, newGoal, 0, newGoal, 2, u.Handle, false))
	if got := len(q.Primary()); got != 1 {
		t.Fatalf("replacement queue has %d records, want only the explicit attack", got)
	}
	got := q.Head()
	if got.ID != replacementID || got.Phase != 0 || got.GoalX != newGoal || got.GoalZ != newGoal {
		t.Fatalf("replacement head = %s phase=%d goal=(%d,%d), want fresh AirStrike at (%d,%d)",
			DescriptorFor(got.ID).Name, got.Phase, got.GoalX, got.GoalZ, newGoal, newGoal)
	}
}

// Unlinking before cleanup must not make a formerly non-last attack look last
// to its cancel callback. Retail leaves the removed node's next link readable
// through that callback [04 R-MOV-03 §6][04 R-AIR-01 §16].
func TestBomberCancelRetainsDetachedSuccessorTruth(t *testing.T) {
	q, u := gateFixture()
	u.Flags |= 2 << units.StandingFireShift
	q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(h pool.Handle) *units.Unit {
		if h == u.Handle {
			return u
		}
		return nil
	}})
	q.Push(Lookup("AirStrike"), NewNodeForOrder(Lookup("AirStrike"), 0, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(100), 1, u.Handle, false))
	old := q.Head()
	old.DynamicGate = 0xE2
	q.Push(Lookup("Wait"), Node{Owner: u.Handle})
	survivor := q.Primary()[1]
	observedCleanup := false
	q.binding.Movement = &MovementGoalAdapter{Release: func(n *Node) bool {
		if n != old {
			return true
		}
		observedCleanup = true
		got := q.Primary()
		if len(got) != 1 || got[0] != survivor {
			t.Errorf("queue during detached cleanup = %v, want only original protected successor", got)
		}
		return true
	}}

	q.PurgeUnprotected()
	if !observedCleanup {
		t.Fatal("old attack cleanup was not observed")
	}
	if got := q.Primary(); len(got) != 1 || got[0] != survivor {
		t.Fatalf("purge queue = %v, want only original protected successor", got)
	}
}
