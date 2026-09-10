package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// releaseFixture builds a one-mover system on the shared 20x20 goals terrain.
func releaseFixture(t *testing.T, def *content.UnitDef, cell int32) (*System, *units.World, pool.Handle) {
	t.Helper()
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(def, 0, world.CellToWorld(cell), numeric.Fixed(0), world.CellToWorld(cell))
	if err != nil {
		t.Fatalf("create %s: %v", def.UnitName, err)
	}
	sys.EnsureUnit(w.Unit(h))
	return sys, w, h
}

// TestReleaseGoalPayloadHandsTheControllerANullGoal locks the release form of
// [04 R-ORD-01 §1]'s record-level install/release helper, as RWU-19-18 spells
// it out: steps 1-4 of the route-acceptance rule of [04 R-PATH-01 §8] run with
// a null goal — cancel the in-flight search, OR `0x80` into the pending word of
// the record that owned the previous payload, clear has-waypoint, clear
// wants-repath — then the payload is virtually deleted and the field cleared.
//
// The two easily-regressed halves are the search cancel (an outstanding request
// for a goal nobody holds any more republishes a route at a stale target) and
// the ABSENCE of the closing `0x20`-`0x200` clear: the release form is step (1)
// alone, "which is why it leaves `0x80` visible", and a release that cancelled
// its own detach bit hid the one case [04 R-ORD-01 §0] names as making the bit
// observable.
func TestReleaseGoalPayloadHandsTheControllerANullGoal(t *testing.T) {
	def := setScratchMovement(&content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500}, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50})
	def.MaxDamage = 100
	sys, _, h := releaseFixture(t, def, 2)

	n := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: h}
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: n, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Fatal("install for the mover was refused")
	}

	// An in-flight search for this follower, plus a follower that believes it
	// holds a route and wants another.
	sys.pathProvider.Submit(path.Request{Unit: h, Player: 0})
	if !sys.HasPathRequest(h) {
		t.Fatal("fixture did not put a search in flight")
	}
	route := handleRow(sys.Routes, h)
	if route == nil {
		t.Fatal("mover has no route record")
	}
	route.Active = true
	route.WantsRepath = true
	// A movement outcome the previous goal already produced. The release must
	// leave it alone: only an INSTALL clears 0x20-0x200 [04 R-ORD-01 §1].
	n.Satisfied |= 0x40

	if !sys.ReleaseGoalPayload(n) {
		t.Fatal("release of the owning record reported nothing released")
	}
	if sys.HasPathRequest(h) {
		t.Error("step 1: the in-flight search was not cancelled")
	}
	if n.Satisfied&0x80 == 0 {
		t.Errorf("step 2: pending word = %#x, want 0x80 raised on the previous payload's record", n.Satisfied)
	}
	if route.Active {
		t.Error("step 3: has-waypoint was not cleared")
	}
	if route.WantsRepath {
		t.Error("step 4: wants-repath was not cleared for the null goal")
	}
	if sys.HasGroundGoal(h, n) {
		t.Error("the payload was not virtually deleted")
	}
	if n.Satisfied&0x40 == 0 {
		t.Errorf("pending word = %#x, want 0x40 untouched: the release form does not clear 0x20-0x200", n.Satisfied)
	}

	// A record that no longer owns the payload releases nothing and raises
	// nothing — the identity test that keeps a late teardown off a successor.
	other := &orders.Node{ID: n.ID, Owner: h}
	if sys.ReleaseGoalPayload(other) {
		t.Error("release for a record holding no payload reported a release")
	}
	if other.Satisfied != 0 {
		t.Errorf("non-owning record's pending word = %#x, want untouched", other.Satisfied)
	}
}

// TestInstallGoalClearsMovementBitsAndAppliesAcceptance locks the other branch
// of the same helper: an install clears pending `0x20`-`0x200` before adopting
// [04 R-ORD-01 §0][04 R-ORD-01 §1], and the controller's acceptance rule
// decides which family may adopt at all — a `canfly` owner is release-only on
// the ground installers ("skip the install entirely — release only — when the
// owner's definition has the `canfly` bit"), and the air installer refuses a
// unit with no flight block, because aircraft never enter the ground scheduler
// [04 R-PATH-01 §8].
func TestInstallGoalClearsMovementBitsAndAppliesAcceptance(t *testing.T) {
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	def := setScratchMovement(&content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500}, profile)
	def.MaxDamage = 100
	sys, w, h := releaseFixture(t, def, 2)

	n := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: h}
	n.Satisfied = 0x20 | 0x40 | 0x80 | 0x100 | 0x200
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: n, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Fatal("the ground controller refused a point goal for a mover")
	}
	if n.Satisfied&0x3E0 != 0 {
		t.Errorf("pending word after install = %#x, want 0x20-0x200 cleared", n.Satisfied)
	}
	if !sys.HasGroundGoal(h, n) {
		t.Error("the installed payload is not bound to its record")
	}

	// The air installer's acceptance: a ground mover has no flight block.
	if sys.InstallAirGoal(orders.AirGoalRequest{Owner: h, Node: n, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Error("the air installer accepted a ground mover")
	}

	// The ground installers' acceptance: a `canfly` owner is release-only.
	flyDef := setScratchMovement(&content.UnitDef{UnitName: "armpeep", MaxVelocity: 2 * 65536, TurnRate: 500}, profile)
	flyDef.MaxDamage = 100
	flyDef.CanFly = true
	fh, err := w.Create(flyDef, 0, world.CellToWorld(5), numeric.Fixed(0), world.CellToWorld(5))
	if err != nil {
		t.Fatalf("create aircraft: %v", err)
	}
	sys.EnsureUnit(w.Unit(fh))
	air := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: fh}
	if sys.InstallPointGoal(orders.PointGoalRequest{Owner: fh, Node: air, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Error("a ground point goal was installed for a canfly owner")
	}
	if sys.HasGroundGoal(fh, air) {
		t.Error("a canfly owner adopted a ground payload")
	}
}

// TestReleaseGoalPayloadIsNoOpForAMoverlessUnit locks the helper's first rule:
// it "runs entirely through the owner's mover", so a unit without one — a
// building, `bmcode 0`, which the allocator never gives a mover
// [04 R-FAC-02 §5] and which carries status-word bit 29 for it [04 §3.4] — is a
// no-op: nothing released, nothing raised.
func TestReleaseGoalPayloadIsNoOpForAMoverlessUnit(t *testing.T) {
	def := &content.UnitDef{UnitName: "armmex", MaxDamage: 100, BMCode: 0, FootprintX: 1, FootprintZ: 1}
	sys, w, h := releaseFixture(t, def, 4)
	if w.Unit(h).Flags&units.BuildingClassStatus == 0 {
		t.Fatal("fixture building is not building class")
	}

	n := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: h}
	// Bind a payload directly: the point installer is the seam an order handler
	// would use, and the no-op under test is the release side.
	sys.installGroundPayload(h, n, path.PointGoal(path.Cell{X: 9, Z: 9}, 0), world.CellToWorld(9), world.CellToWorld(9))
	n.Satisfied = 0

	if sys.ReleaseGoalPayload(n) {
		t.Error("a mover-less owner reported a release")
	}
	if n.Satisfied != 0 {
		t.Errorf("mover-less owner's pending word = %#x, want untouched", n.Satisfied)
	}
	if !sys.HasGroundGoal(h, n) {
		t.Error("a mover-less owner's payload was released")
	}
}
