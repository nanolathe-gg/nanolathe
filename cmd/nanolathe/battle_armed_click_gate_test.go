package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestArmedClickObeysTheCursorShapeGate locks the world-click handler's third
// branch [07 R-CAM-01 §14]: with an order armed, a left click issues the order
// only when the reduced cursor shape is an action shape — an index below
// `cursorred` — and does nothing on `cursorred`, `cursorgrn` or
// `cursornormal`.
//
// The gate is what makes the advertised action the performed one. The order
// resolver cannot stand in for it: its code-12 unit arm admits any live target
// and would strip a unit the armed RECLAIM row never offered to strip. Both
// directions are asserted against the same hover, so a regression in either
// the shape or the gate fails here.
func TestArmedClickObeysTheCursorShapeGate(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	// A mobile reclaimer: `ReclaimUnit`'s phase 0 wants a live mover as well
	// as the capability, so the fixture actor is the BMcode-1 definition with
	// `canreclamate` authored on it [04 §3 `ReclaimUnit`]. armsolar is an
	// ordinary own building, which retail's armed RECLAIM row offers to strip
	// without reading the owner slot at all [07 §8].
	actor := placeUnit(b, "armcons", numeric.Fixed(120*65536), numeric.Fixed(100*65536))
	actor.Def.CanReclamate = true
	target := placeUnit(b, "armsolar", numeric.Fixed(300*65536), numeric.Fixed(200*65536))
	replaceSelectionForTest(t, b, actor)
	b.battleState().SetLatch(input.LatchReclaim)
	sx, sy := screenPos(b.cam, target)

	if got := b.cursorShapeAt(input.LatchReclaim, sx, sy); got != render.CursorReclamate {
		t.Fatalf("armed reclaim over an own building = %d (%s), want cursorreclamate [07 §8]",
			got, render.CursorName(got))
	}
	if !b.orderSelected(12, sx, sy, false) {
		t.Fatalf("an advertised reclaim did not take the issue branch [07 R-CAM-01 §14]")
	}
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 12 || pending[0].Order.Target != target.Handle {
		t.Fatalf("an advertised reclaim was not dispatched: %+v [07 R-CAM-01 §14]", pending)
	}
	// The order the session would resolve from that command is the unit
	// reclaim, so the click performs what the shape advertised.
	if name := orders.DescriptorFor(orders.Resolve(12, actor, target, &pending[0].Order.Position)).Name; name != "ReclaimUnit" {
		t.Fatalf("dispatched reclaim resolves to %q, want ReclaimUnit [04 R-ORD-02 §1]", name)
	}

	// The same hover with a target the admission predicate refuses: a
	// `cancapture` definition is the commander case [04 §3 `ReclaimUnit`]. The
	// shape drops to `cursornormal`, and the gate must then refuse the click
	// although the resolver would still hand back `ReclaimUnit`.
	b2 := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b2.sess.LocalOwner = 0
	actor2 := placeUnit(b2, "armcons", numeric.Fixed(120*65536), numeric.Fixed(100*65536))
	actor2.Def.CanReclamate = true
	target2 := placeUnit(b2, "armsolar", numeric.Fixed(300*65536), numeric.Fixed(200*65536))
	target2.Def.CanCapture = true
	replaceSelectionForTest(t, b2, actor2)
	b2.battleState().SetLatch(input.LatchReclaim)
	sx2, sy2 := screenPos(b2.cam, target2)

	if got := b2.cursorShapeAt(input.LatchReclaim, sx2, sy2); got != render.CursorNormal {
		t.Fatalf("armed reclaim over a `cancapture` target = %d (%s), want cursornormal [07 §8]",
			got, render.CursorName(got))
	}
	if _, hit, _ := b2.pickTarget(sx2, sy2); hit == nil || hit.Handle != target2.Handle {
		t.Fatalf("fixture click does not land on the refused target; the gate assertion below would be vacuous")
	}
	// The resolver alone would still hand back `ReclaimUnit` here; only the
	// front door refuses.
	if name := orders.DescriptorFor(orders.Resolve(12, actor2, target2, &orders.ResolvePos{})).Name; name != "ReclaimUnit" {
		t.Fatalf("fixture premise lost: code 12 resolves %q, so the gate assertion below is vacuous", name)
	}
	if b2.orderSelected(12, sx2, sy2, false) {
		t.Fatalf("a refused click reported an issued order [07 R-CAM-01 §14]")
	}
	if pending := b2.sess.PendingHumanCommands(); len(pending) != 0 {
		t.Fatalf("an armed click under cursornormal dispatched %+v [07 R-CAM-01 §14]", pending)
	}
}

