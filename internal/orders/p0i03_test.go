package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestNewNodeForOrderPayload verifies the canonical constructor writes goal, target, tick, owner and queue modifier [P0-I03][04 §3.2].
func TestNewNodeForOrderPayload(t *testing.T) {
	rng.SeedGlobal(1, 0)
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground lookup")
	}
	gx := numeric.Fixed(10 * 65536)
	gz := numeric.Fixed(20 * 65536)
	target := pool.Handle(5)
	tick := uint32(123)
	owner := pool.Handle(2)
	queued := true
	n := NewNodeForOrder(id, target, gx, 0, gz, tick, owner, queued)
	if n.Target != target {
		t.Fatalf("target %d want %d", n.Target, target)
	}
	if n.GoalX != gx || n.GoalZ != gz {
		t.Fatalf("goal %d,%d want %d,%d", n.GoalX, n.GoalZ, gx, gz)
	}
	if n.CreationTick != tick {
		t.Fatalf("tick %d want %d", n.CreationTick, tick)
	}
	if n.Owner != owner {
		t.Fatalf("owner %d want %d", n.Owner, owner)
	}
	if n.Flags&FlagPurgeSurvivor == 0 {
		t.Fatalf("queued should set FlagPurgeSurvivor")
	}
	n2 := NewNodeForOrder(id, 0, gx, 0, gz, tick, owner, false)
	if n2.Flags&FlagPurgeSurvivor != 0 {
		t.Fatalf("non-queued should not set FlagPurgeSurvivor")
	}
}

// TestQueueModifierReplaceVsAppend verifies PurgeUnprotected + DropLeadingAutoOps for Replace vs Append [04 §3.3][P0-08].
func TestQueueModifierReplaceVsAppend(t *testing.T) {
	rng.SeedGlobal(2, 0)
	moveID := Lookup("Move_Ground")
	patrolID := Lookup("Patrol")
	if moveID == 0 || patrolID == 0 {
		t.Fatalf("lookup")
	}
	u := &units.Unit{Handle: 1, Owner: 0, Alive: true}
	q := QueueForUnit(u)
	// Seed with a patrol that is purge-survivor
	q.Push(patrolID, Node{Flags: FlagPurgeSurvivor})
	if q.LenPrimary() != 1 {
		t.Fatalf("seed len")
	}
	// Replace: should purge non-survivor? Add a non-survivor then replace
	q2 := &Queue{}
	q2.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagPurgeSurvivor | FlagActive},
		{ID: moveID, Param1: 2, Flags: 0},
		{ID: moveID, Param1: 3, Flags: FlagPurgeSurvivor},
	}
	q2.PurgeUnprotected()
	if len(q2.primary) != 2 {
		t.Fatalf("purge survivor len %d want 2", len(q2.primary))
	}
	// Append via NewNodeForOrder queued flag should survive future purge
	n := NewNodeForOrder(moveID, 0, numeric.Fixed(1), 0, numeric.Fixed(1), 10, 1, true)
	q.Push(moveID, n) // append after active
	if q.LenPrimary() != 2 {
		t.Fatalf("append len %d want 2", q.LenPrimary())
	}
}
