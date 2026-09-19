package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestDangerDetoursOnlyAfterAllDirectChoicesFail(t *testing.T) {
	for _, direct := range []bool{false, true} {
		q, u, _ := dangerFixture(true)
		ObserveImpact(u, 16384, 10) // hazard on the +X side
		shortX := u.X - numeric.FixedFromInt(16)
		q.Binding().DangerStepFeasible = func(_ *units.Unit, x, z numeric.Fixed) bool {
			return direct && x == shortX && z == u.Z
		}
		detourQueried := false
		q.Binding().DangerRouteFeasible = func(*units.Unit, numeric.Fixed, numeric.Fixed) bool {
			detourQueried = true
			if direct {
				t.Fatal("detour queried despite a safer direct escape")
			}
			return true
		}
		x, z, ok := q.dangerWithdrawal(u, 2)
		if !ok || z != u.Z || direct && x != shortX || !direct && x != u.X-numeric.FixedFromInt(64) || detourQueried == direct {
			t.Fatalf("direct=%v goal=(%v,%v) admitted=%v detour=%v", direct, x, z, ok, detourQueried)
		}
	}
}

func dangerFixture(modern bool) (*Queue, *units.Unit, *units.Unit) {
	u := &units.Unit{Handle: 1, Alive: true, Owner: 0, Def: &content.UnitDef{BMCode: 1, CanMove: true, CanAttack: true, ManeuverLeashLength: 256}, X: numeric.FixedFromInt(512), Z: numeric.FixedFromInt(512)}
	enemy := &units.Unit{Handle: 2, Alive: true, Owner: 1, Def: &content.UnitDef{BMCode: 1}, X: numeric.FixedFromInt(612), Z: numeric.FixedFromInt(512)}
	u.Flags = units.ArmedStatus | 2<<units.StandingMoveShift | 1<<units.StandingFireShift
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Rules: modeRules(modern), SimRNG: &rng.Simulation{}, Lookup: func(h pool.Handle) *units.Unit {
		if h == u.Handle {
			return u
		}
		if h == enemy.Handle {
			return enemy
		}
		return nil
	}, Hostility: func(a, b *units.Unit) bool { return a.Owner != b.Owner }, DangerVisible: func(*units.Unit, *units.Unit) bool { return true }, DangerCanRespond: func(*units.Unit, *units.Unit, int) bool { return true }, DangerStepFeasible: func(*units.Unit, numeric.Fixed, numeric.Fixed) bool { return true }, World: &WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}})
	return q, u, enemy
}

func TestDangerStrictNoWritesOrDraws(t *testing.T) {
	q, u, enemy := dangerFixture(false)
	q.primary = []*Node{{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 1}}
	before, random := *u, *q.Binding().SimRNG
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger != (dangerState{}) || *q.Binding().SimRNG != random || !reflect.DeepEqual(*u, before) {
		t.Fatal("Strict danger hooks changed state or RNG")
	}
	if len(q.primary) != 1 || q.primary[0].Phase != 1 {
		t.Fatal("Strict changed assignment")
	}
}

func TestDangerProtectsConstructionAndDirectRepairDespiteAutoFlag(t *testing.T) {
	for _, name := range []string{"MobileBuild", "VTOL_MobileBuild", "BuildingBuild", "HelpBuild", "VTOL_HelpBuild", "RepairUnit", "RepairUnitNoMove", "VTOL_RepairUnit"} {
		t.Run(name, func(t *testing.T) {
			q, u, enemy := dangerFixture(true)
			n := &Node{ID: Lookup(name), Owner: u.Handle, Flags: FlagAutoOp, Phase: 3}
			q.primary = []*Node{n}
			ObserveDanger(u, enemy, 10)
			StepDangerResponse(u, 10)
			PurgeOrdersOnDamage(u)
			if len(q.primary) != 1 || q.primary[0] != n || n.Phase != 3 {
				t.Fatal("danger interrupted protected work")
			}
		})
	}
}

