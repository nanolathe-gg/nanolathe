package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestImpactWithdrawsWithoutAttackerKnowledge(t *testing.T) {
	for _, move := range []uint32{0, 1, 2} {
		q, u, _ := dangerFixture(true)
		u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | move<<units.StandingMoveShift
		q.primary = []*Node{{ID: Lookup("Standby"), Owner: u.Handle}}
		q.Binding().Lookup = func(pool.Handle) *units.Unit { t.Fatal("anonymous impact looked up a unit"); return nil }
		q.Binding().DangerVisible = func(*units.Unit, *units.Unit) bool {
			t.Fatal("anonymous impact read hidden contact state")
			return false
		}
		q.Binding().DangerCanRespond = func(*units.Unit, *units.Unit, int) bool {
			t.Fatal("anonymous impact sought an attack target")
			return false
		}
		q.Binding().Weapons = &WeaponAdapter{Acquire: func(*units.Unit, int, uint32) (pool.Handle, bool) {
			t.Fatal("anonymous impact acquired a target")
			return 0, false
		}}
		random := *q.Binding().SimRNG
		ObserveImpact(u, numeric.Angle(16384), 10) // +X side
		StepDangerResponse(u, 10)
		if *q.Binding().SimRNG != random {
			t.Fatal("impact policy drew RNG")
		}
		if move == 0 {
			if q.danger.response != nil {
				t.Fatal("Hold Position withdrew")
			}
			continue
		}
		n := q.danger.response
		if n == nil || n.ID != Lookup("Move_Ground") || n.Target != 0 || n.GoalX >= u.X {
			t.Fatal("hidden hit did not withdraw away from its observed side")
		}
		installed := false
		q.Binding().Movement = &MovementGoalAdapter{Release: func(*Node) bool { return true }, InstallPoint: func(req PointGoalRequest) bool {
			installed = req.Node == n && req.X == n.GoalX && req.Z == n.GoalZ
			return true
		}}
		q.Pump(u, 10)
		if !installed {
			t.Fatal("withdrawal did not install ordinary movement payload")
		}
	}
}

func TestImpactPreservesWorkControlsAndStrict(t *testing.T) {
	for _, kind := range []string{"construction", "manual repair", "control", "stunned", "carrier", "explicit move", "strict"} {
		t.Run(kind, func(t *testing.T) {
			q, u, _ := dangerFixture(kind != "strict")
			n := &Node{Owner: u.Handle, ID: Lookup("Patrol"), Phase: 1}
			switch kind {
			case "construction":
				n.ID = Lookup("HelpBuild")
			case "manual repair":
				n.ID = Lookup("RepairUnit")
				n.Flags = FlagAutoOp
			case "control":
				n.ID = Lookup("Paralyze")
			case "stunned":
				u.Stunned = true
			case "carrier":
				u.Attachment.Carrier = 9
			case "explicit move":
				n.ID = Lookup("Move_Ground")
				n.MoveState = MoveEnRoute
			}
			q.primary = []*Node{n}
			beforeNode, beforeUnit, random := *n, *u, *q.Binding().SimRNG
			ObserveImpact(u, 0, 10)
			StepDangerResponse(u, 10)
			if len(q.primary) != 1 || q.primary[0] != n || !reflect.DeepEqual(*n, beforeNode) || !reflect.DeepEqual(*u, beforeUnit) || *q.Binding().SimRNG != random {
				t.Fatal("impact interrupted protected assignment/control or changed unit/RNG")
			}
			if kind == "strict" && q.danger != (dangerState{}) {
				t.Fatal("Strict wrote impact memory")
			}
		})
	}
}

func TestImpactMemoryRefreshBoundAndFrozenPoints(t *testing.T) {
	q, u, hidden := dangerFixture(true)
	ObserveImpact(u, 0, 1)
	first := q.danger.impacts[0]
	if first.x != u.X || first.z != u.Z+numeric.FixedFromInt(256) {
		t.Fatal("world bearing zero did not place hazard toward +Z")
	}
	u.X += numeric.FixedFromInt(32)
	ObserveImpact(u, 0, 2)
	if q.danger.impacts[0].x != u.X || q.danger.impacts[0].tick != 2 {
		t.Fatal("repeated impact failed to refresh local point")
	}
	for i := 1; i < 5; i++ {
		ObserveImpact(u, numeric.Angle(i*8192), uint32(i+2))
	}
	// Fifth distinct sector evicts the oldest point, independent of hidden state.
	if q.danger.impacts[0].sector != 4 {
		t.Fatal("impact bound did not evict oldest sector")
	}
	before := q.danger.impacts
	hidden.Alive = false
	hidden.X += numeric.FixedFromInt(1000)
	u.X += numeric.FixedFromInt(32)
	u.Stunned = true
	StepDangerResponse(u, 7)
	if q.danger.impacts != before {
		t.Fatal("point tracked later victim/hidden-unit motion or hidden death")
	}
	StepDangerResponse(u, 186)
	if q.danger.impacts != ([4]dangerImpact{}) {
		t.Fatal("stun prevented anonymous danger expiry")
	}
}

func TestImpactSuspendsAutomaticRepairAndExpiresToAssignment(t *testing.T) {
	for _, move := range []uint32{1, 2} {
		q, u, _ := dangerFixture(true)
		u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | move<<units.StandingMoveShift
		patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 2}
		q.primary = []*Node{{ID: Lookup("RepairUnit"), Owner: u.Handle, automaticWork: true}, patrol}
		anchorX, anchorZ := u.X, u.Z
		ObserveImpact(u, 16384, 10)
		if rulesOfUnit(u).AllowAutomaticRepair(u, 10) {
			t.Fatal("impact did not inhibit automatic repair")
		}
		StepDangerResponse(u, 10)
		n := q.danger.response
		if n == nil || len(q.primary) != 2 || q.primary[1] != patrol {
			t.Fatal("impact did not suspend automatic repair with original assignment")
		}
		u.X, u.Z = n.GoalX, n.GoalZ
		StepDangerResponse(u, 189)
		if q.danger.response != n {
			t.Fatal("impact expired before TTL")
		}
		StepDangerResponse(u, 190)
		if q.danger.response != nil || q.danger.impacts != ([4]dangerImpact{}) || patrol.Phase != 0 || !rulesOfUnit(u).AllowAutomaticRepair(u, 190) {
			t.Fatal("impact expiry failed to resume assignment")
		}
		if move == 1 {
			if q.Head() != q.danger.returnMove || q.Head().GoalX != anchorX || q.Head().GoalZ != anchorZ {
				t.Fatal("maneuver did not return to original anchor")
			}
		} else if q.Head() != patrol {
			t.Fatal("roam failed to resume patrol")
		}
	}
}

func TestImpactVisibleContactRespondsFirstAndCommandsClearMemory(t *testing.T) {
	q, u, attacker := dangerFixture(true)
	ObserveImpact(u, 0, 10)
	ObserveDanger(u, attacker, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.Target != attacker.Handle {
		t.Fatal("anonymous point displaced suitable observed response")
	}
	before := q.danger
	q.Binding().Rules = StrictRules{}
	ObserveImpact(u, 16384, 1000)
	StepDangerResponse(u, 1000)
	if q.danger != before {
		t.Fatal("Strict switch mutated Modern impact state")
	}
	q.Binding().Rules = &ModernRules{}
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, GoalX: u.X + numeric.FixedFromInt(10), GoalSupplied: true})
	if q.danger != (dangerState{}) {
		t.Fatal("new command retained anonymous danger")
	}
}
