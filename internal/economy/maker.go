package economy

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// MakerStall reports whether metal maker/extractor stalls when energy carry >0 [P1-06].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func MakerStall(energyCarry float32) bool {
	return energyCarry > 0 // FCOMP 0.0 with <= test, stall when >0 [P1-06]
}

// ExtractorProduction returns spotMetal if not stalled, else 0 [P1-06].
func ExtractorProduction(spotMetal float32, energyCarry float32) float32 {
	if MakerStall(energyCarry) {
		return 0
	}
	return spotMetal
}

// MakerProduction returns 1.0 if not stalled, else 0 [P1-06].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func MakerProduction(makesMetal int32, energyCarry float32) float32 {
	if makesMetal == 0 {
		return 0
	}
	if MakerStall(energyCarry) {
		return 0
	}
	return 1.0 // float32 1.0 production per pass [P1-06]
}

// NegativeEnergyUseRefund computes refund for negative energyUse [P1-06].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Ledger site: selector 0 => -0.5, 1 => -0.7; inverted vs factory site [P0-14][P1-06].
// selector 0 => -0.5, selector 1 => -0.7, other => plain add.
// Returns amount to add to Production (may be negative scaled).
func NegativeEnergyUseRefund(energyUse float64, controllerState uint8, selector int) float32 {
	if energyUse >= 0 {
		return 0
	}
	f := float32(-energyUse) // -energyUse positive
	if controllerState == 2 {
		switch selector {
		case 0:
			return f * -0.5 // FMUL double -0.5 at _004FC488 [P1-06]
		case 1:
			return f * -0.7 // -0.7 at _004FC480
		default:
			return f
		}
	}
	return f
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func InitShareThresholds(p *Player) {
	if p == nil {
		return
	}
	p.MetalShareThreshold = p.Capacity[Metal]
	p.EnergyShareThreshold = p.Capacity[Energy]
}

// WindScalar returns the normalized wind scalar published to generators
// [01 §7.3] [05 "Wind generation"] [P1-I04] per I2 float32.
func (s *Service) WindScalar() float32 {
	if s == nil || s.Wind == nil {
		return 0
	}
	return s.Wind.Scalar
}

// TidalScalar returns the map tidal strength as float32
// [03 §2.2] [05 "Tidal generation"] [P1-I04].
func (s *Service) TidalScalar() float32 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return float32(s.Terrain.Tidal) / 65536
}

