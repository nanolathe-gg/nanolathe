package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// prepareCommunityWeapons derives battle-local reload words before a unit
// world can bind a weapon. The shared catalog remains untouched. Like the
// patch's loader fix, this is an entry operation, not a live-switch mutation
// (community-patch-engine.md CP-DMG-5).
func prepareCommunityWeapons(cat *content.Catalog, mode gameplay.Mode, features community.Features) *content.Catalog {
	if cat == nil {
		return nil
	}
	rules := RuleSetForMode(mode).Combat
	owner := combat.Service{Community: features}
	changed := false
	for _, weapon := range cat.WeaponRecordsByID() {
		if rules.WeaponReloadTime(&owner, weapon) != weapon.ReloadTime {
			changed = true
			break
		}
	}
	if !changed {
		return cat
	}
	cat = cat.Clone()
	for _, weapon := range cat.WeaponRecordsByID() {
		weapon.ReloadTime = rules.WeaponReloadTime(&owner, weapon)
	}
	return cat
}
