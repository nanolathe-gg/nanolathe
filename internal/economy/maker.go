package economy

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// MakerStall is the strict positive-carry gate for makers and extractors
// [R-PROD-01 §5].
func MakerStall(energyCarry float32) bool { return energyCarry > 0 }

// ExtractorProduction is an extractor's per-pass metal: its sampled spot
// yield, or nothing while the maker stall gate holds [R-PROD-01 §5].
func ExtractorProduction(spotMetal, energyCarry float32) float32 {
	if MakerStall(energyCarry) {
		return 0
	}
	return spotMetal
}

// MakerProduction uses the parsed unsigned maker byte as its amount
// [R-PROD-01 §5].
func MakerProduction(makesMetal int32, energyCarry float32) float32 {
	if makesMetal == 0 || MakerStall(energyCarry) {
		return 0
	}
	return float32(uint8(makesMetal))
}

// NegativeEnergyUseRefund returns the production credit for negative authored
// energy use. The special selector arithmetic is float-by-double and narrows
// once at the returned single-precision store [R-ECO-01 §3].
func NegativeEnergyUseRefund(energyUse float64, controllerState uint8, selector int) float32 {
	if energyUse >= 0 {
		return 0
	}
	amount := float32(-energyUse)
	if controllerState == 2 {
		switch selector {
		case 0:
			return float32(0 - float64(amount)*-0.5)
		case 1:
			return float32(0 - float64(amount)*-0.7)
		}
	}
	return amount
}

// InitShareThresholds applies the battle initializer's zero writes. These
// fields remain distinct from capacity and are not refreshed by settlement
// [R-SHARE-01 §3].
func InitShareThresholds(p *Player) {
	if p == nil {
		return
	}
	p.MetalShareThreshold = 0
	p.EnergyShareThreshold = 0
}

// WindScalar is the published wind strength generators multiply by, zero
// when no wind field is bound.
func (s *Service) WindScalar() float32 {
	if s == nil || s.Wind == nil {
		return 0
	}
	return s.Wind.Scalar
}

// TidalScalar is the map's tidal strength as a scalar, zero when no terrain
// is bound.
func (s *Service) TidalScalar() float32 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return float32(s.Terrain.Tidal) / 65536
}

// addContribution applies the retail positive-production discount. The two
// difficulty factors are double constants; production is narrowed only after
// the subtraction [R-ECO-01 §3].
func addContribution(s *Service, p *Player, b *Bucket, contribution float64) {
	if b == nil {
		return
	}
	c := float32(contribution)
	if p == nil || !p.Exists || p.ControllerState != 2 || c <= 0 {
		b.Production = float32(float64(b.Production) + float64(c))
		return
	}
	selector := 2
	if s != nil && s.EconomySelector != nil {
		selector = *s.EconomySelector
	}
	switch selector {
	case 0:
		b.Production = float32(float64(b.Production) - float64(c)*-0.5)
	case 1:
		b.Production = float32(float64(b.Production) - float64(c)*-0.7)
	default:
		b.Production = float32(float64(b.Production) + float64(c))
	}
}

// PerUnitProductionFills performs the per-unit gather in player-slice and
// unit-slot order [R-ECO-01 §2].
func (s *Service) PerUnitProductionFills(player int, w *units.World) {
	if s == nil || w == nil || player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		if u == nil || u.Handle == 0 || u.Def == nil {
			return
		}
		s.ensureUnitBuckets(u.Handle)
		ue := &s.unitBuckets[u.Handle]
		energy := &ue.Buckets[Energy]
		metal := &ue.Buckets[Metal]
		def := u.Def

		// The definition byte selects the building (zero) or mobile (non-zero)
		// branch. Buildings require activation; mobile upkeep runs while
		// activated or moving, but mobile units never reach a generator arm
		// [R-ECO-01 §2].
		buildingActive := !def.BMCode && u.Activated
		branchActive := buildingActive || (def.BMCode && (u.Activated || u.Move.Mode != 0))
		upkeepAdmitted := false
		if branchActive {
			switch {
			case def.EnergyUse < 0:
				selector := 2
				if s.EconomySelector != nil {
					selector = *s.EconomySelector
				}
				addContribution(nil, nil, energy, float64(NegativeEnergyUseRefund(def.EnergyUse, p.ControllerState, selector)))
				// A refund never admits the unit's production branch.
				upkeepAdmitted = false
			case def.EnergyUse >= 0:
				upkeepAdmitted = energy.Carry <= 0
				AdmitOneResource(&ue.Buckets, float32(def.EnergyUse))
			}
		}

		if buildingActive {
			switch {
			case def.ExtractsMetal > 0 && upkeepAdmitted:
				addContribution(s, p, metal, float64(ExtractorProduction(u.SpotMetal, energy.Carry)))
			case def.MakesMetal != 0 && upkeepAdmitted:
				addContribution(s, p, metal, float64(MakerProduction(def.MakesMetal, energy.Carry)))
			case def.ExtractsMetal <= 0 && def.MakesMetal == 0 && def.WindGenerator > 0:
				addContribution(s, p, energy, float64(s.WindScalar())*def.WindGenerator)
			case def.ExtractsMetal <= 0 && def.MakesMetal == 0 && def.TidalGenerator > 0:
				addContribution(s, p, energy, float64(s.TidalScalar())*def.TidalGenerator)
			}
		}

		if u.Remaining == 0 {
			// Passive production uses completion only, independent of the
			// operational bit [05 "Completed-unit eligibility"].
			addContribution(s, p, energy, def.EnergyMake)
			addContribution(s, p, metal, def.MetalMake)
		}

	})
}

