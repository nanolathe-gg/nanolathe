package session

import (
	"github.com/nanolathe/nanolathe/internal/economy"
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
