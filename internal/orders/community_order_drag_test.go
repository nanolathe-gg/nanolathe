package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func communityDragReceipt(n *Node, index uint16) CommunityOrderDragReceipt {
	return CommunityOrderDragReceipt{Unit: n.Owner, Index: index, DescriptorID: int32(n.ID), CreationTick: n.CreationTick,
		Target: n.Target, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, BuildProduct: n.BuildDefKey, BuildFacing: uint8(n.BuildFacing)}
}

func TestCommunityOrderDragHeadInterruptsTailDoesNot(t *testing.T) {
	move := Lookup("Move_Ground")
	u := &units.Unit{Handle: 7, Alive: true, X: 6 << 16, Y: 7 << 16, Z: 8 << 16}
	q := QueueForUnit(u)
	head := &Node{ID: move, Owner: u.Handle, CreationTick: 3, GoalX: 10 << 16, Phase: 4}
	tail := &Node{ID: move, Owner: u.Handle, CreationTick: 4, GoalX: 20 << 16, Phase: 5}
	q.SetPrimary([]*Node{head, tail})
	released := 0
	q.SetBinding(&QueueBinding{
		Lookup: func(h pool.Handle) *units.Unit {
			if h == u.Handle {
				return u
			}
			return nil
		},
		Movement: &MovementGoalAdapter{Release: func(n *Node) bool {
			released++
			if n.GoalX != u.X || n.GoalY != u.Y || n.GoalZ != u.Z {
				t.Fatalf("head interruption goal=(%d,%d,%d), want unit point", n.GoalX, n.GoalY, n.GoalZ)
			}
			return n == head
		}},
	})
	if !DragCommunityOrder(q, communityDragReceipt(tail, 1), CommunityOrderDragDestination{X: 30 << 16}, nil) {
		t.Fatal("tail drag refused")
	}
	if released != 0 || tail.Phase != 0 || tail.GoalX != 30<<16 {
		t.Fatalf("tail drag release=%d phase=%d x=%d", released, tail.Phase, tail.GoalX)
	}
	if !DragCommunityOrder(q, communityDragReceipt(head, 0), CommunityOrderDragDestination{X: 40 << 16}, nil) {
		t.Fatal("head drag refused")
	}
	if released != 1 || head.Phase != 0 || head.GoalX != 40<<16 {
		t.Fatalf("head drag release=%d phase=%d x=%d", released, head.Phase, head.GoalX)
	}
}

func TestCommunityOrderDragRefusesStaleReceiptAndKeepsBuildFacing(t *testing.T) {
	build := Lookup("MobileBuild")
	n := &Node{ID: build, Owner: pool.Handle(3), CreationTick: 8, GoalX: numeric.FixedFromInt(40), BuildDefKey: "lab", BuildFacing: units.FacingWest, Phase: 2}
	q := NewQueueWith([]*Node{n}, nil)
	u := &units.Unit{Handle: n.Owner, X: 4 << 16, Y: 5 << 16, Z: 6 << 16}
	released := 0
	q.SetBinding(&QueueBinding{Lookup: func(pool.Handle) *units.Unit { return u }, Movement: &MovementGoalAdapter{Release: func(*Node) bool {
		released++
		return true
	}}})
	stale := communityDragReceipt(n, 0)
	stale.GoalX++
	if DragCommunityOrder(q, stale, CommunityOrderDragDestination{X: 80 << 16}, nil) {
		t.Fatal("stale receipt moved a changed queue")
	}
	if n.GoalX != numeric.FixedFromInt(40) || n.Phase != 2 {
		t.Fatal("stale receipt mutated the order")
	}
	valid := communityDragReceipt(n, 0)
	if DragCommunityOrder(q, valid, CommunityOrderDragDestination{X: 70 << 16}, func(*Node, CommunityOrderDragDestination) (CommunityOrderDragDestination, bool) {
		return CommunityOrderDragDestination{}, false
	}) {
		t.Fatal("invalid build destination was accepted")
	}
	if released != 1 || n.GoalX != valid.GoalX || n.GoalY != valid.GoalY || n.GoalZ != valid.GoalZ || n.Phase != 2 {
		t.Fatalf("invalid head drag release=%d phase=%d goal=(%d,%d,%d)", released, n.Phase, n.GoalX, n.GoalY, n.GoalZ)
	}
	if !DragCommunityOrder(q, valid, CommunityOrderDragDestination{X: 80 << 16}, func(got *Node, d CommunityOrderDragDestination) (CommunityOrderDragDestination, bool) {
		if got.BuildFacing != units.FacingWest {
			t.Fatalf("facing=%d, want west", got.BuildFacing)
		}
		d.Z = 96 << 16
		return d, true
	}) {
		t.Fatal("valid build drag refused")
	}
	if n.BuildFacing != units.FacingWest || n.GoalX != 80<<16 || n.GoalZ != 96<<16 {
		t.Fatalf("dragged build facing/goal = %d (%d,%d)", n.BuildFacing, n.GoalX, n.GoalZ)
	}
	if released != 2 {
		t.Fatalf("head release count=%d, want 2", released)
	}
}
