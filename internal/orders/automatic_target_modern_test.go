package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func automaticTargetFixture(modern bool) (*Queue, *units.Unit, *units.Unit, *units.Unit, *pool.Handle, *int) {
	q, u, factory := dangerFixture(modern)
	u.Flags = u.Flags&^(uint32(3)<<units.StandingFireShift) | 2<<units.StandingFireShift
	tower := *factory
	tower.Handle = 3
	old := q.Binding().Lookup
	q.Binding().Lookup = func(h pool.Handle) *units.Unit {
		if h == tower.Handle {
			return &tower
		}
		return old(h)
	}
	chosen, calls := factory.Handle, 0
	q.Binding().Weapons = &WeaponAdapter{CanEngage: func(*units.Unit, pool.Handle, int) bool { return true }, Acquire: func(_ *units.Unit, _ int, limit uint32) (pool.Handle, bool) {
		if limit != 0 {
			panic("automatic query must use authored range")
		}
		calls++
		return chosen, true
	}}
	assignSlot(u, 0, 0x02, units.Target{Kind: units.TargetUnit, Unit: factory.Handle})
	return q, u, factory, &tower, &chosen, &calls
}

func TestModernAutomaticOrderReconsidersOwnedTarget(t *testing.T) {
	for _, name := range []string{"Guard_NoMove", "Attack_Chase"} {
		t.Run(name, func(t *testing.T) {
			q, u, factory, tower, chosen, calls := automaticTargetFixture(true)
			scripted, vm := cbUnit(cbProgram("TargetCleared"))
			u.ScriptState = scripted.ScriptState
			assignment := q.PushHead(Lookup("Patrol"), Node{Owner: u.Handle})
			var n *Node
			if name == "Attack_Chase" {
				u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
				if !autoEngage(u, factory, false) {
					t.Fatal("automatic producer refused")
				}
				n = q.primary[0]
				if !n.automaticAttack {
					t.Fatal("automatic producer missing provenance")
				}
			} else {
				n = q.PushHead(Lookup(name), Node{Owner: u.Handle, Target: factory.Handle})
			}
			n.BindTarget(factory.Handle)
			n.Phase = 2
			u.Slots[0].Aim.IssueBit = true
			beforeAim := u.Slots[0].Aim
			beforeNode := *n
			beforeQueue := append([]*Node(nil), q.primary...)
			random := *q.Binding().SimRNG
			StepDangerResponse(u, 1)
			beforeNode.nextAutomaticTargetTick = n.nextAutomaticTargetTick
			if !reflect.DeepEqual(*n, beforeNode) || u.Slots[0].Aim != beforeAim {
				t.Fatal("same target disturbed phase or aim")
			}
			*chosen = tower.Handle
			StepDangerResponse(u, 30)
			if n.Target != factory.Handle {
				t.Fatal("reconsidered before interval")
			}
			StepDangerResponse(u, 31)
			if n.Target != tower.Handle || u.Slots[0].Target.Unit != tower.Handle || *calls != 2 {
				t.Fatal("owned slot did not follow combat priority")
			}
			if !reflect.DeepEqual(q.primary, beforeQueue) || q.primary[len(q.primary)-1] != assignment || n.Param3 != beforeNode.Param3 || n.GuardX != beforeNode.GuardX || n.GuardY != beforeNode.GuardY {
				t.Fatal("retarget changed assignment, queue identity or leash")
			}
			if u.Slots[0].Aim != beforeAim || len(startedArgs(vm)) != 0 || *q.Binding().SimRNG != random {
				t.Fatal("selection cancelled aim or drew RNG")
			}
		})
	}
}

func TestModernAutomaticTargetBypassesExplicitStrictAndControl(t *testing.T) {
	for _, kind := range []string{"explicit", "strict", "return fire", "control", "stunned", "air"} {
		t.Run(kind, func(t *testing.T) {
			q, u, factory, tower, chosen, calls := automaticTargetFixture(kind != "strict")
			if !autoEngage(u, factory, false) {
				t.Fatal("producer refused")
			}
			n := q.primary[0]
			n.BindTarget(factory.Handle)
			n.Phase = 2
			switch kind {
			case "explicit":
				n.automaticAttack = false
			case "strict":
				if n.automaticAttack {
					t.Fatal("Strict wrote Modern provenance")
				}
			case "return fire":
				u.Flags = u.Flags&^(uint32(3)<<units.StandingFireShift) | 1<<units.StandingFireShift
			case "control":
				q.PushHead(Lookup("Paralyze"), Node{Owner: u.Handle})
			case "stunned":
				u.Stunned = true
			case "air":
				n.ID = Lookup("AirStrike")
			}
			*chosen = tower.Handle
			before := *n
			StepDangerResponse(u, 1)
			if *calls != 0 || !reflect.DeepEqual(*n, before) {
				t.Fatal("protected order acquired an opportunity")
			}
		})
	}
}

func TestModernAutomaticTargetKeepsManeuverLeash(t *testing.T) {
	q, u, factory, tower, chosen, _ := automaticTargetFixture(true)
	u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
	if !autoEngage(u, factory, false) {
		t.Fatal("producer refused")
	}
	n := q.primary[0]
	n.Phase = 1
	tower.X = u.X + 300<<16
	*chosen = tower.Handle
	StepDangerResponse(u, 1)
	if n.Target != factory.Handle {
		t.Fatal("opportunity escaped maneuver leash")
	}
}

