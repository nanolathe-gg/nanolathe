package economy

import (
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

// PerUnitProductionFills fills per-unit production buckets before settlement sums [P1-06].
// Handles extractor/maker stall via energyCarry, passive metalmake/energymake when idle, negative energyUse refunds.
// Called from Settle before two-stage sums to preserve stable order and float32 intermediates [I2].
func (s *Service) PerUnitProductionFills(player int, w *units.World) {
	if s == nil || w == nil {
		return
	}
	if player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	for _, u := range w.Iter() {
		if u == nil || !u.Alive || u.Def == nil || int(u.Owner) != player {
			continue
		}
		h := u.Handle
		if h == 0 {
			continue
		}
		s.ensureUnitBuckets(h)
		ue := &s.unitBuckets[h]
		bEnergy := &ue.Buckets[Energy]
		bMetal := &ue.Buckets[Metal]
		def := u.Def
		// Negative energyUse refund path with discount [P1-06] — before extractor/maker.
		if def.EnergyUse < 0 {
			// selector global for economy site: use Service global selector? For now use 0 as default, but allow test to set via EconomySelector field.
			sel := 0
			if s.EconomySelector != nil {
				sel = *s.EconomySelector
			}
			refund := NegativeEnergyUseRefund(def.EnergyUse, p.ControllerState, sel)
			if refund != 0 {
				bEnergy.Production += refund // signed FADD [P1-06]
			}
		} else if def.EnergyUse > 0 {
			// Positive energyUse: recorded to Requested via admission; not production.
			// Handled via Admit path elsewhere; no direct production add.
		}
		// Extractor vs maker dispatch [P1-06] 02_ledger_exact §5.
		// If extractsMetal >0, use spotMetal stored at placement Σ(cell+1)*extractsMetal [P1-06][P1-15] else maker.
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
		// Passive metalmake/energymake when idle (Remaining==0) [P1-06].
		if u.Remaining == 0 {
			if def.EnergyMake != 0 {
				bEnergy.Production += float32(def.EnergyMake)
			}
			if def.MetalMake != 0 {
				bMetal.Production += float32(def.MetalMake)
			}
			// Wind/tidal generators: handled as float32 production; omitted for brevity but would add via WindGenerator * windScalar etc [P1-06].
			// TODO(question): wind/tidal exact scalar and storage bonus with flag 1 handling remains open [P1-06] but P1-06 establishes maker stall and negative fields.
		}
	}
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