func TestDangerSuspendsProducerTaggedRepairAndResumesAssignment(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	assignment := &Node{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 1}
	successor := &Node{ID: Lookup("Move_Ground"), Owner: u.Handle}
	q.primary = []*Node{{ID: Lookup("RepairUnit"), Owner: u.Handle, automaticWork: true}, assignment, successor}
	random := *q.Binding().SimRNG
	ObserveDanger(u, enemy, 10)
	PurgeOrdersOnDamage(u)
	StepDangerResponse(u, 10)
	if len(q.primary) != 3 || q.primary[0] != q.danger.response || q.primary[0].Target != enemy.Handle || q.primary[1] != assignment || q.primary[2] != successor {
		t.Fatal("automatic repair lost assignment or failed to respond")
	}
	if *q.Binding().SimRNG != random {
		t.Fatal("danger selection drew RNG")
	}
	n := q.danger.response
	StepDangerResponse(u, 11)
	if q.danger.response != n {
		t.Fatal("stable visible threat churned response")
	}
	if q.Binding().rules().AllowAutomaticRepair(u, 100) {
		t.Fatal("repair resumed while danger persisted")
	}
	StepDangerResponse(u, 190)
	if len(q.primary) != 2 || q.primary[0] != assignment || assignment.Phase != 0 {
		t.Fatal("expiry did not resume original patrol")
	}
	if !q.Binding().rules().AllowAutomaticRepair(u, 190) {
		t.Fatal("expired memory continued suppressing repair")
	}
}

func TestDangerUsesVisibleMemoryAndRejectsReusedSlots(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	ObserveDanger(u, enemy, 10)
	last := q.danger.contacts[0]
	q.Binding().DangerVisible = func(*units.Unit, *units.Unit) bool { return false }
	enemy.X = numeric.FixedFromInt(900)
	StepDangerResponse(u, 11)
	if q.danger.contacts[0].x != last.x {
		t.Fatal("hidden contact position was tracked")
	}
	if q.danger.response == nil || q.danger.response.Target != 0 {
		t.Fatal("hidden attacker was pursued instead of withdrawing from last observed position")
	}
	replacement := *enemy
	q.Binding().Lookup = func(h pool.Handle) *units.Unit {
		if h == enemy.Handle {
			return &replacement
		}
		return u
	}
	StepDangerResponse(u, 12)
	if q.danger.contacts[0].unit != nil {
		t.Fatal("reused handle retained old danger")
	}
}

func TestDangerHonorsStancesAndManualOrders(t *testing.T) {
	for _, name := range []string{"Move_Ground", "Attack_Chase", "RepairUnit"} {
		q, u, enemy := dangerFixture(true)
		n := &Node{ID: Lookup(name), Owner: u.Handle, Target: enemy.Handle}
		q.primary = []*Node{n}
		ObserveDanger(u, enemy, 10)
		StepDangerResponse(u, 10)
		if q.danger.response != nil || q.primary[0] != n {
			t.Fatalf("interrupted %s", name)
		}
	}
	q, u, enemy := dangerFixture(true)
	u.Flags &^= (uint32(3) << units.StandingMoveShift)
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response != nil {
		t.Fatal("hold position moved")
	}
	u.Flags |= 2 << units.StandingMoveShift
	u.Flags &^= (uint32(3) << units.StandingFireShift)
	StepDangerResponse(u, 40)
	if q.danger.response == nil || q.danger.response.Target != 0 {
		t.Fatal("hold fire attacked or failed last-resort withdrawal")
	}
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle})
	if q.danger != (dangerState{}) || len(q.primary) != 1 {
		t.Fatal("new manual command did not supersede response")
	}
}

func TestDangerWithdrawalConsidersAllThreatsAndFeasibility(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	q.Binding().DangerCanRespond = func(*units.Unit, *units.Unit, int) bool { return false }
	other := &units.Unit{Handle: 3, Alive: true, Owner: 1, Def: enemy.Def, X: numeric.FixedFromInt(412), Z: numeric.FixedFromInt(512)}
	oldLookup := q.Binding().Lookup
	q.Binding().Lookup = func(h pool.Handle) *units.Unit {
		if h == 3 {
			return other
		}
		return oldLookup(h)
	}
	ObserveDanger(u, enemy, 10)
	ObserveDanger(u, other, 10)
	// Both east and west are dangerous; north is blocked, so choose south.
	q.Binding().DangerStepFeasible = func(_ *units.Unit, x, z numeric.Fixed) bool { return x == u.X && z > u.Z }
	StepDangerResponse(u, 10)
	n := q.danger.response
	if n == nil || n.Target != 0 || n.GoalX != u.X || n.GoalZ <= u.Z {
		t.Fatal("retreat ignored multiple threats or local feasibility")
	}
}

