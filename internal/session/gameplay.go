package session

import "github.com/nanolathe-gg/nanolathe/internal/gameplay"

// SetGameplay configures a detached session. Live adapters enqueue HumanGameplay
// so changes happen at the authoritative command boundary, before combat.
func (s *Session) SetGameplay(mode gameplay.Mode) {
	s.Gameplay = mode.Normalize()
	if s.Combat != nil {
		s.Combat.ModernTerrainAdmission = s.Gameplay == gameplay.Modern
	}
}
