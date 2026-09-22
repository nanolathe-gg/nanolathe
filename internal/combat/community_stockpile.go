package combat

import "github.com/nanolathe-gg/nanolathe/internal/content"

// WeaponReloadTime is asked once while deriving a battle's immutable catalog.
// Only authored stockpile records pass through the Community loader clamp;
// the unarmed sentinel stays zero (community-patch-engine.md CP-DMG-5).
func (StrictRules) WeaponReloadTime(_ *Service, w *content.WeaponDef) int32 {
	if w == nil {
		return 0
	}
	return w.ReloadTime
}

func (CommunityRules) WeaponReloadTime(s *Service, w *content.WeaponDef) int32 {
	if s != nil && s.Community.BuildWeaponSlotGuard && w != nil && w.Stockpile && uint16(w.ReloadTime) == 0 {
		return 1
	}
	return (StrictRules{}).WeaponReloadTime(s, w)
}
