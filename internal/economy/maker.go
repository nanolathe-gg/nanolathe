package economy

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// MakerStall is the strict positive-carry gate for makers and extractors
// [R-PROD-01 §5].
func MakerStall(energyCarry float32) bool { return energyCarry > 0 }

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

func (s *Service) WindScalar() float32 {
	if s == nil || s.Wind == nil {
		return 0
	}
	return s.Wind.Scalar
}

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

func (s *Service) SetEconomySelector(v int) {
	if s == nil {
		return
	}
	if s.EconomySelector == nil {
		s.EconomySelector = new(int)
	}
	*s.EconomySelector = v
}

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

func (s *Service) CreditFeatureReclaim(builderHandle pool.Handle, metal, energy float32) {
	if s == nil || builderHandle == 0 {
		return
	}
	s.ensureUnitBuckets(builderHandle)
	b := &s.unitBuckets[builderHandle].Buckets
	addContribution(s, nil, &b[Metal], float64(metal))
	addContribution(s, nil, &b[Energy], float64(energy))
}

func (s *Service) CreditUnitReclaimRefund(killerHandle pool.Handle, victimRemaining float32, victimBuildCostMetal int32, killerController uint8) {
	if s == nil || killerHandle == 0 {
		return
	}
	s.ensureUnitBuckets(killerHandle)
	refund := float32((1 - victimRemaining) * float32(victimBuildCostMetal))
	b := &s.unitBuckets[killerHandle].Buckets[Metal]
	if killerController == 2 && s.EconomySelector != nil {
		switch *s.EconomySelector {
		case 0:
			refund = float32(0 - float64(refund)*-0.5)
		case 1:
			refund = float32(0 - float64(refund)*-0.7)
		}
	}
	b.Production = float32(float64(b.Production) + float64(refund))
}

func StockpileCostDeltaForTick(oldProg, newProg int32, cost float64, buildTime int32) float32 {
	if buildTime <= 0 {
		return float32(cost)
	}
	oldTrunc := int32(float64(oldProg) * cost / float64(buildTime))
	newTrunc := int32(float64(newProg) * cost / float64(buildTime))
	return float32(newTrunc - oldTrunc)
}

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
