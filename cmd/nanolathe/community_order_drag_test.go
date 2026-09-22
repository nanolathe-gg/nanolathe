package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestCommunityOrderHitUsesAuthoredHalfOpenFootprint(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "lab"}, FootprintX: 3, FootprintZ: 2}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"lab": def}}
	// The published display footprint is rotated. Picking deliberately uses
	// the definition's ordinary 3x2 extents, as the source does.
	order := frame.OrderView{GoalX: 100 << 16, GoalY: 20 << 16, GoalZ: 80 << 16, BuildProduct: "lab", Kind: "MobileBuild", FootX: 2, FootZ: 3}
	lowX := order.GoalX - numeric.FixedFromInt(24)
	lowZ := order.GoalZ - order.GoalY/2 - numeric.FixedFromInt(16)
	if !communityOrderHit(order, cat, lowX, 0, lowZ) {
		t.Fatal("inclusive lower authored-footprint edge missed")
	}
	if communityOrderHit(order, cat, order.GoalX+numeric.FixedFromInt(24), 0, lowZ) {
		t.Fatal("exclusive upper X edge admitted")
	}
	if communityOrderHit(order, cat, lowX, 0, order.GoalZ-order.GoalY/2+numeric.FixedFromInt(16)) {
		t.Fatal("exclusive upper projected-Z edge admitted")
	}
}

func TestCommunityQueuedOrderDragContextBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		latch                         input.Latch
		override, strategic, wantLost bool
	}{
		{name: "idle", latch: input.LatchNormal},
		{name: "prepared order", latch: input.LatchMove, wantLost: true},
		{name: "override pressed", latch: input.LatchNormal, override: true, wantLost: true},
		{name: "strategic overview", latch: input.LatchNormal, strategic: true, wantLost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := communityQueuedOrderDragContextLost(tc.latch, tc.override, tc.strategic); got != tc.wantLost {
				t.Fatalf("context lost=%v, want %v", got, tc.wantLost)
			}
		})
	}
}

func TestCommunityQueuedOrderDragContextCancellationCannotCommit(t *testing.T) {
	sess := &session.Session{}
	b := &battleSession{sess: sess, battleUI: &ui.BattleState{}, communityOrderDrag: communityOrderDragState{
		kind: communityDragQueuedOrder, moved: true, preview: communityOrderDragPreview{visible: true},
	}}
	in := input.NewState()
	in.Kbd.SetKey(input.KeyShift, true)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.battleState().Input.Latch = input.LatchMove
	if !b.continueCommunityOrderDrag(in, nil, *in.Mouse, 10, 10) {
		t.Fatal("prepared-order transition did not retain gesture ownership")
	}
	if !b.communityOrderDrag.cancelled || b.communityOrderDrag.preview.visible {
		t.Fatal("prepared-order transition did not cancel and hide preview")
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.battleState().Input.Latch = input.LatchNormal
	if !b.continueCommunityOrderDrag(in, nil, *in.Mouse, 10, 10) {
		t.Fatal("cancelled release did not remain consumed")
	}
	if b.communityOrderDrag.kind != communityDragNone || len(sess.PendingHumanCommands()) != 0 {
		t.Fatalf("cancelled gesture survived or committed: state=%+v commands=%+v", b.communityOrderDrag, sess.PendingHumanCommands())
	}
}

func TestCommunityOrderGestureRefusesStaleSelection(t *testing.T) {
	const unit pool.Handle = 7
	order := frame.OrderView{Unit: unit, Index: 0, DescriptorID: 11, CreationTick: 4, GoalX: 20 << 16, Kind: "Move_Ground"}
	receipt := communityOrderReceipt(order)
	f := &frame.Frame{
		Units:       []frame.UnitView{{Slot: unit, InstanceID: 9, Owner: 0}},
		Selection:   frame.SelectionView{LocalPlayer: 0, Handles: []pool.Handle{unit}},
		OrderQueues: []frame.OrderQueueView{{Unit: unit, Primary: []frame.OrderView{order}}},
	}
	if !communityOrderGestureStillPublished(f, receipt, 9) {
		t.Fatal("unchanged selected receipt refused")
	}
	f.Selection.Handles = nil
	if communityOrderGestureStillPublished(f, receipt, 9) {
		t.Fatal("gesture survived a committed selection change")
	}
	f.Selection.Handles = []pool.Handle{unit}
	f.OrderQueues[0].Primary[0].GoalX++
	if communityOrderGestureStillPublished(f, receipt, 9) {
		t.Fatal("gesture survived a committed queue change")
	}
}
