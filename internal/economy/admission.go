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
	// No clamp. Both ratios are at most one, so funded work never exceeds the
	// pool it was scaled against and neither result can go negative on its own.
	// A defensive max(0, ...) here would be uncited code in the one function
	// whose exact arithmetic is the contract [05 "Two-stage settlement
	// algorithm"] C8 C9 — and it would mask a genuine sign error rather than
	// report one.
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

	if w != nil {
		// The unit slice accumulates FIRST; the player-level mirror bucket
		// folds in only after the whole slice, matching retail's gather order
		// [05 "Unit instance economy state"] (decompile: notes/economy/04 §3).
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
	totalProduction = totalProduction + player.Mirror[res].Production
	sumDebt = sumDebt + player.Mirror[res].Carry
	sumAccepted = sumAccepted + player.Mirror[res].Accepted

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
// Settle runs one full settlement pass for player p per
// [05 "Authoritative settlement order"] C7 and [05 "Stocks, counters, and
// waste"] C10. It is called from inside TickPlayer's deadline block, after the
// gate chain, and is the ONLY assembled entry point — there is no mirror-only
// variant, because a settlement that skips the per-unit half computes the wrong
// ratios rather than a reduced-fidelity version of the right ones.
//
// The pass, in order:
//
//  1. rebuild storage capacity from eligible completed units [05 "Storage capacity"];
//  2. cloak upkeep, a direct sequential debit in unit slot order [05 "Cloak debit"] C13;
//  3. settle each resource independently, metal then energy, summing per-unit
//     debt and accepted work in stable slot order [05 "Two-stage settlement
//     algorithm"] C6 C8 C9;
//  4. commit per-pass counters and cumulative totals, clamp stock to the
//     rebuilt capacity, accrue the overflow to waste with its fractional part,
//     then archive and zero the live buckets [05 "Stocks, counters, and
//     waste"] C10.
//
// w may be nil only in fixtures that exercise the mirror arithmetic alone; the
// per-unit sums then degenerate to the mirror, which is what a player with no
// units settles to anyway.
func (s *Service) Settle(p int, tick uint32, w *units.World) {
	if s == nil || p < 0 || p >= len(s.Players) {
		return
	}
	if s.OnSettle != nil {
		s.OnSettle(p, tick)
	}
	// 1. Capacity is rebuilt from scratch each pass [05 "Storage capacity"] C14.
	// It is a whole-world sweep because a player's capacity is the sum over its
	// own completed units; running it per settled player is what retail's
	// per-slot pass does.
	RebuildCapacity(s, w)
	// 2. Cloak upkeep debits live stock BEFORE the pool is formed, in unit slot
	// order, so an earlier unit's debit can starve a later one [05 "Cloak
	// debit"] C13.
	if s.CloakCost != nil {
		ApplyCloakDebits(s, w, p, s.CloakCost, nil, nil)
	}
	// 3. Energy and metal settle independently; there is no combined shortage
	// ratio [05 "Two-stage settlement algorithm"] C8. Energy goes first because
	// that is the documented aggregation order — the gather walks energy
	// production, requested, accepted, carry and only then the metal four
	// [05 "Authoritative settlement order"]. The two are independent, so the
	// order is not observable today; it is written this way so it stays right
	// when the metal-production gate on energy carry lands.
	s.settleOneResource(p, Energy, w)
	s.settleOneResource(p, Metal, w)
	// 4. Counters and cumulative totals commit before the clamp, so they report
	// the pass rather than available funds [05 "Stocks, counters, and waste"] C10.
	s.Players[p].CommitPostSettlement()
	s.CommitUnitBuckets(w, p)
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
