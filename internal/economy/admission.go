package economy

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// DebtRatio returns min(1, pool/Σdebt) in single precision per [05 "Two-stage settlement algorithm"] C8 C9.
// Zero debt is treated as fully funded. All intermediates are float32.
func DebtRatio(pool, sumDebt float32) float32 {
	if sumDebt == float32(0) {
		return float32(1)
	}
	r := pool / sumDebt
	if r > float32(1) {
		return float32(1)
	}
	return r
}

// AcceptRatio returns min(1, remainingPool/Σaccepted) in single precision per [05 "Two-stage settlement algorithm"] C8 C9.
// Zero accepted is treated as fully funded.
func AcceptRatio(remainingPool, sumAccepted float32) float32 {
	if sumAccepted == float32(0) {
		return float32(1)
	}
	r := remainingPool / sumAccepted
	if r > float32(1) {
		return float32(1)
	}
	return r
}

// NewCarry computes oldCarry×(1−debtRatio)+accepted×(1−acceptRatio) in float32 per [05 "Two-stage settlement algorithm"] C8 C9.
// Evaluation order is forced through float32 intermediates per C9 and I2.
func NewCarry(oldCarry, accepted, debtRatio, acceptRatio float32) float32 {
	one := float32(1)
	invDebt := one - debtRatio
	invAccept := one - acceptRatio
	t1 := oldCarry * invDebt
	t2 := accepted * invAccept
	return t1 + t2
}

// settlePure computes pool, debtRatio, remainingPool, acceptRatio, closingStock all in float32 per C8 C9.
// It does not mutate state; it is the pure arithmetic core for tests and for settleOneResource.
func settlePure(opening, production, sumDebt, sumAccepted float32) (pool, debtRatio, remainingPool, acceptRatio, closingStock float32) {
	pool = opening + production
	debtRatio = DebtRatio(pool, sumDebt)
	fundedDebt := sumDebt * debtRatio
	remainingPool = pool - fundedDebt
	acceptRatio = AcceptRatio(remainingPool, sumAccepted)
	fundedAccepted := sumAccepted * acceptRatio
	closingStock = remainingPool - fundedAccepted
	if closingStock < float32(0) {
		closingStock = float32(0)
	}
	if remainingPool < float32(0) {
		remainingPool = float32(0)
	}
	return
}

// settleOneResource applies the two-stage settlement for one resource kind to player p per [05 "Two-stage settlement algorithm"] C8 C9.
// Energy and metal are settled independently with no combined shortage ratio.
// Steps per resource in float32:
//
//	pool = opening + production
//	debtRatio = min(1, pool/Σdebt) with zero debt fully funded
//	remainingPool = pool − Σdebt×debtRatio
//	acceptRatio = min(1, remainingPool/Σaccepted) with zero accepted fully funded
//	newCarry = oldCarry×(1−debtRatio)+accepted×(1−acceptRatio) uniformly, no remainder distribution.
//	closingStock = remainingPool − Σaccepted×acceptRatio.
//
// All intermediates are forced through float32 per C9.
func (s *Service) settleOneResource(p int, res Res, w *units.World) {
	if s == nil || p < 0 || p >= len(s.Players) {
		return
	}
	player := &s.Players[p]

	// Sums in float32, accumulated in stable slot order per I1 and [05 "Authoritative settlement order"] C6.
	var totalProduction float32
	var sumDebt float32
	var sumAccepted float32

	totalProduction = player.Mirror[res].Production
	sumDebt = player.Mirror[res].Carry
	sumAccepted = player.Mirror[res].Accepted

	if w != nil {
		ForEachUnitOrdered(w, p, func(u *units.Unit) {
			h := u.Handle
			if h == 0 {
				return
			}
			s.ensureUnitBuckets(h)
			b := s.unitBuckets[h].Buckets[res]
			totalProduction = totalProduction + b.Production
			sumDebt = sumDebt + b.Carry
			sumAccepted = sumAccepted + b.Accepted
		})
	}

	opening := player.Stock[res]
	pool, debtRatio, _, acceptRatio, closingStock := settlePure(opening, totalProduction, sumDebt, sumAccepted)

	one := float32(1)
	invDebt := one - debtRatio
	invAccept := one - acceptRatio

	// Mirror bucket newCarry.
	oldCarry := player.Mirror[res].Carry
	accepted := player.Mirror[res].Accepted
	t1 := oldCarry * invDebt
	t2 := accepted * invAccept
	newCarry := t1 + t2
	player.Mirror[res].Carry = newCarry

	if w != nil {
		ForEachUnitOrdered(w, p, func(u *units.Unit) {
			h := u.Handle
			if h == 0 {
				return
			}
			s.ensureUnitBuckets(h)
			ue := &s.unitBuckets[h]
			old := ue.Buckets[res].Carry
			acc := ue.Buckets[res].Accepted
			tt1 := old * invDebt
			tt2 := acc * invAccept
			nc := tt1 + tt2
			ue.Buckets[res].Carry = nc
		})
	}

	// Write closing stock; preserve single precision per [05 "Player slot"] and I2.
	// The caller (tick/settlement pass) will later clamp to rebuilt capacity and
	// carry fractional overflow to waste per [05 "Stocks, counters, and waste"] C10.
	_ = pool // keep for signpost; closingStock already float32.
	player.Stock[res] = closingStock
}

// Settle settles player p for tick per [05 "Authoritative settlement order"] and [05 "Two-stage settlement algorithm"] C8 C9.
// This is the plan API signature func (s *Service) Settle(p int, tick uint32) operating on the mirror bucket
// when no world is available. When a world is available use SettleWithWorld for full per-unit settlement.
// Both resources are settled independently in float32 with retail evaluation order.
func (s *Service) Settle(p int, tick uint32) {
	if s.OnSettle != nil {
		s.OnSettle(p, tick)
	}
	_ = tick
	s.settleOneResource(p, Metal, nil)
	s.settleOneResource(p, Energy, nil)
}

// SettleWithWorld settles player p with full per-unit state via world w per [05 "Authoritative settlement order"] C6 and C8 C9.
// Units are visited in stable slot order per I1; each resource is settled independently; all arithmetic is float32.
func (s *Service) SettleWithWorld(p int, tick uint32, w *units.World) {
	_ = tick
	s.settleOneResource(p, Metal, w)
	s.settleOneResource(p, Energy, w)
}

// AdmissionPure is an exported pure helper for tests: it performs the two-stage settlement arithmetic
// on primitive inputs without world or service state, forcing every intermediate through float32 per C9.
// It returns debtRatio, acceptRatio, closingStock, plus per-entity newCarry computed via NewCarry.
func AdmissionPure(opening, production float32, debts, accepts []float32) (debtRatio, acceptRatio, closingStock float32, newCarries []float32) {
	var sumDebt float32
	var sumAccepted float32
	for i := 0; i < len(debts); i++ {
		sumDebt = sumDebt + debts[i]
	}
	for i := 0; i < len(accepts); i++ {
		sumAccepted = sumAccepted + accepts[i]
	}
	_, debtRatio, _, acceptRatio, closingStock = settlePure(opening, production, sumDebt, sumAccepted)
	newCarries = make([]float32, len(debts))
	for i := 0; i < len(debts); i++ {
		var acc float32
		if i < len(accepts) {
			acc = accepts[i]
		}
		newCarries[i] = NewCarry(debts[i], acc, debtRatio, acceptRatio)
	}
	return
}
