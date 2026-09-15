package session

// PublishOpeningFrame freezes a fully loaded, unticked session for the authored
// opening (DESIGN_GPU_RENDERER §36). It uses ordinary publication without
// executing a phase, consuming RNG or advancing the scheduler. Normal startup
// keeps its existing first-tick publication; only the opt-in intro calls this.
func (s *Session) PublishOpeningFrame() bool {
	if s == nil || s.Snapshot == nil || s.Clock == nil || s.Clock.GlobalTick != 0 {
		return false
	}
	if _, published := s.Snapshot.PublishedTick(); !published {
		s.publishSnapshot(0)
	}
	_, published := s.Snapshot.PublishedTick()
	return published
}
