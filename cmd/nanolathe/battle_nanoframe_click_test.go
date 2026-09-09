package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// nanoframeClickFixture is the placement fixture of battle_place_click_test.go
// with one addition: an own nanoframe standing apart from the builder, so a
// click can land on a unit whose remaining-build fraction is nonzero.
//
// The fixture builder is authored `canreclamate` because that is the mirror
// bit nano-reach reads before it will resolve any assist or repair
// [04 R-ORD-01 §7][04 R-ORD-02 §1]; every stock construction unit authors it.
func nanoframeClickFixture(t *testing.T) (*battleSession, *session.Session, pool.Handle, pool.Handle) {
	t.Helper()
	cat := testCatalogON05()
	terrain := testWorldON05(64, 64)
	builderDef, ok := cat.Unit("armcons")
	if !ok {
		t.Fatal("fixture builder missing")
	}
	builderDef.CanReclamate = true
	// The assist annulus is `builddistance + half` around the target, so the
	// fixture authors a build distance the neighbouring frame falls inside
	// [04 R-ORD-01 §5] `HelpBuild` phase 0.
	builderDef.BuildDistance = 128
	prodDef, ok := cat.Unit("armsolar")
	if !ok {
		t.Fatal("fixture product missing")
	}

	uw := units.NewSliced(32, cat)
	builder, err := uw.Create(builderDef, 0, numeric.Fixed(160<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}
	// CreateNanoframe seeds the remaining-build fraction to 1 and health to 0,
	// which is the state the construction step drives down from
	// [07 R-WGT-01 §10] writer 1.
	frameUnit, err := uw.CreateNanoframe(prodDef, 0, numeric.Fixed(320<<16), 0, numeric.Fixed(320<<16))
	if err != nil {
		t.Fatal(err)
	}

	s := &session.Session{
		State:      session.StateBattle,
		Catalog:    cat,
		World:      terrain,
		Units:      uw,
		LocalOwner: 0,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   &frame.Buffer{},
		Vis:        visibility.New(terrain, 0),
		Movement:   movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid()),
	}
	s.Vis.SetLocal(0)
	b := &battleSession{
		sess: s,
		cat:  cat,
		cam:  &camera.Camera{ViewW: 640, ViewH: 480, MapW: 64 * 16, MapH: 64 * 16},
	}
	// Picking is a hull test over the candidate's root-piece bounds
	// [07 R-REV-01]; these tests hold no VFS, so they install the fixture hull.
	installTestHullModels()
	return b, s, builder, frameUnit
}

// TestClickOnOwnNanoframeDoesNotSelectIt is bug half one of WU-19-106.
//
// The world-click handler's select branch is "cursor kind 0x0F — the
// resolver's select answer: latch idle and the hovered unit is an own
// SELECTABLE unit (own slot, selectable bit, remaining-build fraction `0.0`,
// post-capture grace zero, carrier null or itself a visible carrier)"
// [07 R-CAM-01 §14 step 2]. That is the shared eligibility predicate `E(u)` of
// [07 R-WGT-01 §9][07 R-WGT-01 §10], and a nanoframe fails its second clause.
//
// The click path used to test ownership alone, so a half-built structure could
// be picked up into the selection and given orders it cannot carry out.
func TestClickOnOwnNanoframeDoesNotSelectIt(t *testing.T) {
	b, s, _, nano := nanoframeClickFixture(t)
	c := newReplayController(b)
	s.Step(s.Clock.ScaledAnchor + 1)

	sx, sy := o5ScreenWorld(b.cam, numeric.Fixed(320<<16), 0, numeric.Fixed(320<<16))
	// The pick must actually land on the nanoframe, or the test proves nothing.
	if h, _, _ := b.pickTarget(sx, sy); h != nano {
		t.Fatalf("fixture point %d,%d picks handle %d, want the nanoframe %d", sx, sy, h, nano)
	}

	heldClick(c, sx, sy, 4)
	s.Step(s.Clock.ScaledAnchor + 1)

	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed frame")
	}
	for _, h := range f.Selection.Handles {
		if h == nano {
			t.Fatalf("clicking an own nanoframe selected it; E(u) fails on a nonzero remaining-build fraction [07 R-WGT-01 §10][07 R-CAM-01 §14 step 2]")
		}
	}
	if len(f.Selection.Handles) != 0 {
		t.Fatalf("selection = %v, want empty", f.Selection.Handles)
	}
}