// PerUnitProductionFills fills per-unit production and consumption buckets before
// settlement sums [05 "Unit instance economy state"] [P1-06] [P1-I04].
// It binds every authored economy definition to the one ledger:
// activation/on-off, storage via RebuildCapacity, extraction with SpotMetal,
// tidal/wind generation, metal makers, passive makes, and energyUse.
// Called from Settle before two-stage sums to preserve stable order and
// float32 intermediates [I2] [05 "Authoritative settlement order"] C7.
func (s *Service) PerUnitProductionFills(player int, w *units.World) {
	if s == nil || w == nil {
		return
	}
	if player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	// Deterministic slot-order visitation per [05 "Authoritative settlement order"] C6 and I1.
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		h := u.Handle
		if h == 0 {
			return
		}
		s.ensureUnitBuckets(h)
		ue := &s.unitBuckets[h]
		bEnergy := &ue.Buckets[Energy]
		bMetal := &ue.Buckets[Metal]
		def := u.Def
		if def == nil {
			return
		}
		// Gate: incomplete, dead or disabled units contribute nothing [05 "Completed-unit eligibility"] [P1-I04].
		// EconomyActive checks Remaining==0 and, for OnOffable, Activated.
		isActive := u.EconomyActive()
		// Negative energyUse refund is also gated on active — a half-built
		// plant should not give refund.
		if def.EnergyUse < 0 && isActive {
			sel := 0
			if s.EconomySelector != nil {
				sel = *s.EconomySelector
			}
			refund := NegativeEnergyUseRefund(def.EnergyUse, p.ControllerState, sel)
			if refund != 0 {
				bEnergy.Production += refund // signed FADD [P1-06]
			}
		} else if def.EnergyUse > 0 && isActive {
			// Positive energyUse is a consumption demand: always add to
			// requested, and to accepted only when energy carry non-positive
			// [05 "One-resource admission"] [P1-I04].
			AdmitOneResource(&ue.Buckets, float32(def.EnergyUse))
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Both require the unit to be active (complete and on), otherwise no output.
		if isActive {
			if def.ExtractsMetal > 0 {
				prod := ExtractorProduction(u.SpotMetal, bEnergy.Carry)
				if prod != 0 {
					bMetal.Production += prod // float32 per I2
				}
			} else if def.MakesMetal != 0 {
				prod := MakerProduction(def.MakesMetal, bEnergy.Carry)
				if prod != 0 {
					bMetal.Production += prod
				}
			}
		}
		// Passive energy/metal and wind/tidal require the unit to be active
		// [05 "Completed-unit eligibility"] [05 "Resource contributions"] [P1-I04].
		if isActive {
			if def.EnergyMake != 0 {
				bEnergy.Production += float32(def.EnergyMake)
			}
			if def.MetalMake != 0 {
				bMetal.Production += float32(def.MetalMake)
			}
			// Wind generation: scalar × multiplier [05 "Wind generation"] [P1-I04].
			// Requires operational (active) per [05 "Resource contributions"].
			if def.WindGenerator != 0 {
				scalar := s.WindScalar()
				if scalar != 0 {
					bEnergy.Production += float32(def.WindGenerator) * scalar
				}
			}
			// Tidal generation: map tidal strength × multiplier [05 "Tidal generation"] [P1-I04].
			if def.TidalGenerator != 0 {
				scalar := s.TidalScalar()
				if scalar != 0 {
					bEnergy.Production += float32(def.TidalGenerator) * scalar
				}
			}
		}
	})
}

// EconomySelector holds global mode selector at 0x37EEE for negative refund discount [P1-06].
// 0 => -0.5, 1 => -0.7, other => plain. Separate from factory ModeSelector (inverted pairing).
func (s *Service) SetEconomySelector(v int) {
	if s == nil {
		return
	}
	if s.EconomySelector == nil {
		s.EconomySelector = new(int)
	}
	*s.EconomySelector = v
}

// RepairResourceTerm computes the resource term for repair admission per [05 "Repair"].
// heal term = trunc(1 + (maxDamage*worker-1)/buildTime) and
// resource term = trunc(1 + (buildCostEnergy*worker-1)/buildTime) with truncation toward zero [01 §8] I3.
func RepairResourceTerm(maxDamage, buildCostEnergy, worker, buildTime int32) (healTerm, resourceTerm int32) {
	if buildTime <= 0 {
		return 1, 1
	}
	healTerm = int32(1 + (int64(maxDamage)*int64(worker)-1)/int64(buildTime))
	if healTerm < 1 {
		healTerm = 1
		// lower value survives per [05 "Repair"] — for positive inputs yielding >=1 we clamp to 1
		// but for malformed zero/negative we keep computed value
		if int64(maxDamage)*int64(worker)-1 < 0 {
			healTerm = int32(1 + (int64(maxDamage)*int64(worker)-1)/int64(buildTime))
		}
	}
	resourceTerm = int32(1 + (int64(buildCostEnergy)*int64(worker)-1)/int64(buildTime))
	if resourceTerm < 1 {
		if int64(buildCostEnergy)*int64(worker)-1 >= 0 {
			resourceTerm = 1
		}
	}
	return
}

// AdmitRepair admits a repair energy demand via the one-resource helper [05 "Repair"] [P1-I04].
// It always adds to requested and to accepted only when energy carry non-positive.
// Returns true if admitted (both carries non-positive), false if denied.
func (s *Service) AdmitRepair(builderHandle pool.Handle, targetMaxDamage, targetBuildCostEnergy, worker, buildTime int32) bool {
	if s == nil || builderHandle == 0 {
		return false
	}
	s.ensureUnitBuckets(builderHandle)
	ue := &s.unitBuckets[builderHandle]
	_, resourceTerm := RepairResourceTerm(targetMaxDamage, targetBuildCostEnergy, worker, buildTime)
	b := &ue.Buckets
	// AdmitOneResource records Requested always, Accepted when Carry<=0 [05 "One-resource admission"]
	beforeAccepted := b[Energy].Accepted
	AdmitOneResource(b, float32(resourceTerm))
	return b[Energy].Accepted != beforeAccepted || resourceTerm == 0
}

