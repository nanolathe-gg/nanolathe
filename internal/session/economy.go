package session

import (
	"github.com/nanolathe/nanolathe/internal/economy"
)

// InitShareThresholds initializes per-player sharing thresholds from rebuilt
// capacity once at battle setup. Thresholds are distinct from capacity and are
// read by automatic sharing and resource-bar colouring; they are not aliases
// [05 "Allied resource and sensor sharing"].
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
