package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestPatrolCyclesRatherThanCompletingAtItsFirstPoint is this unit's defect
// test. While `Patrol` ran `Move_Ground`'s body it completed on the arrival bit
// and its record was unlinked: the unit walked to one waypoint and stopped, and
// no chain, no return-to-start waypoint and no cycle ever existed. The row's own
// body rotates instead [04 R-ORD-01 §4], so an arrival must leave the record
// queued, at the tail, with its phase set back to 1 to re-arm the leg.
func TestPatrolCyclesRatherThanCompletingAtItsFirstPoint(t *testing.T) {
	q, unit, _ := workFixture()
	id := Lookup("Patrol")
	if id == 0 || DescriptorFor(id).Handler == nil {
		t.Fatalf("Patrol has no handler after every installer ran [PLAN 18 gate 10]")
	}
	q.Push(id, Node{Owner: unit.Handle,
		GoalX: numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(200 << 16)})
	patrol := q.Primary()[0]

	// Phase 0: the patrol-chain setup appends the return-to-start waypoint, so
	// the queue grows by one record that no move body would ever have made.
	q.Pump(unit, 1)
	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d after phase 0, want 2: the chain setup appends the return waypoint [04 R-ORD-01 §4]", q.LenPrimary())
	}
	if patrol.StaticGate&patrolChainMember == 0 {
		t.Fatalf("phase 0 must mark this record as a chain member [04 R-ORD-01 §4]")
	}

	// Phase 1 arms the leg on the movement outcomes alone: the gate is ASSIGNED
	// 0xE0 after the deadline setter, so the record carries no timer bit and
	// stalls at its gate until the mover reports [R-ORDER-02 §1].
	q.Pump(unit, 2)
	if patrol.Phase != 2 || patrol.DynamicGate != gateMoveOutcomes {
		t.Fatalf("after phase 1: phase %d gate %#x, want phase 2 gate 0xE0 [04 R-ORD-01 §4]", patrol.Phase, patrol.DynamicGate)
	}

	// The mover arrives.
	patrol.Satisfied |= gateArrived
	q.Pump(unit, 3)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d after arrival, want 2: the record must rotate, not complete", q.LenPrimary())
	}
	primary := q.Primary()
	if primary[1] != patrol {
		t.Fatalf("the arrived record must sit at the segment tail after its rotate [04 §3.3]")
	}
	if patrol.Phase != 1 {
		t.Fatalf("rotated record is at phase %d, want 1 so its next visit re-arms the leg", patrol.Phase)
	}
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "no handler") {
			t.Fatalf("Patrol parked on the missing-handler arm: %s", d)
		}
	}
}

// TestQueuedMoveRotatesOnItsSixtyTickDeadline locks the queued-move pair's whole
// row: "Deadline 60, *rotate*" [04 R-ORD-01 §2][R-ORDER-02 §1]. A rally marker
// on a factory's queue cycles; it does not walk a goal and it does not complete,
// which is what it did while it ran the ground move's body.
func TestQueuedMoveRotatesOnItsSixtyTickDeadline(t *testing.T) {
	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, name := range []string{"QMove", "QPatrol"} {
		q, unit, _ := workFixture()
		id := Lookup(name)
		if id == 0 || DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after every installer ran", name)
		}
		q.Push(id, Node{Owner: unit.Handle, GoalX: numeric.Fixed(200 << 16)})
		marker := q.Primary()[0]

		q.Pump(unit, 5)

		if q.LenPrimary() != 1 {
			t.Fatalf("%s: primary length %d, want 1: the marker rotates, it never completes", name, q.LenPrimary())
		}
		if marker.Deadline != 65 || marker.DynamicGate&gateDeadline == 0 {
			t.Fatalf("%s: deadline %d gate %#x, want 65 with bit 0 armed [04 R-ORD-01 §2]", name, marker.Deadline, marker.DynamicGate)
		}
		if marker.Phase != 0 {
			t.Fatalf("%s: phase %d, want 0: the row has no phase machine", name, marker.Phase)
		}
	}
}

