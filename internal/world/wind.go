// Package world — wind field.
//
// Wind state holder seeded in phase 3 (PLAN_03 C11) and published as a
// clamped float32 scalar (I2). It registers callbacks into kernel phases 8
// (wind jitter/interval) and 9 (wind-field update) [01 §4.4][01 §7.3].
//
// Retail wind draws [01 §7.3][GAP T13][05 "Wind generation"][08 "Wind initialization"]:
//
//   - Briefing (battle entry, before any sim tick): CRT draws initial speed
//     `rand() % (max-min+1) + min` and six-bit direction `rand() & 0x3F`,
//     then next-change deadline `((crt*10)/0x8000+5)*30` [01 §7.3].
//   - In sim, when global tick >= deadline: deadline advances by the same
//     CRT interval expression (one CRT draw, 64-bit multiply/divide), then new
//     strength `simRand(max-min)+min` (one sim draw, bound<2 returns 0 without
//     advance) and, when strength !=0, new heading `simRand(0x10000)` (one sim
//     draw, 15-bit-chunk helper for >32767) [01 §7.1][01 §7.2][01 §7.3].
//     Vectors are recomputed instantly via fixed-point trig [04 §5.1] and the
//     scalar is `(float)speed/(float)5000` clamped to exactly 1.0 [01 §7.3].
package world

