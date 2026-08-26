package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestSnapshotQueueOfPreservesPrimarySecondaryAndPayload(t *testing.T) {
	owner := pool.Handle(7)
	move := Lookup("Move_Ground")
	build := Lookup("BuildingBuild")
	q := NewQueueWith([]*Node{
		{ID: move, Owner: owner, Target: 11, GoalX: 12, GoalY: 13, GoalZ: 14,
			CreationTick: 55, Flags: FlagActive | FlagPurgeSurvivor, MoveState: MoveEnRoute,
			PathStatus: 0x100, Param1: 2, Param2: 3, Param3: 4},
		{ID: move, Owner: owner, GoalX: 20, CreationTick: 56},
	}, []*Node{
		{ID: build, Owner: owner, BuildDefKey: "armpeewee", Param1: 9, Param2: 5,
			Phase: 4, DynamicGate: 0x22, Deadline: 99, Satisfied: 0x80},
	})

	got := SnapshotQueueOf(q, owner, func(n *Node) []SnapshotRoutePoint {
		if n == q.primary[0] {
			return []SnapshotRoutePoint{{X: 100, Y: 101, Z: 102, Flags: 3}}
		}
		return nil
	})
	if got.Unit != owner || len(got.Primary) != 2 || len(got.Secondary) != 1 {
		t.Fatalf("queue shape = unit %d primary %d secondary %d", got.Unit, len(got.Primary), len(got.Secondary))
	}
	p := got.Primary[0]
	if p.List != 0 || p.Index != 0 || p.Kind != "Move_Ground" || p.State == "" || p.Target != 11 || p.GoalY != 13 || p.CreationTick != 55 || p.Flags != FlagActive|FlagPurgeSurvivor || p.MoveState != MoveEnRoute || p.PathStatus != 0x100 {
		t.Fatalf("primary payload = %+v", p)
	}
	if len(p.Route) != 1 || p.Route[0].X != 100 || p.Route[0].Flags != 3 {
		t.Fatalf("primary route = %+v", p.Route)
	}
	s := got.Secondary[0]
	if s.List != 1 || s.Index != 0 || s.Kind != "BuildingBuild" || s.BuildProduct != "armpeewee" || s.BuildCount != 5 || s.Param1 != 9 || s.Param2 != 5 || s.Phase != 4 || s.DynamicGate != 0x22 || s.Deadline != 99 || s.Satisfied != 0x80 {
		t.Fatalf("secondary payload = %+v", s)
	}
}

func TestSnapshotQueueOfCopiesRoutesAndBounds(t *testing.T) {
	owner := pool.Handle(3)
	q := NewQueueWith([]*Node{{ID: Lookup("Move_Ground"), Owner: owner}}, nil)
	route := make([]SnapshotRoutePoint, MaxSnapshotRoutePoints+1)
	for i := range route {
		route[i] = SnapshotRoutePoint{X: numeric.Fixed(i), Z: numeric.Fixed(i * 2)}
	}
	got := SnapshotQueueOf(q, owner, func(*Node) []SnapshotRoutePoint { return route })
	if len(got.Primary) != 1 || !got.Primary[0].RouteTruncated || len(got.Primary[0].Route) != MaxSnapshotRoutePoints {
		t.Fatalf("route bound = len %d truncated %v", len(got.Primary[0].Route), got.Primary[0].RouteTruncated)
	}
	if got.Primary[0].Route[0].X != 0 || got.Primary[0].Route[MaxSnapshotRoutePoints-1].Z != numeric.Fixed((MaxSnapshotRoutePoints-1)*2) {
		t.Fatalf("route values = first %+v last %+v", got.Primary[0].Route[0], got.Primary[0].Route[len(got.Primary[0].Route)-1])
	}
	route[0].X = 999
	if got.Primary[0].Route[0].X == 999 {
		t.Fatal("snapshot route aliases provider storage")
	}

	tooMany := make([]*Node, MaxSnapshotOrdersPerList+1)
	for i := range tooMany {
		tooMany[i] = &Node{ID: Lookup("Move_Ground"), Owner: owner, CreationTick: uint32(i)}
	}
	bounded := SnapshotQueueOf(NewQueueWith(tooMany, nil), owner, nil)
	if len(bounded.Primary) != MaxSnapshotOrdersPerList || !bounded.PrimaryTruncated {
		t.Fatalf("order bound = len %d truncated %v", len(bounded.Primary), bounded.PrimaryTruncated)
	}
	if bounded.Primary[len(bounded.Primary)-1].CreationTick != MaxSnapshotOrdersPerList-1 {
		t.Fatalf("order bound changed producer order: last tick %d", bounded.Primary[len(bounded.Primary)-1].CreationTick)
	}
}

func TestSnapshotQueueOfRetainsCoalescedCount(t *testing.T) {
	owner := pool.Handle(4)
	q := NewQueueWith(nil, []*Node{{ID: Lookup("BuildWeapon"), Owner: owner, Param1: 2, Param2: 7}})
	got := SnapshotQueueOf(q, owner, nil)
	if len(got.Secondary) != 1 || got.Secondary[0].BuildCount != 7 || got.Secondary[0].Param2 != 7 {
		t.Fatalf("coalesced count lost: %+v", got.Secondary)
	}
}

func TestQueueSnapshotOwnsNodeValues(t *testing.T) {
	owner := pool.Handle(12)
	node := &Node{ID: Lookup("Move_Ground"), Owner: owner, CreationTick: 17, Param2: 3}
	q := NewQueueWith([]*Node{node}, nil)
	primary, _ := q.Snapshot()
	if len(primary) != 1 || primary[0] == node {
		t.Fatal("queue snapshot retained authoritative node pointer")
	}
	node.Param2 = 99
	if primary[0].Param2 != 3 {
		t.Fatalf("queue snapshot changed with source node: %d", primary[0].Param2)
	}

	staged := &Node{ID: Lookup("Patrol"), Owner: owner, CreationTick: 22}
	q.RestoreSnapshot([]*Node{staged}, nil)
	staged.CreationTick = 88
	if got := q.Primary()[0].CreationTick; got != 22 {
		t.Fatalf("restore retained caller node pointer: %d", got)
	}
}
