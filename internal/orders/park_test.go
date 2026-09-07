package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func parkTestUnit(t *testing.T, footX int32, minWaterDepth int32, canFly bool) (*units.World, *units.Unit) {
	t.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "parkprod"},
		UnitName:         "parkprod",
		FootprintX:       footX,
		FootprintZ:       footX,
		MaxDamage:        100,
		BMCode:           1,
		CanFly:           canFly,
		MinWaterDepth:    minWaterDepth,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newOrdersFixtureWorld(4, cat)
	// Cell (10, 20) exactly: whole units to cells is an arithmetic shift by 20
	// with no footprint bias [04 R-FAC-02 §4].
	h, err := w.Create(def, 0, numeric.Fixed(int64(10)<<20), 0, numeric.Fixed(int64(20)<<20))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatal("no unit")
	}
	return w, u
}

// TestParkPhase0InstallsRectangleGoal locks the [04 R-ORD-01 §2] Park row's
// rectangle: s = FootPrintX with no +3 for a land movement class (template
// MinWaterDepth -10000), origin (cellX-4s, cellZ-3s), size (8s, 6s) cells,
// gate 0xE0, advance.
func TestParkPhase0InstallsRectangleGoal(t *testing.T) {
	ensureParkHandler()
	_, u := parkTestUnit(t, 2, -10000, false)
	n := &Node{ID: Lookup("Park"), Deadline: -1}
	if code := parkHandler(u, n, 0, 100); code != 1 {
		t.Fatalf("phase 0 code=%d, want advance (1)", code)
	}
	if n.DynamicGate != 0xE0 {
		t.Fatalf("gate=%#x, want 0xe0", n.DynamicGate)
	}
	minX, minZ, maxX, maxZ, ok := ParkGoalRect(n)
	if !ok {
		t.Fatal("no rectangle installed")
	}
	// s = 2: origin (10-8, 20-6) = (2, 14), size (16, 12) so max is (17, 25).
	if minX != 2 || minZ != 14 || maxX != 17 || maxZ != 25 {
		t.Fatalf("rect=(%d,%d)-(%d,%d), want (2,14)-(17,25)", minX, minZ, maxX, maxZ)
	}
	// The nearest border is 3s cells away in Z: the border walk is what carries
	// a no-rally product off its pad [04 R-FAC-02 §4].
	if minZ >= 20 || maxZ <= 20 {
		t.Fatalf("rectangle does not straddle the unit's own cell: z %d..%d", minZ, maxZ)
	}
}

// TestParkPhase0RoutesThroughTheRectangleInstaller locks the record-level half
// of the row's "install a rectangle goal with origin (cellX − 4s, cellZ − 3s)
// and size (8s, 6s)" [04 R-ORD-01 §2]: the geometry goes to the SHARED
// rectangle installer — the seam whose movement side performs the `0x80`
// rebind raise of [04 R-ORD-01 §9] — and the helper finishes by clearing
// pending `0x20`-`0x200` [04 R-ORD-01 §1].
//
// Before WU-19-100 phase 0 wrote the rectangle to the parameter words and
// called no installer at all, so a parking product's movement controller held
// no payload and neither the raise nor the clear ever happened.
func TestParkPhase0RoutesThroughTheRectangleInstaller(t *testing.T) {
	ensureParkHandler()
	_, u := parkTestUnit(t, 2, -10000, false)
	var rects []RectangleGoalRequest
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{
		Movement: &MovementGoalAdapter{
			InstallRectangle: func(req RectangleGoalRequest) bool { rects = append(rects, req); return true },
			Release:          func(*Node) bool { return true },
		},
	})
	n := &Node{ID: Lookup("Park"), Owner: u.Handle, Deadline: -1, Satisfied: 0x3E0}

	if code := parkHandler(u, n, 0, 100); code != 1 {
		t.Fatalf("phase 0 code=%d, want advance (1)", code)
	}
	if len(rects) != 1 {
		t.Fatalf("%d rectangle installs, want exactly 1", len(rects))
	}
	// s = 2: origin (10-8, 20-6) = (2, 14), size (8s, 6s) = (16, 12).
	got := rects[0]
	if got.Node != n || got.Owner != u.Handle {
		t.Fatalf("install request identifies (%v, %v), want the Park record and its owner", got.Node, got.Owner)
	}
	if got.CellX != 2 || got.CellZ != 14 || got.Width != 16 || got.Depth != 12 {
		t.Fatalf("install request=(%d,%d) %dx%d, want (2,14) 16x12", got.CellX, got.CellZ, got.Width, got.Depth)
	}
	if n.Satisfied&0x3E0 != 0 {
		t.Fatalf("pending word %#x, want 0x20-0x200 cleared [04 R-ORD-01 §1]", n.Satisfied)
	}
	// The read-back seam still reports the same rectangle for the movement
	// layer's fallback path.
	if minX, minZ, maxX, maxZ, ok := ParkGoalRect(n); !ok || minX != got.CellX || minZ != got.CellZ ||
		maxX != got.CellX+got.Width-1 || maxZ != got.CellZ+got.Depth-1 {
		t.Fatalf("ParkGoalRect=(%d,%d)-(%d,%d) ok=%v, want the installed rectangle", minX, minZ, maxX, maxZ, ok)
	}
}

