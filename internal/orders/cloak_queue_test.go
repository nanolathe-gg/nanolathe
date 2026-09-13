package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Established: cloak records run at the head and complete on one visit without
// changing the displaced mission [04 R-ORD-01 §2, §13]. In particular, the
// waiting mission's dynamic gate must neither delay the toggle nor be consumed
// by it [04 §3.3], and cleanup must preserve another record's movement payload
// and weapon targets [04 R-ORD-01 §9][04 R-UNIT-06 §5].
func TestCloakTogglePreservesGatedMission(t *testing.T) {
	for _, mission := range []struct {
		name  string
		phase uint8
		gate  uint32
	}{
		{"Move_Ground", 1, 0xe0},
		{"MobileBuild", 2, 0xe},
	} {
		for _, producer := range []bool{false, true} {
			insertion := "head"
			if producer {
				insertion = "producer"
			}
			t.Run(mission.name+"/"+insertion, func(t *testing.T) {
				q, u := standingFixture(&content.UnitDef{BMCode: 1, CloakCost: 200})
				q.binding.Lookup = func(h pool.Handle) *units.Unit {
					if h == u.Handle {
						return u
					}
					return nil
				}
				q.Push(Lookup(mission.name), Node{Owner: u.Handle})
				running := q.Primary()[0]
				running.Phase, running.DynamicGate = mission.phase, mission.gate
				running.Deadline = -1
				running.Param2 = 3
				running.Flags |= FlagStopBuildingPending
				q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, QueuedIssue: true})
				next := q.Primary()[1]
				marker := next.Flags & FlagActive
				target := units.Target{Kind: units.TargetUnit, Unit: 2}
				u.SlotAt(0).Target = target
				q.binding.Movement = &MovementGoalAdapter{Release: func(n *Node) bool {
					if n == running || n == next {
						t.Fatal("cloak completion released a mission record")
					}
					return false // the cloak record owns no movement object
				}}
				for i, name := range []string{"Cloak_On", "Cloak_Off"} {
					id := Lookup(name)
					n := NewNodeForOrder(id, 0, 0, 0, 0, 100+uint32(i), u.Handle, false)
					if producer {
						q.Push(id, n)
					} else {
						q.PushHead(id, n)
					}
					q.Pump(u, 100+uint32(i))
					if u.IsCloaked != (i == 0) {
						t.Fatalf("%s did not execute ahead of the gated mission", name)
					}
					if q.LenPrimary() != 2 || q.Primary()[0] != running || q.Primary()[1] != next {
						t.Fatalf("%s changed the mission queue", name)
					}
					if running.Phase != mission.phase || running.DynamicGate != mission.gate || running.Deadline != -1 || running.Param2 != 3 || running.Flags&FlagStopBuildingPending == 0 {
						t.Fatalf("%s changed the waiting mission state: %+v", name, running)
					}
					if next.Flags&FlagActive != marker || u.SlotAt(0).Target != target {
						t.Fatalf("%s changed the queue marker or weapon target", name)
					}
				}
			})
		}
	}
}
