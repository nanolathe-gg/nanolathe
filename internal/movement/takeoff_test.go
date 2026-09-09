package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func takeoffFixture(t *testing.T) (*System, *units.World, *units.Unit) {
	t.Helper()
	ter := syntheticFlat(32, 32)
	sys := NewSystem(ter, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	def := setScratchMovement(&content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("takeoffscout")},
		UnitName:         "takeoffscout",
		CanFly:           true,
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		CruiseAlt:        60,
		MaxVelocity:      4 * 65536,
		Acceleration:     65536 / 4,
		BrakeRate:        65536 / 8,
		TurnRate:         500,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127})
	x, z := world.CellToWorld(8), world.CellToWorld(8)
	h, err := w.Create(def, 0, x, ter.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	return sys, w, u
}

// parkFixture is a completed 1x1 land product standing where a factory plate
// would have left it, registered with the movement system.
func parkFixture(t *testing.T) (*System, *units.World, *units.Unit) {
	t.Helper()
	ter := syntheticFlat(48, 48)
	sys := NewSystem(ter, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	def := setScratchMovement(&content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("parkscout")},
		UnitName:         "parkscout",
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		MinWaterDepth:    -10000,
		MaxVelocity:      2 * 65536,
		Acceleration:     65536 / 2,
		BrakeRate:        65536 / 2,
		TurnRate:         1000,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127})
	x, z := world.CellToWorld(12), world.CellToWorld(12)
	h, err := w.Create(def, 0, x, ter.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	return sys, w, u
}

// parkNodeAt installs the Park record a no-rally factory product carries, with
// the rectangle its phase 0 authors for a 1x1 land product standing at cell
// (12,12): origin (cellX-4s, cellZ-3s) and size (8s, 6s) with s = 1
// [04 R-ORD-01 §2][04 R-FAC-02 §4]. The rectangle arithmetic itself is locked by
// orders.TestParkPhase0InstallsRectangleGoal; this fixture only needs a record
// that has already run phase 0.
func parkNodeAt(t *testing.T, u *units.Unit) *orders.Node {
	t.Helper()
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Park"), orders.Node{
		Phase:       1,
		Deadline:    -1,
		DynamicGate: 0xE0,
		Param1:      uint32(int32(8)), // origin X = 12 - 4s
		Param2:      uint32(int32(9)), // origin Z = 12 - 3s
		Param3:      1,                // s = FootPrintX, no +3 for a land class
	})
	head := q.Primary()[0]
	if _, _, _, _, ok := orders.ParkGoalRect(head); !ok {
		t.Fatal("fixture Park record carries no rectangle")
	}
	return head
}

// TestTakeoffPreambleLeavesTheGroundPlane locks step 4 of the shared takeoff
// preamble together with its occupancy consequence: from the grounded mode 1 the
// setter writes mode 2 and installs the initial climb marker at cruisealt/2
// [04 R-AIR-01 §6], and the ground word belongs to mode-1 movers only, so the
// airborne aircraft stops holding the cells it stood on [04 R-COLL-01 §4].
func TestTakeoffPreambleLeavesTheGroundPlane(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	cell := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	if _, held := sys.Grid.OccupantAt(cell); !held {
		t.Fatal("a grounded aircraft must hold its ground cell [04 R-COLL-01 §4]")
	}
	if u.Move.Mode != 1 {
		t.Fatalf("fixture mover mode=%d, want the grounded 1", u.Move.Mode)
	}

	sys.takeoffPreamble(u, nil)

	if u.Move.Mode != 2 {
		t.Fatalf("mover mode=%d after the preamble, want 2 [04 R-AIR-01 §6 step 4]", u.Move.Mode)
	}
	if !u.Activated {
		t.Fatal("the preamble did not raise the activation edge — the takeoff script hook [04 R-AIR-01 §6 step 3]")
	}
	if _, held := sys.Grid.OccupantAt(cell); held {
		t.Fatal("an airborne aircraft still holds a ground cell [04 R-COLL-01 §4]")
	}
	want := CruiseAltitudeForCarrier(sys.Terrain, u.X, u.Z, u, true)
	got, pending := sys.ClimbTargetFor(u.Handle)
	if !pending {
		t.Fatal("the preamble installed no initial climb marker [04 R-AIR-01 §6 step 4]")
	}
	if got != want {
		t.Fatalf("climb marker altitude=%d, want max(sea, terrain)+cruisealt/2 = %d [04 R-AIR-01 §4]", got, want)
	}
}

// TestTakeoffPreambleIsInertWhenAlreadyAirborne locks [04 R-AIR-01 §6]: "If the
// unit is already airborne the marker is not built and the phase still
// advances, so a mid-air order does not reset the aircraft's climb goal."
func TestTakeoffPreambleIsInertWhenAlreadyAirborne(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	sys.takeoffPreamble(u, nil)
	// Fly it clear of the marker, then consume the marker as the climb step does.
	u.Y = numeric.Fixed(int64(400) << 16)
	sys.releaseAirGoal(u)

	sys.takeoffPreamble(u, nil)

	if _, pending := sys.ClimbTargetFor(u.Handle); pending {
		t.Fatal("a mid-air order rebuilt the initial climb marker [04 R-AIR-01 §6]")
	}
	if u.Move.Mode != 2 {
		t.Fatalf("mover mode=%d, want the unchanged 2", u.Move.Mode)
	}
}

