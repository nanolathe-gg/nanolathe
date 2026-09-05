package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

type mobileApproachObservation struct {
	goal           path.Cell
	moveGoalX      numeric.Fixed
	moveGoalZ      numeric.Fixed
	product        pool.Handle
	remaining      float32
	deadline       int32
	phase          uint8
	moveState      uint8
	firstMoveTick  int32
	rangeReachTick int32
	startTick      int32
	finalX         numeric.Fixed
	finalY         numeric.Fixed
	finalZ         numeric.Fixed
	routePublishAt int32
}

// TestMobileBuilderApproachIsControllerNeutral proves that controller state is
// not an input to MobileBuild's approach, reach, or nanoframe transition. The
// order remains in its approach phase without a product while out of range,
// then admits at the same inclusive range gate for a human and a computer
// owner [04 R-ORD-01 §5][05 R-WORK-01 §2]. The approach geometry is the
// rectangle goal of [04 R-PATH-01 §12], installed by [04 R-PATH-01 §13].
func TestMobileBuilderApproachIsControllerNeutral(t *testing.T) {
	human := runMobileApproachControllerCase(t, 1)
	computer := runMobileApproachControllerCase(t, 2)

	if human != computer {
		t.Fatalf("controller changed MobileBuild behavior: human=%+v computer=%+v", human, computer)
	}
}

