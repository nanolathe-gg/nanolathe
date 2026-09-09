package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

func (b *battleSession) rangesShown() bool {
	if b == nil {
		return false
	}
	if b.shell != nil {
		return b.shell.showRanges
	}
	return b.showRanges
}

// snapshotRanges combines immutable authored radii with the published slot
// flags. The overlay consumers narrow each field independently [04 R-SPEC-01]
// [07 R-P0-11 §3]; visibility does not gate these diagnostic rings.
func (b *battleSession) snapshotRanges(v frame.UnitView) (hud.RangeSet, bool) {
	if b == nil || b.cat == nil {
		return hud.RangeSet{}, false
	}
	d, ok := b.cat.Unit(v.DefName)
	if !ok || d == nil {
		return hud.RangeSet{}, false
	}
	r := hud.RangeSet{
		MinCloak: int32(int16(d.MinCloakDistance)), Sight: int32(int16(d.SightDistance)),
		Radar: int32(int16(d.RadarDistance)), Sonar: int32(int16(d.SonarDistance)),
		RadarJam: int32(int16(d.RadarDistanceJam)), SonarJam: int32(int16(d.SonarDistanceJam)),
		BuildDistance: int32(uint16(d.BuildDistance)), Maneuver: int32(uint16(d.ManeuverLeashLength)),
		KamikazeDistance: int32(uint16(d.KamikazeDistance)), AttackRunLength: int32(uint16(d.AttackRunLength)),
		Kamikaze: d.Kamikaze, HasMover: d.BMCode == 1,
		ExplosionResolved: d.ExplodeAsDef != nil,
	}
	if d.ExplodeAsDef != nil {
		r.ExplosionAreaOfEffect = int32(uint16(d.ExplodeAsDef.AreaOfEffect))
	}
	for i, w := range [3]*content.WeaponDef{d.Weapon1Def, d.Weapon2Def, d.Weapon3Def} {
		if w != nil {
			r.Weapons[i] = hud.RangeWeapon{
				Enabled: v.EnabledWeaponSlots[i], Range: w.Range, Coverage: w.Coverage,
				AreaOfEffect: int32(uint16(w.AreaOfEffect)),
			}
		}
	}
	return r, true
}
