package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rules is the gameplay policy seam of the combat service. Strict 3.1 answers
// exactly as retail; Modern carries the approved Nanolathe policies of
// docs/DESIGN_WEAPONS_PROJECTILES.md §2.3.1 and §2.6.1. Each method is asked at
// the same retail call site the policy's projected boolean used to guard, so
// binding a rule set moves no pipeline logic; it only changes who answers.
//
// An implementation is either zero size or used by pointer, and no answer may
// draw from a stream it was not handed, so a bound rule set costs one indirect
// call and no allocation per question [I4].
type Rules interface {
	// AdmitShot reports whether one resolved fire attempt may launch.
	// StrictRules returns true without work: retail admits the shot and never
	// samples terrain [06 R-WPN-05 §1]. ModernRules runs the
	// DESIGN_WEAPONS_PROJECTILES §2.3.1 preview and records its verdict on the
	// query.
	AdmitShot(q *ShotQuery) bool

	// HoldsFire reports whether the shooter's standing Hold Fire suppresses aim
	// dispatch and burst remainders (DESIGN_WEAPONS_PROJECTILES §2.6.1).
	// StrictRules returns false: the retail standing-fire readers let an
	// already assigned target fire [04 R-STANCE-01 §2] [04 R-STANCE-01 §3].
	HoldsFire(u *units.Unit) bool
}

// ShotQuery is one resolved fire attempt put to Rules.AdmitShot. It carries
// exactly what the pipeline knows at the admission point: the post-spread
// per-shot slot copy, the muzzle the synchronous query returned, the resolved
// aim point, the current tick, the map terrain, the resolved unit target (nil
// for a point target) and the session wind whose phase-8 deadline bounds future
// wind knowledge [01 §7.3].
//
// The service reuses its own query storage and saves/restores it across nested
// fire attempts, so interface dispatch allocates nothing per shot. Rules must
// not retain the query. Launch, Muzzle and Aim are filled by the
// spawner once the muzzle query and accuracy spread have run [06 §4.4]; Blocked
// is the answer's output, read by the caller after the attempt returns.
type ShotQuery struct {
	// Launch is the pipeline's per-shot slot copy, carrying the spread-adjusted
	// angles and the ballistic distance word the preview re-solves from.
	Launch Slot
	// Muzzle is the FORCED fire-time muzzle point, not the aim origin
	// [06 §4.1].
	Muzzle Vec3
	// Aim is the resolved aim point the creators derive yaw and pitch from
	// [06 §6.3].
	Aim  Vec3
	Tick uint32

	Terrain *world.Terrain
	// Target is the resolved unit target, or nil for a point target. Only its
	// current geometry is protected; the policy promises nothing about future
	// movement.
	Target *units.Unit
	Wind   *world.Wind

	// Blocked reports that the rule set proved terrain obstruction before the
	// resolved target. A refused launch keeps the relative Aim pair, so the
	// caller reads this to decide whether to copy the slot's angles back
	// [06 R-WPN-05 §4].
	Blocked bool
}

// StrictRules is the retail rule set: every question answers as the executable
// does, with no work and no state. It is zero size, so converting a value to
// Rules never allocates.
type StrictRules struct{}

// AdmitShot admits every resolved attempt. Retail's admission gate consults
// range, medium and aim readiness and never samples terrain along the flight
// path [06 R-WPN-05 §1].
func (StrictRules) AdmitShot(*ShotQuery) bool { return true }

// HoldsFire never suppresses. Retail's standing-fire readers let an already
// assigned explicit target fire and allow forced guard combat joins
// [04 R-STANCE-01 §2] [04 R-STANCE-01 §3].
func (StrictRules) HoldsFire(*units.Unit) bool { return false }

// strictRules is the shared retail answer handed back for an unbound service.
// Holding the interface value in a package variable keeps the substitution off
// the per-question path entirely.
var strictRules Rules = StrictRules{}

// rules returns the service's bound rule set. A nil field is Strict 3.1, so a
// fixture that builds a Service without rules keeps the retail path.
func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}

// shotPreviewer is the optional extension a rule set implements when its
// AdmitShot inspects the post-spread launch copy rather than answering from the
// attempt alone. The spawner then runs the accuracy spread on value copies, so
// a refusal leaves the shared simulation stream and the slot's stored angles
// exactly where retail left them [06 §4.4]. A rule set that does not implement
// it is never asked to preview and pays nothing: the retail path builds no
// query and takes no copies.
type shotPreviewer interface {
	previewsShot() bool
}

// previewsShot reports whether the bound rule set wants the speculative
// preview described on shotPreviewer.
func previewsShot(r Rules) bool {
	p, ok := r.(shotPreviewer)
	return ok && p.previewsShot()
}
