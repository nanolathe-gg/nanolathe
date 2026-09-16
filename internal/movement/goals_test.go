package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func terrainForGoals() *world.Terrain {
	t := &world.Terrain{CellW: 20, CellH: 20, SeaLevel: 0, Plot: make([]world.PlotCell, 400)}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

// TestGoalFamiliesWiring verifies OW-3-P wiring [04 §7.2][04 §7.4][04 §3.5][M-4].
func TestGoalFamiliesWiring(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500, SightDistance: 128}
	def.MaxDamage = 100
	def.FootprintX = 1
	def.FootprintZ = 1

	// Create attacker and target.
	hAtt, _ := w.Create(def, 0, world.CellToWorld(2), numeric.Fixed(0), world.CellToWorld(2))
	_ = hAtt
	uAtt := w.Unit(hAtt)
	sys.EnsureUnit(uAtt)
	hTgt, _ := w.Create(def, 1, world.CellToWorld(10), numeric.Fixed(0), world.CellToWorld(10))
	uTgt := w.Unit(hTgt)
	sys.EnsureUnit(uTgt)

	goalCell := path.Cell{X: 10, Z: 10}

	// Attack_Chase installs its own payload through the record's goal
	// installers [04 R-ORD-01 §3], so goalForOrder must return whatever the
	// handler bound and must invent nothing when nothing is bound. A record
	// with no payload therefore falls to the ordinary point goal, not to an
	// annulus with a made-up radius.
	nAttack := &orders.Node{ID: orders.Lookup("Attack_Chase"), Target: hTgt, Param2: 0}
	if _, _, _, ok := path.IsAnnulusGoal(sys.goalForOrder(goalCell, nAttack)); ok {
		t.Fatalf("Attack_Chase with no bound payload must not fabricate an annulus goal")
	}
	// With a banded payload installed — substate 7's (outer d, inner d/2) —
	// that payload is what comes back, centred on the target.
	nAttack.Owner = hAtt
	if !sys.InstallAnnulusGoal(orders.AnnulusGoalRequest{
		Owner: hAtt, Node: nAttack,
		X: uTgt.X, Y: uTgt.Y, Z: uTgt.Z,
		OuterRadius: 180, InnerRadius: 90,
	}) {
		t.Fatal("InstallAnnulusGoal refused the chase payload")
	}
	if cent, inner, outer, ok := path.IsAnnulusGoal(sys.goalForOrder(goalCell, nAttack)); !ok {
		t.Fatalf("Attack_Chase want the installed AnnulusGoal")
	} else if inner != 90 || outer != 180 {
		t.Fatalf("Attack annulus radii want 90/180 got %d/%d", inner, outer)
	} else if cent.X != 10 || cent.Z != 10 {
		t.Fatalf("Attack center want target 10,10 got %v", cent)
	}

	// `Follow_Ground` is a POINT goal at the ward's position plus the record's
	// stored anchor offset, arrival radius p1/2 [04 R-ORD-01 §8 point 3].
	// Changed 2026-09-01 (WU-19-6): this asserted an annulus of (p1, p1/2)
	// around the ward, plus a second case pinning the fallback radius 20 when
	// p1 was zero. Neither guard handler calls the annulus installer, and a
	// guard record never carries a caller-supplied radius, so both assertions
	// encoded invented values.
	offset := numeric.Fixed(64 << 16)
	nGuard := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: hTgt, Param1: 64, GoalX: offset, GoalZ: -offset, GoalSupplied: true}
	gGuard := sys.goalForOrder(goalCell, nGuard)
	if _, _, _, ok := path.IsAnnulusGoal(gGuard); ok {
		t.Fatalf("Follow_Ground must not be an annulus goal [04 R-ORD-01 §8]")
	}
	if cent, radius, ok := path.IsPointGoal(gGuard); !ok {
		t.Fatalf("Follow_Ground want PointGoal got %T", gGuard)
	} else {
		wantX := goalCellForWorld(uTgt.X+offset, 1)
		wantZ := goalCellForWorld(uTgt.Z-offset, 1)
		if cent.X != wantX || cent.Z != wantZ {
			t.Fatalf("guard point centre want ward+offset %d,%d got %v", wantX, wantZ, cent)
		}
		if radius != 32 {
			t.Fatalf("guard arrival radius want p1/2 = 32 got %d", radius)
		}
	}
	// With no resolvable ward the offset has nothing to be added to: the goal
	// is the ordinary point goal, never a fabricated radius.
	nGuard2 := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: 0, Param1: 64, GoalX: offset, GoalSupplied: true}
	gGuard2 := sys.goalForOrder(goalCell, nGuard2)
	if cent, radius, ok := path.IsPointGoal(gGuard2); !ok {
		t.Fatalf("wardless guard want PointGoal got %T", gGuard2)
	} else if radius != 0 || cent != goalCell {
		t.Fatalf("wardless guard want PointGoal(goalCell, 0) got %v/%d", cent, radius)
	}
	// The stationary guard installs no goal of any kind and the air twin
	// circles in airspace; neither takes the ground follow's arm
	// [04 R-ORD-01 §8 point 5][04 R-UNIT-06 §1].
	for _, name := range []string{"Guard_NoMove", "VTOL_Follow"} {
		nOther := &orders.Node{ID: orders.Lookup(name), Target: hTgt, Param1: 64, GoalX: offset, GoalSupplied: true}
		gOther := sys.goalForOrder(goalCell, nOther)
		if _, _, _, ok := path.IsAnnulusGoal(gOther); ok {
			t.Fatalf("%s must not produce an annulus goal", name)
		}
		if cent, radius, ok := path.IsPointGoal(gOther); !ok || radius != 0 || cent != goalCell {
			t.Fatalf("%s want the default PointGoal(goalCell, 0) got %T %v/%d", name, gOther, cent, radius)
		}
	}

	// Move_Ground should remain PointGoal [04 §7.2] C8
	nMove := &orders.Node{ID: orders.Lookup("Move_Ground")}
	gMove := sys.goalForOrder(goalCell, nMove)
	if _, _, ok := path.IsPointGoal(gMove); !ok {
		t.Fatalf("Move_Ground want PointGoal got %T", gMove)
	}
	if _, _, _, ok := path.IsAnnulusGoal(gMove); ok {
		t.Fatalf("Move_Ground should not be Annulus")
	}

	// Patrol remains PointGoal; no rectangle or air-goal producer is established [04 §7.2][04 §10.3][OW-3-P]
	for _, name := range []string{"Patrol", "QPatrol", "VTOL_Patrol"} {
		nPat := &orders.Node{ID: orders.Lookup(name)}
		gPat := sys.goalForOrder(goalCell, nPat)
		if _, _, ok := path.IsPointGoal(gPat); !ok {
			t.Fatalf("%s want PointGoal (Rect/Saved unwired) got %T [OW-3-P][04 §7.2][04 §10.3]", name, gPat)
		}
		if _, _, _, ok := path.IsAnnulusGoal(gPat); ok {
			t.Fatalf("%s should not be Annulus", name)
		}
		if _, ok := path.IsRectGoal(gPat); ok {
			t.Fatalf("%s rect unwired should not be RectGoal", name)
		}
	}

	// Explicitly verify RectPerimeterGoal and air-goal surfaces are NOT produced by any ground-order helper here [OW-3-P]
}

