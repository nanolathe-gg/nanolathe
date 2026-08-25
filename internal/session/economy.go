package session

import (
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// InitShareThresholds initializes per-player sharing thresholds from rebuilt
// capacity once at battle setup per [05 "Allied resource and sensor sharing"]
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// automatic sharing (60-tick) and resource-bar colouring; they are not aliases.
func (s *Session) InitShareThresholds() {
	if s == nil || s.Econ == nil || s.Units == nil {
		return
	}
	economy.RebuildCapacity(s.Econ, s.Units)
	for i := 0; i < 10; i++ {
		if s.Econ.Players[i].Exists {
			economy.InitShareThresholds(&s.Econ.Players[i])
		}
	}
}

// CreditFeatureReclaim credits a feature reclaim completion payout to the
// builder's production buckets [05 "Feature reclaim"] [P1-I04].
// It is the sole ledger entry for reclaim reward; it writes to Production
// so the two-stage settlement and waste/overflow still apply. Direct Stock
// mutation outside the ledger is forbidden except for CreditSpawn (spawn) per P1-I04.
func (s *Session) CreditFeatureReclaim(builder pool.Handle, metal, energy float32) {
	if s == nil || s.Econ == nil {
		return
	}
	s.Econ.CreditFeatureReclaim(builder, metal, energy)
}

// CreditUnitReclaimRefund credits the fatal unit-reclaim metal refund at death
// finalization [05 "Unit reclaim"] [P1-I04]. Payment is metal-only, before
// explosion/corpse, via the killer's production bucket.
func (s *Session) CreditUnitReclaimRefund(killer pool.Handle, victimRemaining float32, victimBuildCostMetal int32) {
	if s == nil || s.Econ == nil {
		return
	}
	var ctrl uint8
	if killer != 0 && s.Units != nil {
		if u := s.Units.Unit(killer); u != nil {
			ctrl = u.Owner
			// ControllerState lives on the player, not the unit; map via Owner index.
			if int(u.Owner) >= 0 && int(u.Owner) < 10 {
				ctrl = s.Econ.Players[u.Owner].ControllerState
			}
		}
	}
	s.Econ.CreditUnitReclaimRefund(killer, victimRemaining, victimBuildCostMetal, ctrl)
}

// AdmitRepair admits a repair energy demand via the one-resource helper
// [05 "Repair"] [P1-I04]. The heal term is emitted as kind-10 healing outside
// the ledger; the resource term is admitted here.
func (s *Session) AdmitRepair(builder pool.Handle, targetMaxDamage, targetBuildCostEnergy, worker, buildTime int32) bool {
	if s == nil || s.Econ == nil {
		return false
	}
	return s.Econ.AdmitRepair(builder, targetMaxDamage, targetBuildCostEnergy, worker, buildTime)
}

// AdmitStockpile admits a stockpile visit's deltas through the two-resource
// helper [05 "Stockpile production"] [06 §11.1] [P1-I04].
func (s *Session) AdmitStockpile(builder pool.Handle, energyDelta, metalDelta float32) bool {
	if s == nil || s.Econ == nil {
		return false
	}
	return s.Econ.AdmitStockpile(builder, energyDelta, metalDelta)
}

// SetUnitActivated toggles activation for OnOffable units [05] [P1-I04].
// It drives the operational bit that gates wind/tidal/passive production and
// energyUse consumption. Non-OnOffable units are always active when complete.
func (s *Session) SetUnitActivated(h pool.Handle, on bool) {
	if s == nil || s.Units == nil {
		return
	}
	if u := s.Units.Unit(h); u != nil {
		u.SetActivated(on)
	}
}

// SetUnitCloaked toggles cloak state for upkeep debit [05 "Cloak debit"] [P1-I04].
func (s *Session) SetUnitCloaked(h pool.Handle, on bool) {
	if s == nil || s.Units == nil {
		return
	}
	if u := s.Units.Unit(h); u != nil {
		u.SetCloaked(on)
	}
}