// TestVTOLRepairPatrolInstallsThroughThePump is the registration half of this
// unit. WU-18-3 wrote and tested `VTOL_RepairPatrol`'s body but could only reach
// it by calling the handler directly: pump.go's move list claimed the descriptor
// first, and a family installer assigns only where the handler is still nil. The
// narrowing gives the name back, so the record must now reach that body through
// an ordinary pump — the chain-setup append and the airborne mover are what only
// the real row does [04 R-ORD-01 §7].
func TestVTOLRepairPatrolInstallsThroughThePump(t *testing.T) {
	q, builder, _ := vtolWorkFixture()
	id := Lookup("VTOL_RepairPatrol")
	if id == 0 || DescriptorFor(id).Handler == nil {
		t.Fatalf("VTOL_RepairPatrol has no handler after every installer ran")
	}
	q.Push(id, Node{Owner: builder.Handle,
		GoalX: numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(200 << 16)})
	head := q.Primary()[0]

	q.Pump(builder, 100)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d, want 2: only the row's own phase 0 runs the patrol-chain setup", q.LenPrimary())
	}
	if builder.Move.Mode&0x3 != 2 {
		t.Fatalf("only the row's own preamble puts a grounded aircraft airborne [04 R-ORD-01 §7]")
	}
	if head.Phase != 1 {
		t.Fatalf("phase %d after the first visit, want 1", head.Phase)
	}
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "no handler") {
			t.Fatalf("VTOL_RepairPatrol parked on the missing-handler arm: %s", d)
		}
	}
}

// TestReconciledLeashAgreesBetweenGroundRowAndAirTwin locks the fold of this
// package's two pursuit-leash helpers onto one [R-STANCE-01 §4]. The ground
// `RepairUnit` and its air twin `VTOL_RepairUnit` cite the same `Attack_Chase`
// pre-check, so they must agree — and the geometry below is exactly where the
// retired helper disagreed with the contract.
//
// The anchor is the origin and the leash is 5 whole world units. At (3.9, 3.9)
// the contract truncates both deltas to 3 and the distance to
// `trunc(hypot(3, 3)) = 4`, which is inside the leash; the retired 16.16 form
// kept the fractions, measured 5.51 and abandoned the order. At (4, 4) the
// distance is `trunc(hypot(4, 4)) = 5` and the inclusive compare ends it.
func TestReconciledLeashAgreesBetweenGroundRowAndAirTwin(t *testing.T) {
	const frac39 = 3*65536 + 58982 // 3.9 world units in 16.16

	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, row := range []struct {
		name    string
		fixture func() (*Queue, *units.Unit, *units.Unit)
		handler func(*units.Unit, *Node, uint32, uint32) Code
	}{
		{"RepairUnit", workFixture, repairUnitHandler},
		{"VTOL_RepairUnit", vtolWorkFixture, vtolRepairUnitHandler},
	} {
		_, actor, target := row.fixture()
		node := func() *Node {
			return &Node{ID: Lookup(row.name), Owner: actor.Handle, Target: target.Handle,
				Deadline: -1, Param3: 5, GuardX: 0, GuardY: 0}
		}

		actor.X, actor.Z = numeric.Fixed(frac39), numeric.Fixed(frac39)
		if code := row.handler(actor, node(), 0, 1); code == 5 {
			t.Fatalf("%s: a unit 4 whole units from its anchor ended a leash-5 order; the compare is on truncated whole units [R-STANCE-01 §4]", row.name)
		}

		actor.X, actor.Z = numeric.Fixed(4<<16), numeric.Fixed(4<<16)
		if code := row.handler(actor, node(), 0, 1); code != 5 {
			t.Fatalf("%s: returned %d at distance 5 with leash 5; the compare is inclusive [R-STANCE-01 §4]", row.name, code)
		}
	}
}

