package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// InitShareThresholds applies the battle initializer's zero writes to both
// per-player sharing thresholds, after rebuilding capacity so the settlement
// that follows sees it. The thresholds are "zeroed at battle setup and never
// written again in the reachable image" [05 R-SHARE-01 §3]; they are distinct
// fields, not aliases of capacity, and the automatic dispatcher's step-1
// compare is therefore `0 < stock`.
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