func TestModernAutomaticHandoffPreservesAimWithoutTargetCleared(t *testing.T) {
	for _, kind := range []string{"danger", "producer", "guard", "explicit", "strict", "other slot", "control"} {
		t.Run(kind, func(t *testing.T) {
			q, u, factory, _, _, _ := automaticTargetFixture(kind != "strict")
			scripted, vm := cbUnit(cbProgram("TargetCleared"))
			u.ScriptState = scripted.ScriptState
			n := q.PushHead(Lookup("Attack_NoMove"), Node{Owner: u.Handle, Target: factory.Handle, Phase: 1})
			switch kind {
			case "danger", "strict":
				q.danger.response = n
			case "producer":
				n.automaticAttack = true
			case "guard":
				n.ID = Lookup("Guard_NoMove")
			case "control":
				q.danger.response = n
				q.PushHead(Lookup("Paralyze"), Node{Owner: u.Handle})
			}
			slot := 0
			if kind == "other slot" {
				slot = 2
				q.danger.response = n
				assignSlot(u, slot, 0x02, units.Target{Kind: units.TargetUnit, Unit: factory.Handle})
			}
			s := u.SlotAt(slot)
			s.Flags |= units.SlotFlagAutonomous
			s.Aim.IssueBit = true
			before := s.Aim
			if kind == "danger" || kind == "producer" || kind == "explicit" || kind == "strict" {
				attackNoMoveHandler(u, n, 0, 1)
			} else {
				releaseSlot(u, slot)
			}
			preserve := kind == "danger" || kind == "producer" || kind == "guard"
			if got := len(startedArgs(vm)); preserve && got != 0 || !preserve && got != 1 {
				t.Fatalf("TargetCleared callbacks = %d, preserve=%v", got, preserve)
			}
			if s.Aim != before || s.Flags&units.SlotFlagAutonomous != 0 {
				t.Fatal("handoff lost aim or retained autonomy")
			}
		})
	}
}

func TestModernStationaryGuardScanUsesCombatWithoutRandomChoice(t *testing.T) {
	for _, fire := range []uint32{1, 2} {
		q, u, factory, tower, chosen, calls := automaticTargetFixture(true)
		u.Flags = u.Flags&^(uint32(3)<<units.StandingFireShift) | fire<<units.StandingFireShift
		n := q.PushHead(Lookup("Guard_NoMove"), Node{Owner: u.Handle, Target: factory.Handle, Phase: 3})
		n.BindTarget(factory.Handle)
		random := *q.Binding().SimRNG
		*chosen = tower.Handle
		if code := guardNoMoveHandler(u, n, 0, 1); code != 2 {
			t.Fatalf("guard result=%v", code)
		}
		if fire == 1 && (*calls != 0 || n.Target != factory.Handle) || fire == 2 && (*calls != 1 || n.Target != tower.Handle) {
			t.Fatal("guard scan ignored fire stance or combat selection")
		}
		if *q.Binding().SimRNG != random {
			t.Fatal("Modern target choice drew RNG")
		}
	}
}

func TestModernGuardRetainedTargetDoesNotRestartAndCancelAim(t *testing.T) {
	q, u, factory, _, _, _ := automaticTargetFixture(true)
	scripted, vm := cbUnit(cbProgram("TargetCleared"))
	u.ScriptState = scripted.ScriptState
	n := q.PushHead(Lookup("Guard_NoMove"), Node{Owner: u.Handle, Phase: 2})
	n.BindTarget(factory.Handle)
	u.Slots[0].Aim.IssueBit = true
	aim := u.Slots[0].Aim
	random := *q.Binding().SimRNG
	if code := guardNoMoveHandler(u, n, 0, 1); code != 2 || n.Phase != 2 {
		t.Fatal("retained guard restarted instead of keeping phase")
	}
	if u.Slots[0].Aim != aim || len(startedArgs(vm)) != 0 || *q.Binding().SimRNG != random {
		t.Fatal("retained guard cancelled aim or chose a random restart")
	}
}

func TestStrictStationaryGuardKeepsRegistryScan(t *testing.T) {
	q, u, factory, tower, _, calls := automaticTargetFixture(false)
	q.Binding().Weapons.TargetsInRadius = func(*units.Unit, numeric.Fixed, numeric.Fixed, int32) []pool.Handle {
		return []pool.Handle{tower.Handle}
	}
	n := q.PushHead(Lookup("Guard_NoMove"), Node{Owner: u.Handle, Phase: 3})
	n.BindTarget(factory.Handle)
	if code := guardNoMoveHandler(u, n, 0, 1); code != 2 || n.Target != tower.Handle || n.Phase != 1 || *calls != 0 {
		t.Fatal("Strict guard replaced registry selection with Modern acquisition")
	}
}

func TestModernDangerRetargetKeepsCandidateInsideManeuverLeash(t *testing.T) {
	for _, tc := range []struct {
		name     string
		distance int64
		change   bool
	}{
		{"inside", 255, true}, {"boundary", 256, false}, {"outside", 300, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, u, attacker, tower, chosen, _ := automaticTargetFixture(true)
			u.Flags = u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
			tower.X, tower.Z = u.X+numeric.FixedFromInt(tc.distance), u.Z
			n := q.PushHead(Lookup("Attack_Chase"), Node{Owner: u.Handle, Target: attacker.Handle, Phase: 2, Param3: 256, GuardX: 512, GuardY: 512})
			q.danger.response = n
			ObserveDanger(u, attacker, 10)
			*chosen = tower.Handle
			StepDangerResponse(u, 10)
			if (n.Target == tower.Handle) != tc.change {
				t.Fatal("danger retarget crossed the original maneuver boundary")
			}
			if q.Head() != n || n.Param3 != 256 || n.GuardX != 512 || n.GuardY != 512 {
				t.Fatal("retarget replaced the chase or its anchor")
			}
		})
	}
}