// SetEconomySelector installs the economy selector the settlement reads,
// allocating it on first use.
func (s *Service) SetEconomySelector(v int) {
	if s == nil {
		return
	}
	if s.EconomySelector == nil {
		s.EconomySelector = new(int)
	}
	*s.EconomySelector = v
}

// RepairResourceTerm returns the per-tick heal step and its energy cost for
// one repairing worker.
func RepairResourceTerm(maxDamage, buildCostEnergy, worker, buildTime int32) (healTerm, resourceTerm int32) {
	if buildTime <= 0 {
		return 1, 1
	}
	healNumerator := int64(maxDamage)*int64(worker) - 1
	resourceNumerator := int64(buildCostEnergy)*int64(worker) - 1
	healTerm = int32(1 + healNumerator/int64(buildTime))
	resourceTerm = int32(1 + resourceNumerator/int64(buildTime))
	if healTerm < 1 && healNumerator >= 0 {
		healTerm = 1
	}
	if resourceTerm < 1 && resourceNumerator >= 0 {
		resourceTerm = 1
	}
	return
}

// AdmitRepair charges one repair tick against the builder's buckets and
// reports whether the charge was admitted.
func (s *Service) AdmitRepair(builderHandle pool.Handle, targetMaxDamage, targetBuildCostEnergy, worker, buildTime int32) bool {
	if s == nil || builderHandle == 0 {
		return false
	}
	s.ensureUnitBuckets(builderHandle)
	_, resourceTerm := RepairResourceTerm(targetMaxDamage, targetBuildCostEnergy, worker, buildTime)
	b := &s.unitBuckets[builderHandle].Buckets
	before := b[Energy].Accepted
	AdmitOneResource(b, float32(resourceTerm))
	return b[Energy].Accepted != before || resourceTerm == 0
}

// creditReclaimedMaterial is the production-credit form both reclaim payouts
// use, cloned from the two sites rather than from the shared contribution
// helper [05 R-ECO-01 §3][05 R-ECO-01 §11]:
//
//	if (!discounted)      production := float32( production + contribution )
//	else if (sel == 0)    production := float32( production - contribution * -0.5 )
//	else if (sel == 1)    production := float32( production - contribution * -0.7 )
//	else                  production := float32( production + contribution )
//
// Three properties of that body matter and are the reason it is written out
// here instead of calling addContribution:
//
//   - the subtraction and the multiply happen at the x87 working precision of
//     [R-ECO-01 §1] and the store is the ONLY narrowing. Scaling into a float32
//     first and adding afterwards — which this file's unit-reclaim refund used
//     to do — narrows twice and rounds differently, which §3 warns about
//     explicitly ("the factored form rounds differently");
//   - neither traced site tests the sign of the contribution, where
//     addContribution short-circuits a non-positive one to the plain add. No
//     shipped feature authors a negative pool [05 R-WORK-01 §5-A], so the
//     difference is unobservable on retail content, but cloning the guard here
//     would be cloning something the sites do not have;
//   - a selector this build has not been given is the undiscounted path, which
//     is also what the traced ladder does for any selector above one (hard).
func creditReclaimedMaterial(s *Service, b *Bucket, contribution float32, discounted bool) {
	if b == nil {
		return
	}
	selector := 2
	if s != nil && s.EconomySelector != nil {
		selector = *s.EconomySelector
	}
	if discounted {
		switch selector {
		case 0:
			b.Production = float32(float64(b.Production) - float64(contribution)*-0.5)
			return
		case 1:
			b.Production = float32(float64(b.Production) - float64(contribution)*-0.7)
			return
		}
	}
	b.Production = float32(float64(b.Production) + float64(contribution))
}