func TestDangerManeuverKeepsAnchorAndReturnOrder(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
	assignment := &Node{ID: Lookup("Patrol"), Owner: u.Handle}
	q.primary = []*Node{assignment}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	n := q.danger.response
	if n == nil || n.Param3 != 256 || n.GuardX != 512 || len(q.primary) != 3 || q.primary[1] != q.danger.returnMove || q.primary[2] != assignment {
		t.Fatal("maneuver did not retain anchored return")
	}
	u.X = numeric.FixedFromInt(768)
	StepDangerResponse(u, 11)
	if q.danger.response != nil || q.primary[0] != q.danger.returnMove {
		t.Fatal("inclusive maneuver leash did not return to original post")
	}
}

func TestDangerFutureQueuedWorkDoesNotProtectCurrentPatrol(t *testing.T) {
	for _, name := range []string{"MobileBuild", "RepairUnit"} {
		q, u, enemy := dangerFixture(true)
		patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle}
		future := &Node{ID: Lookup(name), Owner: u.Handle, Target: enemy.Handle}
		q.primary = []*Node{patrol, future}
		ObserveDanger(u, enemy, 10)
		StepDangerResponse(u, 10)
		if q.danger.response == nil || len(q.primary) != 3 || q.primary[1] != patrol || q.primary[2] != future {
			t.Fatalf("future %s disabled patrol response or was lost", name)
		}
	}
	q, u, enemy := dangerFixture(true)
	work := &Node{ID: Lookup("HelpBuild"), Owner: u.Handle, Phase: 2}
	q.primary = []*Node{{ID: Lookup("Standing_MoveOrder"), Owner: u.Handle}, work}
	ObserveDanger(u, enemy, 10)
	PurgeOrdersOnDamage(u)
	if len(q.primary) != 2 || q.primary[1] != work {
		t.Fatal("control record hid active construction")
	}
	if RetaliationOrder(u, enemy) {
		t.Fatal("legacy damage retaliation bypassed Modern scheduler")
	}
}

func TestDangerCanAnswerWithSecondaryWeapon(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	// Per-slot suitability owns the bad-target masks. Only slot two can answer.
	enemy.Def.UnitMask = content.MaskForID(5)
	u.Def.BadTargetCategoryWPRIMask = content.MaskForID(5)
	q.Binding().DangerCanRespond = func(_, _ *units.Unit, slot int) bool { return slot == 2 }
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.ID != Lookup("Attack_Chase") || q.danger.response.Param1 != 2 {
		t.Fatal("viable secondary weapon was ignored")
	}
}

func TestDangerHoldPositionCanReturnFireWithoutMoving(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	u.Flags &^= uint32(3) << units.StandingMoveShift
	q.Binding().Weapons = &WeaponAdapter{CanEngage: func(*units.Unit, pool.Handle, int) bool { return true }}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.ID != Lookup("Attack_NoMove") || len(q.primary) != 1 {
		t.Fatal("hold position failed stationary response")
	}
}

func TestDangerFailedPursuitWithdrawsAndDoesNotChurnRepair(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle}
	q.primary = []*Node{patrol}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	q.danger.response.MoveState = MoveBlocked
	StepDangerResponse(u, 11)
	StepDangerResponse(u, 41)
	n := q.danger.response
	if n == nil || n.Target != 0 {
		t.Fatal("failed pursuit was repeated instead of withdrawing")
	}
	u.X, u.Z = n.GoalX, n.GoalZ
	q.RemoveHead()
	q.Binding().DangerStepFeasible = func(*units.Unit, numeric.Fixed, numeric.Fixed) bool { return false }
	StepDangerResponse(u, 71)
	if q.danger.response == nil || q.danger.response.ID != Lookup("Wait") || q.primary[len(q.primary)-1] != patrol {
		t.Fatal("exhausted retreat immediately resumed unsafe patrol")
	}
	if q.Binding().rules().AllowAutomaticRepair(u, 71) {
		t.Fatal("automatic repair churned during danger")
	}
}

