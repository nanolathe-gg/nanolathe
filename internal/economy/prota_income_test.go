package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func proTAIncomeService(selector int) *Service {
	s := reclaimCreditService(selector)
	s.Community = community.Features{AIDifficultyIncome: true}
	return s
}

// TestProTAIncomeCreditArithmetic locks the three shipped 4.8 forms
// (research/extensions/prota-engine.md "AI and economy evidence audit"):
// Easy float32(p - c*double(-0.5)), Medium float32(p + c), Hard and every other
// selector float32(p - c*double(-4)), each at working precision until the
// single-precision store, with the contribution never narrowed on entry.
func TestProTAIncomeCreditArithmetic(t *testing.T) {
	p := float32(0.1)
	c := float64(float32(1.0/3.0)) * float64(float32(7.3)) // a product just formed, wider than float32
	cases := []struct {
		selector int
		want     float32
	}{
		{0, float32(float64(p) - float64(c*-0.5))},
		{1, float32(float64(p) + c)},
		{2, float32(float64(p) - float64(c*-4))},
		{9, float32(float64(p) - float64(c*-4))},
	}
	for _, tc := range cases {
		if got := proTAIncomeCredit(p, c, tc.selector); got != tc.want {
			t.Fatalf("selector %d: got %v, want %v", tc.selector, got, tc.want)
		}
	}
}

// TestProTAIncomeContributionRoutes checks the per-unit contribution store:
// a computer owner takes 0.5/1/4, a human the plain add, and the switch's zero
// value keeps retail's 0.5/0.7/1 ladder.
func TestProTAIncomeContributionRoutes(t *testing.T) {
	for _, tc := range []struct {
		selector    int
		on          bool
		owner       int
		want        float32
		description string
	}{
		{0, true, 1, 50, "easy computer halves"},
		{1, true, 1, 100, "medium computer is the plain add"},
		{2, true, 1, 400, "hard computer quadruples"},
		{2, true, 2, 100, "a human is never scaled"},
		{1, false, 1, float32(float64(0) - float64(float64(100)*-0.7)), "retail medium keeps seven tenths"},
		{2, false, 1, 100, "retail hard is the plain add"},
	} {
		s := reclaimCreditService(tc.selector)
		s.Community.AIDifficultyIncome = tc.on
		var b Bucket
		addContribution(s, &s.Players[tc.owner], &b, 100)
		if b.Production != tc.want {
			t.Fatalf("%s: production %v, want %v", tc.description, b.Production, tc.want)
		}
	}
}

// TestProTAIncomeFeatureReclaimScaled covers the second acceptance surface the
// 4.8 release note names: both feature-reclaim credits take the package table,
// energy before metal, for a computer builder only.
func TestProTAIncomeFeatureReclaimScaled(t *testing.T) {
	for _, tc := range []struct {
		selector           int
		wantMetal, wantEng float32
	}{
		{0, 500, 125},
		{1, 1000, 250},
		{2, 4000, 1000},
		{5, 4000, 1000},
	} {
		s := proTAIncomeService(tc.selector)
		s.CreditFeatureReclaim(pool.Handle(4), 1, 1000, 250)
		got := s.UnitBuckets(pool.Handle(4))
		if got[Metal].Production != tc.wantMetal || got[Energy].Production != tc.wantEng {
			t.Fatalf("selector %d: metal %v energy %v, want %v / %v", tc.selector, got[Metal].Production, got[Energy].Production, tc.wantMetal, tc.wantEng)
		}
		human := proTAIncomeService(tc.selector)
		human.CreditFeatureReclaim(pool.Handle(4), 2, 1000, 250)
		if got := human.UnitBuckets(pool.Handle(4)); got[Metal].Production != 1000 || got[Energy].Production != 250 {
			t.Fatalf("selector %d: human builder credited %v / %v", tc.selector, got[Metal].Production, got[Energy].Production)
		}
	}
}

// TestProTAIncomeLeavesUnitReclaimRetail locks the bounded negative: the
// death-side unit-reclaim refund keeps retail's 0.5/0.7/1 even with the
// package switch on.
func TestProTAIncomeLeavesUnitReclaimRetail(t *testing.T) {
	for _, tc := range []struct {
		selector int
		want     float32
	}{
		{0, 500},
		{1, float32(float64(0) - float64(float64(1000)*-0.7))},
		{2, 1000},
	} {
		s := proTAIncomeService(tc.selector)
		s.CreditUnitReclaimRefund(pool.Handle(4), 0, 1000, true)
		if got := s.UnitBuckets(pool.Handle(4))[Metal].Production; got != tc.want {
			t.Fatalf("selector %d: unit reclaim refund %v, want retail %v", tc.selector, got, tc.want)
		}
	}
}
