package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func queueTestProject(x, y, z numeric.Fixed) QueuePoint {
	return QueuePoint{X: int32(x >> 16), Y: int32(z >> 16)}
}

func queueTestRect(o frame.OrderView) (QueueRect, bool) {
	return QueueRect{Left: int32(o.GoalX >> 16), Top: int32(o.GoalZ >> 16), Right: int32(o.GoalX>>16) + 16, Bottom: int32(o.GoalZ>>16) + 16}, true
}

func queueTestFrame() *frame.Frame {
	return &frame.Frame{
		Tick:        20,
		Units:       []frame.UnitView{{Slot: 1, Owner: 0, X: 0, Y: 0, Z: 0, Health: 100, MaxHealth: 100, IsBuilding: true}},
		Selection:   frame.SelectionView{LocalPlayer: 0, Handles: []pool.Handle{1}},
		CommandPage: frame.CommandPageView{Builder: 1},
		OrderQueues: []frame.OrderQueueView{{Unit: 1, Primary: []frame.OrderView{
			{Unit: 1, Index: 0, Kind: "Move_Ground", GoalX: numeric.Fixed(16 << 16), GoalZ: numeric.Fixed(8 << 16), CreationTick: 12},
			{Unit: 1, Index: 1, Kind: "MobileBuild", GoalX: numeric.Fixed(32 << 16), GoalZ: numeric.Fixed(8 << 16), BuildProduct: "armmex", FootX: 2, FootZ: 2, CreationTick: 15},
		}}},
	}
}

func TestQueueOverlayShiftGateAndReleasePurity(t *testing.T) {
	frame := queueTestFrame()
	before := frame.OrderQueues[0].Primary[1].CreationTick
	if got := QueueOverlay(frame, QueueOverlayOptions{Tick: 20, LocalOwner: 0, Project: queueTestProject}); got != nil {
		t.Fatalf("overlay without Shift = %v, want nil", got)
	}
	if got := QueueOverlay(frame, QueueOverlayOptions{Tick: 20, ShiftHeld: true, LocalOwner: 0, Project: queueTestProject}); len(got) == 0 {
		t.Fatal("held Shift produced no queue geometry")
	}
	if frame.OrderQueues[0].Primary[1].CreationTick != before {
		t.Fatal("overlay mutated immutable queue input")
	}
}

func TestQueueOverlayOrderAndMasks(t *testing.T) {
	ops := QueueOverlay(queueTestFrame(), QueueOverlayOptions{Tick: 20, ShiftHeld: true, LocalOwner: 0, Project: queueTestProject, BuildRect: queueTestRect})
	var dash, marker, markerSegments int
	for _, op := range ops {
		if op.Kind == QueuePrimitiveDash {
			dash++
			if op.Index == 0 && op.DashAge != 8 {
				t.Fatalf("move dash age = %d, want creation-tick age 8", op.DashAge)
			}
		}
		if op.Kind == QueuePrimitiveMarker {
			marker++
			markerSegments += len(op.Segments)
			if op.Index != 1 {
				t.Fatalf("marker came from order %d, want build order 1", op.Index)
			}
			if !op.ColorKnown {
				t.Fatal("build marker color is known but primitive did not mark it")
			}
		}
	}
	if dash == 0 || marker != 1 || markerSegments != 8 {
		t.Fatalf("dash=%d marker=%d segments=%d, want dash and one eight-line build marker", dash, marker, markerSegments)
	}
}

func TestQueueOverlayMarkerOnlyForOtherLocalUnitsWithBuilderContext(t *testing.T) {
	f := queueTestFrame()
	f.Units = append(f.Units, frame.UnitView{Slot: 2, Owner: 0, Health: 100, MaxHealth: 100})
	f.OrderQueues = append(f.OrderQueues, frame.OrderQueueView{Unit: 2, Primary: []frame.OrderView{{Unit: 2, Index: 0, Kind: "MobileBuild", BuildProduct: "armsolar", FootX: 1, FootZ: 1, GoalX: numeric.Fixed(48 << 16), GoalZ: numeric.Fixed(8 << 16), CreationTick: 19}}})
	ops := QueueOverlay(f, QueueOverlayOptions{Tick: 20, ShiftHeld: true, LocalOwner: 0, Project: queueTestProject, BuildRect: queueTestRect})
	for _, op := range ops {
		if op.Unit == 2 && (op.Kind != QueuePrimitiveMarker || op.Mask != QueueMarkerMask) {
			t.Fatalf("unselected unit operation=%+v, want marker-only mask", op)
		}
	}
}

func TestQueueOverlayDoesNotTreatEveryStructureAsBuilderContext(t *testing.T) {
	f := queueTestFrame()
	// The selected fixed structure has no authored command-page builder and
	// its queue contains no build order. A factory/mex must not enable the
	// marker-only walker for unrelated local units.
	f.CommandPage = frame.CommandPageView{}
	f.OrderQueues[0].Primary = []frame.OrderView{{
		Unit: 1, Index: 0, Kind: "Move_Ground",
		GoalX: numeric.Fixed(16 << 16), GoalZ: numeric.Fixed(8 << 16), CreationTick: 12,
	}}
	f.Units = append(f.Units, frame.UnitView{Slot: 2, Owner: 0, Health: 100, MaxHealth: 100})
	f.OrderQueues = append(f.OrderQueues, frame.OrderQueueView{Unit: 2, Primary: []frame.OrderView{{
		Unit: 2, Index: 0, Kind: "MobileBuild", BuildProduct: "armsolar", FootX: 1, FootZ: 1,
		GoalX: numeric.Fixed(48 << 16), GoalZ: numeric.Fixed(8 << 16), CreationTick: 19,
	}}})
	ops := QueueOverlay(f, QueueOverlayOptions{Tick: 20, ShiftHeld: true, LocalOwner: 0, Project: queueTestProject, BuildRect: queueTestRect})
	for _, op := range ops {
		if op.Unit == 2 {
			t.Fatalf("non-builder structure enabled marker-only overlay: %+v", op)
		}
	}
}
