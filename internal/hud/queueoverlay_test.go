package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
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

// TestQueueDescriptorCensusMatchesOrderTable pins the presentation census
// against the same two bytes on the simulation's order-descriptor table, where
// they are the `Class` and `AckGroup` fields. Both are transcriptions of
// [04 §3.1]'s sixty-seven-record table, and [R-P0-11 §3] is what identifies
// them as the overlay's draw mask and icon byte. The check keeps them from
// drifting without making presentation import the order package at run time.
func TestQueueDescriptorCensusMatchesOrderTable(t *testing.T) {
	seen := 0
	for _, d := range orders.Table() {
		if d.Name == "" {
			continue // the reject sentinel's empty canonical name [04 §3.1]
		}
		seen++
		got, ok := queueDescriptors[d.Name]
		if !ok {
			t.Errorf("%s has no overlay census row [04 §3.1][R-P0-11 §3]", d.Name)
			continue
		}
		if uint8(got.mask) != d.Class {
			t.Errorf("%s overlay mask 0x%02x, order table 0x%02x [R-P0-11 §3]", d.Name, uint8(got.mask), d.Class)
		}
		if got.icon != d.AckGroup {
			t.Errorf("%s overlay icon byte %d, order table %d [R-P0-11 §3]", d.Name, got.icon, d.AckGroup)
		}
	}
	if seen != 67 || len(queueDescriptors) != 67 {
		t.Errorf("census sizes: order table %d, overlay %d; want 67 each [04 §3.1]", seen, len(queueDescriptors))
	}
	// The circle bit is set by no stock record [R-P0-11 §3].
	for name, d := range queueDescriptors {
		if d.mask&QueueCircleMask != 0 {
			t.Errorf("%s sets the circle bit; no stock record does [R-P0-11 §3]", name)
		}
	}
}

// TestQueueOverlayIconHelperRunsFromTheAnchorGetter locks the two facts
// [R-P0-11 §3] establishes about the icon: the bit-2 (dash) helper calls the
// icon helper too, because that helper is also the anchor getter, and whether
// an icon appears is decided by the descriptor's icon byte alone.
func TestQueueOverlayIconHelperRunsFromTheAnchorGetter(t *testing.T) {
	iconFor := func(kind string) (uint8, bool) {
		f := queueTestFrame()
		f.OrderQueues[0].Primary = []frame.OrderView{{
			Unit: 1, Index: 0, Kind: kind, BuildProduct: "armmex", FootX: 2, FootZ: 2,
			GoalX: numeric.Fixed(16 << 16), GoalZ: numeric.Fixed(8 << 16), CreationTick: 12,
		}}
		ops := QueueOverlay(f, QueueOverlayOptions{
			Tick: 20, ShiftHeld: true, LocalOwner: 0, Project: queueTestProject,
			BuildRect: queueTestRect,
			Icon:      func(cursorIndex uint8, _ uint32) (int32, bool) { return 0, true },
		})
		for _, op := range ops {
			if op.Kind == QueuePrimitiveIcon {
				return op.IconCursor, true
			}
		}
		return 0, false
	}

	// Move_Ground: mask 0x12 sets bit 2 and not bit 8, icon byte 14
	// (`cursormove`). The chain still runs the icon helper.
	if got, ok := iconFor("Move_Ground"); !ok || got != 14 {
		t.Errorf("Move_Ground icon = %d, present %v; want cursor slot 14 [R-P0-11 §3]", got, ok)
	}
	// Capture: mask 0x08 is the icon bit alone, icon byte 4.
	if got, ok := iconFor("Capture"); !ok || got != 4 {
		t.Errorf("Capture icon = %d, present %v; want cursor slot 4 [R-P0-11 §3]", got, ok)
	}
	// MobileBuild sets bit 2 but carries icon byte 0, which is the "no icon"
	// encoding, not cursor slot 0: a queued build site draws no order icon.
	if got, ok := iconFor("MobileBuild"); ok {
		t.Errorf("MobileBuild drew icon %d; icon byte 0 means no icon [R-P0-11 §3]", got)
	}
	// A mask of zero draws nothing however privileged the unit.
	if got, ok := iconFor("VTOL_LandIfCan"); ok {
		t.Errorf("VTOL_LandIfCan drew icon %d; mask 0x00 draws nothing [R-P0-11 §3]", got)
	}
	// Wait carries icon byte 19 but mask 0x00, so nothing is drawn; and an
	// order kind with no census row at all draws nothing rather than guessing.
	if got, ok := iconFor("Wait"); ok {
		t.Errorf("Wait drew icon %d; mask 0x00 draws nothing [04 §3.1][R-P0-11 §3]", got)
	}
	if got, ok := iconFor("NotAnOrderKind"); ok {
		t.Errorf("an uncensused kind drew icon %d [R-P0-11 §3]", got)
	}
}

