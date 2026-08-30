package economy

import (
	"math"
	"testing"
)

// TestTwoStageAdmissionIndependent locks C8: energy and metal settle independently with no combined ratio.
func TestTwoStageAdmissionIndependent(t *testing.T) {
	var svc Service
	svc.Players[0].Stock[Metal] = 10
	svc.Players[0].Stock[Energy] = 100
	// Capacity above the closing stock so the post-settlement clamp is a no-op
	// and this fixture keeps asserting the ratios, not the clamp [C10].
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Capacity[Energy] = 1000
	svc.Players[0].Mirror[Metal].Carry = 20
	svc.Players[0].Mirror[Energy].Carry = 20

	svc.Settle(0, 0, nil)

	if svc.Players[0].Stock[Metal] != 0 {
		t.Fatalf("independent Metal stock %v want 0 (pool 10 debt 20 ratio 0.5)", svc.Players[0].Stock[Metal])
	}
	if svc.Players[0].Mirror[Metal].Carry != 10 {
		t.Fatalf("independent Metal carry %v want 10", svc.Players[0].Mirror[Metal].Carry)
	}
	if svc.Players[0].Stock[Energy] != 80 {
		t.Fatalf("independent Energy stock %v want 80 (pool 100 debt 20 ratio 1 remaining 80)", svc.Players[0].Stock[Energy])
	}
	if svc.Players[0].Mirror[Energy].Carry != 0 {
		t.Fatalf("independent Energy carry %v want 0", svc.Players[0].Mirror[Energy].Carry)
	}
}

// TestSettlementRatios exercises the exact private settlement core. The live
// fields are narrowed before either stage, while stage arithmetic uses the
// documented working precision [R-ECO-01 §1, §5].
func TestSettlementRatios(t *testing.T) {
	tests := []struct {
		name                         string
		opening, production          float32
		debt, accepted               float32
		wantPool, wantDebt, wantRest float32
		wantAccept, wantStock        float32
	}{
		{name: "debt clamp", opening: 200, debt: 100, wantPool: 200, wantDebt: 1, wantRest: 100, wantAccept: 1, wantStock: 100},
		{name: "debt shortage", opening: 100, debt: 200, wantPool: 100, wantDebt: 0.5, wantRest: 0, wantAccept: 1, wantStock: 0},
		{name: "accepted shortage", opening: 100, debt: 50, accepted: 100, wantPool: 100, wantDebt: 1, wantRest: 50, wantAccept: 0.5, wantStock: 0},
		{name: "zero sums", opening: 100, wantPool: 100, wantDebt: 1, wantRest: 100, wantAccept: 1, wantStock: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, debt, rest, accepted, stock := settlePure(tt.opening, tt.production, tt.debt, tt.accepted)
			if pool != tt.wantPool || debt != tt.wantDebt || rest != tt.wantRest || accepted != tt.wantAccept || stock != tt.wantStock {
				t.Fatalf("settlePure = pool %v debt %v rest %v accept %v stock %v; want %v %v %v %v %v", pool, debt, rest, accepted, stock, tt.wantPool, tt.wantDebt, tt.wantRest, tt.wantAccept, tt.wantStock)
			}
		})
	}
}

// TestNegativePoolZeroDebt keeps the established negative-pool branch. It is
// intentionally distinct from a generic zero-debt shortcut: when a negative
// pool is compared with zero debt, the division branch is taken [R-ECO-01 §5].
func TestNegativePoolZeroDebt(t *testing.T) {
	pool, debt, rest, accepted, stock := settlePure(-1, 0, 0, 0)
	if pool != -1 {
		t.Fatalf("pool %v want -1", pool)
	}
	if !math.IsInf(float64(debt), -1) {
		t.Fatalf("negative-pool zero-debt ratio %v want negative infinity", debt)
	}
	if rest != 0 || accepted != 1 || stock != 0 {
		t.Fatalf("negative-pool settlement = rest %v accept %v stock %v; want 0 1 0", rest, accepted, stock)
	}
}

// TestUniformDebtScaling locks C8 uniform scaling in the assembled pass: two
// equal debts receive equal carry, with no remainder distribution.
func TestUniformDebtScaling(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Stock[Energy] = 100
	w, hs := settleTestWorld(t, 2)
	svc.UnitBuckets(hs[0])[Energy].Carry = 100
	svc.UnitBuckets(hs[1])[Energy].Carry = 100

	svc.Settle(0, 0, w)

	if got := svc.UnitBuckets(hs[0])[Energy].Carry; got != 50 {
		t.Fatalf("first uniform debt carry %v want 50", got)
	}
	if got := svc.UnitBuckets(hs[1])[Energy].Carry; got != 50 {
		t.Fatalf("second uniform debt carry %v want 50", got)
	}
}

// TestUniformAcceptedScaling exercises the accepted-stage apply-back through
// the live settlement entry point.
func TestUniformAcceptedScaling(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Stock[Energy] = 100
	pl.Mirror[Energy].Accepted = 200

	svc.Settle(0, 0, nil)

	if got := pl.Mirror[Energy].Carry; got != 100 {
		t.Fatalf("accepted-stage carry %v want 100", got)
	}
}

// TestSettlementPoolStore locks the single-precision pool store before either
// settlement stage [R-ECO-01 §5].
func TestSettlementPoolStore(t *testing.T) {
	// 16777216 + 1 is rounded when the pool is stored as a live float32.
	pool, debt, rest, accepted, stock := settlePure(16777216, 1, 16777216, 1)
	if pool != 16777216 {
		t.Fatalf("pool %v want 16777216", pool)
	}
	if debt != 1 {
		t.Fatalf("eval order debt ratio %v want 1", debt)
	}
	if accepted != 0 {
		t.Fatalf("eval order accept ratio %v want 0 (float32 remaining 0)", accepted)
	}
	if rest != 0 || stock != 0 {
		t.Fatalf("eval order rest/stock %v/%v want 0/0", rest, stock)
	}
}
