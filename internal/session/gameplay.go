package session

import "github.com/nanolathe-gg/nanolathe/internal/gameplay"

// SetGameplay configures a detached session. Live adapters enqueue HumanGameplay
// so changes happen at the authoritative command boundary, before combat.
func (s *Session) SetGameplay(mode gameplay.Mode) {
	s.Gameplay = mode.Normalize()
	if s.Combat != nil {
		s.Combat.ModernTerrainAdmission = s.Gameplay == gameplay.Modern
		s.Combat.ModernHoldFire = s.Gameplay == gameplay.Modern
	}
	if s.Build != nil {
		s.Build.ModernConstructionClearance = s.Gameplay == gameplay.Modern
		if s.Build.OrderBinding != nil {
			s.Build.OrderBinding.ModernHoldFire = s.Gameplay == gameplay.Modern
			s.Build.OrderBinding.ModernBomberPass = s.Gameplay == gameplay.Modern
		}
	}
}
