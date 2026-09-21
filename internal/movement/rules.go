package movement

import "github.com/nanolathe-gg/nanolathe/internal/units"

// Rules is the movement system's gameplay seam. It carries one policy:
// whether a ground mover that static ground rejected teaches its owner about
// the ground the route search had taken for unexplored
// (docs/DESIGN_MOVEMENT_PATH.md "Modern learned terrain"). Strict keeps the
// retail loop, in which a rejection changes no search input and a block behind
// unmapped ground never resolves [04 R-MOV-01 §7][04 R-COLL-01 §3].
//
// No other seam owns these two questions. Both are asked by this package's own
// algorithms — the occupancy commit and the request open — about state this
// package owns, and neither is an order decision, a construction decision or a
// choice of search kernel; the kernel still opens the same retail search over
// the same passability port [docs/DESIGN_GAMEPLAY_RULES.md §9 step 3].
//
// The implementation is chosen once when the session binds a rule set. Both
// questions are asked at request granularity — once per rejected commit and
// once per opened search — and never per expanded node. Every implementation
// is a zero-size value or a pointer to one, so dispatch allocates nothing; the
// learned grid belongs to the System.
type Rules interface {
	// StaticRejection is called for a ground mover's rejected commit when the
	// first failing footprint cell failed the static ground test — terrain or
	// a blocking feature — rather than the occupant test [04 R-COLL-01 §2].
	// anchor is the proposed footprint anchor and footX, footZ the footprint
	// the route search reads passability with. It reports whether the mover's
	// owner learned something it did not know.
	//
	// An implementation may write only the System's learned grid. It must not
	// write a transform, occupancy, the visibility grids or resources, and
	// must not draw RNG.
	StaticRejection(s *System, u *units.Unit, anchor Cell, footX, footZ int16) bool

	// LearnedTerrain is called once per opened route search and returns the
	// learned grid that search consults behind the mapping word, or nil for
	// the retail read [04 R-PATH-01 §2].
	LearnedTerrain(s *System) *LearnedTerrain
}

// StrictRules is the retail baseline: nothing is learned and nothing learned
// is read, so a search's inputs are exactly retail's. It is zero size, so
// holding it in a Rules never allocates.
type StrictRules struct{}

// StaticRejection does nothing under Strict 3.1: retail records nothing about
// the cell that rejected a proposal [04 R-MOV-01 §7].
func (StrictRules) StaticRejection(*System, *units.Unit, Cell, int16, int16) bool { return false }

// LearnedTerrain is nil under Strict 3.1: the search reads the mapping word
// and nothing else [04 R-PATH-01 §2]. Knowledge a Modern session gathered
// before a switch is kept but not consulted.
func (StrictRules) LearnedTerrain(*System) *LearnedTerrain { return nil }

// ModernRules carries the approved learned-terrain policy. It is zero size and
// is held by pointer so a later set may embed it and override one answer.
type ModernRules struct{}

// StaticRejection teaches the owner the mapping blocks the route search reads
// for the rejected footprint and still calls unexplored.
func (*ModernRules) StaticRejection(s *System, u *units.Unit, anchor Cell, footX, footZ int16) bool {
	return s.learnRejectedFootprint(u, anchor, footX, footZ)
}

// LearnedTerrain hands the search the System's learned grid; nil until the
// first lesson, which leaves the read retail's.
func (*ModernRules) LearnedTerrain(s *System) *LearnedTerrain {
	if s == nil {
		return nil
	}
	return s.learned
}

// strictRules is the shared Strict value, converted to the interface once at
// package initialisation so the default path cannot allocate.
var strictRules Rules = StrictRules{}

// rules returns the bound seam. An unset field is the retail baseline, which
// keeps fixtures and a system reconstructed by a restore on the retail path.
func (s *System) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}