// TestAirPatrolInstallsItsMarker covers the payload an air patrol had never
// installed: its phase 2 called the point installer, which takes the canfly
// release-only arm, so the record armed gate 0xE0 and waited for an arrival bit
// nothing could raise.
//
// The geometry assertion is the one [04 R-ORD-02 §2] corrected on 2026-08-30:
// the marker sits 320 world units BEYOND the waypoint on the far side from the
// aircraft, not short of it.
func TestAirPatrolInstallsItsMarker(t *testing.T) {
	var got []AirGoalRequest
	u := &units.Unit{Def: &content.UnitDef{UnitName: "flier", CanFly: true}, Alive: true}
	u.X, u.Z = numeric.Fixed(100<<16), numeric.Fixed(500<<16)
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Movement: &MovementGoalAdapter{
		InstallAir: func(req AirGoalRequest) bool { got = append(got, req); return true },
		InstallPoint: func(PointGoalRequest) bool {
			t.Fatalf("an air patrol must not install a ground point goal")
			return false
		},
	}})

	// Waypoint due +X of the aircraft at the same Z.
	n := &Node{Owner: u.Handle, GoalX: numeric.Fixed(900 << 16), GoalZ: numeric.Fixed(500 << 16)}
	installAirPatrolMarker(u, n)

	if len(got) != 1 {
		t.Fatalf("installs = %d, want 1 — the air patrol installed no payload", len(got))
	}
	if got[0].Radius != airPatrolArrivalRadius {
		t.Fatalf("radius = %#x, want %#x", got[0].Radius, airPatrolArrivalRadius)
	}
	if got[0].Node != n {
		t.Fatalf("install carried the wrong node identity")
	}
	// Beyond, not short: the marker's X must exceed the waypoint's, by the
	// setback, and Z must be unchanged along this axis-aligned leg.
	markerX := got[0].X.Raw() >> 16
	if markerX <= 900 {
		t.Fatalf("marker X = %d, want beyond the waypoint at 900 [04 R-ORD-02 §2 correction]", markerX)
	}
	if delta := markerX - 900; delta < airPatrolSetback-2 || delta > airPatrolSetback+2 {
		t.Fatalf("marker overshoot = %d, want %d", delta, airPatrolSetback)
	}
	if markerZ := got[0].Z.Raw() >> 16; markerZ < 498 || markerZ > 502 {
		t.Fatalf("marker Z = %d, want the waypoint's 500 on an axis-aligned leg", markerZ)
	}

	// No air seam bound: nothing installs and nothing panics.
	loose := &units.Unit{Def: u.Def, Alive: true}
	installAirPatrolMarker(loose, &Node{Owner: loose.Handle})
}

