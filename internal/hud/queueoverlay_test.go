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

// TestQueueOverlayMarkerRectMatchesGhostRect locks WU-16-8 C1: the queued
// build marker occupies the same screen rectangle the armed placement ghost
// would occupy on the same site [07 §9]. Both project the cell-aligned
// footprint with the site height at both corners, then remove the view origin
// the projection bakes in; the overlay must pass that rectangle through
// untouched, and at age 0 the eight marker lines must sit on its own edges.
func TestQueueOverlayMarkerRectMatchesGhostRect(t *testing.T) {
	const originX, originY = 128, 32
	// The retail projection: x - camX + originX, z - y/2 - camZ + originY.
	project := func(x, y, z numeric.Fixed) QueuePoint {
		return QueuePoint{
			X: int32(x>>16) + originX - originX,
			Y: int32(z>>16) - int32(y>>16)/2 + originY - originY,
		}
	}
	// The ghost's rectangle for a 4x4 footprint anchored at cell (6, 9),
	// standing at site height 24.
	const cellX, cellZ, foot, siteH = 6, 9, 4, 24
	ghost := func() QueueRect {
		px := func(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }
		a := project(px(cellX*16), px(siteH), px(cellZ*16))
		b := project(px((cellX+foot)*16), px(siteH), px((cellZ+foot)*16))
		return QueueRect{Left: a.X, Top: a.Y, Right: b.X, Bottom: b.Y}
	}()

	f := queueTestFrame()
	f.Tick = 40
	f.OrderQueues[0].Primary = []frame.OrderView{{
		Unit: 1, Index: 0, Kind: "MobileBuild", BuildProduct: "armsolar",
		FootX: foot, FootZ: foot,
		GoalX:        numeric.Fixed(int64((cellX+foot/2)*16) << 16),
		GoalY:        numeric.Fixed(int64(siteH) << 16),
		GoalZ:        numeric.Fixed(int64((cellZ+foot/2)*16) << 16),
		CreationTick: 40,
	}}
	ops := QueueOverlay(f, QueueOverlayOptions{
		Tick: 40, ShiftHeld: true, LocalOwner: 0, Project: project,
		BuildRect: func(frame.OrderView) (QueueRect, bool) { return ghost, true },
	})
	var seen int
	for _, op := range ops {
		if op.Kind != QueuePrimitiveMarker {
			continue
		}
		seen++
		want := BuildMarkerSegments(ghost.Left, ghost.Top, ghost.Right, ghost.Bottom, 0, true)
		if len(op.Segments) != len(want) {
			t.Fatalf("marker segments = %d, want %d", len(op.Segments), len(want))
		}
		for i := range want {
			if op.Segments[i] != want[i] {
				t.Fatalf("marker segment %d = %+v, want the ghost rectangle's %+v [07 §9]", i, op.Segments[i], want[i])
			}
		}
		// At age 0 the inner lines are the ghost rectangle's own edges.
		if op.Segments[4].X0 != ghost.Left || op.Segments[5].X0 != ghost.Right ||
			op.Segments[6].Y0 != ghost.Top || op.Segments[7].Y0 != ghost.Bottom {
			t.Fatalf("age-0 marker does not sit on the ghost footprint %+v", ghost)
		}
	}
	if seen != 1 {
		t.Fatalf("marker primitives = %d, want exactly one for the queued build", seen)
	}
}

// TestDashSpritesSpacingAndCadence locks the travelling-dash chain's placement
// arithmetic [R-P0-11 §3]: three cells between sprites, the phase seeded from
// the order's age wrapped at 30 ticks, and a segment under one world unit
// drawing nothing.
func TestDashSpritesSpacingAndCadence(t *testing.T) {
	const distance = int64(480) << 16 // exactly ten 3-cell steps
	a := QueueWorldPoint{}
	b := QueueWorldPoint{X: numeric.Fixed(distance)}
	// The retail placement: a 16.16 fraction of the segment, truncated once when
	// the fraction is formed and once when it is applied.
	at := func(phase int64) int64 { return distance * ((phase << 16) / distance) >> 16 }

	var xs []int64
	DashSprites(a, b, 0, 3, 1, func(_ int, x, _, _ numeric.Fixed) { xs = append(xs, int64(x)) })
	if len(xs) != 10 {
		t.Fatalf("sprites on a 480-unit segment = %d, want 10 at three cells apart", len(xs))
	}
	if xs[0] != 0 || xs[1] != at(DashSpriteSpacing) {
		t.Fatalf("sprite 1 at %d, want %d — three cells along the segment [R-P0-11 §3]", xs[1], at(DashSpriteSpacing))
	}

	// Age 15 is half of the 30-tick wrap, so the chain has marched half a step.
	var shifted []int64
	DashSprites(a, b, 15, 3, 1, func(_ int, x, _, _ numeric.Fixed) { shifted = append(shifted, int64(x)) })
	if len(shifted) == 0 || shifted[0] != at((int64(15)*3<<20)/30) {
		t.Fatalf("phase seed = %v, want ((age mod 30)*3<<20)/30 [R-P0-11 §3]", shifted)
	}

	var none int
	DashSprites(a, QueueWorldPoint{X: numeric.Fixed(0xFFFF)}, 0, 3, 1, func(int, numeric.Fixed, numeric.Fixed, numeric.Fixed) { none++ })
	if none != 0 {
		t.Fatalf("sub-world-unit segment drew %d sprites, want none [R-P0-11 §3]", none)
	}

	// A multi-frame chain advances one frame per sprite along the segment.
	var frames []int
	DashSprites(a, b, 0, 1, 4, func(f int, _, _, _ numeric.Fixed) { frames = append(frames, f) })
	for i, got := range frames {
		if got != i%4 {
			t.Fatalf("chain frame %d = %d, want %d [R-P0-11 §3]", i, got, i%4)
		}
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