// specialPlayerSlot reports whether a player slot takes the difficulty-scaled
// production path: the slot's record must exist and its control byte must be 2,
// the computer-controlled value [05 R-ECO-01 §3][05 R-ECO-01 §11]. An owner
// outside the ten slots is not a record and takes the plain path.
func (s *Service) specialPlayerSlot(owner uint8) bool {
	if s == nil || int(owner) >= len(s.Players) {
		return false
	}
	p := &s.Players[owner]
	return p.Exists && p.ControllerState == 2
}

// CreditFeatureReclaim is the credit half of the feature-reclaim payout
// [05 R-WORK-01 §5]: the feature definition's WHOLE energy and metal values are
// added to the reclaiming builder's production accumulators, neither passing
// through an admission helper, as one completion event.
//
// Correction (PT3-05 follow-up). Both additions used to be made with a NIL
// player record, so the computer player's difficulty discount was never applied
// to reclaimed trees, rocks or wrecks, while the sibling unit-reclaim refund
// below did apply it — two credit paths for reclaimed material, one adjusted
// and one not. The trace settles it: the payout reads the builder's player
// record, tests that the record exists and that its control byte is 2, and
// routes EACH of the two additions through the same selector ladder as every
// other member of the family, with the same pairing (selector 0 halves,
// selector 1 takes seven tenths, anything else is the plain add). The omission
// was a defect, not a faithful reading [05 R-ECO-01 §11].
//
// Retail credits ENERGY first and metal second. The two buckets are
// independent, so the order is not observable; it is matched anyway because
// nothing is gained by differing.
//
// builderOwner is required rather than optional on purpose. An owner that a
// caller may omit is an owner a future call site omits by accident, and the
// omission is silent: the credit simply lands on the undiscounted arm, which is
// the exact defect this correction removed. A caller that genuinely has no
// player record — none exists in this build — would name a slot outside the
// ten, which specialPlayerSlot already reports as not special.
func (s *Service) CreditFeatureReclaim(builderHandle pool.Handle, builderOwner uint8, metal, energy float32) {
	if s == nil || builderHandle == 0 {
		return
	}
	s.ensureUnitBuckets(builderHandle)
	b := &s.unitBuckets[builderHandle].Buckets
	discounted := s.specialPlayerSlot(builderOwner)
	creditReclaimedMaterial(s, &b[Energy], energy, discounted)
	creditReclaimedMaterial(s, &b[Metal], metal, discounted)
}

// CreditUnitReclaimRefund is the death-side metal refund of a lethal cause-5
// reclaim pulse [05 "Unit reclaim"]: `(1 - victim remaining) x victim metal
// build cost`, paid to the killing builder's metal production accumulator, with
// no energy counterpart.
//
// Correction (PT3-05 follow-up). The discount is unchanged in substance — the
// gate is the killer's control byte 2 and the pairing is selector 0 to a half,
// selector 1 to seven tenths, which the trace confirms is NOT inverted here —
// but the arithmetic was not the traced one. It scaled the refund into a
// float32 and then added that, narrowing twice; the site subtracts
// `contribution x K` from the accumulator in one expression and stores once.
// Both reclaim payouts now share that single form.
func (s *Service) CreditUnitReclaimRefund(killerHandle pool.Handle, victimRemaining float32, victimBuildCostMetal int32, killerController uint8) {
	if s == nil || killerHandle == 0 {
		return
	}
	s.ensureUnitBuckets(killerHandle)
	refund := float32((1 - victimRemaining) * float32(victimBuildCostMetal))
	b := &s.unitBuckets[killerHandle].Buckets[Metal]
	creditReclaimedMaterial(s, b, refund, killerController == 2)
}

// AdmitStockpile charges one stockpile tick against the builder's buckets
// and reports whether the charge was admitted.
func (s *Service) AdmitStockpile(builderHandle pool.Handle, energyDelta, metalDelta float32) bool {
	if s == nil || builderHandle == 0 {
		return false
	}
	s.ensureUnitBuckets(builderHandle)
	b := &s.unitBuckets[builderHandle].Buckets
	beforeE, beforeM := b[Energy].Accepted, b[Metal].Accepted
	AdmitTwoResource(b, energyDelta, metalDelta)
	return b[Energy].Accepted != beforeE || b[Metal].Accepted != beforeM || (energyDelta == 0 && metalDelta == 0)
}
