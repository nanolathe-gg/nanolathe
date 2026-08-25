package session

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestStrictSkirmish_MoveOrderReachesGoal implements G2 [ON-10 §11 G2].
func TestStrictSkirmish_MoveOrderReachesGoal(t *testing.T) {
	const maxTick = 500
	const simSeed, crtSeed uint32 = 100, 200
	rng.SeedGlobal(simSeed, crtSeed)
	cat := strictMinimalCatalog()
	for _, d := range cat.Units {
		d.CanMove = true
		d.CanPatrol = true
		d.MaxVelocity = 65536 // 1 world unit per tick, fast enough for 10-cell travel within 300 ticks
		d.TurnRate = 1000
		d.FootprintX = 1
		d.FootprintZ = 1
		d.SightDistance = 200
	}
	terrain := strictMinimalTerrain()
	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].StatusHalfwordAt144 = 1
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(crtSeed)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
		t.Fatalf("G2: bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	s.Clock.ScaledAnchor = 0
	def := cat.Units["armcom"]
	sx := numeric.Fixed(5 * 16 * 65536)
	sz := numeric.Fixed(5 * 16 * 65536)
	sy := terrain.HeightAt(sx, sz)
	if sy == numeric.Fixed(-1) {
		sy = 0
	}
	h, _ := s.Units.Create(def, 0, sx, sy, sz)
	u := s.Units.Unit(h)
	publishOne(s, u)
	s.Movement.EnsureUnit(u)
	goalX := numeric.Fixed(15 * 16 * 65536)
	goalZ := numeric.Fixed(15 * 16 * 65536)
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		id = orders.Lookup("QMove")
	}
	if id == 0 {
		t.Fatalf("G2: no Move_Ground descriptor")
	}
	node := orders.NewMoveNode(id, goalX, goalZ, s.Clock.GlobalTick, h, false)
	q := orders.QueueForUnit(u)
	q.Push(id, node)
	stage1 := q.Head() != nil && q.Head().GoalX == goalX && q.Head().GoalZ == goalZ
	if !stage1 {
		t.Fatalf("G2 stage1 order queued failed")
	}
	t.Logf("G2 stage1 order queued handle %d goal %d %d", h, goalX.Raw(), goalZ.Raw())
	s.SetTraceEnabled(true)
	s.ClearTrace()
	stages := map[string]uint32{"order_queued": 0}
	lastCompleted := "order_queued"
	var pathSubmittedTick, routePublishedTick, movementBeginsTick, routeAdvanceTick, goalToleranceTick, orderCompleteTick *uint32
	initialX, initialZ := u.X, u.Z
	routeCountBefore := -1
	distBefore := numeric.Fixed(1 << 30)
	const arrivalThresh = int64(2 * 65536)
	const arrivalThresh2 = arrivalThresh * arrivalThresh
	for tick := 1; tick <= maxTick; tick++ {
		s.Step(int32(tick))
		// No manual SubmitMove fallback [RX-06]: production must submit path
		// requests itself or the gate fails at stage 2 with a failure record.
		// Stage 2: path request submitted — infer from scheduler HasRequest OR route published
		if _, ok := stages["path_request_submitted"]; !ok {
			hasReq := s.Movement != nil && s.Movement.Scheduler != nil && s.Movement.Scheduler.HasRequest(h)
			route := s.Movement.Routes[h]
			if route != nil && route.Active && route.Count > 0 {
				hasReq = true // route implies request was submitted
			}
			// Also check trace for path submission via scheduler Tick? Use HasRequest history
			if hasReq {
				stages["path_request_submitted"] = uint32(tick)
				tmp := uint32(tick)
				pathSubmittedTick = &tmp
				lastCompleted = "path_request_submitted"
				t.Logf("G2 stage2 path request submitted at tick %d", tick)
			}
		}
		// Stage 3: route published
		if _, ok := stages["route_published"]; !ok {
			if route := s.Movement.Routes[h]; route != nil && route.Active && route.Count > 0 {
				stages["route_published"] = uint32(tick)
				tmp := uint32(tick)
				routePublishedTick = &tmp
				routeCountBefore = int(route.Count)
				lastCompleted = "route_published"
				t.Logf("G2 stage3 route published at tick %d count %d", tick, route.Count)
				// If path request not yet marked, mark it now as same tick (request must have preceded publish)
				if _, ok2 := stages["path_request_submitted"]; !ok2 {
					stages["path_request_submitted"] = uint32(tick)
					tmp2 := uint32(tick)
					pathSubmittedTick = &tmp2
					t.Logf("G2 stage2 inferred from route publish at tick %d", tick)
				}
			}
		}
		// Stage 4: movement begins
		if _, ok := stages["movement_begins"]; !ok {
			moved := u.X != initialX || u.Z != initialZ
			if moved {
				stages["movement_begins"] = uint32(tick)
				tmp := uint32(tick)
				movementBeginsTick = &tmp
				lastCompleted = "movement_begins"
				t.Logf("G2 stage4 movement begins at tick %d pos %d %d", tick, u.X.Raw(), u.Z.Raw())
			} else if st := s.Movement.Steers[h]; st != nil && st.Speed != 0 {
				stages["movement_begins"] = uint32(tick)
				tmp := uint32(tick)
				movementBeginsTick = &tmp
				lastCompleted = "movement_begins"
				t.Logf("G2 stage4 movement begins (steer speed %d) at tick %d", st.Speed, tick)
			}
		}
		// Stage 5: route points advance
		if _, ok := stages["route_points_advance"]; !ok {
			if route := s.Movement.Routes[h]; route != nil && routeCountBefore >= 0 {
				if int(route.Count) < routeCountBefore {
					stages["route_points_advance"] = uint32(tick)
					tmp := uint32(tick)
					routeAdvanceTick = &tmp
					lastCompleted = "route_points_advance"
					t.Logf("G2 stage5 route points advance at tick %d count %d -> %d", tick, routeCountBefore, route.Count)
					routeCountBefore = int(route.Count)
				}
			}
			evs := s.TraceEvents()
			for _, ev := range evs {
				if ev.Kind == TraceMovementStep && ev.Handle == h {
					if distBefore.Raw() != 1<<30 && ev.X.Raw() < distBefore.Raw() && ev.X.Raw() > 0 && ev.X.Raw() < int64(1<<28) {
						if _, ok := stages["route_points_advance"]; !ok && ev.X.Raw() != distBefore.Raw() {
							// DistToGoal decreasing indicates progress
							// Only mark if we have moved at least once
							if u.X != initialX || u.Z != initialZ {
								stages["route_points_advance"] = uint32(tick)
								tmp := uint32(tick)
								routeAdvanceTick = &tmp
								lastCompleted = "route_points_advance"
								t.Logf("G2 stage5 route points advance via dist %d -> %d at tick %d", distBefore.Raw(), ev.X.Raw(), tick)
							}
						}
					}
					distBefore = ev.X
				}
			}
		} else {
			// Update routeCountBefore for next tick
			if route := s.Movement.Routes[h]; route != nil {
				routeCountBefore = int(route.Count)
			}
			// Still track dist for logging
			for _, ev := range s.TraceEvents() {
				if ev.Kind == TraceMovementStep && ev.Handle == h {
					distBefore = ev.X
				}
			}
		}
		// Stage 6: goal tolerance (strict 2 world units, not 5 cells)
		if _, ok := stages["goal_tolerance"]; !ok {
			dx := int64(goalX) - int64(u.X)
			dz := int64(goalZ) - int64(u.Z)
			dist2 := dx*dx + dz*dz
			if dist2 <= arrivalThresh2 {
				stages["goal_tolerance"] = uint32(tick)
				tmp := uint32(tick)
				goalToleranceTick = &tmp
				lastCompleted = "goal_tolerance"
				t.Logf("G2 stage6 goal tolerance at tick %d pos %d %d dist2 %d", tick, u.X.Raw(), u.Z.Raw(), dist2)
			}
		}
		// Stage 7: order completes
		if _, ok := stages["order_complete"]; !ok {
			if q.LenPrimary() == 0 {
				stages["order_complete"] = uint32(tick)
				tmp := uint32(tick)
				orderCompleteTick = &tmp
				lastCompleted = "order_complete"
				t.Logf("G2 stage7 order complete (queue empty) at tick %d", tick)
				break
			}
			if head := q.Head(); head != nil && head.MoveState == orders.MoveArrived {
				stages["order_complete"] = uint32(tick)
				tmp := uint32(tick)
				orderCompleteTick = &tmp
				lastCompleted = "order_complete"
				t.Logf("G2 stage7 order complete (MoveArrived) at tick %d", tick)
				break
			}
		}
		if len(stages) >= 7 {
			break
		}
	}
	requiredOrder := []string{"order_queued", "path_request_submitted", "route_published", "movement_begins", "route_points_advance", "goal_tolerance", "order_complete"}
	for i, name := range requiredOrder {
		if _, ok := stages[name]; !ok {
			evs := s.TraceEvents()
			last50 := LastNTraceStrings(evs, 50)
			stateHash := HashState(s)
			traceHash := HashTrace(evs)
			manifest := strictCatalogHash(cat)
			handles := []string{fmt.Sprintf("%d", h)}
			defKeys := []string{def.UnitName}
			queueHead := strictQueueHeadString(h, s)
			pathStatus := strictPathStatus(h, s)
			aimState := strictAimState(h, s)
			resourceStocks := strictResourceStocks(0, s)
			projCount := 0
			if s.Combat != nil {
				projCount = s.Combat.Count()
			}
			resultLatch := strictResultLatch(s)
			evidence := StrictGateEvidence{
				Commit: strictCommit(), ContentManifest: manifest, Map: "test", Seed: simSeed, CrtSeed: crtSeed,
				Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer"}},
				MaxTick: maxTick, Milestones: stages, Winner: -1, Reason: "G2 move", FinalTick: s.Clock.GlobalTick,
				FinalStateHash: stateHash, TraceHash: traceHash,
			}
			fr := StrictFailureRecord{
				LastCompleted: lastCompleted, CurrentTick: s.Clock.GlobalTick, Seed: simSeed, CrtSeed: crtSeed,
				Handles: handles, DefKeys: defKeys, QueueHead: queueHead, PathStatus: pathStatus,
				AimState: aimState, ResourceStocks: resourceStocks, ProjectileCount: projCount,
				ResultLatch: resultLatch, Last50Trace: last50, Evidence: &evidence,
			}
			t.Logf("G2 FAILURE record: %s", FormatFailure(fr))
			for _, n := range requiredOrder {
				if tick, ok := stages[n]; ok {
					t.Logf(" completed %s at %d", n, tick)
				} else {
					t.Logf(" missing %s (first missing at position %d)", n, i)
					break
				}
			}
			// For strict gate, missing movement stages indicate production defect in movement/authoritativeTick.
			t.Fatalf("G2 move gate missing stage %q (last completed %q) maxTick %d curTick %d [G2 1..7]", name, lastCompleted, maxTick, s.Clock.GlobalTick)
		}
	}
	var prevTick uint32
	for _, name := range requiredOrder {
		tick := stages[name]
		if tick < prevTick {
			t.Fatalf("G2 stage order violation: %s tick %d before prev %d", name, tick, prevTick)
		}
		prevTick = tick
	}
	evs := s.TraceEvents()
	stateHash := HashState(s)
	traceHash := HashTrace(evs)
	rng.SeedGlobal(simSeed, crtSeed)
	cat2 := strictMinimalCatalog()
	for _, d := range cat2.Units {
		d.CanMove = true
		d.CanPatrol = true
		d.MaxVelocity = 65536
		d.TurnRate = 1000
		d.FootprintX = 1
		d.FootprintZ = 1
		d.SightDistance = 200
	}
	terrain2 := strictMinimalTerrain()
	m2 := strictSyntheticMission()
	s2 := &Session{Catalog: cat2, World: terrain2, Mission: m2}
	w2, _ := newSlicedWorld(cat2)
	s2.Units = w2
	s2.Econ = strictEconomyForTest()
	s2.Econ.Players[0].Exists = true
	s2.Econ.Players[0].ControllerState = 1
	s2.Econ.Players[0].StatusHalfwordAt144 = 1
	s2.Econ.SeedDeadlines(0)
	var crt2 rng.CRT = rng.NewCRT(crtSeed)
	s2.InitWindForSession(&crt2, 0)
	_ = createAndBindServices(s2)
	s2.RegisterAll()
	s2.State = StateBattle
	s2.Clock.ScaledAnchor = 0
	h2, _ := s2.Units.Create(cat2.Units["armcom"], 0, sx, sy, sz)
	u2 := s2.Units.Unit(h2)
	publishOne(s2, u2)
	s2.Movement.EnsureUnit(u2)
	id2 := orders.Lookup("Move_Ground")
	if id2 == 0 {
		id2 = orders.Lookup("QMove")
	}
	q2 := orders.QueueForUnit(u2)
	q2.Push(id2, orders.NewMoveNode(id2, goalX, goalZ, s2.Clock.GlobalTick, h2, false))
	s2.SetTraceEnabled(true)
	s2.ClearTrace()
	for tick := 1; tick <= int(stages["order_complete"]); tick++ {
		s2.Step(int32(tick))
	}
	traceHash2 := HashTrace(s2.TraceEvents())
	stateHash2 := HashState(s2)
	if traceHash != traceHash2 {
		t.Fatalf("G2 determinism: trace hash mismatch %s vs %s", traceHash, traceHash2)
	}
	if stateHash != stateHash2 {
		t.Fatalf("G2 determinism: state hash mismatch %s vs %s", stateHash, stateHash2)
	}
	warnings := []string{}
	fallbacks := []string{}
	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(cat), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer"}},
		MaxTick: maxTick,
		Milestones: map[string]uint32{
			"path_request_submitted": *pathSubmittedTick,
			"route_published":        *routePublishedTick,
			"movement_begins":        *movementBeginsTick,
			"route_points_advance":   *routeAdvanceTick,
			"goal_tolerance":         *goalToleranceTick,
			"order_complete":         *orderCompleteTick,
		},
		Winner: -1, Reason: "G2 move arrival", FinalTick: s.Clock.GlobalTick,
		FinalStateHash: stateHash, TraceHash: traceHash, Fallbacks: fallbacks, Warnings: warnings,
	}
	_ = sha256.Sum256
	_ = fmt.Sprintf
	t.Logf("G2 evidence: %s", FormatEvidence(ev))
}