func runMobileApproachControllerCase(t *testing.T, controller uint8) mobileApproachObservation {
	t.Helper()
	svc, builder, node := approachFixture(t, 10, 10)
	econ := &economy.Service{}
	econ.Players[builder.Owner].ControllerState = controller
	svc.Economy = econ

	startX, startY, startZ := builder.X, builder.Y, builder.Z

	// Phase 2 construction submits the request before movement integration.
	// With no published route yet, that same tick's movement visit must not
	// change the transform; phase 5 publishes the route afterwards [01 §4.4]
	// [04 §7.3].
	svc.Pump(builder, 0)
	if node.Target != 0 {
		t.Fatalf("controller %d allocated product %d before approach completed", controller, node.Target)
	}
	if node.Phase != uint8(State2) || node.MoveState != orders.MoveEnRoute {
		t.Fatalf("controller %d approach state=(phase %d, move %d), want state2/en-route", controller, node.Phase, node.MoveState)
	}
	if !svc.Movement.HasPathRequest(builder.Handle) {
		t.Fatalf("controller %d did not bind an out-of-range MobileBuild to path search", controller)
	}

	var requestGoal path.Cell
	requestFound := false
	for _, request := range svc.Movement.PathRequestsSnapshot() {
		if request.Unit != builder.Handle {
			continue
		}
		// The goal is the rectangle border, not a chosen point
		// [04 R-PATH-01 §13]; its first enumerated cell is a stable identity for
		// the controller-parity comparison [04 R-MOV-03 §9].
		cells := request.Goal.Enumerate(nil)
		if len(cells) < 2 {
			t.Fatalf("controller %d approach goal has %d cells, want the rectangle border", controller, len(cells))
		}
		requestGoal, requestFound = cells[0], true
		break
	}
	if !requestFound {
		t.Fatalf("controller %d path request is absent from the deterministic request queue", controller)
	}
	moveGoalX, moveGoalZ, moveGoalBound := svc.Movement.MoveGoalFor(builder.Handle, node)
	if !moveGoalBound {
		t.Fatalf("controller %d did not bind the selected approach as the mover goal", controller)
	}
	if route := svc.Movement.Routes[builder.Handle]; route != nil && route.Active {
		t.Fatalf("controller %d route was active before the phase-5 publication boundary", controller)
	}
	svc.Movement.BeginTick(0)
	firstMovement := svc.Movement.StepUnit(builder.Handle, 0)
	svc.Movement.EndTick(0)
	if firstMovement.Moved || builder.X != startX || builder.Y != startY || builder.Z != startZ {
		t.Fatalf("controller %d moved before route publication: start=(%d,%d,%d) got=(%d,%d,%d)", controller,
			startX, startY, startZ, builder.X, builder.Y, builder.Z)
	}
	svc.Movement.Scheduler.Tick(0)
	route := svc.Movement.Routes[builder.Handle]
	if route == nil || !route.Active || route.Count == 0 {
		t.Fatalf("controller %d phase-5 scheduler did not publish the approach route: %+v", controller, route)
	}

	firstMoveTick, reachedTick, startTick := int32(-1), int32(-1), int32(-1)
	for tick := uint32(1); tick < 2000; tick++ {
		// Construction runs before movement. Every visit before the mover reaches
		// the shared range gate must remain product-free [04 R-ORD-01 §5].
		svc.Pump(builder, tick)
		if node.Target != 0 {
			startTick = int32(tick)
			if reachedTick < 0 || startTick <= reachedTick {
				t.Fatalf("controller %d allocated at tick %d before a prior movement visit reached range (reached=%d)", controller, startTick, reachedTick)
			}
		}

		beforeX, beforeY, beforeZ := builder.X, builder.Y, builder.Z
		svc.Movement.BeginTick(tick)
		movementResult := svc.Movement.StepUnit(builder.Handle, tick)
		svc.Movement.EndTick(tick)
		if movementResult.Moved || builder.X != beforeX || builder.Y != beforeY || builder.Z != beforeZ {
			if firstMoveTick < 0 {
				firstMoveTick = int32(tick)
			}
		}

		if node.Target == 0 {
			cx, cz, fx, fz, ok := svc.SiteCentrePublic(node)
			if !ok {
				t.Fatalf("controller %d lost the queued site footprint during traversal", controller)
			}
			if reachedTick < 0 && svc.IsWithinNanoRangePublic(builder, cx, cz, fx, fz) {
				reachedTick = int32(tick)
			}
		}
		svc.Movement.Scheduler.Tick(tick)
		if node.Target != 0 {
			break
		}
		if node.Phase != uint8(State2) || node.MoveState != orders.MoveEnRoute {
			t.Fatalf("controller %d left approach without a product at tick %d: phase=%d move=%d", controller, tick, node.Phase, node.MoveState)
		}
	}
	if startTick < 0 || node.Target == 0 {
		t.Fatalf("controller %d never reached MobileBuild start within the traversal bound; firstMove=%d reached=%d final=(%d,%d,%d)",
			controller, firstMoveTick, reachedTick, builder.X, builder.Y, builder.Z)
	}
	if firstMoveTick < 0 || startTick <= firstMoveTick {
		t.Fatalf("controller %d start tick %d must follow physical movement beginning at %d", controller, startTick, firstMoveTick)
	}
	if builder.X == startX && builder.Y == startY && builder.Z == startZ {
		t.Fatalf("controller %d allocated without physically changing the builder transform", controller)
	}

	product := svc.World.Unit(node.Target)
	if product == nil || product.Remaining != 1 {
		t.Fatalf("controller %d product at start = %#v, want a remaining=1 nanoframe", controller, product)
	}
	if node.MoveState != orders.MoveArrived {
		t.Fatalf("controller %d move state %d after reaching site, want arrived", controller, node.MoveState)
	}

	return mobileApproachObservation{
		goal:           requestGoal,
		moveGoalX:      moveGoalX,
		moveGoalZ:      moveGoalZ,
		product:        node.Target,
		remaining:      product.Remaining,
		deadline:       node.Deadline,
		phase:          node.Phase,
		moveState:      node.MoveState,
		firstMoveTick:  firstMoveTick,
		rangeReachTick: reachedTick,
		startTick:      startTick,
		finalX:         builder.X,
		finalY:         builder.Y,
		finalZ:         builder.Z,
		routePublishAt: 0,
	}
}

