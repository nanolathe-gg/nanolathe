// The order package's gameplay-rule seam. Every decision the central gameplay
// mode used to project onto the queue binding as a boolean is a method here,
// so the queue asks its rule set instead of reading a mode projection
// [docs/INVARIANTS.md I11].

package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Rules is the set of gameplay decisions the order package defers rather than
// deciding itself. Each policy owns its concrete decision site; transient
// observations and response state belong to Queue, never cached rule objects.
//
// Every method takes the concrete state its answer needs — the unit, the order
// record, the authoritative tick — and never a closure or a retained slice, so
// a monomorphic call site is one indirect call and no allocation. An
// implementation is either zero-size or a pointer to session-lifetime state.
// Strict 3.1 draws no randomness in any of them.
type Rules interface {
	// CrowdedMoveArrival admits bounded Modern completion near a friendly crowd.
	CrowdedMoveArrival(u *units.Unit, n *Node, tick uint32) bool
	// MarkAutomaticAttack records producer provenance only for Modern.
	MarkAutomaticAttack(n *Node)
	// PreserveAutomaticTarget avoids cancelling a same-target automatic handoff.
	PreserveAutomaticTarget(u *units.Unit, slot int) bool
	// GuardTarget replaces the stationary guard scan when handled is true.
	GuardTarget(u *units.Unit, n *Node) (target pool.Handle, handled bool)
	// RetaliationOrder is retail's immediate damage edge; Modern defers order
	// changes to its normal per-unit danger step.
	RetaliationOrder(victim, attacker *units.Unit) bool
	// ObserveDanger and StepDangerResponse own bounded, locally observed Modern
	// danger memory and response. Strict leaves all state untouched.
	ObserveDanger(victim, attacker *units.Unit, tick uint32)
	// ObserveImpact remembers an anonymous impact side, never an attacker identity.
	ObserveImpact(u *units.Unit, bearing numeric.Angle, tick uint32)
	StepDangerResponse(u *units.Unit, tick uint32)
	// ProtectWorkOnDamage preserves active construction/manual repair and the
	// original assignment behind a Modern autonomous response.
	ProtectWorkOnDamage(u *units.Unit) bool
	// AllowAutomaticRepair prevents repeated repair/resume cycles under fire.
	AllowAutomaticRepair(u *units.Unit, tick uint32) bool
	// ReactionResult bounds a Modern reaction failure to its own record.
	ReactionResult(q *Queue, n *Node, code Code, tick uint32) Code
	// BeforeCommand lets a new producer command supersede a Modern response.
	BeforeCommand(q *Queue)

	// HoldsFire reports whether the shooter's standing Hold Fire keeps automatic
	// combat off its weapon slots: it refuses a combat join, including the
	// guard's forced join whose force flag bypasses both retail standing-order
	// fields [04 R-STANCE-01 §3][04 R-UNIT-06 §1]; it declines the stationary
	// guard's takeover of an acquired target [04 R-ORD-01 §3]; and at the
	// standing-fire write it retires the automatic combat already running.
	HoldsFire(u *units.Unit) bool

	// DeferBomberLeash reports whether an accepted bombing pass may reach
	// release before its maneuver leash returns it to post. The target and
	// cancellation checks of the air entry still precede this decision
	// [04 R-AIR-01 §8][04 R-STANCE-01 §4].
	DeferBomberLeash(u *units.Unit, n *Node) bool

	// GuardSeeksPad reports whether a damaged aircraft guard borrowed the
	// patrol pad selection on this visit, keeping its ward and successors
	// [04 R-ORD-02 §3].
	GuardSeeksPad(u *units.Unit, n *Node, tick uint32) bool

	// GuardWorksNearby reports whether a builder guard selected a nearby job
	// on this visit, keeping its ward and successors [04 R-UNIT-06 §1].
	GuardWorksNearby(u *units.Unit, n *Node, tick uint32) bool

	// GuardResumesFromPad reports whether a guard attached to a carrier may
	// resume through the ordinary takeoff preamble instead of taking retail's
	// carried-guard queue cancellation [04 R-UNIT-06 §1].
	GuardResumesFromPad(u *units.Unit) bool
}