func TestActivateMoveWiresAnnulus(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 50}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500}
	def.MaxDamage = 100
	h, _ := w.Create(def, 0, world.CellToWorld(1), numeric.Fixed(0), world.CellToWorld(1))
	u := w.Unit(h)
	sys.EnsureUnit(u)
	hTgt, _ := w.Create(def, 1, world.CellToWorld(8), numeric.Fixed(0), world.CellToWorld(8))
	// Attack head
	n := &orders.Node{ID: orders.Lookup("Attack_Chase"), Target: hTgt, GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(8), Param2: 2, GoalSupplied: true}
	q := orders.QueueForUnit(u)
	q.Push(n.ID, *n)
	head := q.Head()
	if head == nil {
		t.Fatal("head nil")
	}
	// Bind and activate
	if !sys.ActivateMove(u, head) {
		t.Fatal("ActivateMove failed")
	}
	pending := sys.pathProvider.allRequests()
	if len(pending) == 0 {
		t.Fatalf("no scheduler request after ActivateMove attack")
	}
	req := pending[0]
	// No payload was installed for this record, so the submitted goal is the
	// ordinary point goal — the chase's own installers are what put a shaped
	// payload on a record [04 R-ORD-01 §3].
	if _, _, _, ok := path.IsAnnulusGoal(req.Goal); ok {
		t.Fatalf("ActivateMove Attack_Chase fabricated an annulus goal with no payload bound")
	}
	// Verify RT search still succeeds (schedule tick publishes)
	sys.Scheduler.Tick(1)
	// Move order should still be point
	u2def := &content.UnitDef{UnitName: "armflea2", MaxVelocity: 2 * 65536, TurnRate: 500}
	u2def.MaxDamage = 100
	h2, _ := w.Create(u2def, 0, world.CellToWorld(1), numeric.Fixed(0), world.CellToWorld(1))
	u2 := w.Unit(h2)
	sys.EnsureUnit(u2)
	nMove := &orders.Node{ID: orders.Lookup("Move_Ground"), GoalX: world.CellToWorld(5), GoalZ: world.CellToWorld(5), GoalSupplied: true}
	q2 := orders.QueueForUnit(u2)
	q2.Push(nMove.ID, *nMove)
	head2 := q2.Head()
	if !sys.ActivateMove(u2, head2) {
		t.Fatal("ActivateMove move failed")
	}
	pending2 := sys.pathProvider.allRequests()
	foundMove := false
	for _, r := range pending2 {
		if r.Unit == h2 {
			if _, _, ok := path.IsPointGoal(r.Goal); !ok {
				t.Fatalf("Move_Ground pending want PointGoal got %T", r.Goal)
			}
			foundMove = true
		}
	}
	if !foundMove {
		t.Fatalf("move pending not found")
	}
}

