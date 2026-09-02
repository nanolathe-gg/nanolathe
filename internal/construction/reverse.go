package construction

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// Reverse computes the deconstruction step — the construction step routine's
// reverse arm, taken when the worker factor is negative [P0-15]. It is
// metal-only and goes direct to the builder's metal bucket, bypassing the
// ordinary two-resource admission helper; a clamp to zero ends the target with
// cause 9 and no corpse.
//
// workerFactor negative increases remaining: newRem = clamp(old + |worker|/buildTime).
// refund = metalCost * (new-old), positive.
// Discounted when the TARGET's owner is in state 2 with selector 0 or 1, by the
// same 0.5/0.7 pairing the forward arm uses — tied to the victim's owner, not
// the builder's [P0-15].
//
// TODO(question): which order passes a negative worker factor is unknown. A
// bounded census over 862 + 3901 call sites found no call into the step routine
// with a negative immediate, so the arithmetic below is established but its
// dispatch origin is not. Decider: static trace of the step routine's callers
// for a computed (non-immediate) worker factor.

// ReverseStep computes the new remaining and the refund for the reverse arm
// [P0-15]. old remaining 0..1, worker negative (e.g. -1), buildTime > 0.
// Returns newRemaining clamped 0..1, refund amount (metalCost*(new-old)), health delta via diff-of-trunc.
func ReverseStep(old float32, worker int32, buildTime int32, maxDamage int32, metalCost int32) (float32, float32, int32) {
	if buildTime <= 0 {
		buildTime = 1
	}
	delta := float32(-worker) / float32(buildTime)
	nv := old + delta
	if nv > 1 {
		nv = 1
	}
	if nv < 0 {
		nv = 0
	}
	refund := float32(metalCost) * (nv - old)
	healthDelta := int32(float32(maxDamage)*old) - int32(float32(maxDamage)*nv)
	return nv, refund, healthDelta
}

// ReverseRefund applies metal-only refund to builder's bucket via direct path
// [05 "Resurrection", "Established fact — reverse and deconstruction"].
// No energy refund — the reverse arm credits only metal, directly, with no
// admission and no energy credit.
// Discount ties to the TARGET's owner state2, not the builder's, with the
// same inverted 0.5/0.7 pairing as cancel-current but victim-owned
// [05 "Resurrection", "Established fact — reverse and deconstruction"].
func ReverseRefund(builderBucket *float32, refund float32, isSpecial bool, modeSelector int) {
	if builderBucket == nil || refund <= 0 {
		return
	}
	if isSpecial {
		switch modeSelector {
		case 0:
			*builderBucket += refund * -0.5
		case 1:
			*builderBucket += refund * -0.7
		default:
			*builderBucket += refund
		}
	} else {
		*builderBucket += refund
	}
}

// ReverseCause9 checks if reverse clamped to 1.0 should emit cause-9 no-corpse kill [P0-15].
func ReverseCause9(newRemaining float32) bool {
	return newRemaining >= 1.0
}

// ApplyReverse performs one reverse tick on target via builder [P0-15].
func ApplyReverse(builder *units.Unit, target *units.Unit, worker int32, isSpecial bool, modeSelector int, econBucket *float32) bool {
	if target == nil || target.Def == nil {
		return false
	}
	if target.Remaining >= 1.0 {
		return true
	}
	buildTime := target.Def.BuildTime
	if buildTime <= 0 {
		buildTime = 1
	}
	old := target.Remaining
	nv, refund, healthDelta := ReverseStep(old, worker, buildTime, target.MaxHealth, target.Def.BuildCostMetal)
	target.Remaining = nv
	target.Health += healthDelta
	if target.Health > target.MaxHealth {
		target.Health = target.MaxHealth
	}
	if target.Health < 0 {
		target.Health = 0
	}
	if econBucket != nil {
		ReverseRefund(econBucket, refund, isSpecial, modeSelector)
	} else if builder != nil {
		builder.SpotMetal += refund
	}
	if ReverseCause9(nv) {
		return true
	}
	return false
}