func TestDangerStateIsLocalAndStrictSwitchKeepsRestartableAssignment(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	other, _, _ := dangerFixture(true)
	patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 2, DynamicGate: gateArrived}
	q.primary = []*Node{patrol}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if other.danger != (dangerState{}) {
		t.Fatal("danger escaped the victim queue")
	}
	if patrol.Phase != 0 || patrol.DynamicGate != 0 {
		t.Fatal("suspended assignment cannot restart after switch/load")
	}
	previous := q.danger
	q.Binding().Rules = StrictRules{}
	ObserveDanger(u, enemy, 1000)
	StepDangerResponse(u, 1000)
	if q.danger != previous {
		t.Fatal("Strict switch touched staged Modern state")
	}
	// Existing ordinary reaction completes under Strict, exposing the restartable
	// original assignment. No reaction-specific handler is needed after switch.
	q.RemoveHead()
	if q.primary[0] != patrol || patrol.Phase != 0 {
		t.Fatal("Strict completion lost assignment")
	}
}

func TestDangerRepairProvenanceComesFromProducer(t *testing.T) {
	f := newGuardFixture(t, 1, 1)
	q := QueueForUnit(f.guard)
	q.Binding().Rules = &ModernRules{}
	f.ward.Health = 1
	f.guard.Def.Builder, f.guard.Def.CanReclamate = true, true
	q.primary = []*Node{guardNode(f)}
	q.primary[0].Phase = 1
	// The ordinary guard producer knows why it chose the ward as a patient.
	guardHandler(f.guard, q.primary[0], 0, 100)
	found := false
	for _, n := range q.primary {
		if n.ID == Lookup("RepairUnit") {
			found = n.automaticWork
		}
	}
	if !found {
		t.Fatal("guard-chosen repair lacked explicit provenance")
	}
}

func TestDangerRestoredRepairHasUnknownProtectedProvenance(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	q.PushHead(Lookup("RepairUnit"), Node{Owner: u.Handle, Target: enemy.Handle, automaticWork: true})
	ObserveImpact(u, numeric.Angle(16384), 10)
	ObserveDanger(u, enemy, 10)
	images, err := RetailOrderImagesWithPayload(u, func(h pool.Handle) (uint16, bool) { return uint16(h), h != 0 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary, Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype, DescriptorName: image.DescriptorName, BuildTypeName: image.BuildTypeName}
	}
	if err = RetailRestoreOrdersAtTick(u, records, map[uint16]pool.Handle{1: 1, 2: 2}, q.Binding(), 20); err != nil {
		t.Fatal(err)
	}
	q = QueueOfUnit(u)
	if q.danger != (dangerState{}) || q.primary[0].automaticWork || !protectedDangerWork(q) {
		t.Fatal("load retained transient danger or treated unknown repair as automatic")
	}
	n := q.primary[0]
	ObserveDanger(u, enemy, 21)
	StepDangerResponse(u, 21)
	PurgeOrdersOnDamage(u)
	if len(q.primary) != 1 || q.primary[0] != n {
		t.Fatal("restored repair interrupted")
	}
}

func TestDangerFireAtWillReconsidersThroughCombatRanking(t *testing.T) {
	for _, fire := range []uint32{1, 2} {
		q, u, enemy := dangerFixture(true)
		u.Flags = u.Flags&^(uint32(3)<<units.StandingFireShift) | fire<<units.StandingFireShift
		tower := &units.Unit{Handle: 3, Alive: true, Owner: 1, Def: enemy.Def, X: enemy.X, Z: enemy.Z}
		oldLookup := q.Binding().Lookup
		q.Binding().Lookup = func(h pool.Handle) *units.Unit {
			if h == 3 {
				return tower
			}
			return oldLookup(h)
		}
		calls := 0
		q.Binding().Weapons = &WeaponAdapter{CanEngage: func(*units.Unit, pool.Handle, int) bool { return true }, Acquire: func(_ *units.Unit, _ int, limit uint32) (pool.Handle, bool) {
			if limit != 0 {
				t.Fatal("acquisition used tick as range limit")
			}
			calls++
			return tower.Handle, true
		}}
		ObserveDanger(u, enemy, 10)
		StepDangerResponse(u, 10)
		initial := q.danger.response
		StepDangerResponse(u, 40)
		if fire == 1 {
			if calls != 0 || q.danger.response != initial {
				t.Fatal("ReturnFire gained proactive opportunity targeting")
			}
		} else {
			if calls != 1 || q.danger.response == nil || q.danger.response.Target != tower.Handle {
				t.Fatal("reaction pinned obsolete target instead of using combat ranking")
			}
			selected := q.danger.response
			StepDangerResponse(u, 41)
			StepDangerResponse(u, 70)
			if q.danger.response != selected {
				t.Fatal("stable combat selection churned order")
			}
		}
	}
}