// TestInstallingASecondGoalResubmitsTheMover locks the installer contract of
// [04 R-ORD-01 §1] at the seam that broke `Attack_Chase`: installing a goal for
// a record REPLACES that record's goal, so the mover has to be re-aimed.
//
// ActivateMove admits exactly one submission per active order, which is right —
// it is what stops a record re-pathing every tick. The consequence is that the
// installer, not the activator, is what makes a second goal reach the mover.
// Without that, a record whose goal moves (a `Move_Ground` re-arm, and every
// `Attack_Chase` maneuver substate [04 R-ORD-01 §3]) keeps walking to the first
// goal it ever had: an ordered attacker marched to the spot its target had been
// standing on when the order was given, stopped there, and never followed.
func TestInstallingASecondGoalResubmitsTheMover(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500}
	def.MaxDamage = 100
	h, _ := w.Create(def, 0, world.CellToWorld(1), numeric.Fixed(0), world.CellToWorld(1))
	u := w.Unit(h)
	sys.EnsureUnit(u)

	n := &orders.Node{ID: orders.Lookup("Attack_Chase"), Owner: h}
	q := orders.QueueForUnit(u)
	q.Push(n.ID, *n)
	head := q.Head()
	if head == nil {
		t.Fatal("head nil")
	}

	if !sys.InstallPointGoal(orders.PointGoalRequest{
		Owner: h, Node: head, X: world.CellToWorld(8), Z: world.CellToWorld(8), Radius: 180,
	}) {
		t.Fatal("first install refused")
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("first ActivateMove refused")
	}
	pendingFirst := sys.pathProvider.allRequests()
	if len(pendingFirst) != 1 {
		t.Fatalf("first activation submitted %d requests, want 1", len(pendingFirst))
	}
	if c, r, ok := path.IsPointGoal(pendingFirst[0].Goal); !ok || r != 180 || c != (path.Cell{X: 8, Z: 8}) {
		t.Fatalf("first request goal = %v radius %d ok=%v, want the cell (8,8) point goal of radius 180", c, r, ok)
	}
	// Re-activating without a new goal must NOT submit again: one submission
	// per active order is the rule this test is careful not to break.
	if sys.ActivateMove(u, head) {
		t.Fatal("ActivateMove submitted twice for one unchanged active order")
	}

	// The record's goal moves. The installer must reopen the mover.
	if !sys.InstallPointGoal(orders.PointGoalRequest{
		Owner: h, Node: head, X: world.CellToWorld(2), Z: world.CellToWorld(9), Radius: 45,
	}) {
		t.Fatal("second install refused")
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("a record that installed a second goal was not re-submitted to the mover")
	}
	pendingSecond := sys.pathProvider.allRequests()
	if len(pendingSecond) != 1 {
		t.Fatalf("after the second install %d requests are outstanding, want the one replacement", len(pendingSecond))
	}
	c, r, ok := path.IsPointGoal(pendingSecond[0].Goal)
	if !ok || c != (path.Cell{X: 2, Z: 9}) || r != 45 {
		t.Fatalf("the mover is still aimed at %v radius %d; the second install must re-aim it at cell (2,9) radius 45", c, r)
	}
}

