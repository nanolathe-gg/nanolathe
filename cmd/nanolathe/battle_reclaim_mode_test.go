package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Reclaim hover and armed-click admission read the committed frame's mover
// mode. A pending request must not make a still-airborne unit reclaimable or
// hide a still-grounded unit [05 R-WORK-01 §4][07 §8][07 R-CAM-01 §14].
func TestPublishedCommittedModeControlsArmedReclaimClick(t *testing.T) {
	for _, tc := range []struct {
		name               string
		request, committed uint8
		wantShape          int
		wantIssued         bool
	}{
		{name: "blocked touchdown remains airborne", request: 1, committed: 2, wantShape: render.CursorNormal},
		{name: "pending takeoff remains grounded", request: 2, committed: 1, wantShape: render.CursorReclamate, wantIssued: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
			b.sess.LocalOwner = 0
			actor := placeUnit(b, "armcons", numeric.Fixed(120<<16), numeric.Fixed(100<<16))
			actor.Def.CanReclamate = true
			target := placeUnit(b, "armsolar", numeric.Fixed(300<<16), numeric.Fixed(200<<16))
			target.Def.CanFly = true
			target.Move.Mode = tc.request
			target.Move.ModeMirror = tc.committed
			replaceSelectionForTest(t, b, actor)

			// Keep a real unprotected builder order behind the admission gate. A
			// refused click must leave both it and the armed latch intact; an
			// admitted nonqueued reclaim would replace it at the session boundary.
			q := orders.QueueForUnit(actor)
			q.SetPrimary([]*orders.Node{{
				ID: orders.Lookup("Move_Ground"), Owner: actor.Handle,
				DynamicGate: 1, Deadline: 1000,
			}})
			before := q.Head()
			b.battleState().SetLatch(input.LatchReclaim)
			sx, sy := screenPos(b.cam, target)
			if _, hit, _ := b.pickTarget(sx, sy); hit == nil || hit.Handle != target.Handle {
				t.Fatal("fixture click misses target")
			}
			if got := b.cursorShapeAt(input.LatchReclaim, sx, sy); got != tc.wantShape {
				t.Fatalf("armed reclaim shape = %d (%s), want %d (%s); request/committed=%d/%d",
					got, render.CursorName(got), tc.wantShape, render.CursorName(tc.wantShape), tc.request, tc.committed)
			}

			in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
			in.Mouse.SetPosition(float32(sx), float32(sy))
			in.Mouse.SetButton(input.MouseButtonLeft, true)
			b.handleInput(in, nil)
			if tc.wantIssued {
				if got := b.battleState().Input.Latch; got != input.LatchNormal {
					t.Fatalf("admitted reclaim left latch %d, want idle", got)
				}
				pending := b.sess.PendingHumanCommands()
				if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 12 || pending[0].Order.Target != target.Handle {
					t.Fatalf("admitted reclaim dispatch = %+v, want code 12 targeting %d", pending, target.Handle)
				}
				return
			}
			if got := b.battleState().Input.Latch; got != input.LatchReclaim {
				t.Fatalf("refused reclaim retired latch: got %d, want %d", got, input.LatchReclaim)
			}
			if pending := b.sess.PendingHumanCommands(); len(pending) != 0 {
				t.Fatalf("refused reclaim dispatched %+v", pending)
			}
			applyPendingBattleCommands(b)
			if q.LenPrimary() != 1 || q.Head() != before {
				t.Fatalf("refused reclaim changed builder queue: before=%p after=%+v", before, q.Primary())
			}
		})
	}
}