func TestDangerCanUseShorterSafeWithdrawal(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	q.Binding().DangerCanRespond = func(*units.Unit, *units.Unit, int) bool { return false }
	q.Binding().DangerStepFeasible = func(_ *units.Unit, x, z numeric.Fixed) bool { return z == u.Z && x == u.X-numeric.FixedFromInt(16) }
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.GoalX != u.X-numeric.FixedFromInt(16) {
		t.Fatal("safe short corridor was ignored")
	}
}

func TestDangerBlockedManualMoveSuspendsAndRestartsDestination(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	move := &Node{ID: Lookup("Move_Ground"), Owner: u.Handle, MoveState: MoveBlocked, Phase: 1, GoalX: numeric.FixedFromInt(800), GoalZ: numeric.FixedFromInt(900)}
	q.primary = []*Node{move}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || len(q.primary) != 2 || q.primary[1] != move {
		t.Fatal("blocked manual move stayed inactive under observed fire")
	}
	StepDangerResponse(u, 190)
	if q.primary[0] != move || move.Phase != 0 || move.MoveState != 0 || move.GoalX != numeric.FixedFromInt(800) || move.GoalZ != numeric.FixedFromInt(900) {
		t.Fatal("blocked move lost original destination")
	}
	q, u, enemy = dangerFixture(true)
	work := &Node{ID: Lookup("RepairUnit"), Owner: u.Handle, Phase: 2}
	q.primary = []*Node{{ID: Lookup("Move_Ground"), Owner: u.Handle, MoveState: MoveBlocked}, work}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response != nil {
		t.Fatal("blocked approach interrupted protected repair parent")
	}
}

func TestDangerCanUseShorterDiagonalWithdrawal(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	q.Binding().DangerCanRespond = func(*units.Unit, *units.Unit, int) bool { return false }
	wantX, wantZ := u.X-numeric.FixedFromInt(11), u.Z+numeric.FixedFromInt(11)
	q.Binding().DangerStepFeasible = func(_ *units.Unit, x, z numeric.Fixed) bool { return x == wantX && z == wantZ }
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.GoalX != wantX || q.danger.response.GoalZ != wantZ {
		t.Fatal("safe short diagonal corridor was ignored")
	}
}

func TestDangerFailureResultPreservesOnlyAutomaticAssignment(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, automatic := range []bool{false, true} {
			q, u, enemy := dangerFixture(modern)
			assignment := &Node{ID: Lookup("Wait"), Owner: u.Handle, Param1: 1000, Deadline: -1}
			// An invalid chase phase returns cancel-all through the real primary
			// pump. Only the tracked Modern reaction may narrow that result.
			reaction := &Node{ID: Lookup("Attack_Chase"), Owner: u.Handle, Target: enemy.Handle, Phase: 99}
			q.primary = []*Node{reaction, assignment}
			if automatic {
				q.danger.response = reaction
				q.danger.resume = assignment
			}
			q.Pump(u, 10)
			if modern && automatic {
				if len(q.primary) != 1 || q.primary[0] != assignment {
					t.Fatal("automatic reaction failure cancelled original assignment")
				}
			} else if len(q.primary) != 0 {
				t.Fatal("Strict or explicit failure semantics changed")
			}
		}
	}
}

