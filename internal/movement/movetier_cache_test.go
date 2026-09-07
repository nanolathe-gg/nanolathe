package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// moveTierFixture builds one mover with authored MoveRate thresholds so each
// nonzero tier is reachable: MoveRate1 is one cell per tick and MoveRate2 two,
// both read through the fixed-point accessor in 16.16 world units per tick
// [04 R-MOV-01 §6].
func moveTierFixture(t *testing.T) (*System, *units.Unit, *CollisionState) {
	t.Helper()
	system := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	def := &content.UnitDef{
		UnitName: "move-tier-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 4 * int32(worldUnitsPerCell), Acceleration: int32(worldUnitsPerCell),
		BrakeRate: int32(worldUnitsPerCell), TurnRate: 65535, BMCode: 1,
		MoveRate1: int32(worldUnitsPerCell), MoveRate2: 2 * int32(worldUnitsPerCell),
	}
	w := newMovementFixtureWorld(4)
	h, err := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	system.BindWorld(w)
	u := w.Unit(h)
	system.EnsureUnit(u)
	coll := system.Collisions[h]
	if coll == nil {
		t.Fatal("fixture mover has no collision state")
	}
	return system, u, coll
}

// TestMoveTierCacheTerms locks the classifier's four tier-0 terms and the cache
// it publishes. Category 0 holds when the mover's blocked flag is set, when the
// unit is attached to a carrier, or when the scalar speed word and the adjacent
// signed 16-bit turn residual are BOTH zero; otherwise the signed inclusive
// MoveRate1/MoveRate2 comparison selects 1..3 [04 §5.2][04 R-MOV-01 §6]. The
// classified value is written to the unit's cached tier as the classifier's
// final act, which is the word the weapon drift gate reads [06 R-WPN-03 §2].
func TestMoveTierCacheTerms(t *testing.T) {
	cell := int32(worldUnitsPerCell)
	cases := []struct {
		name         string
		speed        int32
		turnResidual int16
		blocked      bool
		carried      bool
		want         uint8
	}{
		// The blocked term. This is the case the build had missing entirely: the
		// integrator passed a literal false, so a rejected mover that retains its
		// capped speed classified as tier 1 [04 R-COLL-01 §5].
		{name: "blocked with speed", speed: cell, blocked: true, want: 0},
		{name: "blocked at three cells", speed: 3 * cell, blocked: true, want: 0},
		// The carrier term.
		{name: "carried with speed", speed: cell, carried: true, want: 0},
		// The both-magnitudes-zero term, and its two halves separately.
		{name: "speed and residual zero", want: 0},
		{name: "residual alone is nonzero", turnResidual: 7, want: 1},
		{name: "negative residual alone", turnResidual: -7, want: 1},
		// The signed inclusive threshold ladder, both bounds inclusive.
		{name: "at MoveRate1", speed: cell, want: 1},
		{name: "just above MoveRate1", speed: cell + 1, want: 2},
		{name: "at MoveRate2", speed: 2 * cell, want: 2},
		{name: "above MoveRate2", speed: 2*cell + 1, want: 3},
		// Blocked outranks a tier-3 speed word.
		{name: "blocked above MoveRate2", speed: 3 * cell, blocked: true, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			system, u, coll := moveTierFixture(t)
			coll.Blocked = tc.blocked
			coll.TurnResidual = tc.turnResidual
			if tc.carried {
				u.Attachment.Carrier = u.Handle
			}
			// Seed a nonzero cache so a test expecting 0 proves a write, not a
			// missing one.
			u.MoveTier = 3
			system.emitMovementCallbacks(u, tc.speed)
			if u.MoveTier != tc.want {
				t.Fatalf("cached tier=%d want %d (speed=%d residual=%d blocked=%t carried=%t) [04 §5.2][04 R-MOV-01 §6]",
					u.MoveTier, tc.want, tc.speed, tc.turnResidual, tc.blocked, tc.carried)
			}
		})
	}
}