// CreditFeatureReclaim adds the one-time feature reclaim payout to the builder's
// production buckets [05 "Feature reclaim"] [P1-I04].
// It is the only writer for reclaim reward; it writes to Production, not directly to Stock,
// so the two-stage settlement and waste/overflow still apply.
func (s *Service) CreditFeatureReclaim(builderHandle pool.Handle, metal, energy float32) {
	if s == nil || builderHandle == 0 {
		return
	}
	if metal == 0 && energy == 0 {
		return
	}
	s.ensureUnitBuckets(builderHandle)
	ue := &s.unitBuckets[builderHandle]
	// TODO(question): special-player scaling for feature reclaim where required [05 "Feature reclaim"] remains open; plain add until closed.
	ue.Buckets[Metal].Production += metal
	ue.Buckets[Energy].Production += energy
}

// CreditUnitReclaimRefund adds the fatal unit-reclaim metal refund to the killer's
// metal production bucket at death finalization [05 "Unit reclaim"] [P1-I04].
// refund = (1 - victimRemaining) × victimBuildCostMetal, metal-only, before explosion/corpse.
func (s *Service) CreditUnitReclaimRefund(killerHandle pool.Handle, victimRemaining float32, victimBuildCostMetal int32, killerController uint8) {
	if s == nil || killerHandle == 0 {
		return
	}
	refund := float32(1-victimRemaining) * float32(victimBuildCostMetal)
	if refund == 0 {
		return
	}
	s.ensureUnitBuckets(killerHandle)
	ue := &s.unitBuckets[killerHandle]
	// Branch contains no energy credit [05 "Unit reclaim"].
	// Ordinary addition vs special scaling via selector [P1-06] — mirror CreditConstructionTermination scaling family.
	if killerController == 2 && s.EconomySelector != nil {
		switch *s.EconomySelector {
		case 0:
			ue.Buckets[Metal].Production += refund * -0.7
			return
		case 1:
			ue.Buckets[Metal].Production += refund * -0.5
			return
		}
	}
	ue.Buckets[Metal].Production += refund
}

// StockpileCostDeltaForTick computes the per-visit stockpile deltas via the
// difference of truncations [06 §11.1] [P1-I04].
// It is a thin wrapper around combat.StockpileCostDelta but exposed here to
// keep the ledger as the sole economy owner.
func StockpileCostDeltaForTick(oldProg, newProg int32, cost float64, buildTime int32) float32 {
	if buildTime <= 0 {
		return float32(cost)
	}
	oldTrunc := int32(float64(oldProg) * cost / float64(buildTime))
	newTrunc := int32(float64(newProg) * cost / float64(buildTime))
	return float32(newTrunc - oldTrunc)
}

// AdmitStockpile admits a stockpile visit's energy/metal deltas through the
// ordinary two-resource helper [05 "Two-resource admission"] [06 §11.1] [P1-I04].
// Returns true when both carries non-positive (admitted), false otherwise.
func (s *Service) AdmitStockpile(builderHandle pool.Handle, energyDelta, metalDelta float32) bool {
	if s == nil || builderHandle == 0 {
		return false
	}
	s.ensureUnitBuckets(builderHandle)
	ue := &s.unitBuckets[builderHandle]
	b := &ue.Buckets
	before := b[Energy].Accepted
	AdmitTwoResource(b, energyDelta, metalDelta)
	// AdmitTwoResource adds to Accepted only when both carries non-positive.
	// Detect admission by whether Accepted grew.
	return b[Energy].Accepted != before || b[Metal].Accepted != before || (energyDelta == 0 && metalDelta == 0)
}
