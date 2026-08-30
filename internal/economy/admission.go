package economy

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// settlePure computes settlement stages without mutating state. Live fields
// are single precision, while products and differences use the documented
// working precision until their named stores [R-ECO-01 §1, §5].
func settlePure(opening, production, sumDebt, sumAccepted float32) (pool, debtRatio, remainingPool, acceptRatio, closingStock float32) {
	// The pool is explicitly stored as a single before either stage.  The
	// stage products and differences remain at the working precision until
	// their named state stores [R-ECO-01 §5].
	pool = float32(float64(opening) + float64(production))
	var remaining float64
	if sumDebt > pool {
		debtRatio = float32(float64(pool) / float64(sumDebt))
		remaining = float64(pool) - float64(pool)
	} else {
		debtRatio = 1
		remaining = float64(pool) - float64(sumDebt)
	}
	if float64(sumAccepted) > remaining {
		acceptRatio = float32(remaining / float64(sumAccepted))
		closingStock = float32(remaining - remaining)
	} else {
		acceptRatio = 1
		closingStock = float32(remaining - float64(sumAccepted))
	}
	remainingPool = float32(remaining)
	return
}

type resourceTotals struct {
	production float32
	requested  float32
	debt       float32
	accepted   float32
}

func (s *Service) gatherResourceTotals(p int, res Res, w *units.World) resourceTotals {
	var totals resourceTotals
	if s == nil || p < 0 || p >= len(s.Players) {
		return totals
	}
	if w != nil {
		ForEachUnitOrdered(w, p, func(u *units.Unit) {
			if u == nil || u.Handle == 0 {
				return
			}
			s.ensureUnitBuckets(u.Handle)
			b := s.unitBuckets[u.Handle].Buckets[res]
			totals.production += b.Production
			totals.requested += b.Requested
			totals.debt += b.Carry
			totals.accepted += b.Accepted
		})
	}
	mirror := s.Players[p].Mirror[res]
	totals.production += mirror.Production
	totals.requested += mirror.Requested
	totals.debt += mirror.Carry
	totals.accepted += mirror.Accepted
	return totals
}

func (s *Service) preparePassAggregates(p int, w *units.World) [2]resourceTotals {
	var all [2]resourceTotals
	if s == nil || p < 0 || p >= len(s.Players) {
		return all
	}
	player := &s.Players[p]
	for _, res := range [...]Res{Energy, Metal} {
		totals := s.gatherResourceTotals(p, res, w)
		all[res] = totals
		player.AIProduction[res] = totals.production
		player.AIConsumption[res] = totals.requested
	}
	player.aiAggregatesPrepared = true
	return all
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
// The pool and ratios are narrowed at their live-field stores; carry
// apply-back follows the working-precision operand order [R-ECO-01 §5].
func (s *Service) settleOneResource(p int, res Res, w *units.World, totals resourceTotals) {
	if s == nil || p < 0 || p >= len(s.Players) {
		return
	}
	player := &s.Players[p]

	opening := player.Stock[res]
	pool, debtRatio, _, acceptRatio, closingStock := settlePure(opening, totals.production, totals.debt, totals.accepted)

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
			acceptedTerm := float64(acc) - float64(acceptRatio)*float64(acc)
			carryTerm := float64(old) - float64(debtRatio)*float64(old)
			ue.Buckets[res].Carry = float32(acceptedTerm + carryTerm)
			ue.Archived[res] = ArchivedBucket{Production: ue.Buckets[res].Production, Requested: ue.Buckets[res].Requested}
			clearPassInputs(&ue.Buckets[res])
		})
	}
	// The player mirror is applied after all alive unit buckets in slot order.
	acceptedTerm := float64(player.Mirror[res].Accepted) - float64(acceptRatio)*float64(player.Mirror[res].Accepted)
	carryTerm := float64(player.Mirror[res].Carry) - float64(debtRatio)*float64(player.Mirror[res].Carry)
	player.Mirror[res].Carry = float32(acceptedTerm + carryTerm)
	player.ArchivedMirror[res] = ArchivedBucket{Production: player.Mirror[res].Production, Requested: player.Mirror[res].Requested}
	clearPassInputs(&player.Mirror[res])

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
//  3. gather both resources in stable slot order, then commit per-pass
//     counters and cumulative totals before either pool is formed [05
//     "Stocks, counters, and waste"] C6 C10;
//  4. settle each resource independently, energy then metal, apply back to
//     alive units then the mirror, and finally clamp stock and accrue waste
//     [05 "Two-stage settlement algorithm"] C8 C9 [05 "Stocks, counters, and
//     waste"] C10.
//
// w may be nil only in fixtures that exercise the mirror arithmetic alone; the
// per-unit sums then degenerate to the mirror, which is what a player with no
// units settles to anyway.
func (s *Service) Settle(p int, tick uint32, w *units.World) {
	if s == nil || p < 0 || p >= len(s.Players) {
		return
	}
	s.Players[p].aiAggregatesPrepared = false
	// 1. Capacity is rebuilt from scratch each pass [05 "Storage capacity"] C14.
	if w != nil {
		rebuildCapacityPlayer(s, p, w)
	}
	// 1b. Per-unit production fills (maker stall, extractor, passive, negative refund).
	// It runs before pool sums so produced amounts are included in totalProduction for two-stage ratios.
	s.PerUnitProductionFills(p, w)
	// 2. Cloak upkeep debits live stock BEFORE the pool is formed, in unit slot
	// order, so an earlier unit's debit can starve a later one [05 "Cloak
	// debit"] C13.
	if s.CloakCost != nil {
		ApplyCloakDebits(s, w, p, s.CloakCost, nil, nil)
	}
	// 3. Gather both resource totals, then commit pass counters before either
	// resource forms its pool [R-ECO-01 §6].
	totals := s.preparePassAggregates(p, w)
	s.Players[p].commitPassCounters()
	// 4. Energy and metal settle independently; there is no combined shortage
	// ratio [05 "Two-stage settlement algorithm"] C8. Energy goes first because
	// that is the documented aggregation order — the gather walks energy
	// production, requested, accepted, carry and only then the metal four
	// [05 "Authoritative settlement order"]. The two are independent, so the
	// order is not observable today; it is written this way so it stays right
	// when the metal-production gate on energy carry lands.
	s.settleOneResource(p, Energy, w, totals[Energy])
	s.settleOneResource(p, Metal, w, totals[Metal])
	// 5. Clamp only after both resources have closed and their apply-back has
	// archived/cleared live inputs [05 "Stocks, counters, and waste"] C10.
	s.Players[p].commitCapacityWaste()
}
