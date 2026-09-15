package session

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func resourceMoveFixture(t *testing.T) (*Session, pool.Handle) {
	t.Helper()
	s, h := repeatClickFixture(t)
	u := s.Units.Unit(h)
	u.Def.BMCode = 1
	u.Flags &^= units.BuildingClassStatus
	return s, h
}

func enqueueResourceMove(t *testing.T, s *Session, h pool.Handle) uint64 {
	t.Helper()
	sequence, err := s.EnqueueHumanCommandWithSequence(HumanCommand{
		Kind: HumanOrder,
		Order: HumanOrderCommand{Handles: []pool.Handle{h}, Code: 2,
			Position: orders.ResolvePos{X: 100 << 16, Z: 100 << 16}, Queued: true, TrackQueuedMove: true},
	})
	if err != nil || sequence == 0 {
		t.Fatalf("move receipt = %d, error = %v", sequence, err)
	}
	return sequence
}

// This receipt-and-replacement policy belongs to the Enhanced gesture, not
// retail: DESIGN_INTERFACE_HUD_INPUT §3.10. Both arrival timings must preserve
// existing moves/builds, including an older move at the exact same point.
func TestResourceMoveReceiptCancellationAcrossInputBoundaries(t *testing.T) {
	for _, applied := range []bool{false, true} {
		for _, air := range []bool{false, true} {
			t.Run(fmt.Sprintf("applied=%t/air=%t", applied, air), func(t *testing.T) {
				s, h := resourceMoveFixture(t)
				s.Clock = &clock.State{}
				u := s.Units.Unit(h)
				u.Def.CanFly = air
				s.applyHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
					Handles: []pool.Handle{h}, Code: 2, Queued: true,
					Position: orders.ResolvePos{X: 100 << 16, Z: 100 << 16},
				}}, 0)
				q := orders.QueueForUnit(u)
				oldMove := q.Head()
				queueBuild(s, h, "solar", 200<<16, 200<<16, true, 0)
				oldBuild := q.Primary()[1]
				sequence := enqueueResourceMove(t, s, h)
				if applied {
					s.Clock.GlobalTick = 1
					s.applyHumanCommands(1)
					prim := q.Primary()
					if len(prim) != 3 || prim[0] != oldMove || prim[1] != oldBuild || prim[2].HumanMoveSequence != sequence {
						t.Fatalf("first click did not append its own move: %+v", prim)
					}
				}
				if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanCancelQueuedMove,
					CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: sequence, Handles: []pool.Handle{h}},
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
					Builder: h, Product: "mex", WX: 100 << 16, WZ: 100 << 16, Queued: true, AppendOnly: true,
				}}); err != nil {
					t.Fatal(err)
				}
				s.applyHumanCommands(s.Clock.GlobalTick + 1)
				prim := q.Primary()
				if len(prim) != 3 || prim[0] != oldMove || prim[1] != oldBuild || prim[2].BuildDefKey != "mex" {
					t.Fatalf("replacement changed existing work or lost the build: %+v", prim)
				}
				for _, n := range prim {
					if n.HumanMoveSequence == sequence {
						t.Fatal("first click's move survived cancellation")
					}
				}
			})
		}
	}
}

func TestResourceMoveReceiptStaleCancellationLeavesReplacement(t *testing.T) {
	s, h := resourceMoveFixture(t)
	sequence := enqueueResourceMove(t, s, h)
	s.applyHumanCommands(1)
	q := orders.QueueForUnit(s.Units.Unit(h))
	q.RemovePrimaryNode(q.Head(), false) // first move completed or was cancelled
	newSequence := enqueueResourceMove(t, s, h)
	s.applyHumanCommands(2)
	replacement := q.Head()
	for _, receipt := range []uint64{sequence, 0} {
		s.applyHumanCommand(HumanCommand{Kind: HumanCancelQueuedMove,
			CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: receipt, Handles: []pool.Handle{h}},
		}, 3)
	}
	if q.LenPrimary() != 1 || q.Head() != replacement || replacement.HumanMoveSequence != newSequence {
		t.Fatal("stale receipt removed the replacement move at the same point")
	}
}