import (
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// windDenominator is the fixed divisor for the normalized wind scalar.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const windDenominator int32 = 5000

// Wind holds authoritative battle wind state [01 §7.3][05 "Wind generation"].
//
// It is seeded at battle entry from the CRT stream after map selection has
// retained min/max bounds [08 "Wind initialization"][PLAN_14 C17] and then
// advances deterministically via the two global RNG streams. Presentation
// interpolates committed state; simulation never reads wall-clock.
//
// WindMin/Max are the inclusive map bounds from the selected OTA/TNT header
// or defaults [02 "Map files"][03 §2.2]. They are immutable after creation;
// mutation is through the tick callbacks only, preserving call-order
// determinism (I4).
type Wind struct {
	// Bounds — inclusive map limits used for every draw [02 "Map files"].
	Min int32 // minwindspeed
	Max int32 // maxwindspeed

	// Current strength and 16-bit heading [01 §7.3].
	// Briefing direction `rand()&0x3F` is expanded to a full 16-bit circle
	// as `dir6<<10` (64 steps of 1024) so the same trig path applies.
	Strength int32  // current wind strength (integer speed)
	Heading  uint16 // 16-bit angle 0..65535; 0 is +Z per [04 §5.1]

	// Scalar is the normalized strength fed to wind generators [05 "Wind generation"].
	// Published as float32 per I2 allowlist: (float)strength/(float)5000 clamped to 1.0 [01 §7.3].
	Scalar float32 // I2 allowlist: wind scalar

	// DirX, DirZ are the world-space drift vectors recomputed instantly on
	// every due tick as -2*strength*cos/sin via fixed-point helpers [05 "Wind generation"][01 §7.3].
	DirX int32
	DirZ int32

	// Deadline control [01 §7.3].
	NextChange uint32 // tick when next redraw falls due
	LastChange uint32 // tick of last completed redraw
	Changed    bool   // true for exactly one tick after a redraw (burst gate)

	pending bool // internal: jitter advanced NextChange, field should apply strength/heading
}

// NewWind creates a wind holder with the given inclusive bounds.
// Bounds are clamped so Max >= Min; a collapsed range disables the bounded
// draw (bound<2 returns 0 without advancing) [01 §7.1]. The holder is
// zero-initialised with NextChange==0 so the first sim poll would redraw
// immediately if SeedBriefing were not called — retail leaves it zero at
// 0x37ec4 and the first Advance redraws at battle start.
func NewWind(min, max int32) *Wind {
	if max < min {
		max = min
	}
	return &Wind{Min: min, Max: max}
}

// SeedBriefing performs the briefing-screen draws that happen before any
// simulation tick, after map selection has retained the bounds [08 "Wind initialization"].
//
// It consumes exactly three CRT draws [01 §7.3][GAP T13]:
//
//  1. speed = CRT % (max-min+1) + min  (15-bit-chunk helper for >32767)
//  2. direction = CRT & 0x3F  (six-bit, expanded to 16-bit)
//  3. interval = ((CRT*10)/0x8000+5)*30  (64-bit multiply/divide path)
//
// The resulting state is published instantly (no interpolation) and NextChange
// is set to tick+interval [05 "Wind generation"].
func (w *Wind) SeedBriefing(crt *rng.CRT, tick uint32) {
	if w == nil || crt == nil {
		return
	}
	// 1. Initial speed from CRT [01 §7.3].
	var strength int32
	spanPlus1 := int64(w.Max) - int64(w.Min) + 1
	if spanPlus1 >= 2 {
		// CRT bounded helper handles >32767 via 15-bit chunk concatenation [01 §7.2].
		// Bound is spanPlus1; clamp to uint32 domain for the helper.
		if spanPlus1 > 0xffffffff {
			spanPlus1 = 0xffffffff
		}
		strength = int32(crt.Uint32n(uint32(spanPlus1))) + w.Min
	} else if spanPlus1 == 1 {
		// bound 1 <2: rng contract returns 0 without advancing [01 §7.1][01 §7.2].
		strength = w.Min
	} else {
		strength = w.Min
	}
	w.Strength = strength

	// 2. Initial six-bit direction, expanded to full 16-bit angle [01 §7.3][08 "Wind initialization"].
	// raw 0..63 -> angle 0..64512 step 1024 (65536/64).
	dir6 := uint16(crt.Rand() & 0x3F) // [01 §7.3] rand()&0x3F
	w.Heading = dir6 << 10

	// Publish derived values [01 §7.3][05 "Wind generation"].
	w.Scalar = windScalar(strength)
	w.DirX, w.DirZ = windVectors(strength, w.Heading)

	// 3. First next-change interval from CRT [01 §7.3].
	interval := windInterval(crt)
	w.NextChange = tick + interval
	w.LastChange = tick
	w.Changed = true
	w.pending = false
}

// Jitter implements the phase-8 wind jitter / randomized interval callback [01 §4.4].
//
// It runs every sub-tick. When tick < NextChange it only clears the one-tick
// Changed burst flag and returns. When due, it advances NextChange by one CRT
// interval draw `((crt*10)/0x8000+5)*30` and marks pending so the phase-9 field
// callback will consume simulation draws in the same tick [01 §7.3][GAP T13].
// Call order is fixed: jitter (phase 8, CRT) before field (phase 9, sim) —
// consuming one CRT draw before up to two sim draws, preserving the global
// stream order (I4).
func (w *Wind) Jitter(tick uint32, crt *rng.CRT) {
	if w == nil {
		return
	}
	if crt == nil {
		// No CRT source: keep prior behavior (presentation-only tick).
		if tick < w.NextChange {
			w.Changed = false
			w.pending = false
		}
		return
	}
	if tick < w.NextChange {
		w.Changed = false
		w.pending = false
		return
	}
	interval := windInterval(crt) // one CRT draw [01 §7.3]
	w.NextChange = tick + interval
	w.pending = true
}

// Field implements the phase-9 wind-field update callback [01 §4.4].
//
// When jitter has marked pending (tick was due), it draws the new strength
// and heading from the simulation stream and republishes every derived value
// instantly [01 §7.3][05 "Wind generation"]. The change takes effect in the
// same tick with no interpolation; wind-generator units receive
// SetDirection/SetSpeed bursts only on this tick (consumed by economy/COB).
// Returns true when a redraw happened.
func (w *Wind) Field(tick uint32, sim *rng.Simulation) bool {
	if w == nil {
		return false
	}
	if !w.pending {
		// Not due this tick — keep Changed false (cleared by Jitter).
		return false
	}
	w.pending = false
	if sim == nil {
		w.Changed = false
		return false
	}

	// New strength: simRand(max-min)+min [01 §7.3]. Note inclusive-exclusive
	// difference from briefing's max-min+1.
	span := int64(w.Max) - int64(w.Min)
	var strength int32 = w.Min
	if span >= 2 {
		if span > 0xffffffff {
			span = 0xffffffff
		}
		strength = int32(sim.Uint32n(uint32(span))) + w.Min // bound<2 no-advance is inside Uint32n
	} else if span < 2 {
		// collapsed/spurious range: no draw, yield min [01 §7.1]
		strength = w.Min
	}
	w.Strength = strength

	// New heading only when strength !=0 [01 §7.3].
	if strength != 0 {
		h := sim.Uint32n(0x10000) // full 16-bit domain, 15-bit-chunk path [01 §7.1]
		w.Heading = uint16(h)
	}
	w.Scalar = windScalar(strength)
	w.DirX, w.DirZ = windVectors(strength, w.Heading)
	w.LastChange = tick
	w.Changed = true
	return true
}

// Advance is a convenience that runs jitter then field in order for tests or
// single-callback callers. When used via kernel registration, prefer the
// split Jitter/Field pair to make phase ownership explicit.
func (w *Wind) Advance(tick uint32, crt *rng.CRT, sim *rng.Simulation) bool {
	w.Jitter(tick, crt)
	return w.Field(tick, sim)
}

// Update is an alias for Field with the global simulation stream, provided
// because the task description names a method Update. It runs the phase-9
// logic; call Jitter first when using split phases.
func (w *Wind) Update(tick uint32) bool {
	if w == nil {
		return false
	}
	sim := rng.Global.Sim
	if sim == nil {
		w.Changed = false
		return false
	}
	return w.Field(tick, sim)
}

// Register wires the holder into the kernel's twelve-phase graph.
// Phase 8 (wind jitter) receives the CRT interval draw, phase 9 (wind-field)
// receives the simulation strength/heading draws [01 §4.4][01 §7.3].
// If crt or sim are nil the global streams (rng.Global) are used, which are
// seeded in phase 3 [PLAN_03 C10][PLAN_03 C11]. The method is idempotent-safe
// for vet/build checks; duplicate Register calls add duplicate entries (caller
// should register once per session, owned by internal/session [PLAN_14 WU-14-2]).
func (w *Wind) Register(k *kernel.Kernel, crt *rng.CRT, sim *rng.Simulation) {
	if w == nil || k == nil {
		return
	}
	if crt == nil {
		crt = rng.Global.Crt
	}
	if sim == nil {
		sim = rng.Global.Sim
	}
	// Capture locals for closure; tick order is caller-provided (kernel increments before phase 1 [01 §4.4]).
	k.Register(kernel.PhaseWindJitter, "wind-jitter", func(tick uint32) {
		w.Jitter(tick, crt)
	})
	k.Register(kernel.PhaseWindField, "wind-field", func(tick uint32) {
		w.Field(tick, sim)
	})
}

// Register is a package-level helper that registers w into k using the global
// streams when w is non-nil. It satisfies the plan's "plus Register function
// that registers into kernel" wording for callers that prefer a function.
func Register(k *kernel.Kernel, w *Wind) {
	if w == nil || k == nil {
		return
	}
	w.Register(k, rng.Global.Crt, rng.Global.Sim)
}

// windScalar returns the clamped float32 scalar published to wind generators.
// It is exactly (float)speed/(float)5000 stored as float32 and clamped from
// above at 1.0, matching the overflow store of the float bit pattern for 1.0
// [01 §7.3]. I2 allowlist permits float32 here.
func windScalar(strength int32) float32 {
	s := float32(strength) / float32(windDenominator) // [05 "Wind generation"]
	if s > 1.0 {
		s = 1.0
	}
	if s < 0 {
		s = 0
	}
	return s
}

// windInterval evaluates the CRT interval expression with 64-bit
// multiply/divide as retail does [01 §7.3]:
//
//	((crt*10)/0x8000+5)*30  => 150..420 ticks (5..14 seconds)
func windInterval(crt *rng.CRT) uint32 {
	// CRT Rand returns 0..32767 (0x7FFF) [01 §7.2].
	d := int64(crt.Rand())
	return uint32((d*10/0x8000 + 5) * 30) // 64-bit intermediate already via int64
}

// windVectors recomputes world X/Z vectors as -2*strength*cos/sin [05 "Wind generation"].
// It uses the simulation trig table [04 §5.1] through numeric.Cos/Sin.
func windVectors(strength int32, heading uint16) (int32, int32) {
	if strength == 0 {
		return 0, 0
	}
	a := numeric.Angle(heading)
	cos := numeric.Cos(a) // scaled 8192 [04 §5.1]
	sin := numeric.Sin(a)
	amp := int32(-2 * strength)
	// MulRound performs (a*b+4096)>>13 with 64-bit intermediate [04 §5.1].
	dx := numeric.MulRound(amp, cos)
	dz := numeric.MulRound(amp, sin)
	return dx, dz
}
