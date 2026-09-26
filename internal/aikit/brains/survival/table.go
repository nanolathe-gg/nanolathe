package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// defInfo is what the survival layer derives once per definition from the
// immutable catalog.
type defInfo struct {
	tower bool  // a land tower that fires at ground units
	aa    bool  // a land tower whose fire reaches aircraft
	wall  bool  // an unarmed wall piece (becomes a feature when finished)
	gdps  int32 // damage per second against ground units
}

// table indexes defInfo by UnitInfo.Index.
type table struct {
	d []defInfo
}

func (t *table) of(u *aikit.UnitInfo) *defInfo {
	if u == nil || int(u.Index) >= len(t.d) {
		return &defInfo{}
	}
	return &t.d[u.Index]
}

// build classifies every definition by its authored keys, never by a stock
// unit name, so any content set that authors the ordinary keys works.
func (t *table) build(k *aikit.Kit) {
	units := k.Table.Units
	t.d = make([]defInfo, len(units))
	for i, u := range units {
		def := u.Def
		if def == nil || u.Role.Has(aikit.RoleMobile) || def.BMCode != 0 {
			continue
		}
		land := def.MinWaterDepth <= 0
		d := &t.d[i]
		if def.IsFeature && u.DPS == 0 && land && !u.Role.Any(aikit.RoleEnergy|aikit.RoleExtractor|aikit.RoleMetalMaker|aikit.RoleStorage|aikit.RoleFactory|aikit.RoleRadar) {
			d.wall = true
			continue
		}
		if !u.Role.Has(aikit.RoleDefense) || !land {
			continue
		}
		d.gdps = groundDPS(def)
		d.tower = d.gdps > 0
		d.aa = u.AirDPS > 0
	}
}

// groundDPS is the sustained damage per second of a definition's weapons
// that can fire at ground units: not anti-air only, not interceptors, not
// water-only, not paralyzers, not a manually fired super-weapon. Integer,
// like UnitInfo.DPS.
func groundDPS(def *content.UnitDef) int32 {
	var dps int32
	for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if w == nil || content.IsWeaponInactive(w) || w.Interceptor || w.ToAirWeapon || w.WaterWeapon || w.Paralyzer {
			continue
		}
		dmg := int64(w.DamageDefault)
		if dmg <= 0 || dmg >= 4000 {
			continue
		}
		burst := max(int64(w.Burst), 1)
		reload := max(int64(w.ReloadTime), 1)
		dps += int32(dmg * burst * 30 / reload)
	}
	return dps
}

// cos1000 and sin1000 read the simulation's integer sine table, ×1000.
func cos1000(a uint32) int64 { return int64(numeric.Cos(numeric.Angle(uint16(a)))) * 1000 / 8192 }
func sin1000(a uint32) int64 { return int64(numeric.Sin(numeric.Angle(uint16(a)))) * 1000 / 8192 }