// TestVTOLPatrolSeeksAPadOnlyWhenHurt locks `VTOL_Patrol` phase 2's low-health
// pad seek [04 R-ORD-02 §2], whose candidate rule is the one [04 R-AIR-01 §7]
// gives `VTOL_SeekAttack` and [04 R-AIR-01 §11] settles: below three quarters
// of `maxdamage` ((uint)(int16)health < (maxdamage >> 2) * 3) the row filters
// the target registry's third list to the activated air bases within 0xF00,
// releases the payload, draws one RNG(count) and spawns `VTOL_Landing` at that
// pad at the head, gate 0, *restart*.
//
// Both halves are asserted, because the draw is the half that moves every later
// simulation draw if it is taken on the wrong visit (I4): a healthy aircraft
// takes none at all.
func TestVTOLPatrolSeeksAPadOnlyWhenHurt(t *testing.T) {
	q, flier, _ := vtolWorkFixture()
	padDef := &content.UnitDef{MaxDamage: 100, Builder: true, IsAirBase: true}
	// Two pads, so the bounded pick actually draws: the draw helper returns 0
	// WITHOUT advancing the stream for a count below 2 [04 R-ORD-01 §1], so a
	// one-pad fixture would prove nothing about the draw.
	padA := &units.Unit{
		Handle: 7, Owner: flier.Owner, Def: padDef, Alive: true, Activated: true,
		X: flier.X, Y: flier.Y, Z: flier.Z,
	}
	padB := &units.Unit{
		Handle: 8, Owner: flier.Owner, Def: padDef, Alive: true, Activated: true,
		X: flier.X, Y: flier.Y, Z: flier.Z,
	}
	// The candidate set is the target registry's third list, which the movement
	// system holds and refills on its own cadence, so the fixture supplies the
	// list through the port rather than an enumerator [04 R-AIR-01 §11].
	pads := map[pool.Handle]*units.Unit{padA.Handle: padA, padB.Handle: padB}
	q.binding.Lookup = func(h pool.Handle) *units.Unit { return pads[h] }
	q.binding.Movement = &MovementGoalAdapter{
		AirBases: func(uint8) []pool.Handle { return []pool.Handle{padA.Handle, padB.Handle} },
	}
	landingID := Lookup("VTOL_Landing")
	if landingID == 0 {
		t.Fatal("VTOL_Landing has no descriptor")
	}

	id := Lookup("VTOL_Patrol")
	q.Push(id, Node{Owner: flier.Handle, Deadline: -1,
		GoalX: numeric.Fixed(400 << 16), GoalZ: numeric.Fixed(400 << 16)})
	n := q.Primary()[0]
	n.Phase = 2

	// Full health: no seek, no draw, the leg holds on its own 30-tick deadline.
	before := rng.Global.Sim.Draws()
	if code := vtolPatrolHandler(flier, n, 0, 100); code != 2 {
		t.Fatalf("a healthy patrol leg returned %d, want hold (2)", code)
	}
	if got := rng.Global.Sim.Draws() - before; got != 0 {
		t.Fatalf("healthy patrol leg drew %d times, want none [I4]", got)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("primary length %d, want 1: a healthy aircraft spawns no landing", q.LenPrimary())
	}

	// Below three quarters of maxdamage: the seek runs, takes exactly one draw
	// over the candidate list and puts the landing record at the head.
	flier.Health = 50 // (100 >> 2) * 3 = 75
	n.Phase = 2
	before = rng.Global.Sim.Draws()
	if code := vtolPatrolHandler(flier, n, 0, 100); code != 0 {
		t.Fatalf("a hurt patrol leg returned %d, want restart (0) with the landing at the head", code)
	}
	if got := rng.Global.Sim.Draws() - before; got != 1 {
		t.Fatalf("pad seek drew %d times, want exactly one RNG(count) [04 R-AIR-01 §7][I4]", got)
	}
	if n.DynamicGate != 0 {
		t.Fatalf("gate %#x after the seek, want 0 [04 R-ORD-02 §2]", n.DynamicGate)
	}
	if q.LenPrimary() != 2 || q.Primary()[0].ID != landingID {
		t.Fatalf("head is %q, want the spawned VTOL_Landing", DescriptorFor(q.Primary()[0].ID).Name)
	}
	if got := q.Primary()[0].Target; got != padA.Handle && got != padB.Handle {
		t.Fatalf("landing target = %d, want one of the two pads", got)
	}
}