// TestMobileBuilderInclusiveRangeGate keeps the comparison boundary narrow: a
// builder at exactly the reach limit is admitted for both controllers. The
// limit is centre-to-centre minus both half-footprint diagonals, compared
// inclusively [05 R-WORK-01 §2][05 R-WORK-01 §12].
func TestMobileBuilderInclusiveRangeGate(t *testing.T) {
	for _, controller := range []uint8{1, 2} {
		svc, builder, node := approachFixture(t, 10, 10)
		econ := &economy.Service{}
		econ.Players[builder.Owner].ControllerState = controller
		svc.Economy = econ
		cx, cz, fx, fz, ok := svc.SiteCentrePublic(node)
		if !ok {
			t.Fatal("queued MobileBuild site has no footprint anchor")
		}
		// builddistance + builderPad + productPad whole world units of centre
		// separation is the last admitted position [05 R-WORK-01 §2].
		limit := int64(builder.Def.BuildDistance) + int64(nanoFootprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)) + int64(nanoFootprintPad(fx, fz))
		builder.X = cx - numeric.Fixed(limit<<16)
		builder.Z = cz
		if !svc.IsWithinNanoRangePublic(builder, cx, cz, fx, fz) {
			t.Fatalf("controller %d equality fixture was outside the inclusive range gate", controller)
		}
		// The reach expression is consulted on the arrival-failure wake and
		// nowhere else [05 R-WORK-01 §13], so the visit that tests the
		// comparison boundary is a `0x40` visit. Raise it; a builder at exactly
		// the limit passes the inclusive compare, retires the approach and
		// allocates in the same visit.
		node.Satisfied |= 0x40
		svc.Pump(builder, 0)
		if node.Target == 0 {
			t.Fatalf("controller %d did not allocate at the inclusive range equality", controller)
		}
	}
}

// TestComputerMobileBuilderObstructionKeepsRetry proves that removing the
// controller exemption does not turn a blocked site into a teleport or forced
// allocation. Once the computer builder is in range, the ordinary MobileBuild
// blocked-area budget remains authoritative [04 R-ORD-01 §5].
func TestComputerMobileBuilderObstructionKeepsRetry(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	econ := &economy.Service{}
	econ.Players[builder.Owner].ControllerState = 2
	svc.Economy = econ

	anchorX, anchorZ, _, _, ok := svc.siteAnchorCell(node)
	if !ok {
		t.Fatal("queued MobileBuild site has no footprint anchor")
	}
	cx, cz, fx, fz, ok := svc.SiteCentrePublic(node)
	if !ok {
		t.Fatal("queued MobileBuild site has no footprint centre")
	}
	limit := int64(builder.Def.BuildDistance) + int64(nanoFootprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)) + int64(nanoFootprintPad(fx, fz))
	builder.X = cx - numeric.Fixed(limit<<16)
	builder.Z = cz
	blocked := &svc.Terrain.Plot[int(anchorZ)*int(svc.Terrain.CellW)+int(anchorX)]
	blocked.SetOccupantA(9)

	beforeX, beforeZ := builder.X, builder.Z
	// "Once the computer builder is in range" is now a wake, not a per-visit
	// measurement: the reach expression runs under `satisfied & 0x40` alone and
	// a passing test retires the approach into the placement validator
	// [05 R-WORK-01 §13]. The blocked-area budget the test locks is what the
	// validator's rejection then runs [R-ORDER-02 §1].
	node.Satisfied |= 0x40
	svc.Pump(builder, 40)
	if node.Target != 0 {
		t.Fatalf("blocked computer MobileBuild allocated product %d", node.Target)
	}
	if builder.X != beforeX || builder.Z != beforeZ {
		t.Fatalf("blocked computer builder teleported from (%d,%d) to (%d,%d)", beforeX, beforeZ, builder.X, builder.Z)
	}
	if node.Phase != uint8(State2) || node.Param3 != 1 || node.Deadline != 70 {
		t.Fatalf("blocked retry = phase %d counter %d deadline %d, want state2/1/70", node.Phase, node.Param3, node.Deadline)
	}
	if svc.Movement.HasPathRequest(builder.Handle) {
		t.Fatal("in-range obstructed builder started a replacement walk instead of retaining the blocked-area retry")
	}
}