// TestBuilderClickOnOwnNanoframeAssistsIt is bug half two of WU-19-106.
//
// With the select branch correctly refusing a nanoframe, the click falls to
// the handler's next branch — "cursor kind below `0x11` → issue the resolved
// order (the latch code, or the contextual code with the latch idle) for the
// selection at the pointer's world point" [07 R-CAM-01 §14 step 3]. The
// contextual code resolves "nano-reach passes and the target is unfinished →
// resolve as code 8", and code 8 on an unfinished target is `HelpBuild`
// [04 R-ORD-02 §1]. The own-unit reject that follows it in the same step is
// gated on the target being COMPLETE, so it never covers a nanoframe.
func TestBuilderClickOnOwnNanoframeAssistsIt(t *testing.T) {
	b, s, builder, nano := nanoframeClickFixture(t)
	c := newReplayController(b)
	s.Step(s.Clock.ScaledAnchor + 1)

	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder}},
	}); err != nil {
		t.Fatalf("select: %v", err)
	}
	s.Step(s.Clock.ScaledAnchor + 1)

	sx, sy := o5ScreenWorld(b.cam, numeric.Fixed(320<<16), 0, numeric.Fixed(320<<16))
	if h, _, _ := b.pickTarget(sx, sy); h != nano {
		t.Fatalf("fixture point %d,%d picks handle %d, want the nanoframe %d", sx, sy, h, nano)
	}

	heldClick(c, sx, sy, 4)
	s.Step(s.Clock.ScaledAnchor + 1)

	q := orders.QueueForUnit(s.Units.Unit(builder))
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("clicking a nanoframe with a builder selected queued nothing; want HelpBuild [04 R-ORD-02 §1]")
	}
	head := q.Head()
	if name := orders.DescriptorFor(head.ID).Name; name != "HelpBuild" {
		t.Fatalf("queued %q, want HelpBuild [04 R-ORD-02 §1] code 1 step 3", name)
	}
	if head.Target != nano {
		t.Fatalf("HelpBuild targets handle %d, want the nanoframe %d", head.Target, nano)
	}

	// The builder is still the only selected unit: the click issued an order,
	// it did not change the selection [07 R-CAM-01 §14 step 3].
	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed frame")
	}
	if len(f.Selection.Handles) != 1 || f.Selection.Handles[0] != builder {
		t.Fatalf("selection = %v, want just the builder %d", f.Selection.Handles, builder)
	}
}

// TestCursorOverOwnNanoframeIsNotTheSelectShape covers the same divergence at
// the pointer: step 4 of the shape chooser is "when that candidate set is
// empty the idle latch over an own, SELECTABLE, FINISHED unit gives
// `cursorselect` and everything else gives `cursornormal`", and the tests
// behind "selectable, finished" are the shared eligibility predicate
// [07 §8 "The shape chooser is closed"][07 R-WGT-01 §9].
//
// The pick copy the chooser reads used to leave the remaining-build fraction
// zero, so a nanoframe advertised the select shape the click branch would then
// refuse — the cursor promised a selection the click could not make.
func TestCursorOverOwnNanoframeIsNotTheSelectShape(t *testing.T) {
	b, s, _, nano := nanoframeClickFixture(t)
	s.Step(s.Clock.ScaledAnchor + 1)

	sx, sy := o5ScreenWorld(b.cam, numeric.Fixed(320<<16), 0, numeric.Fixed(320<<16))
	h, target, _ := b.pickTarget(sx, sy)
	if h != nano {
		t.Fatalf("fixture point %d,%d picks handle %d, want the nanoframe %d", sx, sy, h, nano)
	}
	if target == nil || target.Remaining == 0 {
		t.Fatalf("the pick copy dropped the remaining-build fraction (%v); the chooser cannot see the nanoframe [07 R-WGT-01 §10]", target)
	}

	got := hud.ChooseCursor(input.LatchNormal,
		hud.CursorSelection{Viewer: s.LocalOwner},
		hud.CursorHover{OverWorld: true, Target: target})
	if got == render.CursorSelect {
		t.Fatalf("the idle pointer offered cursorselect over a nanoframe; E(u) fails on it [07 §8 step 4][07 R-WGT-01 §9]")
	}
	if got != render.CursorNormal {
		t.Fatalf("idle cursor over a nanoframe = %d, want cursornormal (%d)", got, render.CursorNormal)
	}
}
