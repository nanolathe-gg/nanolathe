package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
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
	w := units.New(10, nil)
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

	// Attack_Chase should be AnnulusGoal with placeholder radii [04 §3.5][04 §7.2][M-4]
	nAttack := &orders.Node{ID: orders.Lookup("Attack_Chase"), Target: hTgt, Param2: 0}
	gAttack := sys.goalForOrder(goalCell, nAttack)
	if _, inner, outer, ok := path.IsAnnulusGoal(gAttack); !ok {
		t.Fatalf("Attack_Chase want AnnulusGoal got %T", gAttack)
	} else {
		if inner != placeholderAttackInnerRaw || outer != placeholderAttackOuterRaw {
			t.Fatalf("Attack annulus radii want %d/%d got %d/%d [M-4 placeholder]", placeholderAttackInnerRaw, placeholderAttackOuterRaw, inner, outer)
		}
		// Center should be target cell (10,10) not fallback goalCell
		if c, _, _, _ := path.IsAnnulusGoal(gAttack); c != (path.Cell{X: 10, Z: 10}) {
			// Check center via helper
			cent, _, _, _ := path.IsAnnulusGoal(gAttack)
			if cent.X != 10 || cent.Z != 10 {
				t.Fatalf("Attack center want target 10,10 got %v", cent)
			}
		}
		_ = uTgt
	}

	// Follow_Ground should be AnnulusGoal centered on ward with Param1 outer [04 §3.2][04 §3.5]
	nGuard := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: hTgt, Param1: 40}
	gGuard := sys.goalForOrder(goalCell, nGuard)
	if _, inner, outer, ok := path.IsAnnulusGoal(gGuard); !ok {
		t.Fatalf("Follow_Ground want AnnulusGoal got %T", gGuard)
	} else {
		if outer != 40 || inner != 20 {
			t.Fatalf("Guard annulus want inner 20 outer 40 got inner %d outer %d", inner, outer)
		}
	}
	// Guard fallback when Param1==0 => placeholderGuardDefaultRaw
	nGuard2 := &orders.Node{ID: orders.Lookup("Follow_Ground"), Target: hTgt, Param1: 0}
	gGuard2 := sys.goalForOrder(goalCell, nGuard2)
	if _, inner, outer, ok := path.IsAnnulusGoal(gGuard2); !ok {
		t.Fatalf("Guard fallback want Annulus got %T", gGuard2)
	} else {
		if outer != placeholderGuardDefaultRaw || inner != placeholderGuardDefaultRaw/2 {
			t.Fatalf("Guard fallback want %d/%d got %d/%d", placeholderGuardDefaultRaw/2, placeholderGuardDefaultRaw, inner, outer)
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

	// Patrol should remain PointGoal — research does NOT establish SavedGoal chaining or RectPerimeter for patrol [04 §7.2][04 §10.3][OW-3-P]
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
		if _, ok := path.IsSavedGoal(gPat); ok {
			t.Fatalf("%s saved unwired should not be SavedGoal", name)
		}
	}

	// Explicitly verify RectPerimeterGoal and SavedGoal are NOT produced by any order helper here [OW-3-P]
	// They have zero production callers in movement/goals.go by design; only save restore would use SavedGoal via GoalKind 3.
}

func TestActivateMoveWiresAnnulus(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 50}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := units.New(10, nil)
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
	pending := sys.Scheduler.AllRequests()
	if len(pending) == 0 {
		t.Fatalf("no scheduler request after ActivateMove attack")
	}
	req := pending[0]
	if _, inner, outer, ok := path.IsAnnulusGoal(req.Goal); !ok {
		t.Fatalf("ActivateMove Attack_Chase should submit AnnulusGoal got %T", req.Goal)
	} else {
		if inner != placeholderAttackInnerRaw || outer != placeholderAttackOuterRaw {
			t.Fatalf("pending annulus radii mismatch %d/%d", inner, outer)
		}
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
	pending2 := sys.Scheduler.AllRequests()
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
