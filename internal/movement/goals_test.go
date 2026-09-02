package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
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
	nGuard := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: hTgt, Param1: 64, GoalX: offset, GoalZ: -offset}
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
	nGuard2 := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: 0, Param1: 64, GoalX: offset}
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
		nOther := &orders.Node{ID: orders.Lookup(name), Target: hTgt, Param1: 64, GoalX: offset}
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
	n := &orders.Node{ID: orders.Lookup("Attack_Chase"), Target: hTgt, GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(8), Param2: 2}
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
	nMove := &orders.Node{ID: orders.Lookup("Move_Ground"), GoalX: world.CellToWorld(5), GoalZ: world.CellToWorld(5)}
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
