// Package world — wind field.
//
// Wind state seeded at battle entry and advanced by the two global RNG
// streams. Session.authoritativeTick calls its two update steps directly in
// phase order [01 §4.4].
//
// Retail wind draws [01 §7.3], [05 "Wind generation"], [GAP T13]:
//
//   - Briefing (battle entry, before any sim tick): CRT draws the initial speed
//     `rand() % (max-min+1) + min`, then the six-bit direction `rand() & 0x3F`,
//     then the first next-change deadline `((crt*10)/0x8000+5)*30`. All three
//     are raw inline CRT expressions, so all three consume a draw
//     unconditionally — see rng.CRT.Uint32n.
//   - In sim, when the global tick passes the wind deadline: the deadline
//     advances by the same CRT interval expression (one CRT draw, 64-bit
//     multiply/divide), then the new strength is `simRand(maxWind-minWind)+min`
//     (one simulation draw; bound<2 returns 0 without advancing) and, only when
//     the strength is nonzero, the new heading is `simRand(0x10000)` (one
//     simulation draw — not two; see rng.Simulation.Uint32n).
//   - The change takes effect instantly with no interpolation, the world X/Z
//     vectors are recomputed, and the scalar published to generators is
//     `(float)speed / (float)5000` clamped from above at exactly 1.0.
package world

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// windDenominator is the fixed divisor for the normalized wind scalar:
// "strength divided by a fixed 5000 denominator clamped to one"
// [05 "Wind generation"], [01 §7.3].
const windDenominator int32 = 5000

// Wind holds authoritative battle wind state [01 §7.3], [05 "Wind generation"].
//
// Min/Max are the inclusive map bounds from the OTA/TNT header or the
// canonical defaults [03 §2.2]. They are immutable after creation; every other
// field advances only through the two tick callbacks, so call order stays
// deterministic (I4).
type Wind struct {
	// Bounds — inclusive map limits used for every draw [02 "Map files"].
	Min int32 // minwindspeed
	Max int32 // maxwindspeed

	Strength int32  // current wind strength (integer speed) [01 §7.3]
	Heading  uint16 // 16-bit angle, 65536 per circle [04 §5.1]

	// Scalar is the normalized strength fed to wind generators.
	// I2 allowlist: wind scalar published to consumers [01 §7.3].
	Scalar float32

	// DirX, DirZ are the world-space wind vectors, recomputed on every change.
	DirX int32
	DirZ int32

	NextChange uint32 // tick the next redraw falls due [01 §7.3]
	LastChange uint32 // tick of the last completed redraw
	Changed    bool   // true for exactly one tick after a redraw (callback burst gate)

	pending bool // jitter saw the deadline pass; field applies strength/heading
}

// NewWind creates a wind holder with the given inclusive bounds. A reversed
// range is collapsed rather than rejected; a collapsed range still consumes its
// draws, because retail's expressions are unguarded [01 §7.3].
func NewWind(min, max int32) *Wind {
	if max < min {
		max = min
	}
	return &Wind{Min: min, Max: max}
}

// boundFor narrows a draw bound to the helper's uint32 domain. Map bounds are
// authored 16-bit values, so this only guards against corrupt input.
func boundFor(span int64) uint32 {
	if span < 0 {
		return 0
	}
	if span > 0xffffffff {
		return 0xffffffff
	}
	return uint32(span)
}

// SeedBriefing performs the briefing-screen draws, which happen at battle entry
// before any simulation tick [01 §7.3], [08 "Wind initialization"].
//
// It consumes exactly three CRT draws, unconditionally. Retail writes these as
// raw inline `rand() % n` expressions, so a map whose minimum and maximum wind
// are equal still consumes the speed draw; skipping it would shift every later
// CRT consumer by one.
func (w *Wind) SeedBriefing(crt *rng.CRT, tick uint32) {
	if w == nil || crt == nil {
		return
	}
	// 1. Initial speed: rand() % (max-min+1) + min [01 §7.3].
	w.Strength = int32(crt.Uint32n(boundFor(int64(w.Max)-int64(w.Min)+1))) + w.Min

	// 2. Initial direction: rand() & 0x3F, six bits [01 §7.3].
	//
	// TODO(question): research gives the briefing direction as a six-bit value
	// and every later heading as a full 16-bit simRand(0x10000) [01 §7.3], but
	// does not state how the six-bit value is widened into the heading field.
	// 64 steps of 1024 is the arithmetically clean reading; it is not attested.
	dir6 := uint16(crt.Rand() & 0x3F)
	w.Heading = dir6 << 10

	w.publish(tick)

	// 3. First next-change deadline [01 §7.3].
	w.NextChange = tick + windInterval(crt)
	w.pending = false
}