// TestPatrolPhaseTwoArmIsTheStandingFireScan locks the middle arm of ground
// `Patrol`'s phase 2 as [04 R-ORD-01 §9] corrects it: the idle-arm target scan
// of [04 R-STANCE-01 §3] handed to the auto-engage issuer of §4 with
// `force = 0`, and the *wait* taken only when the issuer accepts.
//
// [04 R-ORD-01 §4]'s row worded this as "a next patrol record exists", and the
// handler implemented a successor test. The handler body has no read of the
// record chain at that point; §4's label was wrong.
//
// The scan's own gate is what the two cases contrast: it searches ONLY when the
// standing fire field reads exactly 2, fire at will [04 R-STANCE-01 §3]. A
// hold-fire patroller with the same enemy at the same distance never scans, so
// it takes the row's other arm — the `30 + RNG(30)` re-arm with *hold* — and
// spawns nothing. Nothing here asserts a successor either way: both cases run
// with a second record queued behind, which under the retired reading would
// have decided the outcome by itself.
func TestPatrolPhaseTwoArmIsTheStandingFireScan(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fireField uint32
		wantCode  Code
		wantSpawn bool
	}{
		{"fire at will engages", 2, 3, true},
		{"hold fire does not", 0, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := &content.UnitDef{BMCode: 1, CanAttack: true, CanPatrol: true, SightDistance: 400, MaxDamage: 3000}
			q, u := standingFixture(def)
			u.Flags |= units.ArmedStatus
			// Standing move field 2 and the fire field under test. autoEngage
			// with force = 0 refuses a zero move field, so the move stance is
			// held constant across both cases [04 R-STANCE-01 §4].
			u.Flags = (u.Flags &^ (stanceFieldMask << stanceMoveShift)) | (2 << stanceMoveShift)
			u.Flags = (u.Flags &^ (stanceFieldMask << stanceFireShift)) | (tc.fireField << stanceFireShift)

			enemy := &units.Unit{
				Handle: 2, Def: &content.UnitDef{BMCode: 1, MaxDamage: 100}, Alive: true,
				X: numeric.Fixed(120 << 16), Z: numeric.Fixed(90 << 16), Health: 100, MaxHealth: 100,
			}
			q.binding.Lookup = func(h pool.Handle) *units.Unit {
				if h == enemy.Handle {
					return enemy
				}
				return nil
			}
			q.binding.Weapons = &WeaponAdapter{
				Acquire: func(_ *units.Unit, slot int, limit uint32) (pool.Handle, bool) {
					if slot != 0 {
						return 0, false
					}
					if limit != uint32(def.SightDistance) {
						t.Fatalf("scan range limit %d, want the definition's sightdistance %d [04 R-STANCE-01 §3]", limit, def.SightDistance)
					}
					return enemy.Handle, true
				},
			}

			// A leg at phase 2 with a second record behind it: under the retired
			// successor reading the successor alone decided this.
			patrol := Lookup("Patrol")
			leg := q.PushHead(patrol, Node{Owner: u.Handle, Phase: 2, Deadline: -1})
			q.Push(patrol, Node{Owner: u.Handle, Deadline: -1})
			before := q.LenPrimary()

			code := patrolHandler(u, leg, 0, 100)
			if code != tc.wantCode {
				t.Fatalf("phase 2 returned %d, want %d [04 R-ORD-01 §9]", code, tc.wantCode)
			}
			if leg.Phase != 1 {
				t.Fatalf("phase 2 left the record at phase %d, want 1 — both arms reset it [04 R-ORD-01 §4]", leg.Phase)
			}
			spawned := q.LenPrimary() > before
			if spawned != tc.wantSpawn {
				t.Fatalf("queue length %d -> %d, want a spawned attack record: %v", before, q.LenPrimary(), tc.wantSpawn)
			}
			if tc.wantSpawn {
				if leg.DynamicGate != 0 {
					t.Fatalf("the accepted issue left gate %#x, want it cleared [04 R-ORD-01 §9]", leg.DynamicGate)
				}
				head := q.Primary()[0]
				if head == leg {
					t.Fatal("the spawned attack was not head-inserted [04 R-STANCE-01 §4]")
				}
				if head.Target != enemy.Handle {
					t.Fatalf("the spawned record targets %d, want the scanned enemy %d", head.Target, enemy.Handle)
				}
			} else if leg.Deadline == -1 {
				t.Fatal("the no-target arm did not arm its 30 + RNG(30) deadline [04 R-ORD-01 §4]")
			}
		})
	}
}
