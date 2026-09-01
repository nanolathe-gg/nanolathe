package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestStanceGadgetIsResolvedByTheLongestSuffix locks the one thing that made
// the two stance buttons unreachable: `ARMMOVEORD` ends in `MOVE`'s letters, so
// the click path's substring chain sent it to the contextual move latch. The
// stage and grey pass already resolved it correctly by longest suffix; the
// click path now uses the same table, so what a button shows and what it does
// can never disagree [04 R-STANCE-01 §2][07 R-HUD-03 §6].
func TestStanceGadgetIsResolvedByTheLongestSuffix(t *testing.T) {
	rows := []struct{ gadget, want string }{
		{"ARMMOVEORD", "MOVEORD"},
		{"CORMOVEORD", "MOVEORD"},
		{"ARMFIREORD", "FIREORD"},
		{"ARMMOVE", "MOVE"},
	}
	for _, row := range rows {
		if got := commandButtonName(row.gadget); got != row.want {
			t.Fatalf("commandButtonName(%q) = %q, want %q", row.gadget, got, row.want)
		}
	}
}

// TestStanceCyclePressTransmitsTheNextValue locks the four-arm cycle and the
// broadcast of [04 R-STANCE-01 §2]: a press reads the published three-bit panel
// field, computes 0→1, 1→2, 2→0, 3→0, and transmits the new value; a field
// reading 4 is not applicable to the selection and presses nothing.
//
// Nothing dispatched these at all before this landed, which is why a player
// could not move a unit off hold fire or hold position.
func TestStanceCyclePressTransmitsTheNextValue(t *testing.T) {
	rows := []struct {
		from uint8
		want int32
		sent bool
	}{
		{from: 0, want: 1, sent: true},
		{from: 1, want: 2, sent: true},
		{from: 2, want: 0, sent: true},
		{from: 3, want: 0, sent: true}, // the mixed sentinel cycles to hold
		{from: 4, sent: false},         // not applicable: the gadget is grey
	}
	for _, row := range rows {
		b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
		u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
		replaceSelectionForTest(t, b, u)
		f := b.sess.Snapshot.Current()
		if f == nil {
			t.Fatal("no committed frame after selection")
		}
		f.CommandPage.FireStance = row.from
		b.cycleStance(true)
		pending := b.sess.PendingHumanCommands()
		if !row.sent {
			if len(pending) != 0 {
				t.Fatalf("stance %d: pressed a not-applicable gadget and transmitted %+v", row.from, pending)
			}
			continue
		}
		if len(pending) != 1 || pending[0].Kind != session.HumanStance {
			t.Fatalf("stance %d: dispatched %+v, want one HumanStance", row.from, pending)
		}
		if !pending[0].Stance.Fire || pending[0].Stance.Value != row.want {
			t.Fatalf("stance %d: transmitted %+v, want fire with value %d", row.from, pending[0].Stance, row.want)
		}
	}
}

// TestStanceCommandDepositsTheValueInTheSelectedUnit is the whole chain, from
// the transmitted command to the two-bit field the auto-engage issuer reads:
// the broadcast finds the selected unit, the standing handler runs on its
// single visit and completes, and the field carries the new value
// [04 R-STANCE-01 §2][04 R-STANCE-01 §5].
func TestStanceCommandDepositsTheValueInTheSelectedUnit(t *testing.T) {
	cat := testCatalogON05()
	def, _ := cat.Unit("armcons")
	def.FireStandOrders = true
	def.MobileStandOrders = true
	b := newTestBattle(cat, testWorldON05(20, 20))
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)

	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanStance, Stance: session.HumanStanceCommand{Fire: true, Value: 2},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	applyPendingBattleCommands(b)
	if got := (u.Flags >> units.StandingFireShift) & units.StandingFieldMask; got != 2 {
		t.Fatalf("fire stance = %d after a fire-at-will press, want 2", got)
	}

	// The move field is the twin, and a definition that does not accept the
	// standing move order is skipped by the broadcast even while selected.
	def.MobileStandOrders = false
	before := (u.Flags >> units.StandingMoveShift) & units.StandingFieldMask
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanStance, Stance: session.HumanStanceCommand{Fire: false, Value: 0},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	applyPendingBattleCommands(b)
	if got := (u.Flags >> units.StandingMoveShift) & units.StandingFieldMask; got != before {
		t.Fatalf("move stance = %d, want %d: a unit lacking mobilestandorders is skipped", got, before)
	}
}
