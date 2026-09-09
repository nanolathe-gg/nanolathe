package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestBuildGhostRunsKnownSiteGate locks the build cursor's use of the
// PLAYER-record form of the footprint blocker [04 R-P0-08-B §1]: the ghost
// rejects a site the local viewing slot cannot currently see, while the
// null-player form every other placement caller uses accepts the same site.
// The ghost previously called the null-player form, so an unseen site drew as
// placeable.
func TestBuildGhostRunsKnownSiteGate(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(64, 64)
	uw := units.NewSliced(32, cat)
	builderDef, ok := cat.Unit("armcons")
	if !ok {
		t.Fatal("fixture builder missing")
	}
	builder, err := uw.Create(builderDef, 0, numeric.Fixed(160<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}
	// ModeHistoryEnabled zeroes the word mask at construction, so nothing is
	// currently visible to any viewing slot [03 §3.1].
	s := &session.Session{
		State:      session.StateBattle,
		Catalog:    cat,
		World:      terrain,
		Units:      uw,
		LocalOwner: 0,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   &frame.Buffer{},
		Vis:        visibility.New(terrain, visibility.ModeHistoryEnabled),
	}
	s.Vis.SetLocal(0)
	b := &battleSession{
		sess: s,
		cat:  cat,
		cam:  &camera.Camera{ViewW: 640, ViewH: 480, MapW: 64 * 16, MapH: 64 * 16},
	}
	prodDef, ok := cat.Unit("armsolar")
	if !ok {
		t.Fatal("fixture product missing")
	}
	footX, footZ := footprintCellsForCatalog(cat, prodDef)
	const cx, cz = int32(15), int32(15)

	// The null-player form still accepts the site: nothing about the terrain,
	// footprint or occupancy refuses it. Only the known-site gate does.
	if _, err := s.PreviewPlacement(cx, cz, prodDef, footX, footZ, pool.Handle(builder)); err != nil {
		t.Fatalf("null-player preview rejected the fixture site (%v); the test would prove nothing", err)
	}
	if _, err := b.checkProductPlacement(cx, cz, prodDef, footX, footZ, uint16(builder)); err == nil {
		t.Fatal("the build ghost accepted an unseen site: it is not running the known-site gate [04 R-P0-08-B §1]")
	}

	// Once the site's LOS cell carries the local viewing slot's bit, the two
	// forms agree again — the gate is a visibility test, not a second rule set.
	revealPlacementSite(t, s, terrain, cx, cz, footX, footZ)
	if _, err := b.checkProductPlacement(cx, cz, prodDef, footX, footZ, uint16(builder)); err != nil {
		t.Fatalf("the build ghost rejected a visible, otherwise legal site: %v", err)
	}
}

// revealPlacementSite sets the local viewing slot's bit on the LOS cell the
// known-site gate projects the footprint centre onto: world centre
// ((footX+2·cellX)·8, (footZ+2·cellZ)·8), then vx = worldX>>5 and
// vz = (worldZ − height>>1)>>5 [04 R-P0-08-B §1][03 §2.1].
func revealPlacementSite(t *testing.T, s *session.Session, terrain *world.Terrain, cx, cz, footX, footZ int32) {
	t.Helper()
	worldX := (footX + 2*cx) * 8
	worldZ := (footZ + 2*cz) * 8
	height := int32(terrain.HeightAt(numeric.FixedFromInt(int64(worldX)), numeric.FixedFromInt(int64(worldZ))).Raw() >> 16)
	vx := worldX >> 5
	vz := (worldZ - (height >> 1)) >> 5
	mask := s.Vis.WordMask()
	idx := int(vz*s.Vis.W + vx)
	if idx < 0 || idx >= len(mask) {
		t.Fatalf("fixture LOS cell (%d,%d) outside the %dx%d grid", vx, vz, s.Vis.W, s.Vis.H)
	}
	mask[idx] |= 1 << 0 // the local viewing slot
}

// placeClickFixture builds the production command composition over the
// synthetic ON-05 catalog: typed dispatch through the session's human-command
// queue, selection and command page read back from the published frame.
func placeClickFixture(t *testing.T, cellW, cellH int32) (*battleSession, *session.Session, pool.Handle) {
	t.Helper()
	cat := testCatalogON05()
	terrain := testWorldON05(cellW, cellH)
	uw := units.NewSliced(32, cat)
	builderDef, ok := cat.Unit("armcons")
	if !ok {
		t.Fatal("fixture builder missing")
	}
	builder, err := uw.Create(builderDef, 0, numeric.Fixed(160<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}
	// The build ghost is the one placement caller that binds the blocker's
	// fourth argument to a player record and runs the known-site gate
	// [04 R-P0-08-B §1], so the fixture needs a visibility service. Mode 0
	// leaves history and current coverage disabled, which fills the word mask
	// with every viewing slot's bit — the whole fixture map is visible, so
	// these tests still measure the mouse-button contract and not fog.
	s := &session.Session{
		State:      session.StateBattle,
		Catalog:    cat,
		World:      terrain,
		Units:      uw,
		LocalOwner: 0,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   &frame.Buffer{},
		Vis:        visibility.New(terrain, 0),
	}
	s.Vis.SetLocal(0)
	b := &battleSession{
		sess: s,
		cat:  cat,
		cam:  &camera.Camera{ViewW: 640, ViewH: 480, MapW: cellW * 16, MapH: cellH * 16},
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
		f := BattleInputFrame{MouseX: x, MouseY: y, Buttons: BattleMouseButtons{Left: true}, Elapsed: 1.0 / 30.0}
		// The device sampler reports the press edge on the first held frame
		// only; a hand-authored frame states its edges explicitly [07 §2].
		f.PressedButtons[input.MouseButtonLeft] = i == 0
		c.Step(f, nil)
	}
	f := BattleInputFrame{MouseX: x, MouseY: y, Elapsed: 1.0 / 30.0}
	f.ReleasedButtons[input.MouseButtonLeft] = true
	c.Step(f, nil)
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
			c := newReplayController(b)
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
			if !b.battleState().Input.BuildOK {
				t.Fatalf("fixture site %d,%d is not a legal placement", sx, sy)
			}
			expectedY := numeric.Fixed(int64(b.battleState().Input.BuildSiteH) << 16)
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
	c := newReplayController(b)
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
	if b.battleState().Input.BuildOK {
		t.Fatalf("site past the map corner (cell %d,%d) validated as legal", b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ)
	}
	heldClick(c, sx, sy, 6)
	s.Step(s.Clock.ScaledAnchor + 1)

	if q := orders.QueueForUnit(s.Units.Unit(builder)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("refused placement queued %s", orders.DescriptorFor(q.Head().ID).Name)
	}
	if b.battleState().Input.BuildDef != prodDef.CanonicalKey {
		t.Fatalf("refused placement disarmed: buildDef=%q", b.battleState().Input.BuildDef)
	}
}

// TestHeldRejectedPlacementClickQueuesNothing covers the third exit from the
// placement press: the site is legal but the command boundary refuses the
// product, so placement disarms. The rest of the held click must not become a
// world order either.
func TestHeldRejectedPlacementClickQueuesNothing(t *testing.T) {
	b, s, builder := placeClickFixture(t, 64, 64)
	c := newReplayController(b)
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
	if !b.battleState().Input.BuildOK {
		t.Fatalf("fixture site %d,%d is not a legal placement", sx, sy)
	}
	heldClick(c, sx, sy, 6)
	s.Step(s.Clock.ScaledAnchor + 1)

	if q := orders.QueueForUnit(s.Units.Unit(builder)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("rejected placement queued %s", orders.DescriptorFor(q.Head().ID).Name)
	}
}