// TestParkAddsThreeOnNonNegativeMinWaterDepth locks the corrected +3 term: the
// word read is the movement class's MinWaterDepth, not a yard-map width
// [04 R-FAC-02 §4][04 R-FAC-02 §7].
func TestParkAddsThreeOnNonNegativeMinWaterDepth(t *testing.T) {
	ensureParkHandler()
	_, land := parkTestUnit(t, 2, -10000, false)
	_, ship := parkTestUnit(t, 2, 0, false)
	landNode := &Node{ID: Lookup("Park"), Deadline: -1}
	shipNode := &Node{ID: Lookup("Park"), Deadline: -1}
	parkHandler(land, landNode, 0, 0)
	parkHandler(ship, shipNode, 0, 0)
	if landNode.Param3 != 2 {
		t.Fatalf("land s=%d, want 2 (no +3 on the template default)", landNode.Param3)
	}
	if shipNode.Param3 != 5 {
		t.Fatalf("non-negative MinWaterDepth s=%d, want 5", shipNode.Param3)
	}
}

// TestParkPhase1Branches locks the three phase-1 outcomes of the Park row.
func TestParkPhase1Branches(t *testing.T) {
	ensureParkHandler()
	_, u := parkTestUnit(t, 2, -10000, false)

	// Arrival bit completes.
	n := &Node{ID: Lookup("Park"), Phase: 1, Deadline: -1}
	if code := parkHandler(u, n, parkArrivalBit, 100); code != 5 {
		t.Fatalf("arrival code=%d, want complete (5)", code)
	}

	// No arrival and nothing behind it: deadline 30, restart. The shared
	// deadline setter ORs bit 0 [04 R-ORD-01 §1].
	q := QueueForUnit(u)
	q.Push(Lookup("Park"), Node{Phase: 1, Deadline: -1})
	head := q.Primary()[0]
	head.Phase = 1
	head.DynamicGate = 0
	if code := parkHandler(u, head, 0, 100); code != 0 {
		t.Fatalf("waiting code=%d, want restart (0)", code)
	}
	if head.Deadline != 130 || head.DynamicGate&1 == 0 {
		t.Fatalf("deadline=%d gate=%#x, want 130 with bit 0", head.Deadline, head.DynamicGate)
	}

	// A record behind it completes it immediately.
	q.Push(Lookup("Move_Ground"), Node{Deadline: -1})
	head.Phase = 1
	if code := parkHandler(u, head, 0, 200); code != 5 {
		t.Fatalf("record-behind code=%d, want complete (5)", code)
	}
}

// TestParkCanFlyReidentifiesAsAirMove locks the canfly branch: goal = own
// position, the record becomes VTOL_Move, restart [04 R-FAC-02 §4].
func TestParkCanFlyReidentifiesAsAirMove(t *testing.T) {
	ensureParkHandler()
	_, u := parkTestUnit(t, 2, -10000, true)
	n := &Node{ID: Lookup("Park"), Deadline: -1}
	if code := parkHandler(u, n, 0, 0); code != 0 {
		t.Fatalf("canfly code=%d, want restart (0)", code)
	}
	if n.ID != Lookup("VTOL_Move") {
		t.Fatalf("record id=%d, want VTOL_Move", n.ID)
	}
	if n.GoalX != u.X || n.GoalZ != u.Z {
		t.Fatalf("goal=(%d,%d), want own position (%d,%d)", n.GoalX.Raw(), n.GoalZ.Raw(), u.X.Raw(), u.Z.Raw())
	}
	if _, _, _, _, ok := ParkGoalRect(n); ok {
		t.Fatal("air branch must leave no rectangle behind")
	}
}

// TestParkHasADispatchableHandler is the regression this file exists for: the
// descriptor shipped with no handler at all, so the pump recorded a nil-handler
// diagnostic and a released factory product sat at phase 0 forever.
func TestParkHasADispatchableHandler(t *testing.T) {
	ensureParkHandler()
	id := Lookup("Park")
	if id == 0 {
		t.Fatal("Park descriptor missing")
	}
	if DescriptorFor(id).Handler == nil {
		t.Fatal("Park has no handler: the pump cannot dispatch it")
	}
}
