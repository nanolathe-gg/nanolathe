package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// A computer player marked FullIncome is credited as a human is at every
// discount site — production, the feature-reclaim credit and the unit-reclaim
// refund — on every difficulty word and with the ProTA 4.8 income table on,
// while an unmarked computer player keeps the retail ladder
// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income"). With no mark
// set anywhere the ledger is the retail one, which the retail ladder tests
// lock.
func TestAFullIncomePlayerIsCreditedAsAHuman(t *testing.T) {
	for _, prota := range []bool{false, true} {
		for selector := 0; selector <= 2; selector++ {
			s := reclaimCreditService(selector)
			s.Community = community.Features{AIDifficultyIncome: prota}
			s.Players[3] = Player{Exists: true, ControllerState: 2, FullIncome: true}
			if !s.DiscountsCredit(1) || s.DiscountsCredit(3) || s.DiscountsCredit(2) {
				t.Fatalf("prota=%t selector %d: DiscountsCredit is %t %t %t for Classic, human, full", prota, selector, s.DiscountsCredit(1), s.DiscountsCredit(2), s.DiscountsCredit(3))
			}
			var b Bucket
			addContribution(s, &s.Players[3], &b, 1000)
			if b.Production != 1000 {
				t.Fatalf("prota=%t selector %d: production credited %v of 1000", prota, selector, b.Production)
			}
			s.CreditFeatureReclaim(pool.Handle(4), 3, 1000, 250)
			if got := s.UnitBuckets(pool.Handle(4)); got[Metal].Production != 1000 || got[Energy].Production != 250 {
				t.Fatalf("prota=%t selector %d: feature reclaim credited %v / %v of 1000 / 250", prota, selector, got[Metal].Production, got[Energy].Production)
			}
			s.CreditUnitReclaimRefund(pool.Handle(5), 0, 1000, s.DiscountsCredit(3))
			if got := s.UnitBuckets(pool.Handle(5))[Metal].Production; got != 1000 {
				t.Fatalf("prota=%t selector %d: unit reclaim refunded %v of 1000", prota, selector, got)
			}
			var classic Bucket
			addContribution(s, &s.Players[1], &classic, 1000)
			if want := [2][3]float32{{500, 700, 1000}, {500, 1000, 4000}}[map[bool]int{false: 0, true: 1}[prota]][selector]; classic.Production != want {
				t.Fatalf("prota=%t selector %d: the Classic computer player was credited %v, want %v", prota, selector, classic.Production, want)
			}
		}
	}
}
