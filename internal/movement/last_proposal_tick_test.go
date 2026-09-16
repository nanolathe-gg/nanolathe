package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// proposalFixture builds one mover on the flat integrate terrain, optionally
// with a published two-point route so its next commit is a real proposal.
func proposalFixture(t *testing.T, def *content.UnitDef, withRoute bool) (*System, *units.World, pool.Handle) {
	t.Helper()
	system := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	h, err := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	system.BindWorld(w)
	system.EnsureUnit(w.Unit(h))
	if withRoute {
		moveID := orders.Lookup("Move_Ground")
		if moveID == 0 {
			t.Fatal("Move_Ground order is unavailable")
		}
		q := orders.QueueForUnit(w.Unit(h))
		q.Push(moveID, orders.Node{GoalX: world.CellToWorld(6), GoalZ: world.CellToWorld(0), GoalSupplied: true})
		head := q.Head()
		// Route points are whole world units, six cells of sixteen.
		handleRow(system.Routes, h).PublishAtRevision([]Point{{X: 0, Z: 0}, {X: 96, Z: 0}}, system.staticObstacleRevision())
		setHandleRow(&system.activeOrders, h, &activeMove{order: head, token: 7})
		system.nextActivation = 7
	}
	return system, w, h
}

func stepOnce(s *System, h pool.Handle, tick uint32) StepResult {
	s.BeginTick(tick)
	res := s.StepUnit(h, tick)
	s.EndTick(tick)
	return res
}

// TestCommitStampsLastProposalTickBeforeValidation locks the write of
// [04 R-COLL-01 §1]: the last-proposal tick is set on ANY non-stationary
// proposal, before the cell test and before the validator, so it records the
// tick on which the unit tried to change position or mode — blocked ticks
// included. The stationary early return of the same section writes nothing at
// all, so a unit at rest lets the stamp age.
func TestCommitStampsLastProposalTickBeforeValidation(t *testing.T) {
	def := &content.UnitDef{
		UnitName: "proposal-stamp-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535, BMCode: 1,
	}

	t.Run("moving proposal stamps", func(t *testing.T) {
		system, _, h := proposalFixture(t, def, true)
		if got := handleRow(system.Collisions, h).LastProposalTick; got != 0 {
			t.Fatalf("fresh mover already carries a proposal tick %d", got)
		}
		stepOnce(system, h, 41)
		if got := handleRow(system.Collisions, h).LastProposalTick; got != 41 {
			t.Fatalf("last-proposal tick = %d after a moving commit at tick 41", got)
		}
	})

	t.Run("blocked proposal still stamps", func(t *testing.T) {
		system, w, h := proposalFixture(t, def, true)
		// A foreign occupant on the proposed footprint makes the validator
		// reject; the stamp precedes the validator, so it must land anyway.
		blocker, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(0))
		if err != nil {
			t.Fatalf("create blocker: %v", err)
		}
		system.EnsureUnit(w.Unit(blocker))
		res := stepOnce(system, h, 12)
		if !res.Blocked {
			t.Fatalf("proposal into the blocker was not rejected: %+v", res)
		}
		if got := handleRow(system.Collisions, h).LastProposalTick; got != 12 {
			t.Fatalf("blocked commit left the last-proposal tick at %d, want the proposal tick 12", got)
		}
	})

	t.Run("stationary proposal writes nothing", func(t *testing.T) {
		system, _, h := proposalFixture(t, def, false)
		handleRow(system.Collisions, h).LastProposalTick = 3
		stepOnce(system, h, 90)
		if got := handleRow(system.Collisions, h).LastProposalTick; got != 3 {
			t.Fatalf("stationary early return wrote the proposal tick %d; it must write nothing [04 R-COLL-01 §1]", got)
		}
	})
}

// TestHoverBobKeepsAmplitudeWhileMoving is the consequence the stamp exists
// for. The bob's age term is the current tick minus the last-proposal tick
// [04 R-MOV-01 §5]; with no writer for that field it stayed at zero, every age
// past tick 60 clamped to 60 and every hovercraft's bob amplitude was
// identically zero for the rest of the battle. A hovercraft that proposed a
// position this tick has age zero and keeps the full amplitude.
func TestHoverBobKeepsAmplitudeWhileMoving(t *testing.T) {
	def := &content.UnitDef{
		UnitName: "hover-proposal-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535, BMCode: 1,
		CanHover: true,
	}
	system, w, h := proposalFixture(t, def, true)
	const tick = 400 // well past the 60-tick fade window
	stepOnce(system, h, tick)
	coll := handleRow(system.Collisions, h)
	if coll.LastProposalTick != tick {
		t.Fatalf("hovercraft last-proposal tick = %d at tick %d", coll.LastProposalTick, tick)
	}
	// Read the amplitude the post-move correction would have used, with speed
	// forced to zero so the age term is the only input under test.
	if bob := newHoverBob(w.Unit(h), 0, tick, coll.LastProposalTick); bob == nil || bob.amp == 0 {
		t.Fatalf("hover bob amplitude = %+v at age 0; the age fade of [04 R-MOV-01 §5] has swallowed it", bob)
	}
	// Sixty ticks after the last proposal the fade is complete, which is the
	// half of the contract that was already correct.
	if bob := newHoverBob(w.Unit(h), 0, tick+60, coll.LastProposalTick); bob == nil || bob.amp != 0 {
		t.Fatalf("hover bob amplitude = %+v sixty ticks after the last proposal, want a completed fade", bob)
	}
}
