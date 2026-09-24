package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// passCase steps a mover heading east into a cell held by a second mover and
// reports whether the commit was rejected (DESIGN_MOVEMENT_PATH "Modern
// allied pass-through").
type passCase struct {
	rules        Rules
	ownerB       uint8
	allied       bool
	headingB     uint16 // the blocker's committed heading; 0x4000 is west
	moverEndCell int32  // the mover's final route point, in cells along its row
	blockerRoute bool
}

func runPassCase(t *testing.T, c passCase) (blocked bool, sys *System) {
	t.Helper()
	sys = NewSystem(syntheticTerrainForIntegrate(), Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255}, NewOccupancyGrid())
	sys.Rules = c.rules
	w := newMovementFixtureWorld(4)
	def := setScratchMovement(&content.UnitDef{
		UnitName: "allied-pass-test", FootprintX: 1, FootprintZ: 1, BMCode: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255})
	row := world.CellToWorld(10)
	// The mover reaches two cells on its first tick; the blocker holds that
	// destination cell [04 R-COLL-01 §2][04 R-MOV-01 §4].
	b, err := w.Create(def, c.ownerB, world.CellToWorld(7), 0, row)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.Create(def, 0, world.CellToWorld(5), 0, row)
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	sys.EnsureUnit(w.Unit(a))
	sys.EnsureUnit(w.Unit(b))
	handleRow(sys.Collisions, b).Heading = c.headingB
	rowZ := int32(row.Raw() >> 16)
	if c.blockerRoute {
		handleRow(sys.Routes, b).PublishAtRevision([]Point{{X: 7*16 + 8, Z: rowZ}, {X: 8, Z: rowZ}}, sys.staticObstacleRevision())
	}
	move := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(w.Unit(a))
	q.Push(move, orders.Node{Owner: a, GoalX: world.CellToWorld(c.moverEndCell), GoalZ: row, GoalSupplied: true})
	if c.allied {
		q.SetBinding(&orders.QueueBinding{Lookup: w.Unit, World: &orders.WorldQueryAdapter{DeclaresAlliance: func(from, toward uint8) bool { return true }}})
	}
	head := q.Head()
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: head.Owner, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	handleRow(sys.Routes, a).PublishAtRevision([]Point{{X: 5*16 + 8, Z: rowZ}, {X: c.moverEndCell*16 + 8, Z: rowZ}}, sys.staticObstacleRevision())
	setHandleRow(&sys.activeOrders, a, &activeMove{order: head, token: 7})
	sys.BeginTick(1)
	res := sys.StepUnit(a, 1)
	sys.EndTick(1)
	if !res.Blocked && handleRow(sys.Collisions, a).CachedAnchor.X != 7 {
		t.Fatalf("an unblocked mover did not commit into the passed cell: anchor %+v", handleRow(sys.Collisions, a).CachedAnchor)
	}
	if res.Blocked && handleRow(sys.Collisions, a).BlockerID != int(b) {
		t.Fatalf("rejected by %d, not by the blocker %d", handleRow(sys.Collisions, a).BlockerID, b)
	}
	return res.Blocked, sys
}

func TestAlliedPassThrough(t *testing.T) {
	west := uint16(0x4000)
	for _, tc := range []struct {
		name    string
		c       passCase
		blocked bool
	}{
		{"strict keeps the retail rejection", passCase{rules: StrictRules{}, headingB: west, moverEndCell: 18, blockerRoute: true}, true},
		{"community keeps the retail rejection", passCase{rules: CommunityRules{}, headingB: west, moverEndCell: 18, blockerRoute: true}, true},
		{"modern same owner head-on passes", passCase{rules: &ModernRules{}, headingB: west, moverEndCell: 18, blockerRoute: true}, false},
		{"modern mutually allied head-on passes", passCase{rules: &ModernRules{}, ownerB: 1, allied: true, headingB: west, moverEndCell: 18, blockerRoute: true}, false},
		{"modern other owner without alliance blocks", passCase{rules: &ModernRules{}, ownerB: 1, headingB: west, moverEndCell: 18, blockerRoute: true}, true},
		{"modern same-direction traffic blocks", passCase{rules: &ModernRules{}, headingB: 0xC000, moverEndCell: 18, blockerRoute: true}, true},
		{"modern idle blocker blocks", passCase{rules: &ModernRules{}, headingB: west, moverEndCell: 18, blockerRoute: false}, true},
		{"modern mover near its route end blocks", passCase{rules: &ModernRules{}, headingB: west, moverEndCell: 8, blockerRoute: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocked, _ := runPassCase(t, tc.c)
			if blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v", blocked, tc.blocked)
			}
		})
	}
}

// The answers themselves, and that a Strict tick never resolves an alliance
// query.
func TestAlliedPassThroughAnswers(t *testing.T) {
	for _, r := range []Rules{StrictRules{}, CommunityRules{}} {
		if r.AlliedPassThrough(nil) {
			t.Fatalf("%T allows pass-through", r)
		}
	}
	if !(&ModernRules{}).AlliedPassThrough(nil) {
		t.Fatal("Modern does not allow pass-through")
	}
	_, sys := runPassCase(t, passCase{rules: StrictRules{}, allied: true, headingB: 0x4000, moverEndCell: 18, blockerRoute: true})
	if sys.passAlliance != nil {
		t.Fatal("a Strict tick resolved the alliance query")
	}
}
