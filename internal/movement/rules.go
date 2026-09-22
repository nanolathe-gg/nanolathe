package movement

import "github.com/nanolathe-gg/nanolathe/internal/units"

// Rules is the movement system's gameplay seam. It carries the learned-terrain
// policy and the contested-cell claim answer. Strict keeps the retail blocked
// mover loop and owner-state claim rule; Community may select the stable
// lower-unit-index claim rule (docs/DESIGN_MOVEMENT_PATH.md "Community
// contested-cell claims") [04 R-MOV-01 §7][04 R-COLL-01 §3]
// (community-patch-engine.md CP-DMG-2).
//
// No other seam owns these decisions. They are asked by this package's own
// algorithms — the occupancy commit, rejected movement commit and request open
// — about state this package owns, and none is an order decision, construction
// decision or choice of search kernel; the kernel still opens the same retail
// search over the same passability port [docs/DESIGN_GAMEPLAY_RULES.md §9 step 3].
//
// The implementation is chosen when the session binds a rule set. ClaimConflict
// is asked per contested cell; the learned-terrain questions are asked once per
// rejected commit and once per opened search, never per expanded node. Every
// implementation is a zero-size value or a pointer to one, so dispatch allocates
// nothing; the learned grid belongs to the System.
type Rules interface {
	// ClaimConflict reports whether claimant displaces incumbent from one
	// contested occupancy cell. ArbitrateOverlap asks it for every ground,
	// air and building/yard stamp, including a restamp after a host vacates.
	// It may inspect the System's projected feature table and the incumbent's
	// owner state, but must not mutate either unit or draw RNG; the grid owns
	// the host/intruder effects after this answer.
	ClaimConflict(s *System, incumbent, claimant int) bool

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

// CommunityRules is the reserved Community 3.9 layer. It embeds StrictRules so
// every unchanged answer remains retail's; Community movement contracts
// override only the questions they own without changing the other layers.
type CommunityRules struct{ StrictRules }

// ClaimConflict preserves retail's owner-state branch under Strict 3.1.
func (StrictRules) ClaimConflict(s *System, incumbent, _ int) bool {
	return s != nil && s.Grid != nil && s.Grid.displaceable(incumbent)
}

// ClaimConflict uses the Community patch's unsigned unit-index tie-break when
// the resolved profile enables it. A strictly lower claimant wins; equality
// therefore keeps a self re-claim unchanged. Profiles that disable the feature
// retain the Strict answer (community-patch-engine.md CP-DMG-2).
func (CommunityRules) ClaimConflict(s *System, incumbent, claimant int) bool {
	if s == nil || !s.Community.GridClaimTieBreak {
		return (StrictRules{}).ClaimConflict(s, incumbent, claimant)
	}
	return uint16(claimant) < uint16(incumbent)
}

// StaticRejection does nothing under Strict 3.1: retail records nothing about
// the cell that rejected a proposal [04 R-MOV-01 §7].
func (StrictRules) StaticRejection(*System, *units.Unit, Cell, int16, int16) bool { return false }

// LearnedTerrain is nil under Strict 3.1: the search reads the mapping word
// and nothing else [04 R-PATH-01 §2]. Knowledge a Modern session gathered
// before a switch is kept but not consulted.
func (StrictRules) LearnedTerrain(*System) *LearnedTerrain { return nil }

// ModernRules carries the approved learned-terrain policy. It is zero size and
// is held by pointer so a later set may embed it and override one answer.
type ModernRules struct{ CommunityRules }

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
