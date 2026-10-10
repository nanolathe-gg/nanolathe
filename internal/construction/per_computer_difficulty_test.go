package construction

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// RefundSelector answers the refunded owner's own ledger word when it has one
// and the battle's ModeSelector otherwise (docs/DESIGN_MULTIPLAYER.md §6.6),
// with or without a ledger.
func TestRefundSelectorPrefersTheOwnersWord(t *testing.T) {
	s := &Service{ModeSelector: 1}
	if got := s.RefundSelector(2); got != 1 {
		t.Fatalf("no ledger: %d, want the battle's 1", got)
	}
	s.Economy = &economy.Service{}
	s.Economy.SetPlayerSelector(2, 0)
	if got, other := s.RefundSelector(2), s.RefundSelector(3); got != 0 || other != 1 {
		t.Fatalf("own word %d, other owner %d; want 0 and the battle's 1", got, other)
	}
	s.Economy.ClearPlayerSelector(2)
	if got := s.RefundSelector(2); got != 1 {
		t.Fatalf("cleared: %d, want the battle's 1", got)
	}
}

// The cancel refund is discounted by the BUILDER owner's own word, not the
// battle's [05 R-ECO-01 §11]: trunc((1-.25)*4.9) = 3 onto .1 is 1.6 on easy
// and 3.1 on hard.
func TestCancelRefundTakesTheBuilderOwnersWord(t *testing.T) {
	for _, tc := range []struct {
		name       string
		battle     int
		own        int
		want       float32
		ownerAware bool
	}{
		{"own easy over battle hard", 2, 0, 1.6, true},
		{"own hard over battle easy", 0, 2, 3.1, true},
		{"another player's word", 2, 0, 3.1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, factory, product, node := factoryWithAttachedProduct(t)
			product.Remaining, product.Def.BuildCostMetal = .25, 4.9
			s.ModeSelector = tc.battle
			owner := factory.Owner
			if !tc.ownerAware {
				owner++
			}
			s.Economy.SetPlayerSelector(owner, tc.own)
			s.SetIsSpecialSecondState(func(uint8) bool { return true })
			s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production = .1
			s.handleCancelCurrent(factory, node, 100)
			got := s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production
			if math.Float32bits(got) != math.Float32bits(tc.want) {
				t.Fatalf("builder refund = %.17g, want %.17g", got, tc.want)
			}
		})
	}
}

// Reverse work refunds the TARGET's account through the target owner's own
// word; a word held by the builder's owner does not reach it
// [05 R-WORK-01 §1][05 R-ECO-01 §11].
func TestReverseRefundTakesTheTargetOwnersWord(t *testing.T) {
	s, builder, original, _ := factoryWithAttachedProduct(t)
	h, err := s.World.Create(original.Def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	target := s.World.Unit(h)
	target.Remaining = .5
	target.Def.BuildTime, target.Def.BuildCostMetal = 100, 12
	s.ModeSelector = 2
	s.Economy.SetPlayerSelector(target.Owner, 1)
	s.Economy.SetPlayerSelector(builder.Owner, 0)
	s.SetIsSpecialSecondState(func(owner uint8) bool { return owner == target.Owner })
	s.Economy.UnitBuckets(target.Handle)[economy.Metal].Production = .1
	if !s.sharedStep(builder, target, -25, 10) {
		t.Fatal("reverse work refused")
	}
	if got := s.Economy.UnitBuckets(target.Handle)[economy.Metal].Production; math.Float32bits(got) != math.Float32bits(float32(2.2)) {
		t.Fatalf("target refund = %.17g, want the medium %.17g", got, float32(2.2))
	}
}
