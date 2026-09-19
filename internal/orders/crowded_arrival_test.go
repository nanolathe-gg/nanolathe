package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func crowdedOrderFixture(modern bool) (*Queue, *units.Unit, *Node) {
	q, u, _ := dangerFixture(modern)
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, GoalX: u.X + numeric.FixedFromInt(48), GoalZ: u.Z, GoalSupplied: true})
	q.Binding().Movement = &MovementGoalAdapter{CrowdedMoveBlocked: func(*units.Unit, *Node) (int32, int32, bool) { return 30, 30, true }}
	return q, u, q.Head()
}

func TestCrowdedArrivalDwellAndStrictBypass(t *testing.T) {
	for _, modern := range []bool{false, true} {
		q, u, n := crowdedOrderFixture(modern)
		before, random := *n, *q.Binding().SimRNG
		for tick := uint32(100); tick <= 190; tick++ {
			if got := CrowdedMoveArrival(u, n, tick); got != (modern && tick == 190) {
				t.Fatalf("modern=%v tick=%d arrival=%v", modern, tick, got)
			}
		}
		if *q.Binding().SimRNG != random {
			t.Fatal("dwell drew RNG")
		}
		if !modern && !reflect.DeepEqual(before, *n) {
			t.Fatal("Strict wrote node")
		}
	}
}

func TestCrowdedArrivalExcludesOtherAssignments(t *testing.T) {
	for _, kind := range []string{"far", "extreme", "moving", "build behind", "repair behind", "attack", "automatic work", "danger", "return", "paralyze", "carried", "incomplete", "air"} {
		t.Run(kind, func(t *testing.T) {
			q, u, n := crowdedOrderFixture(true)
			switch kind {
			case "far":
				n.GoalX = u.X + numeric.FixedFromInt(97)
			case "extreme":
				n.GoalX = numeric.Fixed(-1 << 63)
				u.X = numeric.Fixed(1<<63 - 1)
			case "moving":
				u.Move.Speed = 1
			case "build behind":
				q.Push(Lookup("MobileBuild"), Node{})
			case "repair behind":
				q.Push(Lookup("RepairUnit"), Node{})
			case "attack":
				n.ID = Lookup("Attack_Chase")
			case "automatic work":
				n.automaticWork = true
			case "danger":
				q.danger.response = n
			case "return":
				q.danger.returnMove = n
			case "paralyze":
				q.PushHead(Lookup("Paralyze"), Node{})
			case "carried":
				u.Attachment.Carrier = 2
			case "incomplete":
				u.Remaining = 1
			case "air":
				u.Def.CanFly = true
			}
			for tick := uint32(1); tick <= 100; tick++ {
				if CrowdedMoveArrival(u, n, tick) {
					t.Fatal("completed protected/remote assignment")
				}
			}
		})
	}
}

func TestCrowdedArrivalRestartsAfterProgressOrUnobservedInterval(t *testing.T) {
	for _, change := range []string{"anchor", "goal", "crowd clears", "mode gap"} {
		t.Run(change, func(t *testing.T) {
			q, u, n := crowdedOrderFixture(true)
			for tick := uint32(1); tick < 90; tick++ {
				CrowdedMoveArrival(u, n, tick)
			}
			switch change {
			case "anchor":
				q.Binding().Movement.CrowdedMoveBlocked = func(*units.Unit, *Node) (int32, int32, bool) { return 31, 30, true }
			case "goal":
				n.GoalZ++
			case "crowd clears":
				q.Binding().Movement.CrowdedMoveBlocked = func(*units.Unit, *Node) (int32, int32, bool) { return 30, 30, false }
				CrowdedMoveArrival(u, n, 90)
				q.Binding().Movement.CrowdedMoveBlocked = func(*units.Unit, *Node) (int32, int32, bool) { return 30, 30, true }
			}
			// The missing observation also models returning from Strict/control tasks.
			for tick := uint32(91); tick <= 181; tick++ {
				if got := CrowdedMoveArrival(u, n, tick); got != (tick == 181) {
					t.Fatalf("tick=%d arrival=%v", tick, got)
				}
			}
		})
	}
}

func TestCrowdedArrivalRestoredMoveStartsFreshDwell(t *testing.T) {
	q, u, n := crowdedOrderFixture(true)
	for tick := uint32(1); tick <= 90; tick++ {
		CrowdedMoveArrival(u, n, tick)
	}
	images, err := RetailOrderImagesWithPayload(u, func(h pool.Handle) (uint16, bool) { return uint16(h), h != 0 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary, Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype, DescriptorName: image.DescriptorName, BuildTypeName: image.BuildTypeName}
	}
	if err = RetailRestoreOrdersAtTick(u, records, map[uint16]pool.Handle{1: 1}, q.Binding(), 100); err != nil {
		t.Fatal(err)
	}
	n = QueueOfUnit(u).Head()
	if n == nil || n.crowdedArrival != (crowdedArrivalState{}) {
		t.Fatal("restore retained transient dwell")
	}
	for tick := uint32(100); tick <= 190; tick++ {
		if got := CrowdedMoveArrival(u, n, tick); got != (tick == 190) {
			t.Fatalf("restored tick=%d arrival=%v", tick, got)
		}
	}
}

func TestCrowdedArrivalRadiusBoundary(t *testing.T) {
	for _, offset := range []int64{96, 97} {
		_, u, n := crowdedOrderFixture(true)
		n.GoalX = u.X + numeric.FixedFromInt(offset)
		for tick := uint32(1); tick <= 91; tick++ {
			if got := CrowdedMoveArrival(u, n, tick); got != (offset == 96 && tick == 91) {
				t.Fatalf("offset=%d tick=%d arrival=%v", offset, tick, got)
			}
		}
	}
}
