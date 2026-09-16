package render

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// Strip storage and the strip lifecycle itself are internal/session's: the
// per-tick sweep evaluates a removal verdict before the update work, compacts
// left stably, and evicts the oldest object once the pre-insert count exceeds
// 400 [03 "Strip storage and lifecycle"][R-STRIP-01 §1]. This file holds the
// fixed effect pool the presentation side owns.

// FixedEffectCap is the fixed pool capacity [03 §1] C5 (I5).
const FixedEffectCap = 300 // 0x54-byte records, 300 entries [03 §1] (I5)

// FixedEffectPool is the fixed effect pool [03 §1] C5.
// It holds up to 300 fixed-size records; appends at or above the cap allocate
// nothing [03 §1]. Full integration lives in effects.go (WU-13-2).
type FixedEffectPool struct {
	records          []EffectRecord
	fragments        [FixedEffectCap]fragmentGeometry
	fragmentContext  FragmentStepContext
	fragmentStepping bool
	gravity          numeric.Fixed                          // default per-tick gravity when record.Gravity is zero [03 §2.2]
	heightAt         func(x, z numeric.Fixed) numeric.Fixed // terrain height query; nil skips terrain/water contact [03 §1]
	seaLevel         numeric.Fixed                          // sea level in world units byte*65536 [03 §2.2]
}