// TestSetMoverModeIsEdgeGuarded locks the setter's first line: it does nothing
// when the current low two bits already equal the request [04 R-AIR-01 §3]. A
// setter that re-ran would clear and re-stamp occupancy and re-fire the
// activation callbacks on every visit.
func TestSetMoverModeIsEdgeGuarded(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	if sys.SetMoverMode(u, 1) {
		t.Fatal("mode 1 requested on a mode-1 mover reported a change [04 R-AIR-01 §3]")
	}
	if !sys.SetMoverMode(u, 2) {
		t.Fatal("mode 2 requested on a mode-1 mover reported no change")
	}
	if sys.SetMoverMode(u, 2) {
		t.Fatal("mode 2 requested twice reported a second change [04 R-AIR-01 §3]")
	}
	// Grounding again zeroes the scalar speed and lowers the activation edge.
	u.Move.Speed = numeric.Fixed(1 << 16)
	if !sys.SetMoverMode(u, 1) {
		t.Fatal("mode 1 requested on a mode-2 mover reported no change")
	}
	if u.Move.Speed != 0 {
		t.Fatalf("grounding left speed=%d, want 0 [04 R-AIR-01 §3]", u.Move.Speed)
	}
	if u.Activated {
		t.Fatal("grounding did not lower the activation edge — the landing script hook [04 R-AIR-01 §3]")
	}
	cell := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	if _, held := sys.Grid.OccupantAt(cell); !held {
		t.Fatal("a re-grounded mover did not re-stamp the ground plane [04 R-COLL-01 §4]")
	}
}

// TestRectPerimeterArrivalIsBorderMembership locks [04 §7.2]: for a
// rectangle-perimeter goal "enumerated goal cells are exactly the rectangle
// border, where h is 0, and arrival requires lying on that border" — the rule
// [04 R-FAC-02 §4] cites for the `Park` a no-rally factory product receives.
//
// The steering point bound beside the arrival handle is one border cell chosen
// to steer at. Testing arrival against that single cell instead of the border
// meant only the mover that reached that exact cell could ever complete its
// `Park`; every other product of the same factory shares the same rectangle and
// so the same cell, and queued behind it.
func TestRectPerimeterArrivalIsBorderMembership(t *testing.T) {
	sys, _, u := parkFixture(t)
	// Park's rectangle for a 1x1 land product at cell (12,12): origin
	// (cellX-4s, cellZ-3s), size (8s, 6s) with s = 1 [04 R-FAC-02 §4].
	head := parkNodeAt(t, u)
	minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(head)
	if !ok {
		t.Fatal("Park phase 0 installed no rectangle")
	}
	sys.ActivateMove(u, head)
	ah := sys.arrivalHandles[u.Handle]
	if ah == nil || ah.border == nil {
		t.Fatal("a Park record's arrival handle carries no rectangle border [04 §7.2]")
	}
	// Corrected 2026-09-01 (WU-19-24): the fixture took its border cells from
	// ParkGoalRect's `(origin, 8s x 6s)` arguments directly. Those are the
	// CONSTRUCTOR's arguments; the class stores them grown by the mover's own
	// footprint [04 R-PATH-01 §12], so for this 1x1 product the argument
	// rectangle's own minimum corner is now interior. The cells must come from
	// the handle's rectangle, which is the one the search is aimed at.
	rect := *ah.border
	if rect.Min.X != minX-1 || rect.Min.Z != minZ-1 || rect.Max.X != maxX+1 || rect.Max.Z != maxZ+1 {
		t.Fatalf("arrival rectangle %v, want the argument rectangle [%d,%d]x[%d,%d] grown by the 1x1 product [04 R-PATH-01 §12]", rect, minX, maxX, minZ, maxZ)
	}
	// A border cell that is deliberately NOT the bound steering point.
	other := Cell{X: rect.Min.X, Z: rect.Min.Z + 1}
	if other.X == ah.goalX && other.Z == ah.goalZ {
		t.Fatal("fixture picked the bound steering point; choose another border cell")
	}
	if !onRectBorder(rect, other.X, other.Z) {
		t.Fatalf("fixture cell (%d,%d) is not on the border %v", other.X, other.Z, rect)
	}
	coll := sys.Collisions[u.Handle]
	if coll == nil {
		t.Fatal("no collision state for the product")
	}
	coll.CachedAnchor = other
	if !sys.finalGoalReached(u, true) {
		t.Fatalf("a mover standing on border cell (%d,%d) did not arrive [04 §7.2]", other.X, other.Z)
	}
	if head.Satisfied&0x20 == 0 {
		t.Fatal("arrival did not raise the record's 0x20 bit [R-P0-01]")
	}
	// The rectangle's interior is not the goal: a cell strictly inside must not
	// arrive, or a product would complete its Park on the pad it started from.
	head.Satisfied = 0
	inside := Cell{X: (rect.Min.X + rect.Max.X) / 2, Z: (rect.Min.Z + rect.Max.Z) / 2}
	if onRectBorder(rect, inside.X, inside.Z) {
		t.Fatalf("fixture centre (%d,%d) lies on the border; widen the rectangle", inside.X, inside.Z)
	}
	coll.CachedAnchor = inside
	if sys.finalGoalReached(u, true) || head.Satisfied&0x20 != 0 {
		t.Fatalf("a mover inside the rectangle at (%d,%d) arrived; only the border is the goal [04 §7.2]", inside.X, inside.Z)
	}
}