// TestRefusedArmedClickKeepsTheLatchArmed locks the other half of
// [07 R-CAM-01 §14] step 3: a branch that is not taken does *nothing*, and a
// latch that retired would be something. An armed click the shape gate refuses
// therefore leaves the order armed, so the player can aim again without
// re-arming; a click that issues retires it, or keeps it on the Shift sticky
// rule as it always has [07 §9].
//
// The click runs through the real pointer handler, which is where the latch
// lives. The fixture passes no presentation client, so the Modern command drag
// declines the press (interface design §3.11) and the retail armed-click branch
// services it.
func TestRefusedArmedClickKeepsTheLatchArmed(t *testing.T) {
	// Shared fixture: a mobile reclaimer selected, one own building under the
	// pointer. `cancapture` on the target definition is what the unit-reclaim
	// admission predicate refuses [04 §3].
	fixture := func(t *testing.T, refused bool) (*battleSession, int32, int32) {
		t.Helper()
		b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
		b.sess.LocalOwner = 0
		actor := placeUnit(b, "armcons", numeric.Fixed(120*65536), numeric.Fixed(100*65536))
		actor.Def.CanReclamate = true
		target := placeUnit(b, "armsolar", numeric.Fixed(300*65536), numeric.Fixed(200*65536))
		target.Def.CanCapture = refused
		replaceSelectionForTest(t, b, actor)
		b.battleState().SetLatch(input.LatchReclaim)
		sx, sy := screenPos(b.cam, target)
		return b, sx, sy
	}

	b, sx, sy := fixture(t, true)
	clickAt(b, sx, sy, false)
	if got := b.battleState().Input.Latch; got != input.LatchReclaim {
		t.Fatalf("a refused armed click retired the latch: %d, want %d [07 R-CAM-01 §14]",
			got, input.LatchReclaim)
	}

	b, sx, sy = fixture(t, false)
	clickAt(b, sx, sy, false)
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("an issued armed click kept the latch: %d, want idle [07 §9]", got)
	}

	// Shift keeps the latch on an issued click, which is the existing rule and
	// must survive the gate [07 R-CAM-01 §14] step 1.
	b, sx, sy = fixture(t, false)
	clickAt(b, sx, sy, true)
	if got := b.battleState().Input.Latch; got != input.LatchReclaim || !b.battleState().Input.ShiftLatchSticky {
		t.Fatalf("Shift-queued armed click latch=%d sticky=%v, want the latch kept [07 §9]",
			got, b.battleState().Input.ShiftLatchSticky)
	}
}

// TestCommandDragShortReleaseObeysTheShapeGate carries the same rule into the
// Modern command drag (interface design §3.11), which is the path an armed
// RECLAIM, REPAIR or MOVE press takes under the Enhanced renderer. A short
// release is an ordinary armed click: the shape gate judges it, and a refused
// one leaves the order armed [07 R-CAM-01 §14]. The dragged branches dispatch
// their own target lists and are not affected.
func TestCommandDragShortReleaseObeysTheShapeGate(t *testing.T) {
	for _, refused := range []bool{true, false} {
		name := "issued"
		if refused {
			name = "refused"
		}
		t.Run(name, func(t *testing.T) {
			b, cl, _, _ := resourceFixture(t, false)
			// Picking is a hull test over the candidate's root piece
			// [07 R-REV-01]; this fixture authors no model source of its own.
			installTestHullModels()
			b.cat.Units["armcons"].CanReclamate = true
			target := placeUnit(b, "armsolar", numeric.Fixed(300*65536), numeric.Fixed(200*65536))
			// `cancapture` on the target definition is the clause the
			// unit-reclaim admission predicate refuses [04 §3].
			target.Def.CanCapture = refused
			applyPendingBattleCommands(b)
			b.battleState().SetLatch(input.LatchReclaim)
			sx, sy := screenPos(b.cam, target)

			dragInput(b, cl, sx, sy, input.MouseButtonLeft, "press", input.Modifiers{})
			if b.modernDrag == nil {
				t.Fatal("an armed press in the viewport did not start a command drag")
			}
			dragInput(b, cl, sx, sy, input.MouseButtonLeft, "release", input.Modifiers{})

			want := input.LatchNormal
			if refused {
				want = input.LatchReclaim
			}
			if got := b.battleState().Input.Latch; got != want {
				t.Fatalf("short release latch = %d, want %d [07 R-CAM-01 §14]", got, want)
			}
		})
	}
}
