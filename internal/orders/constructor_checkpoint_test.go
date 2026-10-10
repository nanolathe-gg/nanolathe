package orders

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Equal patrol records remain different assignments: borrowing from either
// changes which continuation owns the work (DESIGN_UNITS_ORDERS_COB "Modern
// patrol work"). Canonical capture must preserve that reference, not its address.
func TestConstructorCheckpointWorkAssignmentIdentity(t *testing.T) {
	graph := func(assignment int) *units.Unit {
		first, second := &Node{ID: rowRepairPatrol}, &Node{ID: rowRepairPatrol}
		work := &Node{ID: rowRepairUnit, automaticWork: true}
		work.workAssignment = []*Node{nil, first, second}[assignment]
		return &units.Unit{Orders: &Queue{primary: []*Node{work, first, second}}}
	}
	base := orderCheckpointDigests(t, graph(1))
	if got := orderCheckpointDigests(t, graph(1)); got != base {
		t.Fatal("assignment addresses changed the canonical capture")
	}
	for _, assignment := range []int{0, 2} {
		got := orderCheckpointDigests(t, graph(assignment))
		if got.Full == base.Full || got.Owners[checkpoint.OwnerOrders-1] == base.Owners[checkpoint.OwnerOrders-1] {
			t.Fatalf("assignment %d did not change full and orders digests", assignment)
		}
		for owner := range got.Owners {
			if owner != int(checkpoint.OwnerOrders-1) && got.Owners[owner] != base.Owners[owner] {
				t.Fatalf("assignment %d changed unrelated owner %d", assignment, owner+1)
			}
		}
	}
}

// Continuations can survive queue removal. Discovery must follow their links
// to a fixed point, preserve sharing, and terminate even with a retained cycle.
func TestConstructorCheckpointRetainsDetachedWorkAssignments(t *testing.T) {
	work, first, second := &Node{ID: rowRepairUnit}, &Node{ID: rowRepairPatrol}, &Node{ID: rowRepairPatrol}
	work.workAssignment, first.workAssignment, second.workAssignment = first, second, first
	q := &Queue{primary: []*Node{work}}
	c := orderCheckpointContext(t, &units.Unit{Orders: q})
	collectOrdersCheckpoint(t, c)
	for i, want := range []*Node{work, first, second} {
		if id, known := c.Nodes.Find(want); !known || id != checkpoint.ObjectID(i+1) {
			t.Fatalf("retained assignment %d: id %d, known %v", i, id, known)
		}
	}
	base := orderCheckpointBytes(t, c)
	second.workAssignment = second
	if got := orderCheckpointBytes(t, c); bytes.Equal(got, base) {
		t.Fatal("retained assignment alias change was omitted")
	}
	if q.primary[0] != work || work.workAssignment != first || first.workAssignment != second || second.workAssignment != second {
		t.Fatal("capture changed assignment links")
	}
}

// Writing never discovers graph objects. A link changed after collection must
// fail at its field rather than silently omit or intern its new continuation.
func TestConstructorCheckpointRejectsUndiscoveredWorkAssignment(t *testing.T) {
	work := &Node{ID: rowRepairUnit}
	c := orderCheckpointContext(t, &units.Unit{Orders: &Queue{primary: []*Node{work}}})
	collectOrdersCheckpoint(t, c)
	work.workAssignment = &Node{ID: rowRepairPatrol}
	var out bytes.Buffer
	err := (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c)
	if err == nil || !strings.Contains(err.Error(), "orders.nodes[0].workAssignment") || !strings.Contains(err.Error(), "undiscovered reference") {
		t.Fatalf("undiscovered assignment error = %v", err)
	}
	if _, known := c.Nodes.Find(work.workAssignment); known || len(c.Nodes.Values()) != 1 {
		t.Fatal("writer discovered the assignment")
	}
}

