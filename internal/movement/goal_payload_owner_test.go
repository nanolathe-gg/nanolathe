package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestInstallerPendingBitStaysOnTheInstallingRecord locks [04 R-ORD-01 §0]
// "Established — whose pending word an installer's `0x80` lands in": a goal
// installer writes `0x80` into the record it is installing for and into no
// other record's pending word, and its closing clear of `0x20`–`0x200` cancels
// that self-raise.
//
// The regression this locks was game-wide: a patrol chain is several `Patrol`
// records on one mover, and the record behind installing its own leg raised
// `0x80` on the record ahead, which was stalled at gate `0xE0`. Per the
// path-outcome table for the movement families that bit rotates a `Patrol`
// record to the tail with phase reset to 1, so every leg retired on the tick it
// was armed and no ground unit travelled.
func TestInstallerPendingBitStaysOnTheInstallingRecord(t *testing.T) {
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
	// The record behind takes the mover's single payload slot from the record
	// ahead. That eviction is our representation, not a retail event.
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: handle, Node: behind, X: world.CellToWorld(9), Z: world.CellToWorld(9), Radius: 16}) {
		t.Fatal("install for the following record was refused")
	}
	if ahead.Satisfied&0xE0 != 0 {
		t.Fatalf("leading record's pending word = %#x, want no movement outcome from another record's installer", ahead.Satisfied)
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
	// The identity check keeps that detach off a record that no longer owns the
	// payload.
	ahead.Satisfied = 0
	if !sys.ReleaseGoal(ahead) {
		t.Fatal("release of the non-owning record was refused")
	}
	if ahead.Satisfied&0x80 != 0 {
		t.Fatalf("non-owning record's pending word = %#x, want no detach bit", ahead.Satisfied)
	}
}
