package session

import "github.com/nanolathe/nanolathe/internal/clock"

// SetPaused applies a local single-player pause at the session scheduling
// boundary. It changes only the clock gate; AdvanceSP therefore retains its
// anchor/carry and produces the established one capped burst on resume
// [01 §4.3][07 §11].
func (s *Session) SetPaused(paused bool) bool {
	if s == nil {
		return false
	}
	s.ensureClock()
	s.Clock.Paused = paused
	return s.Clock.Paused
}

// AdjustSpeed applies one local relative speed request. Requested and Active
// are both clamped to the full retail range 1..20, and a changed request is
// visible immediately at the scheduling boundary [01 §4.3][07 §11].
func (s *Session) AdjustSpeed(delta int) (speed int32, changed bool) {
	if s == nil {
		return 0, false
	}
	s.ensureClock()
	old := s.Clock.Requested
	if old < 1 {
		old = 1
	} else if old > 20 {
		old = 20
	}
	next := old + int32(delta)
	if next < 1 {
		next = 1
	} else if next > 20 {
		next = 20
	}
	if next == old {
		// Keep malformed legacy state normalized even when the requested value
		// is already at a clamp boundary.
		s.Clock.Requested = old
		return old, false
	}
	s.Clock.Requested = next
	s.Clock.Active = next
	return next, true
}

func (s *Session) ensureClock() {
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
}
