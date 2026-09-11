package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestInstallRaisesTheReleaseBitOnTheEvictedRecord locks [04 R-ORD-01 §9]:
// every order record has its own payload field, but the unit's movement
// controller has ONE payload slot, and handing that controller a goal raises
// `0x80` on the record that owns the object the slot HELD — which need not be
// the record being installed for. Two records on one mover can each hold an
// object; only the one in the slot is bound, and a rebind is the "goal-handle
// detach or rebind" producer of the movement families' outcome table.
//
// Corrected by WU-19-68. This test read
// TestInstallerPendingBitStaysOnTheInstallingRecord and asserted the opposite:
// that an installer "writes `0x80` into the record it is installing for and
// into no other record's pending word". §9 shows the installer does not reach
// the other record's FIELD, it reaches the controller's SLOT, and the raise
// follows the object in that slot to its owner.
//
// The liveness the old assertion was protecting is real and still holds, by a
// different mechanism: [04 §3.3] step 3 stops the pump's walk AT a gated record
// with nothing satisfied, so the record behind a stalled patrol leg is never
// pumped and never installs. TestPatrollingGroundUnitsTravel is the standing
// proof of that end of it.
func TestInstallRaisesTheReleaseBitOnTheEvictedRecord(t *testing.T) {
	terrain := terrainForGoals()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	def := setScratchMovement(&content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500}, profile)
	def.MaxDamage = 100
	handle, err := w.Create(def, 0, world.CellToWorld(2), numeric.Fixed(0), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(handle))

	patrol := orders.Lookup("Patrol")
	ahead := &orders.Node{ID: patrol, Owner: handle, Phase: 2, DynamicGate: 0xE0}
	behind := &orders.Node{ID: patrol, Owner: handle, Phase: 1}

	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: handle, Node: ahead, X: world.CellToWorld(6), Z: world.CellToWorld(6)}) {
		t.Fatal("install for the leading record was refused")
	}
	// The record behind takes the controller's single payload slot from the
	// record ahead. The displaced object's OWN record takes the `0x80`.
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: handle, Node: behind, X: world.CellToWorld(9), Z: world.CellToWorld(9), Radius: 16}) {
		t.Fatal("install for the following record was refused")
	}
	if ahead.Satisfied&0x80 == 0 {
		t.Fatalf("evicted record's pending word = %#x, want `0x80` from the rebind [04 R-ORD-01 §9]", ahead.Satisfied)
	}
	if ahead.Satisfied&0x60 != 0 {
		t.Fatalf("evicted record's pending word = %#x, want the rebind bit ALONE — no arrival, no route-released", ahead.Satisfied)
	}
	if behind.Satisfied&0x3E0 != 0 {
		t.Fatalf("installing record's pending word = %#x, want 0x20-0x200 cleared by its own installer", behind.Satisfied)
	}

	// Reinstalling for the record that already owns the slot is equally silent:
	// the self-raise is cancelled by the same closing clear.
	behind.Satisfied |= 0x40
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: handle, Node: behind, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Fatal("reinstall for the owning record was refused")
	}
	if behind.Satisfied&0x3E0 != 0 {
		t.Fatalf("reinstalled record's pending word = %#x, want 0x20-0x200 cleared", behind.Satisfied)
	}

	// A detach from outside the record — the queue teardown / head replacement
	// path — is what makes `0x80` observable at all [04 R-ORD-01 §0].
	if !sys.ReleaseGoal(behind) {
		t.Fatal("release of the owning record was refused")
	}
	if behind.Satisfied&0x80 == 0 {
		t.Fatalf("released record's pending word = %#x, want 0x80 from the detach", behind.Satisfied)
	}
	// The earlier record still owns its displaced object, but the controller
	// is already null. Deleting that object cannot raise a new release bit.
	ahead.Satisfied = 0
	if !sys.ReleaseGoal(ahead) {
		t.Fatal("release of the non-owning record was refused")
	}
	if ahead.Satisfied&0x80 != 0 {
		t.Fatalf("non-owning record's pending word = %#x, want no detach bit", ahead.Satisfied)
	}
}