// Jitter is the phase-8 callback: wind jitter / randomized interval update
// [01 §4.4].
//
// It runs every sub-tick. When the deadline has not passed it only clears the
// one-tick Changed burst flag. When it has, it advances the deadline by one CRT
// interval draw and marks the change pending so the phase-9 callback consumes
// the simulation draws in the same tick. The split matters: phase 8 draws from
// the CRT stream strictly before phase 9 draws from the simulation stream, and
// that ordering is behavior (I4).
func (w *Wind) Jitter(tick uint32, crt *rng.CRT) {
	if w == nil || crt == nil {
		return
	}
	if tick < w.NextChange {
		w.Changed = false
		w.pending = false
		return
	}
	w.NextChange = tick + windInterval(crt) // one CRT draw [01 §7.3]
	w.pending = true
}

// Field is the phase-9 callback: wind-field update [01 §4.4].
//
// When jitter marked the tick due, it draws the new strength and heading from
// the simulation stream and republishes every derived value instantly — there
// is no interpolation [05 "Wind generation"]. Wind generators receive their
// SetDirection/SetSpeed burst only on a tick where this returns true.
func (w *Wind) Field(tick uint32, sim *rng.Simulation) bool {
	if w == nil || !w.pending {
		return false
	}
	w.pending = false
	if sim == nil {
		w.Changed = false
		return false
	}

	// New strength: simRand(maxWind-minWind) + minWind [01 §7.3]. Note the
	// exclusive span here against the briefing draw's inclusive max-min+1.
	// A span below 2 returns 0 without advancing, which is the helper's
	// contract, so this call is made unconditionally.
	w.Strength = int32(sim.Uint32n(boundFor(int64(w.Max)-int64(w.Min)))) + w.Min

	// New heading only when the strength is nonzero [01 §7.3]. One draw.
	if w.Strength != 0 {
		w.Heading = uint16(sim.Uint32n(0x10000))
	}

	w.publish(tick)
	return true
}

// publish recomputes every derived value from the current strength and heading
// and marks the change tick. The change is instant [05 "Wind generation"].
func (w *Wind) publish(tick uint32) {
	w.Scalar = windScalar(w.Strength)
	w.DirX, w.DirZ = windVectors(w.Strength, w.Heading)
	w.LastChange = tick
	w.Changed = true
}

// windScalar is the normalized strength published to wind generators: exactly
// (float)speed / (float)5000, stored as float32 and clamped from above at 1.0
// [01 §7.3], [05 "Wind generation"]. I2 allowlist. There is no lower clamp —
// research states only the ceiling, and authored bounds are non-negative.
func windScalar(strength int32) float32 {
	s := float32(strength) / float32(windDenominator)
	if s > 1.0 {
		s = 1.0
	}
	return s
}

// windInterval evaluates the CRT interval expression with the 64-bit
// multiply/divide retail uses [01 §7.3]:
//
//	((crt * 10) / 0x8000 + 5) * 30  =>  150..420 ticks, five to fourteen seconds
func windInterval(crt *rng.CRT) uint32 {
	d := int64(crt.Rand()) // 0..32767 [01 §7.2]
	return uint32((d*10/0x8000 + 5) * 30)
}

// windVectors recomputes the world X/Z wind vectors from strength and heading
// using the shared simulation trig table [04 §5.1]. The direction vector pair
// is −2 × the fixed-point trig of the heading with the speed as the magnitude
// [01 §4.4].
//
// TODO(question): the cos→X / sin→Z axis assignment below is not attested in
// research — only the −2 amplitude factor is established. Nothing reads
// DirX/DirZ yet; the first consumer (particle drift is presentation-side
// [05 "Wind generation"]) must settle the axis before depending on it.
func windVectors(strength int32, heading uint16) (int32, int32) {
	if strength == 0 {
		return 0, 0
	}
	a := numeric.Angle(heading)
	amp := int32(-2 * strength)
	// MulRound is (a*b + 4096) >> 13: round to nearest before truncation [04 §5.1].
	return numeric.MulRound(amp, numeric.Cos(a)), numeric.MulRound(amp, numeric.Sin(a))
}