// These are the movement owner's published record notifications, independent
// of MoveState. Search setup alone does not establish an empty route away
// from the goal [04 R-PATH-01 §7][04 R-PATH-01 §9][04 R-COLL-01 §6].
func TestDangerUsesPublishedMovementFailure(t *testing.T) {
	cases := []struct {
		name   string
		bits   uint32
		failed bool
	}{
		{"no notification", 0, false},
		{"arrived", gateArrived, false},
		{"payload released", 0x80, false},
		{"start satisfied or connected ray", 0x100, false},
		{"ray miss or rejected start", 0x200, false},
		{"no route", gateNoRoute, true},
		{"rejected then empty away from goal", 0x200 | gateNoRoute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, modern := range []bool{false, true} {
				q, u, enemy := dangerFixture(modern)
				move := &Node{ID: Lookup("Move_Ground"), Owner: u.Handle, MoveState: MoveEnRoute, PathStatus: 0x200, Satisfied: tc.bits, Phase: 1, Deadline: -1, GoalX: numeric.FixedFromInt(800), GoalZ: numeric.FixedFromInt(900)}
				q.primary = []*Node{move}
				ObserveDanger(u, enemy, 10)
				StepDangerResponse(u, 10)
				if (q.danger.response != nil) != (modern && tc.failed) {
					t.Fatalf("modern=%v notification=%#x: response=%v", modern, tc.bits, q.danger.response)
				}
				if !modern || !tc.failed {
					if move.Satisfied != tc.bits || move.MoveState != MoveEnRoute || move.PathStatus != 0x200 {
						t.Fatal("bypassed move state changed")
					}
					continue
				}
				if move.Satisfied != 0 || move.MoveState != 0 || move.PathStatus != 0 || move.Phase != 0 {
					t.Fatal("suspended move retained consumed movement failure")
				}
				StepDangerResponse(u, 190)
				if q.Head() != move || move.Satisfied != 0 || move.MoveState != 0 || move.PathStatus != 0 || move.GoalX != numeric.FixedFromInt(800) || move.GoalZ != numeric.FixedFromInt(900) {
					t.Fatal("resumed move retained stale failure or lost destination")
				}
			}
		})
	}
}

func TestDangerPublishedNoRouteEndsPursuitAndDefersFailedTarget(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	response := q.danger.response
	response.MoveState = MoveEnRoute
	response.Satisfied = 0x200 | gateNoRoute
	StepDangerResponse(u, 11)
	if q.danger.response != nil || q.containsDangerNode(response) || q.danger.contacts[0].failedUntil != 101 {
		t.Fatal("published no-route did not end pursuit and cool down target")
	}
	StepDangerResponse(u, 41)
	if q.danger.response == nil || q.danger.response.Target != 0 {
		t.Fatal("failed pursuit retried instead of considering withdrawal")
	}
}

func TestDangerRetargetRespectsControlHeadAndStillExpires(t *testing.T) {
	for _, name := range []string{"Paralyze", "Activate", "Standing_MoveOrder"} {
		t.Run(name, func(t *testing.T) {
			q, u, enemy := dangerFixture(true)
			u.Flags = u.Flags&^(uint32(3)<<units.StandingFireShift) | 2<<units.StandingFireShift
			tower := *enemy
			tower.Handle = 3
			old := q.Binding().Lookup
			q.Binding().Lookup = func(h pool.Handle) *units.Unit {
				if h == 3 {
					return &tower
				}
				return old(h)
			}
			acquisitions := 0
			q.Binding().Weapons = &WeaponAdapter{CanEngage: func(*units.Unit, pool.Handle, int) bool { return true }, Acquire: func(*units.Unit, int, uint32) (pool.Handle, bool) { acquisitions++; return 3, true }}
			ObserveDanger(u, enemy, 10)
			StepDangerResponse(u, 10)
			response := q.danger.response
			control := q.PushHead(Lookup(name), Node{Owner: u.Handle, Param1: 1000})
			StepDangerResponse(u, 40)
			if q.Head() != control || q.danger.response != response || acquisitions != 0 || q.danger.contacts[0].unit != enemy {
				t.Fatal("retargeting bypassed control head or dropped valid observation")
			}
			StepDangerResponse(u, 190)
			if q.Head() != control || q.containsDangerNode(response) || q.danger.contacts[0].unit != nil {
				t.Fatal("control head prevented expiry or was preempted during cleanup")
			}
		})
	}
}

func TestDangerReturnAfterExpiryWaitsForParalyze(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
	q.Binding().DangerCanRespond = func(*units.Unit, *units.Unit, int) bool { return false }
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	response := q.danger.response
	u.X, u.Z = response.GoalX, response.GoalZ
	control := q.PushHead(Lookup("Paralyze"), Node{Owner: u.Handle, Param1: 1000})
	u.Stunned = true
	StepDangerResponse(u, 190)
	if q.Head() != control || q.containsDangerNode(response) || q.danger.returnMove != nil || !q.danger.withdrew {
		t.Fatal("expiry returned to post ahead of Paralyze")
	}
	q.RemoveHead()
	u.Stunned = false
	StepDangerResponse(u, 191)
	if q.danger.returnMove == nil || q.Head() != q.danger.returnMove || q.Head().GoalX != numeric.FixedFromInt(512) || q.Head().GoalZ != numeric.FixedFromInt(512) {
		t.Fatal("post-control visit lost deferred return")
	}
}