// TestQueueOverlayPrivilegedSourcesGetTheFullMask locks [R-P0-11 §3]'s
// correction: all four sources — tracked, command-page subject, hovered, and
// *every* selected unit — get the full five-bit mask, and the marker-only
// fallback for the rest runs only when one of the first three resolves to a
// live builder. The selection does not create a builder context.
func TestQueueOverlayPrivilegedSourcesGetTheFullMask(t *testing.T) {
	// Two local units, each holding one Capture order: mask 0x08 is the icon
	// bit alone, so an icon primitive appears only for a full-mask queue.
	order := func(u pool.Handle) frame.OrderQueueView {
		return frame.OrderQueueView{Unit: u, Primary: []frame.OrderView{{
			Unit: u, Kind: "Capture", GoalX: numeric.Fixed(16 << 16), CreationTick: 12,
		}}}
	}
	base := func() *frame.Frame {
		return &frame.Frame{
			Tick: 20,
			Units: []frame.UnitView{
				{Slot: 1, Owner: 0, Health: 100, MaxHealth: 100},
				{Slot: 2, Owner: 0, Health: 100, MaxHealth: 100},
			},
			Selection:   frame.SelectionView{LocalPlayer: 0},
			OrderQueues: []frame.OrderQueueView{order(1), order(2)},
		}
	}
	run := func(f *frame.Frame, opt QueueOverlayOptions) map[pool.Handle]bool {
		opt.Tick, opt.ShiftHeld, opt.LocalOwner = 20, true, 0
		opt.Project = queueTestProject
		opt.Icon = func(uint8, uint32) (int32, bool) { return 0, true }
		out := map[pool.Handle]bool{}
		for _, op := range QueueOverlay(f, opt) {
			if op.Kind == QueuePrimitiveIcon {
				out[op.Unit] = true
			}
		}
		return out
	}

	for _, tc := range []struct {
		name string
		opt  QueueOverlayOptions
		sel  []pool.Handle
		page pool.Handle
	}{
		{name: "tracked", opt: QueueOverlayOptions{TrackedUnit: 1}},
		{name: "command page subject", page: 1},
		{name: "hovered", opt: QueueOverlayOptions{HoveredUnit: 1}},
		{name: "selected", sel: []pool.Handle{1}},
	} {
		f := base()
		f.Selection.Handles = tc.sel
		f.CommandPage.Builder = tc.page
		got := run(f, tc.opt)
		if !got[1] {
			t.Errorf("%s: unit 1 did not get the full mask [R-P0-11 §3]", tc.name)
		}
		if got[2] {
			t.Errorf("%s: unit 2 got the full mask without being privileged [R-P0-11 §3]", tc.name)
		}
	}

	// Every selected unit, not only a lone one: "single-selected" was the
	// wording §3 corrects.
	f := base()
	f.Selection.Handles = []pool.Handle{1, 2}
	if got := run(f, QueueOverlayOptions{}); !got[1] || !got[2] {
		t.Errorf("a two-unit selection did not give both queues the full mask [R-P0-11 §3]")
	}

	// The builder-context test reads only the first three sources. A selected
	// builder does not arm the marker-only fallback for the other local units.
	markers := func(f *frame.Frame, opt QueueOverlayOptions) map[pool.Handle]bool {
		opt.Tick, opt.ShiftHeld, opt.LocalOwner = 20, true, 0
		opt.Project = queueTestProject
		opt.BuildRect = queueTestRect
		out := map[pool.Handle]bool{}
		for _, op := range QueueOverlay(f, opt) {
			if op.Kind == QueuePrimitiveMarker {
				out[op.Unit] = true
			}
		}
		return out
	}
	withBuild := func() *frame.Frame {
		f := base()
		for i := range f.OrderQueues {
			f.OrderQueues[i].Primary = []frame.OrderView{{
				Unit: f.OrderQueues[i].Unit, Kind: "MobileBuild", BuildProduct: "armmex",
				FootX: 2, FootZ: 2, GoalX: numeric.Fixed(16 << 16), CreationTick: 12,
			}}
		}
		return f
	}

	f = withBuild()
	f.Selection.Handles = []pool.Handle{1}
	if got := markers(f, QueueOverlayOptions{Builder: func(v frame.UnitView) bool { return v.Slot == 1 }}); got[2] {
		t.Errorf("a selected builder armed the marker-only fallback; only sources 1-3 do [R-P0-11 §3]")
	}
	f = withBuild()
	if got := markers(f, QueueOverlayOptions{HoveredUnit: 1, Builder: func(v frame.UnitView) bool { return v.Slot == 1 }}); !got[2] {
		t.Errorf("a hovered builder did not arm the marker-only fallback [R-P0-11 §3]")
	}
	f = withBuild()
	if got := markers(f, QueueOverlayOptions{HoveredUnit: 1, Builder: func(frame.UnitView) bool { return false }}); got[2] {
		t.Errorf("the marker-only fallback ran with no builder context [R-P0-11 §3]")
	}
	// A nil Builder callback suppresses the fallback rather than admitting
	// every local unit.
	f = withBuild()
	if got := markers(f, QueueOverlayOptions{HoveredUnit: 1}); got[2] {
		t.Errorf("a nil Builder callback admitted a non-privileged unit [R-P0-11 §3]")
	}
}