// TestMoveTierCacheHoldsStaleVerdictUntilNextCrossCellProposal locks the
// stale-by-construction semantics of the blocked flag as they reach the cached
// tier — the contract of [04 R-COLL-01 §5] "the flag persists stale by
// construction", read through [04 §5.2]'s clarification that the classifier's
// inhibit term IS that flag.
//
// After a rejection the clamp leaves the unit inside its old rectangle with a
// capped speed. A later tick that does not cross a cell takes the fast path or
// the stationary return, neither of which reruns the validator, so nothing
// rewrites the flag: the unit keeps classifying as tier 0 while it moves inside
// its cell. Only the next cross-cell verdict replaces the value.
func TestMoveTierCacheHoldsStaleVerdictUntilNextCrossCellProposal(t *testing.T) {
	system := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	def := &content.UnitDef{
		UnitName: "stale-tier-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535, BMCode: 1,
	}
	blockerHandle, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	moverHandle, err := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	system.BindWorld(w)
	system.EnsureUnit(w.Unit(moverHandle))
	system.EnsureUnit(w.Unit(blockerHandle))

	moveID := orders.Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground order is unavailable")
	}
	mover := w.Unit(moverHandle)
	queue := orders.QueueForUnit(mover)
	queue.Push(moveID, orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(0)})
	head := queue.Head()
	system.Routes[moverHandle].PublishAtRevision([]Point{{X: 0, Z: 0}, {X: 48, Z: 0}}, system.staticObstacleRevision())
	system.activeOrders[moverHandle] = &activeMove{order: head, token: 41}
	system.nextActivation = 41

	system.BeginTick(1)
	res := system.StepUnit(moverHandle, 1)
	system.EndTick(1)
	if !res.Blocked {
		t.Fatalf("cross-cell proposal into the occupant was not rejected: %+v", res)
	}
	coll := system.Collisions[moverHandle]
	if !coll.Blocked {
		t.Fatal("the verdict did not persist on the mover's blocked flag [04 R-COLL-01 §5]")
	}
	// The rejection caps but does not zero the scalar speed, so the tier-0 cache
	// below is the flag's doing and not the speed word's [04 R-COLL-01 §5].
	if coll.Speed == 0 {
		t.Fatal("a rejected proposal must retain a capped speed, not zero it")
	}
	if mover.MoveTier != 0 {
		t.Fatalf("cached tier=%d want 0 for a freshly rejected mover [04 §5.2]", mover.MoveTier)
	}

	// A tick with no new verdict. The validator does not run, so the flag keeps
	// its old value and the classifier keeps publishing tier 0 even though the
	// mover is carrying a nonzero speed word.
	system.emitMovementCallbacks(mover, coll.Speed)
	if mover.MoveTier != 0 {
		t.Fatalf("cached tier=%d want 0 while the verdict is stale [04 R-COLL-01 §5]", mover.MoveTier)
	}

	// The next cross-cell verdict replaces it. The commit's validation gate is
	// the only simulation writer of the flag [04 R-COLL-01 §5], so run one
	// accepted cross-cell proposal and reclassify: the cache must follow the new
	// verdict, at the tier the retained speed selects.
	system.Grid.Clear(Cell{X: 2, Z: 0}, 1, 1, int(blockerHandle))
	coll.VX, coll.VZ = int32(worldUnitsPerCell), 0
	fastPath, stillBlocked := coll.CommitOne(system.Grid, coll.Mode, func(Cell) bool { return true }, nil)
	if fastPath || stillBlocked {
		t.Fatalf("the replacement proposal was not an accepted cross-cell verdict: fastPath=%t blocked=%t", fastPath, stillBlocked)
	}
	if coll.Blocked {
		t.Fatal("an accepted cross-cell proposal must clear the blocked flag [04 R-COLL-01 §5]")
	}
	system.emitMovementCallbacks(mover, coll.Speed)
	if mover.MoveTier == 0 {
		t.Fatalf("cached tier=%d want nonzero once the verdict is replaced (speed=%d) [04 §5.2]", mover.MoveTier, coll.Speed)
	}
}
