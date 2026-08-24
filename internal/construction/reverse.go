package construction

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Uses metal-only direct bucket path, no energy, cause-9 no-corpse when clamped.
// workerFactor negative increases remaining: newRem = clamp(old + |worker|/buildTime).
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Discounted when target owner state2 with selector 0/1 via same 0.5/0.7 pairing but tied to victim owner not builder [P0-15].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// old remaining 0..1, worker negative (e.g., -1), buildTime >0.
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

// ReverseRefund applies metal-only refund to builder's bucket via direct path [P0-15].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Discount ties to victim owner state2, not builder, with same inverted pairing as cancel-current but victim-owned [P0-15].
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