// StrictRules answers every decision the way retail 3.1 does: no Hold Fire
// suppression of a join, no deferred leash, no guard pad or nearby-work
// selection, and the carried-guard cancellation. It is zero-size, so placing
// it in a Rules interface never allocates and the retail path never reaches a
// Modern implementation.
type StrictRules struct{}

// HoldsFire is retail's answer: the standing-order fields are read by the
// caller's own gates, a forced join bypasses them, and the standing-fire
// handler touches no record [04 R-STANCE-01 §2][04 R-STANCE-01 §3].
func (StrictRules) HoldsFire(*units.Unit) bool { return false }

// DeferBomberLeash is retail's answer: an air attack tests its maneuver leash
// before its phase body, with no exemption for a bombing pass [04 R-AIR-01 §8].
func (StrictRules) DeferBomberLeash(*units.Unit, *Node) bool { return false }

// GuardSeeksPad is retail's answer: `VTOL_Follow` does not seek pads
// [04 R-ORD-02 §3].
func (StrictRules) GuardSeeksPad(*units.Unit, *Node, uint32) bool { return false }

// GuardWorksNearby is retail's answer: a guard copies its ward's work and does
// not scan surrounding allies or wrecks [04 R-UNIT-06 §1].
func (StrictRules) GuardWorksNearby(*units.Unit, *Node, uint32) bool { return false }

// GuardResumesFromPad is retail's answer: a carried guard cancels its whole
// queue [04 R-UNIT-06 §1].
func (StrictRules) GuardResumesFromPad(*units.Unit) bool { return false }

// rules returns the rule set this binding carries. A binding composed without
// one answers as Strict 3.1, so a fixture or a reconstructed queue that never
// set the field runs the retail path.
func (b *QueueBinding) rules() Rules {
	if b == nil || b.Rules == nil {
		return StrictRules{}
	}
	return b.Rules
}

// rulesOfUnit reads the rule set of a unit's queue without creating a queue,
// exactly as bindingOfUnit reads the binding.
func rulesOfUnit(u *units.Unit) Rules {
	return bindingOfUnit(u).rules()
}

// The three guard legs ask the rule set from the positions their mode
// projection was read: the carried-guard branch of the guard entry, the
// aircraft pad leg and the nearby-work leg. Each dispatch sits where the
// boolean sat, so the guard handler's leg order is unchanged.
func modernGuardOnRepairPad(u *units.Unit) bool {
	return rulesOfUnit(u).GuardResumesFromPad(u)
}

func modernGuardSeekPad(u *units.Unit, n *Node, tick uint32) bool {
	return rulesOfUnit(u).GuardSeeksPad(u, n, tick)
}

func modernGuardNearbyWork(u *units.Unit, n *Node, tick uint32) bool {
	return rulesOfUnit(u).GuardWorksNearby(u, n, tick)
}

// Strict keeps damage responses event-driven and never touches danger memory.
func (StrictRules) ObserveDanger(*units.Unit, *units.Unit, uint32) {}
func (StrictRules) StepDangerResponse(*units.Unit, uint32)         {}
func (StrictRules) ProtectWorkOnDamage(*units.Unit) bool           { return false }
func (StrictRules) AllowAutomaticRepair(*units.Unit, uint32) bool  { return true }
func (StrictRules) BeforeCommand(*Queue)                           {}

func (StrictRules) RetaliationOrder(victim, attacker *units.Unit) bool {
	return strictRetaliationOrder(victim, attacker)
}

func (StrictRules) ReactionResult(_ *Queue, _ *Node, code Code, _ uint32) Code { return code }

func (StrictRules) PreserveAutomaticTarget(*units.Unit, int) bool      { return false }
func (StrictRules) GuardTarget(*units.Unit, *Node) (pool.Handle, bool) { return 0, false }

func (StrictRules) MarkAutomaticAttack(*Node) {}

func (StrictRules) ObserveImpact(*units.Unit, numeric.Angle, uint32) {}

func (StrictRules) CrowdedMoveArrival(*units.Unit, *Node, uint32) bool { return false }
