package render

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// Strip storage and the strip lifecycle itself are internal/session's: the
// per-tick sweep evaluates a removal verdict before the update work, compacts
// left stably, and evicts the oldest object once the pre-insert count exceeds
// 400 [03 "Strip storage and lifecycle"][R-STRIP-01 §1]. This file holds the
// fixed effect pool the presentation side owns.

// FixedEffectCap is the fixed pool capacity [03 §1] C5 (I5).
const FixedEffectCap = 300 // 0x54-byte records, 300 entries [03 §1] (I5)

// FixedEffectPool is the battle-sized fixed effect pool [03 §1] C5.
// Its zero value holds up to 300 fixed-size records; appends at or above the
// configured battle-entry cap allocate nothing [03 §1][CP-LIM-1].
type FixedEffectPool struct {
	records            []EffectRecord
	fragments          []fragmentGeometry
	capacity           int
	fragmentCursor     int
	fragmentRoundRobin bool
	fragmentContext    FragmentStepContext
	fragmentStepping   bool
	gravity            numeric.Fixed                          // default per-tick gravity when record.Gravity is zero [03 §2.2]
	heightAt           func(x, z numeric.Fixed) numeric.Fixed // terrain height query; nil skips terrain/water contact [03 §1]
	seaLevel           numeric.Fixed                          // sea level in world units byte*65536 [03 §2.2]
}

// The auxiliary CP-LIM-1 allocator is the paired eight-vertex fragment
// geometry owned here: both its general and inline piece allocation paths map
// to the shatter lifecycle of [04 R-COB-04 §3] and its initial templates of
// [04 R-COB-04 §4]. It therefore scales with this pool rather than forming a
// third owner.

// NewFixedEffectPool creates an empty fixed effect pool sized at battle entry.
// Zero selects retail's 300-record capacity [CP-LIM-1].
func NewFixedEffectPool(capacity int) *FixedEffectPool {
	roundRobin := capacity > 0
	if capacity <= 0 {
		capacity = FixedEffectCap
	}
	return &FixedEffectPool{
		records:            make([]EffectRecord, 0, capacity),
		fragments:          make([]fragmentGeometry, capacity),
		capacity:           capacity,
		fragmentRoundRobin: roundRobin,
	}
}

func (p *FixedEffectPool) ensureStorage() {
	if p == nil || p.capacity > 0 {
		return
	}
	p.capacity = FixedEffectCap
	p.records = make([]EffectRecord, 0, FixedEffectCap)
	p.fragments = make([]fragmentGeometry, FixedEffectCap)
}
