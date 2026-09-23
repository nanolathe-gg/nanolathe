package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A guard's queue connector ends at the ward, not at its stored goal. The
// `Follow_Ground` goal triple is the follow offset from the ward
// [04 R-ORD-01 §8 point 2]; retail's anchor getter resolves a targeted node to
// the target's position [07 R-P0-11 §3]. Projecting the offset put the line at
// the map origin — the play-test report of a commander's guard line running to
// the top-left corner while its shipyard ward was off screen.
func TestQueueOverlayGuardLineEndsAtTheWard(t *testing.T) {
	const commander, shipyard = pool.Handle(1), pool.Handle(2)
	offset := numeric.Fixed(-96 << 16)
	f := &frame.Frame{
		Tick: 40,
		Units: []frame.UnitView{
			{Slot: commander, Owner: 0, X: numeric.Fixed(1000 << 16), Y: numeric.Fixed(20 << 16), Z: numeric.Fixed(900 << 16), Health: 100, MaxHealth: 100},
			{Slot: shipyard, Owner: 0, X: numeric.Fixed(3000 << 16), Y: numeric.Fixed(5 << 16), Z: numeric.Fixed(400 << 16), Health: 100, MaxHealth: 100},
		},
		Selection: frame.SelectionView{LocalPlayer: 0, Handles: []pool.Handle{commander}},
		OrderQueues: []frame.OrderQueueView{{Unit: commander, Primary: []frame.OrderView{
			{Unit: commander, Target: shipyard, Kind: "Follow_Ground", GoalX: offset, GoalZ: 0, CreationTick: 10},
			{Unit: commander, Index: 1, Kind: "Move_Ground", GoalX: numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(300 << 16), CreationTick: 11},
		}}},
	}
	opts := hud.QueueOverlayOptions{
		Tick: 40, ShiftHeld: true, LocalOwner: 0, TrackedUnit: commander,
		Project: func(x, y, z numeric.Fixed) hud.QueuePoint {
			return hud.QueuePoint{X: int32(x >> 16), Y: int32(z >> 16)}
		},
	}
	var dashes []hud.QueuePrimitive
	for _, op := range hud.QueueOverlay(queueAnchorFrame(f), opts) {
		if op.Kind == hud.QueuePrimitiveDash {
			dashes = append(dashes, op)
		}
	}
	if len(dashes) != 2 {
		t.Fatalf("dash segments = %d, want the guard and the move", len(dashes))
	}
	ward := hud.QueueWorldPoint{X: f.Units[1].X, Y: f.Units[1].Y, Z: f.Units[1].Z}
	if dashes[0].WorldB != ward {
		t.Fatalf("guard connector ends at %+v, want the ward at %+v", dashes[0].WorldB, ward)
	}
	// The running anchor advances to the ward, so the next order's segment
	// starts there; a targetless order keeps its own stored goal.
	if dashes[1].WorldA != ward || dashes[1].WorldB.X != numeric.Fixed(200<<16) || dashes[1].WorldB.Z != numeric.Fixed(300<<16) {
		t.Fatalf("move connector = %+v -> %+v", dashes[1].WorldA, dashes[1].WorldB)
	}
	// The committed frame is immutable presentation input [I6].
	if got := f.OrderQueues[0].Primary[0]; got.GoalX != offset || got.GoalY != 0 || got.GoalZ != 0 {
		t.Fatalf("anchor resolution mutated the committed order: %+v", got)
	}
}

// A frame with nothing to resolve is handed through untouched, and a target
// the frame no longer carries, or a build site, keeps its stored goal.
func TestQueueAnchorFrameKeepsStoredGoals(t *testing.T) {
	f := &frame.Frame{
		Units: []frame.UnitView{{Slot: 1, X: numeric.Fixed(64 << 16)}, {Slot: 3, X: numeric.Fixed(512 << 16)}},
		OrderQueues: []frame.OrderQueueView{{Unit: 1, Primary: []frame.OrderView{
			{Kind: "Move_Ground", GoalX: numeric.Fixed(32 << 16)},
			{Kind: "Follow_Ground", Target: 9, GoalX: numeric.Fixed(-48 << 16)},
			{Kind: "MobileBuild", Target: 3, BuildProduct: "armmex", GoalX: numeric.Fixed(520 << 16)},
		}}},
	}
	if got := queueAnchorFrame(f); got != f {
		t.Fatal("a frame without a resolvable target was copied")
	}
}
