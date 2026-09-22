package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// VeteranLevel applies authored thresholds only when the selected Community
// table enables CP-UD-1. Modern embeds this implementation; Strict never reads
// the parsed metadata.
func (CommunityRules) VeteranLevel(q VeteranLevelRequest) uint32 {
	if q.Service == nil || !q.Service.Community.Veterancy {
		return StrictRules{}.VeteranLevel(q)
	}
	return AuthoredVeteranLevel(q.Definition, q.Kills, q.Unbounded)
}

// VeteranLeadAdmitted uses a strict comparison with the first authored
// threshold. Compiled definitions always carry at least the default list; an
// empty malformed definition has no qualifying threshold.
func (CommunityRules) VeteranLeadAdmitted(q VeteranRequest) bool {
	if q.Service == nil || !q.Service.Community.Veterancy {
		return StrictRules{}.VeteranLeadAdmitted(q)
	}
	return q.Definition != nil && len(q.Definition.VeterancyThresholds) != 0 && uint32(q.Kills) > q.Definition.VeterancyThresholds[0]
}

// VeteranSpreadDivisor substitutes the authored rate. A nonpositive compiled
// rate produces zero and the caller retains the ordinary no-division branch.
func (CommunityRules) VeteranSpreadDivisor(q VeteranRequest) uint32 {
	if q.Service == nil || !q.Service.Community.Veterancy {
		return StrictRules{}.VeteranSpreadDivisor(q)
	}
	if q.Definition == nil || q.Definition.VeterancyAccuracyBuffRate <= 0 {
		return 0
	}
	return uint32(q.Kills) / uint32(q.Definition.VeterancyAccuracyBuffRate)
}

// AdmitTarget applies the Community weapon-target keys at the one physical
// gate shared by automatic acquisition, damage reaction and order binding.
// The feature flag and authored keys are both required; Strict therefore
// ignores the parsed metadata exactly as retail does. Community behavior:
// docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-1..3.
func (CommunityRules) AdmitTarget(q TargetAdmission) bool {
	if q.Service == nil || !q.Service.Community.WeaponTargetKeys || q.Weapon == nil {
		return StrictRules{}.AdmitTarget(q)
	}
	w := q.Weapon
	// nottoair is the first Community reject and deliberately wins over the
	// surfacefire bypass (research/extensions/community-patch-engine.md,
	// "CP-WPN-1 (B). nottoair = 1"). A landed aircraft remains eligible.
	if w.NotToAir && q.Target.UnitMode == airborneMoverMode {
		return false
	}
	if !w.WaterWeapon {
		return StrictRules{}.AdmitTarget(q)
	}
	// surfacefire bypasses both retail above-sea rejects in the water branch.
	if !w.SurfaceFire {
		if !q.Target.Floater && q.Target.Y > q.Sea {
			return false
		}
		if q.Target.CanHover && q.Target.Y+(q.Target.ModelTop>>1) > q.Sea {
			return false
		}
	}
	// All water-weapon allow paths converge here. Both operands have already
	// been truncated separately; equality with sea level is submerged.
	if w.NotToUnderwater && q.Target.Y+q.Target.ModelTop <= q.Sea {
		return false
	}
	return true
}

// SlotMayFire applies the firing-unit terrain tags after reload has counted
// down and before any target, aim, resource or RNG work. A negative terrain
// answer is the off-map sentinel and falls through unchanged. Community
// behavior: docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-4.
func (CommunityRules) SlotMayFire(s *Service, u *units.Unit, weapon *content.WeaponDef, terrain *world.Terrain) bool {
	if s == nil || !s.Community.WeaponTargetKeys || u == nil || weapon == nil || terrain == nil {
		return true
	}
	h := int32(terrain.HeightAt(u.X, u.Z) >> 16)
	if h < 0 {
		return true
	}
	water := h <= int32(terrain.SeaLevel)
	return !(weapon.NoOverWater && water || weapon.NoOverLand && !water)
}

// DetonationBroadcast suppresses only the damage/broadcast call for a tagged
// attacker-less zero-damage map weapon. Impact effects, projectile retirement
// and all other lifecycle work remain unchanged. The test order is damage,
// attacker, then tag as the source contract specifies. Community behavior:
// docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-5.
func (CommunityRules) DetonationBroadcast(s *Service, p *Projectile, weapon *content.WeaponDef) bool {
	if weapon == nil || weapon.DamageDefault != 0 || p == nil || p.Shooter != 0 {
		return true
	}
	return s == nil || !s.Community.WeaponTargetKeys || !weapon.NoMapWeaponAlert
}

// GuidanceAdmitted preserves propulsion and guidance at and above the water
// plane for every tagged self-propelled projectile, without consulting its
// target. Community behavior: docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-3.
func (CommunityRules) GuidanceAdmitted(s *Service, p *Projectile, weapon *content.WeaponDef, preMotionY int16, sea uint8) bool {
	if s != nil && s.Community.WeaponTargetKeys && weapon != nil && weapon.SurfaceFire {
		return true
	}
	return StrictRules{}.GuidanceAdmitted(s, p, weapon, preMotionY, sea)
}

// ShotTimeAdmitted preserves the range clause, then lets an enabled
// surfacefire tag take the success exit before the non-water shooter-depth
// and ballistic clauses. The source router tests the tag itself, so the
// short-circuit also applies when a tagged weapon is not a water weapon.
// Community behavior: docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-3.
func (CommunityRules) ShotTimeAdmitted(q ShotTimeAdmission) bool {
	if q.Shooter == nil || q.Weapon == nil {
		return false
	}
	if !WithinRange(q.Shooter.X, q.Shooter.Z, q.Target.X, q.Target.Z, q.Weapon.Range) {
		return false
	}
	if q.Service != nil && q.Service.Community.WeaponTargetKeys && q.Weapon.SurfaceFire {
		return true
	}
	return strictShotTimeAdmitsAfterRange(q)
}