// TestFeatureWorkGoalIsTheFootprintRectangle is WU-19-5's half of
// workApproachGoal: `Reclaim` and `Resurrect` install a RECTANGLE from the
// FEATURE's footprint — "origin cell, size" [04 R-ORD-01 §5] — where the origin
// is the anchor cell the grid resolver returns [05 R-ECO-02 §2], with no
// half-footprint offset. Before WU-19-5 both rows fell through to the ordinary
// point goal on the record's own goal cell, which for a feature is the anchor
// cell itself: a cell the reclaimer can never occupy, so the search failed and
// the record abandoned instead of walking to the rock.
//
// Corrected 2026-09-01 (WU-19-24): this asserted the BARE footprint rectangle,
// `(12,9)..(14,10)` for a 3x2 feature. [04 R-PATH-01 §12] establishes that the
// class's constructor grows the installer's `(origin, size)` by the OWNING
// MOVER's own footprint, so a 1x1 reclaimer's rectangle is `(11,8)..(15,11)`
// and the feature's own cells are interior — never enumerated, which is what
// makes a blocking feature reachable at all.
//
// This arm is the fallback for a record whose payload is not bound; the handler
// installs the same rectangle itself and goalForOrderWithFootprint consults the
// bound payload first.
func TestFeatureWorkGoalIsTheFootprintRectangle(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	def := &content.UnitDef{UnitName: "armck", MaxVelocity: 2 * 65536, TurnRate: 500, BuildDistance: 128}
	def.MaxDamage = 100
	def.FootprintX, def.FootprintZ = 1, 1
	h, err := w.Create(def, 0, world.CellToWorld(2), numeric.Fixed(0), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("builder placement refused: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)

	// The world adapter is the seam internal/session composes: a cell resolves
	// to an anchored feature view carrying the definition's footprint.
	q := orders.QueueForUnit(u)
	binding := &orders.QueueBinding{
		World: &orders.WorldQueryAdapter{
			LookupFeature: func(cx, cz int32) (orders.FeatureView, bool) {
				if cx < 12 || cx > 14 || cz < 9 || cz > 10 {
					return orders.FeatureView{}, false
				}
				// Every cell of the footprint resolves to the same anchor, the
				// way the terrain's fringe hop does [05 R-ECO-02 §2].
				return orders.FeatureView{CX: 12, CZ: 9, FootprintX: 3, FootprintZ: 2}, true
			},
		},
	}
	q.SetBinding(binding)

	for _, name := range []string{"Reclaim", "Resurrect"} {
		n := &orders.Node{ID: orders.Lookup(name), Owner: h, GoalX: world.CellToWorld(13), GoalZ: world.CellToWorld(10), GoalSupplied: true}
		goal, ok := sys.workApproachGoal(u, path.Cell{X: 13, Z: 10}, n, 1, 1)
		if !ok {
			t.Fatalf("%s: the feature rows must produce a goal once a footprint is readable", name)
		}
		rect, isRect := path.IsRectGoal(goal)
		if !isRect {
			t.Fatalf("%s: goal %T is not a rectangle-perimeter goal", name, goal)
		}
		// origin (12,9), size 3x2, mover footprint 1x1:
		//   x1 = 12 − 1 = 11, x2 = 12 + 3 = 15
		//   z1 =  9 − 1 =  8, z2 =  9 + 2 = 11   [04 R-PATH-01 §12]
		if rect.Min.X != 11 || rect.Min.Z != 8 || rect.Max.X != 15 || rect.Max.Z != 11 {
			t.Fatalf("%s: rectangle %v, want the 3x2 footprint at (12,9) grown by the 1x1 mover: (11,8)..(15,11)", name, rect)
		}
		// Every cell of the feature's own footprint is INTERIOR — the search
		// never enumerates it, so a blocking feature is not a goal cell
		// [04 R-PATH-01 §12].
		for _, c := range []path.Cell{{X: 12, Z: 9}, {X: 13, Z: 9}, {X: 14, Z: 10}} {
			if goal.StartSatisfied(c) {
				t.Fatalf("%s: the feature's own cell %v is on the goal border", name, c)
			}
		}
		// Arrival is lying ON the grown border, and nowhere else.
		if !goal.StartSatisfied(path.Cell{X: 11, Z: 9}) {
			t.Fatalf("%s: the anchor flush against the feature's west edge is not on the border", name)
		}
		if goal.StartSatisfied(path.Cell{X: 19, Z: 19}) {
			t.Fatalf("%s: a cell far outside the rectangle satisfies the start predicate", name)
		}
	}

	// A record whose goal resolves to no feature produces nothing, rather than
	// a rectangle around an empty cell.
	n := &orders.Node{ID: orders.Lookup("Reclaim"), Owner: h, GoalX: world.CellToWorld(2), GoalZ: world.CellToWorld(2), GoalSupplied: true}
	if _, ok := sys.workApproachGoal(u, path.Cell{X: 2, Z: 2}, n, 1, 1); ok {
		t.Fatalf("an empty cell produced a feature rectangle")
	}
}

// TestRectangleGoalIsTheTargetFootprintGrownByTheMover locks the rectangle-goal
// constructor of [04 R-PATH-01 §12]. The installer's arguments are the TARGET's
// anchor cell and footprint size; the class stores
//
//	x1 = originX − fx    x2 = originX + sizeX
//	z1 = originZ − fz    z2 = originZ + sizeZ
//
// with `(fx, fz)` the OWNING MOVER's footprint. The two counts are the whole
// point of the correction: a one-cell target and a one-cell mover give a 3x3
// rectangle whose border is eight cells, and a 2x2 mover a 4x4 rectangle whose
// border is twelve — where the bare footprint the build stored before had a
// one-cell "border" that was the target's own impassable cell, so every rock
// and tree reclaim published an empty route and abandoned.
func TestRectangleGoalIsTheTargetFootprintGrownByTheMover(t *testing.T) {
	terrain := terrainForGoals()
	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	// The target is one cell at (10,10) and is never enumerated.
	const targetX, targetZ int32 = 10, 10

	for _, tc := range []struct {
		name       string
		moverFoot  int32
		wantMin    path.Cell
		wantMax    path.Cell
		wantBorder int
	}{
		{"1x1 mover", 1, path.Cell{X: 9, Z: 9}, path.Cell{X: 11, Z: 11}, 8},
		{"2x2 mover", 2, path.Cell{X: 8, Z: 8}, path.Cell{X: 11, Z: 11}, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := &content.UnitDef{UnitName: "armck", MaxVelocity: 2 * 65536, TurnRate: 500, MaxDamage: 100}
			def.FootprintX, def.FootprintZ = tc.moverFoot, tc.moverFoot
			h, err := w.Create(def, 0, world.CellToWorld(2), numeric.Fixed(0), world.CellToWorld(2))
			if err != nil {
				t.Fatalf("mover placement refused: %v", err)
			}
			u := w.Unit(h)
			sys.EnsureUnit(u)

			n := &orders.Node{Owner: h}
			if !sys.InstallRectangleGoal(orders.RectangleGoalRequest{Owner: h, Node: n, CellX: targetX, CellZ: targetZ, Width: 1, Depth: 1}) {
				t.Fatal("the rectangle payload was rejected")
			}
			goal := sys.moveGoalPayload(h, n)
			if goal == nil {
				t.Fatal("the rectangle payload was not retained")
			}
			rect, isRect := path.IsRectGoal(goal)
			if !isRect {
				t.Fatalf("goal %T is not a rectangle-perimeter goal", goal)
			}
			if rect.Min != tc.wantMin || rect.Max != tc.wantMax {
				t.Fatalf("rectangle (%v)..(%v), want (%v)..(%v) for a 1x1 target at (10,10) grown by this mover [04 R-PATH-01 §12]", rect.Min, rect.Max, tc.wantMin, tc.wantMax)
			}
			cells := goal.Enumerate(nil)
			if len(cells) != tc.wantBorder {
				t.Fatalf("the border enumerates %d cells, want %d", len(cells), tc.wantBorder)
			}
			// The target's own cell is interior: never enumerated, never an
			// arrival cell [04 R-PATH-01 §12].
			for _, c := range cells {
				if c.X == targetX && c.Z == targetZ {
					t.Fatal("the target's own cell is a goal cell")
				}
				if !goal.StartSatisfied(c) {
					t.Fatalf("enumerated cell %v is not on the border the arrival test admits", c)
				}
			}
			if goal.StartSatisfied(path.Cell{X: targetX, Z: targetZ}) {
				t.Fatal("the target's own cell satisfies arrival")
			}
			if goal.StartSatisfied(path.Cell{X: 2, Z: 2}) {
				t.Fatal("a cell outside the rectangle satisfies arrival")
			}
		})
	}
}
