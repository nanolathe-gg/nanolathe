package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rules is the construction service's gameplay seam. It carries the one
// decision that is not retail behaviour: whether a blocked product exit, a
// refused yard close or a blocked build site may ask idle units of the same
// player to walk away. Retail has no such request, so StrictRules answers
// with nothing at all [04 R-FAC-02 §5] [04 R-FAC-02 §6].
//
// The implementation is chosen once when the session binds a rule set; the
// call sites below never build a closure or select a mode per call. Every
// implementation is therefore a zero-size value or a pointer to
// session-lifetime state, so dispatch allocates nothing.
type Rules interface {
	// YieldObstruction is called on each blocked construction attempt with the
	// rectangle that must become clear: the product exit rectangle, the cells
	// a refused close selected, or the snapped build-site footprint.
	// closingYard is the yard-cell window of a refused close and is nil for
	// the other two; urgent marks the build-site caller, whose blocker route
	// is staged for priority. tick is the authoritative tick the resulting
	// order is created on.
	//
	// An implementation may only issue ordinary orders. It must not write
	// transforms, occupancy or resources, and must not draw RNG.
	YieldObstruction(s *Service, requester *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32, urgent bool)
}

// StrictRules is the retail baseline: no clearance request is ever made, so
// blocked production keeps the retail allocation retry and the refused close
// unchanged [04 R-ORDER-02 §1]. It is zero-size, so holding it in a Rules
// never allocates.
type StrictRules struct{}

// YieldObstruction does nothing under Strict 3.1: retail issues no clearance
// order, writes no state and draws no random number here.
func (StrictRules) YieldObstruction(*Service, *units.Unit, world.FootprintRect, []world.YardCell, uint32, bool) {
}

// strictRules is the shared Strict value, converted to the interface once at
// package initialisation so the default path cannot allocate.
var strictRules Rules = StrictRules{}

// rules returns the configured rule set. An unset field is the retail
// baseline, which keeps fixtures and headless composition Strict by default.
func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}
