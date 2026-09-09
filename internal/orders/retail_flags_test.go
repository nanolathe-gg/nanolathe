package orders

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func retailRecordForNode(t *testing.T, node *Node) (save.OrderRecord, uint32) {
	t.Helper()
	owner := &units.Unit{Handle: 100, Alive: true}
	BindQueue(owner, NewQueueWith([]*Node{node}, nil))
	images, err := RetailOrderImages(owner, func(h pool.Handle) (uint16, bool) {
		if h == owner.Handle {
			return 9, true
		}
		return 0, false
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("image count = %d, want 1", len(images))
	}
	word := binary.LittleEndian.Uint32(images[0].Main[0x32:])
	return save.OrderRecord{ParentStableID: 9, Main: images[0].Main, DescriptorName: images[0].DescriptorName}, word
}

func restoreSingleRetailNode(t *testing.T, node *Node) (*units.Unit, *Node, uint32) {
	t.Helper()
	record, word := retailRecordForNode(t, node)
	restoredOwner := &units.Unit{Handle: 200, Alive: true}
	if err := RetailRestoreOrders(restoredOwner, []save.OrderRecord{record}, map[uint16]pool.Handle{9: restoredOwner.Handle}, nil); err != nil {
		t.Fatal(err)
	}
	return restoredOwner, QueueOfUnit(restoredOwner).Head(), word
}

func TestRetailQueueFlagsUsesCanonicalRuntimeBits(t *testing.T) {
	static := DescriptorFor(Lookup("Attack_Chase")).StaticGate &^ staticTargetObserver
	n := &Node{
		StaticGate:     static,
		Flags:          FlagActive | FlagAutoOp | FlagTombstone | FlagRetryMark | FlagStopBuildingPending,
		CaptionPending: true,
	}
	word := retailQueueFlags(n)
	wantRuntime := retailFlagActive | retailFlagCaptionPending | retailFlagAutoOp | retailFlagTombstone | retailFlagRetryMark | retailFlagStopBuildingPending
	if got := word & retailLocalFlagWireMask; got != wantRuntime {
		t.Fatalf("runtime wire flags = %#x, want %#x", got, wantRuntime)
	}
	gotStatic, gotFlags, gotCaption := restoreRetailQueueFlags(word)
	if gotStatic != static {
		t.Fatalf("static gate = %#x, want retained target-observer clear %#x", gotStatic, static)
	}
	wantFlags := n.Flags
	if gotFlags != wantFlags {
		t.Fatalf("local flags = %#x, want %#x", gotFlags, wantFlags)
	}
	if !gotCaption {
		t.Fatal("caption-pending flag was not restored")
	}
}

func TestRetailOrderFlagRoundTripPreservesReplaceAndCallbackState(t *testing.T) {
	moveID := Lookup("Move_Ground")
	getBuiltID := Lookup("GetBuilt")
	patrolID := Lookup("Patrol")
	attackID := Lookup("Attack_Chase")
	if moveID == 0 || getBuiltID == 0 || patrolID == 0 || attackID == 0 {
		t.Fatal("required descriptors unavailable")
	}
	cases := []struct {
		name  string
		node  Node
		check func(*testing.T, *units.Unit, *Node)
	}{
		{
			name: "move is removed by replacement",
			node: Node{ID: moveID, Owner: 100, StaticGate: DescriptorFor(moveID).StaticGate, Flags: FlagActive, Param1: 1},
			check: func(t *testing.T, u *units.Unit, _ *Node) {
				q := QueueOfUnit(u)
				q.PurgeUnprotected()
				q.Push(moveID, Node{Owner: u.Handle, Param1: 2})
				if q.LenPrimary() != 1 || q.Head().Param1 != 2 {
					t.Fatalf("replacement queue = %#v, want only the new Move", q.Primary())
				}
			},
		},
		{
			name: "protected record survives replacement",
			node: Node{ID: getBuiltID, Owner: 100, StaticGate: DescriptorFor(getBuiltID).StaticGate, Flags: FlagActive},
			check: func(t *testing.T, u *units.Unit, restored *Node) {
				if restored.Flags&FlagPurgeSurvivor == 0 {
					t.Fatal("GetBuilt lost its static purge-survivor property")
				}
				QueueOfUnit(u).PurgeUnprotected()
				if QueueOfUnit(u).Head() != restored {
					t.Fatal("replacement purge removed protected GetBuilt record")
				}
			},
		},
		{
			name: "patrol does not acquire StopBuilding",
			node: Node{ID: patrolID, Owner: 100, StaticGate: DescriptorFor(patrolID).StaticGate, Flags: FlagActive},
			check: func(t *testing.T, _ *units.Unit, restored *Node) {
				if restored.Flags&FlagStopBuildingPending != 0 {
					t.Fatal("Patrol acquired a StopBuilding callback obligation from its descriptor mask")
				}
			},
		},
		{
			name: "mutated target observer mask stays cleared",
			node: Node{ID: attackID, Owner: 100, StaticGate: DescriptorFor(attackID).StaticGate &^ staticTargetObserver, Flags: FlagActive},
			check: func(t *testing.T, _ *units.Unit, restored *Node) {
				if restored.StaticGate&staticTargetObserver != 0 {
					t.Fatal("restore regenerated the cleared target-observer static bit")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, restored, _ := restoreSingleRetailNode(t, &tc.node)
			tc.check(t, u, restored)
		})
	}
}

func TestRetailOrderFlagRoundTripKeepsOneShotCallbacks(t *testing.T) {
	moveID := Lookup("Move_Ground")
	patrolID := Lookup("Patrol")
	if moveID == 0 || patrolID == 0 {
		t.Fatal("required descriptors unavailable")
	}

	t.Run("caption is consumed once and stays consumed across another save", func(t *testing.T) {
		record, _ := retailRecordForNode(t, &Node{
			ID: moveID, Owner: 100, StaticGate: DescriptorFor(moveID).StaticGate,
			Flags: FlagActive | FlagStopBuildingPending, CaptionPending: true,
		})
		firstQueue, firstOwner, status := arrivedFixture(nil)
		if err := RetailRestoreOrders(firstOwner, []save.OrderRecord{record}, map[uint16]pool.Handle{9: firstOwner.Handle}, firstQueue.Binding()); err != nil {
			t.Fatal(err)
		}
		first := QueueOfUnit(firstOwner).Head()
		if !first.CaptionPending || first.Flags&FlagStopBuildingPending == 0 {
			t.Fatalf("first restore lost pending state: caption=%v flags=%#x", first.CaptionPending, first.Flags)
		}
		NotifyCaptionClear(firstOwner, first, "")
		NotifyCaptionClear(firstOwner, first, "")
		if got := status.count(statusOK); got != 1 || first.CaptionPending {
			t.Fatalf("caption clears=%d pending=%v, want one clear and consumed state", got, first.CaptionPending)
		}

		images, err := RetailOrderImages(firstOwner, func(h pool.Handle) (uint16, bool) {
			if h == firstOwner.Handle {
				return 9, true
			}
			return 0, false
		})
		if err != nil {
			t.Fatal(err)
		}
		secondRecord := save.OrderRecord{ParentStableID: 9, Main: images[0].Main, DescriptorName: images[0].DescriptorName}
		cleanup := newCleanupCase(t)
		if err := RetailRestoreOrders(cleanup.u, []save.OrderRecord{secondRecord}, map[uint16]pool.Handle{9: cleanup.u.Handle}, cleanup.q.Binding()); err != nil {
			t.Fatal(err)
		}
		secondQueue := QueueOfUnit(cleanup.u)
		second := secondQueue.Head()
		if second.CaptionPending || second.Flags&FlagStopBuildingPending == 0 {
			t.Fatalf("second restore rearmed caption or lost callback: caption=%v flags=%#x", second.CaptionPending, second.Flags)
		}
		secondQueue.RemoveHead()
		if got := stopBuildingCount(startedArgs(cleanup.vm)); got != 1 {
			t.Fatalf("StopBuilding callbacks=%d, want exactly one after restored removal", got)
		}
	})

	t.Run("Patrol removal remains free of StopBuilding", func(t *testing.T) {
		record, _ := retailRecordForNode(t, &Node{ID: patrolID, Owner: 100, StaticGate: DescriptorFor(patrolID).StaticGate, Flags: FlagActive})
		cleanup := newCleanupCase(t)
		if err := RetailRestoreOrders(cleanup.u, []save.OrderRecord{record}, map[uint16]pool.Handle{9: cleanup.u.Handle}, cleanup.q.Binding()); err != nil {
			t.Fatal(err)
		}
		QueueOfUnit(cleanup.u).RemoveHead()
		if got := stopBuildingCount(startedArgs(cleanup.vm)); got != 0 {
			t.Fatalf("Patrol removal arranged %d StopBuilding callbacks, want none", got)
		}
	})
}