// A return receipt and a route-wide pause affect future borrowing even when
// the ordinary move rows and their targets have identical values.
func TestConstructorCheckpointPatrolReturnState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Queue, *Node, *Node)
	}{
		{"return reference", func(_ *Queue, work, ret *Node) { work.workReturn = ret }},
		{"return provenance", func(_ *Queue, _, ret *Node) { ret.patrolReturn = true }},
		{"return arrival", func(_ *Queue, _, ret *Node) { ret.patrolReturnArrived = true }},
		{"route pause", func(q *Queue, _, _ *Node) { q.patrolWorkPaused = true }},
		{"exact anchor x", func(_ *Queue, _, ret *Node) { ret.GoalX += numeric.Fixed(1) }},
		{"exact anchor y", func(_ *Queue, _, ret *Node) { ret.GoalY += numeric.Fixed(1) }},
		{"exact anchor z", func(_ *Queue, _, ret *Node) { ret.GoalZ += numeric.Fixed(1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			work := &Node{ID: rowRepairUnit}
			ret := &Node{ID: rowMoveGround, GoalX: 1<<40 + 17, GoalY: -23, GoalZ: 29}
			q := &Queue{primary: []*Node{work, ret}}
			u := &units.Unit{Orders: q}
			before := orderCheckpointDigests(t, u)
			tt.mutate(q, work, ret)
			after := orderCheckpointDigests(t, u)
			if before.Full == after.Full || before.Owners[checkpoint.OwnerOrders-1] == after.Owners[checkpoint.OwnerOrders-1] {
				t.Fatal("patrol return state did not change full and orders digests")
			}
		})
	}
}

func TestConstructorCheckpointWorkReturnIdentity(t *testing.T) {
	graph := func(index int) *units.Unit {
		first, second := &Node{ID: rowMoveGround}, &Node{ID: rowMoveGround}
		work := &Node{ID: rowRepairUnit, workReturn: []*Node{nil, first, second}[index]}
		return &units.Unit{Orders: &Queue{primary: []*Node{work, first, second}}}
	}
	base := orderCheckpointDigests(t, graph(1))
	if got := orderCheckpointDigests(t, graph(1)); got != base {
		t.Fatal("return addresses changed canonical capture")
	}
	for _, index := range []int{0, 2} {
		if got := orderCheckpointDigests(t, graph(index)); got.Full == base.Full || got.Owners[checkpoint.OwnerOrders-1] == base.Owners[checkpoint.OwnerOrders-1] {
			t.Fatalf("return %d did not change full and orders digests", index)
		}
	}
}

func TestConstructorCheckpointRetainsDetachedWorkReturns(t *testing.T) {
	work, assignment, ret := &Node{ID: rowRepairUnit}, &Node{ID: rowRepairPatrol}, &Node{ID: rowMoveGround, patrolReturn: true}
	work.workAssignment, work.workReturn, ret.workAssignment, ret.workReturn = assignment, ret, assignment, work
	c := orderCheckpointContext(t, &units.Unit{Orders: &Queue{primary: []*Node{work}}})
	collectOrdersCheckpoint(t, c)
	for i, want := range []*Node{work, assignment, ret} {
		if id, known := c.Nodes.Find(want); !known || id != checkpoint.ObjectID(i+1) {
			t.Fatalf("detached continuation %d: id %d, known %v", i, id, known)
		}
	}
	orderCheckpointBytes(t, c)
	if work.workAssignment != assignment || work.workReturn != ret || ret.workAssignment != assignment || ret.workReturn != work {
		t.Fatal("capture changed continuation links")
	}
}

func TestConstructorCheckpointRejectsUndiscoveredWorkReturn(t *testing.T) {
	work := &Node{ID: rowRepairUnit}
	c := orderCheckpointContext(t, &units.Unit{Orders: &Queue{primary: []*Node{work}}})
	collectOrdersCheckpoint(t, c)
	work.workReturn = &Node{ID: rowMoveGround}
	var out bytes.Buffer
	err := (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c)
	if err == nil || !strings.Contains(err.Error(), "orders.nodes[0].workReturn") || !strings.Contains(err.Error(), "undiscovered reference") {
		t.Fatalf("undiscovered return error = %v", err)
	}
	if _, known := c.Nodes.Find(work.workReturn); known || len(c.Nodes.Values()) != 1 {
		t.Fatal("writer discovered the return")
	}
}
