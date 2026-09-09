package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// reclaimCreditService is a bare ledger with one computer-controlled slot and
// one ordinary slot, plus a difficulty selector.
func reclaimCreditService(selector int) *Service {
	s := &Service{}
	s.Players[1] = Player{Exists: true, ControllerState: 2} // computer player [05 R-ECO-01 §3]
	s.Players[2] = Player{Exists: true, ControllerState: 1} // an ordinary human slot
	sel := selector
	s.EconomySelector = &sel
	return s
}

// TestFeatureReclaimCreditIsScaledForAComputerPlayer is the PT3-05 follow-up
// regression. Feature reclaim used to credit with a nil player record, so a
// computer builder was paid in full where retail scales it; the trace shows the
// payout reading the builder's player record and routing EACH of its two
// additions through the same selector ladder as the rest of the family
// [05 R-WORK-01 §5][05 R-ECO-01 §11]. Both resources are asserted because the
// two additions are separate sites and only one of them would have been caught
// by a metal-only test.
func TestFeatureReclaimCreditIsScaledForAComputerPlayer(t *testing.T) {
	// The two documented factors and the undiscounted case. Easy halves,
	// medium takes seven tenths, hard does not scale [05 R-ECO-01 §3].
	cases := []struct {
		selector             int
		wantMetal, wantEnrgy float32
	}{
		{0, 500, 125},  // easy: half of 1000 / 250
		{1, 700, 175},  // medium: seven tenths
		{2, 1000, 250}, // hard: undiscounted
		{7, 1000, 250}, // any selector above one is the plain add
	}
	for _, c := range cases {
		s := reclaimCreditService(c.selector)
		s.CreditFeatureReclaim(pool.Handle(4), 1, 1000, 250) // owner 1 is the computer slot
		got := s.UnitBuckets(pool.Handle(4))
		if got[Metal].Production != c.wantMetal || got[Energy].Production != c.wantEnrgy {
			t.Fatalf("selector %d credited metal %v energy %v, want %v / %v",
				c.selector, got[Metal].Production, got[Energy].Production, c.wantMetal, c.wantEnrgy)
		}
	}
}

// TestFeatureReclaimCreditIsRawForAHumanPlayer locks the gate's other side: the
// discount is per-OWNER, not per-difficulty. On the same easy selector a human
// builder is credited in full, and so is an owner whose slot does not exist —
// the traced gate tests the record before the control byte.
func TestFeatureReclaimCreditIsRawForAHumanPlayer(t *testing.T) {
	// A human slot, a slot whose record does not exist, and an owner outside the
	// ten slots — the last standing in for the traced "no player record" arm,
	// which is now the only way to reach it: the owner is a required argument.
	for _, owner := range []uint8{2, 5, 99} {
		s := reclaimCreditService(0)
		s.CreditFeatureReclaim(pool.Handle(4), owner, 1000, 250)
		got := s.UnitBuckets(pool.Handle(4))
		if got[Metal].Production != 1000 || got[Energy].Production != 250 {
			t.Fatalf("owner %d credited metal %v energy %v, want the full 1000 / 250",
				owner, got[Metal].Production, got[Energy].Production)
		}
	}
}

// TestBothReclaimPayoutsUseOneCreditForm locks the property that made the
// defect possible: reclaimed material is credited by one body, so the feature
// and unit payouts cannot drift apart again. A full-value unit refund and a
// feature credit of the same magnitude land the same number on the same
// computer player's metal accumulator [05 "Unit reclaim"][05 R-WORK-01 §5].
func TestBothReclaimPayoutsUseOneCreditForm(t *testing.T) {
	for _, selector := range []int{0, 1, 2} {
		feature := reclaimCreditService(selector)
		feature.CreditFeatureReclaim(pool.Handle(4), 1, 1000, 0)

		unit := reclaimCreditService(selector)
		// remaining 0 makes the refund the victim's whole 1000 metal cost.
		unit.CreditUnitReclaimRefund(pool.Handle(4), 0, 1000, 2)

		f := feature.UnitBuckets(pool.Handle(4))[Metal].Production
		u := unit.UnitBuckets(pool.Handle(4))[Metal].Production
		if f != u {
			t.Fatalf("selector %d: feature reclaim credited %v, unit reclaim %v", selector, f, u)
		}
	}
}

// TestReclaimCreditUsesTheSingleNarrowingForm locks the arithmetic [05 R-ECO-01
// §3] insists on: the scale and the subtraction are evaluated at working
// precision and only the store narrows. The accumulator is seeded first, so the
// expression under test is the two-operand one the sites perform rather than a
// scale against zero.
//
// Honest scope: a search over ordinary accumulator and contribution magnitudes
// did not exhibit a value where the pre-scaling form this file used to carry
// disagrees with this one — double rounding needs the intermediate to land on a
// float32 tie. The form is cloned because §3 states it and warns that the
// factored form rounds differently, not because a divergence has been shown.
func TestReclaimCreditUsesTheSingleNarrowingForm(t *testing.T) {
	for _, selector := range []int{0, 1} {
		k := -0.5
		if selector == 1 {
			k = -0.7
		}
		s := reclaimCreditService(selector)
		s.CreditFeatureReclaim(pool.Handle(4), 1, 1234.5, 0)
		s.CreditFeatureReclaim(pool.Handle(4), 1, 6789.25, 0)

		want := float32(0 - 1234.5*k)
		want = float32(float64(want) - 6789.25*k)
		if got := s.UnitBuckets(pool.Handle(4))[Metal].Production; got != want {
			t.Fatalf("selector %d accumulated %v, want %v", selector, got, want)
		}
	}
}

func TestUnitReclaimRefundUsesStoredDefinitionCost(t *testing.T) {
	source := int32(16777217)
	s := reclaimCreditService(0)
	s.CreditUnitReclaimRefund(pool.Handle(4), 0.25, float32(source), 1)
	if got := s.UnitBuckets(pool.Handle(4))[Metal].Production; got != 12582912 {
		t.Fatalf("refund = %v, want 12582912 from the single-float definition cost", got)
	}
}

// The old intermediate single store changes this result by one float32 ULP.
func TestUnitReclaimRefundNarrowsOnlyAtBucketStore(t *testing.T) {
	s := reclaimCreditService(1)
	s.CreditUnitReclaimRefund(pool.Handle(4), 0.001, 11, 2)
	const want float32 = 7.692299842834473
	if got := s.UnitBuckets(pool.Handle(4))[Metal].Production; got != want {
		t.Fatalf("refund=%v, want %v", got, want)
	}
}
