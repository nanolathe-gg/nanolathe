package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// BindStagedOrderQueue gives a directly staged unit the session-owned order
// queue a command would have bound for it. A capture fixture creates units
// outside the command boundary, where a fresh unit has no queue at all, so an
// order pushed at staging time would otherwise be dropped without a trace.
func (s *Session) BindStagedOrderQueue(u *units.Unit) { s.bindOrderQueue(u) }

// RevealStagedMap lifts the viewing player's fog at once, with the `+nowisee`
// command's own refresh, for a capture that publishes an opening frame before
// any tick could apply that command. It changes no gameplay rule.
func (s *Session) RevealStagedMap() {
	if s == nil || s.Vis == nil {
		return
	}
	mode := s.Vis.Mode() &^ (visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	eligible, observers := visibilityModeRefreshInputs(s, mode)
	s.Vis.RefreshMode(mode, true, eligible, observers)
}
