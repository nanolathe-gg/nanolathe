package session

import "github.com/nanolathe-gg/nanolathe/internal/units"

// BindStagedOrderQueue gives a directly staged unit the session-owned order
// queue a command would have bound for it. A capture fixture creates units
// outside the command boundary, where a fresh unit has no queue at all, so an
// order pushed at staging time would otherwise be dropped without a trace.
func (s *Session) BindStagedOrderQueue(u *units.Unit) { s.bindOrderQueue(u) }