func TestResourceMoveCancellationUsesImmutableCapturedActors(t *testing.T) {
	s, h := resourceMoveFixture(t)
	u := s.Units.Unit(h)
	other, err := s.Units.Create(u.Def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sequence := enqueueResourceMove(t, s, h)
	s.applyHumanCommands(1)
	q := orders.QueueForUnit(u)
	want := q.Head()
	u.Flags |= 0x10
	// An empty actor set cannot fall back to the current selection.
	s.applyHumanCommand(HumanCommand{Kind: HumanCancelQueuedMove,
		CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: sequence},
	}, 2)
	if q.Head() != want {
		t.Fatal("empty captured actors cancelled the selected unit's move")
	}
	handles := []pool.Handle{h}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanCancelQueuedMove,
		CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: sequence, Handles: handles},
	}); err != nil {
		t.Fatal(err)
	}
	handles[0] = other
	pending := s.PendingHumanCommands()
	pending[0].CancelQueuedMove.Handles[0] = other
	s.applyHumanCommands(3)
	if q.LenPrimary() != 0 {
		t.Fatal("caller or diagnostic snapshot mutated captured cancellation actors")
	}
}

func TestResourceMoveTrackingAdmissionAndOrdinaryToggle(t *testing.T) {
	for _, kind := range []string{"ordinary", "unqueued", "targeted", "other-order"} {
		t.Run(kind, func(t *testing.T) {
			s, h := resourceMoveFixture(t)
			c := HumanOrderCommand{Handles: []pool.Handle{h}, Code: 2, Queued: true, TrackQueuedMove: true,
				Position: orders.ResolvePos{X: 100 << 16, Z: 100 << 16}}
			switch kind {
			case "ordinary":
				c.TrackQueuedMove = false
			case "unqueued":
				c.Queued = false
			case "targeted":
				c.Target = h
			case "other-order":
				c.Code = 9
				s.Units.Unit(h).Def.CanPatrol = true
			}
			for tick := uint32(1); tick <= 2; tick++ {
				if _, err := s.EnqueueHumanCommandWithSequence(HumanCommand{Kind: HumanOrder, Order: c}); err != nil {
					t.Fatal(err)
				}
				s.applyHumanCommands(tick)
				q := orders.QueueForUnit(s.Units.Unit(h))
				if tick == 1 && q.LenPrimary() != 1 {
					t.Fatalf("first command not admitted: %+v", q.Primary())
				}
				for _, n := range q.Primary() {
					if n.HumanMoveSequence != 0 {
						t.Fatal("tracking leaked outside queued targetless moves")
					}
				}
				if tick == 2 && c.Queued && kind != "targeted" && q.LenPrimary() != 0 {
					t.Fatal("ordinary repeat-click toggle was bypassed")
				}
			}
		})
	}
}

func TestResourceMoveCancellationReleasesActiveGoalOnce(t *testing.T) {
	s, h := resourceMoveFixture(t)
	sequence := enqueueResourceMove(t, s, h)
	s.applyHumanCommands(1)
	u := s.Units.Unit(h)
	q := orders.QueueForUnit(u)
	n := q.Head()
	var installed *orders.Node
	var released []*orders.Node
	q.SetBinding(&orders.QueueBinding{Movement: &orders.MovementGoalAdapter{
		InstallPoint: func(req orders.PointGoalRequest) bool { installed = req.Node; return true },
		Release:      func(node *orders.Node) bool { released = append(released, node); return true },
	}})
	q.Pump(u, 1)
	if installed != n {
		t.Fatal("move did not install its active goal")
	}
	c := HumanCommand{Kind: HumanCancelQueuedMove,
		CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: sequence, Handles: []pool.Handle{h}},
	}
	s.applyHumanCommand(c, 2)
	s.applyHumanCommand(c, 3)
	if q.LenPrimary() != 0 || len(released) != 1 || released[0] != n {
		t.Fatalf("active move cleanup: queue=%v, releases=%v", q.Primary(), released)
	}
}

func TestResourceMoveCancellationRejectsForeignDeadAndNonmoveNodes(t *testing.T) {
	for _, kind := range []string{"foreign", "dead", "nonmove", "secondary"} {
		t.Run(kind, func(t *testing.T) {
			s, h := resourceMoveFixture(t)
			sequence := enqueueResourceMove(t, s, h)
			s.applyHumanCommands(1)
			u := s.Units.Unit(h)
			q := orders.QueueForUnit(u)
			n := q.Head()
			switch kind {
			case "foreign":
				u.Owner = 1
			case "dead":
				u.Alive = false
			case "nonmove":
				n.ID = orders.Lookup("MobileBuild")
			case "secondary":
				q.SetPrimary(nil)
				q.SetSecondary([]*orders.Node{n})
			}
			beforePrimary, beforeSecondary := q.Primary(), q.Secondary()
			s.applyHumanCommand(HumanCommand{Kind: HumanCancelQueuedMove,
				CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: sequence, Handles: []pool.Handle{h}},
			}, 2)
			if !reflect.DeepEqual(q.Primary(), beforePrimary) || !reflect.DeepEqual(q.Secondary(), beforeSecondary) {
				t.Fatal("cancellation touched a node outside its admitted actors and primary move family")
			}
		})
	}
}
