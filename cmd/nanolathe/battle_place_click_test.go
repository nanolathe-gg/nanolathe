package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// placeClickFixture builds the production command composition over the
// synthetic ON-05 catalog: typed dispatch through the session's human-command
// queue, selection and command page read back from the published frame.
func placeClickFixture(t *testing.T, cellW, cellH int32) (*battleSession, *session.Session, pool.Handle) {
	t.Helper()
	cat := testCatalogON05()
	terrain := testWorldON05(cellW, cellH)
	uw := units.New(32, cat)
	builderDef, ok := cat.Unit("armcons")
	if !ok {
		t.Fatal("fixture builder missing")
	}
	builder, err := uw.Create(builderDef, 0, numeric.Fixed(160<<16), 0, numeric.Fixed(160<<16))
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
	}
	b := &battleSession{
		sess:  s,
		cat:   cat,
		cam:   &camera.Camera{ViewW: 640, ViewH: 480, MapW: cellW * 16, MapH: cellH * 16},
		latch: input.LatchNormal,
	}
	return b, s, builder
}

// heldClick presses the left button, holds it across several rendered frames
// the way a human click does, then releases. A one-frame click is not
// representative: the presentation loop samples input far faster than a finger
// leaves the button.
func heldClick(c *BattleController, x, y int32, held int) {
	c.Step(BattleInputFrame{MouseX: x, MouseY: y, Elapsed: 1.0 / 30.0}, nil)
	for i := 0; i < held; i++ {
		c.Step(BattleInputFrame{MouseX: x, MouseY: y, Buttons: BattleMouseButtons{Left: true}, Elapsed: 1.0 / 30.0}, nil)
	}
	c.Step(BattleInputFrame{MouseX: x, MouseY: y, Elapsed: 1.0 / 30.0}, nil)
}

// TestHeldPlacementClickQueuesOnlyTheBuildOrder locks the mouse-button
// contract: one left press/release pair runs exactly one world path
// [07 §9 "Mouse-button assignment is closed"].
//
// Placement commits on the press edge and then disarms, so without a capture
// latch the remaining held frames of an ordinary click fell through to drag
// selection, and the release issued a contextual Move that purged the build
// order the same click had just queued — the builder walked to the site and
// never built.
func TestHeldPlacementClickQueuesOnlyTheBuildOrder(t *testing.T) {
	for _, held := range []int{1, 4, 12} {
		t.Run(fmt.Sprintf("held%dframes", held), func(t *testing.T) {
			b, s, builder := placeClickFixture(t, 64, 64)
			c := NewBattleController(b)
			s.Step(s.Clock.ScaledAnchor + 1)

			if err := b.enqueueHumanCommand(session.HumanCommand{
				Kind:      session.HumanSelectionReplace,
				Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder}},
			}); err != nil {
				t.Fatalf("select: %v", err)
			}
			s.Step(s.Clock.ScaledAnchor + 1)
			f, ok := b.currentSnapshot()
			if !ok || f.CommandPage.Builder != builder {
				t.Fatalf("builder not on the command page: ok=%v page=%+v", ok, f.CommandPage)
			}

			prodDef, ok := b.cat.Unit("armsolar")
			if !ok {
				t.Fatal("fixture product missing")
			}
			b.armPlacement(prodDef)

			sx, sy := o5ScreenWorld(b.cam, numeric.Fixed(240<<16), 0, numeric.Fixed(240<<16))
			c.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Elapsed: 1.0 / 30.0}, nil)
			if !b.buildOK {
				t.Fatalf("fixture site %d,%d is not a legal placement", sx, sy)
			}
			expectedY := numeric.Fixed(int64(b.buildSiteH) << 16)
			heldClick(c, sx, sy, held)
			s.Step(s.Clock.ScaledAnchor + 1)

			u := s.Units.Unit(builder)
			q := orders.QueueForUnit(u)
			if q == nil || q.LenPrimary() != 1 {
				n := 0
				if q != nil {
					n = q.LenPrimary()
				}
				t.Fatalf("primary queue has %d orders, want exactly the build order", n)
			}
			head := q.Head()
			if !orders.IsMobileBuild(head.ID) {
				t.Fatalf("queued %s, want a mobile build order", orders.DescriptorFor(head.ID).Name)
			}
			if content.CanonicalKey(head.BuildDefKey) != prodDef.CanonicalKey {
				t.Fatalf("queued product %q, want %q", head.BuildDefKey, prodDef.CanonicalKey)
			}
			if head.GoalY != expectedY {
				t.Fatalf("queued build height %d, want validated site height %d", head.GoalY, expectedY)
			}
		})
	}
}

// TestHeldRefusedPlacementClickQueuesNothing covers the other exit from the
// placement press: an illegal site queues nothing and stays armed, and the
// rest of the held click must not fall through to a world order either.
func TestHeldRefusedPlacementClickQueuesNothing(t *testing.T) {
	// A map smaller than the viewport leaves screen area past its south-east
	// corner, where the footprint rectangle falls out of bounds.
	b, s, builder := placeClickFixture(t, 20, 20)
	c := NewBattleController(b)
	s.Step(s.Clock.ScaledAnchor + 1)
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder}},
	}); err != nil {
		t.Fatalf("select: %v", err)
	}
	s.Step(s.Clock.ScaledAnchor + 1)

	prodDef, ok := b.cat.Unit("armsolar")
	if !ok {
		t.Fatal("fixture product missing")
	}
	b.armPlacement(prodDef)
	sx, sy := int32(600), int32(430)
	c.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Elapsed: 1.0 / 30.0}, nil)
	if b.buildOK {
		t.Fatalf("site past the map corner (cell %d,%d) validated as legal", b.buildCellX, b.buildCellZ)
	}
	heldClick(c, sx, sy, 6)
	s.Step(s.Clock.ScaledAnchor + 1)

	if q := orders.QueueForUnit(s.Units.Unit(builder)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("refused placement queued %s", orders.DescriptorFor(q.Head().ID).Name)
	}
	if b.buildDef != prodDef.CanonicalKey {
		t.Fatalf("refused placement disarmed: buildDef=%q", b.buildDef)
	}
}

// TestHeldRejectedPlacementClickQueuesNothing covers the third exit from the
// placement press: the site is legal but the command boundary refuses the
// product, so placement disarms. The rest of the held click must not become a
// world order either.
func TestHeldRejectedPlacementClickQueuesNothing(t *testing.T) {
	b, s, builder := placeClickFixture(t, 64, 64)
	c := NewBattleController(b)
	s.Step(s.Clock.ScaledAnchor + 1)
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder}},
	}); err != nil {
		t.Fatalf("select: %v", err)
	}
	s.Step(s.Clock.ScaledAnchor + 1)

	// armfac is a placement product absent from the builder's authored list,
	// so commitBuild refuses it: the GUI may not invent a product [R-P0-03].
	prodDef, ok := b.cat.Unit("armfac")
	if !ok {
		t.Fatal("fixture product missing")
	}
	b.armPlacement(prodDef)
	sx, sy := o5ScreenWorld(b.cam, numeric.Fixed(240<<16), 0, numeric.Fixed(240<<16))
	c.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Elapsed: 1.0 / 30.0}, nil)
	if !b.buildOK {
		t.Fatalf("fixture site %d,%d is not a legal placement", sx, sy)
	}
	heldClick(c, sx, sy, 6)
	s.Step(s.Clock.ScaledAnchor + 1)

	if q := orders.QueueForUnit(s.Units.Unit(builder)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("rejected placement queued %s", orders.DescriptorFor(q.Head().ID).Name)
	}
}
