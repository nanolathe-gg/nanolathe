// The wind field.
//
// Wind state is owned by the battle session and advanced by its two RNG
// streams. The session's phase 8 performs the complete scheduled redraw
// [01 §4.4][R-CORE-01 §4.4.1].
//
// Retail wind draws [01 §7.3], [05 "Wind generation"], [GAP T13], [R-CORE-02]:
//
//   - The briefing-screen speed and jitter-countdown draws (`rand() %
//     (max-min+1)+min`, then `rand() & 0x3F` — a countdown, not a direction;
//     the briefing has no heading) are FRONT-END DISPLAY STATE only: their
//     globals have no battle-side reader. Battle entry itself consumes NO wind draws — it
//     zeroes the deadline, and the strict gate (not due while tick < deadline)
//     leaves the zeroed deadline unfired at tick 0. The first wind chain runs
//     inside the first sub-tick (tick 1 > 0) [R-CORE-02].
//   - In sim, when the global tick passes the wind deadline: the deadline
//     advances by the CRT interval expression `((crt*10)/0x8000+5)*30` (one CRT
//     draw, 64-bit multiply/divide), then the new strength is
//     `simRand(maxWind-minWind)+min` (one simulation draw; bound<2 returns 0
//     without advancing) and, only when the strength is nonzero, the new
//     heading is `simRand(0x10000)` (one simulation draw — not two; see
//     rng.Simulation.Uint32n).
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

	// BriefingCountdown is the briefing screen's display-jitter countdown —
	// the `rand() & 0x3F` value drawn at briefing entry, decremented per
	// briefing update and re-armed with `rand() % 63` when it expires
	// [01 §7.3]. Front-end display state only; no battle-side reader.
	BriefingCountdown int32

	NextChange uint32 // tick the next redraw falls due [01 §7.3]
	LastChange uint32 // tick of the last completed redraw
	Changed    bool   // true for exactly one tick after a redraw (callback burst gate)
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

// SeedBriefing is the FRONT-END briefing-screen entry draw helper [01 §7.3]
// [R-CORE-02]. It draws the two briefing-screen display values — the speed
// `rand() % (max-min+1) + min`, then the display-jitter countdown
// `rand() & 0x3F` — and nothing else: the entry draws exactly twice and arms
// no deadline (the earlier third, interval draw here was not retail's).
//
// The second value is NOT a direction (RWU-19-39 corrected [01 §7.3], which
// had called it a "six-bit direction"): the briefing screen has no wind
// heading at all, only a speed, and the countdown gates BriefingUpdate's
// speed drift. Heading stays untouched here.
//
// It must NEVER run at battle entry: retail's battle bootstrap performs no
// wind draws (it only zeroes the deadline), and the briefing values are
// display state with no battle-side reader [R-CORE-02]. Nanolathe has no
// briefing screen yet; no production caller exists. AUDIT(parity-spine):
// retained as the clearly-labeled front-end presentation path for the future
// briefing display.
func (w *Wind) SeedBriefing(crt *rng.CRT, tick uint32) {
	if w == nil || crt == nil {
		return
	}
	// 1. Displayed speed: rand() % (max-min+1) + min [01 §7.3].
	w.Strength = int32(crt.Uint32n(boundFor(int64(w.Max)-int64(w.Min)+1))) + w.Min

	// 2. Jitter countdown: rand() & 0x3F, 0..63 updates [01 §7.3].
	w.BriefingCountdown = int32(crt.Rand() & 0x3F)

	w.publish(tick)
}

// BriefingUpdate is the briefing screen's per-update wind-display routine
// [01 §7.3] (RWU-19-39): it decrements the countdown and, when the countdown
// is below one, drifts the displayed speed by `−2 + rand() % 5` (−2..+2),
// clamps it to the map bounds, and re-arms the countdown with `rand() % 63`
// (0..62). Two CRT draws on an expiring update, none otherwise. Front-end
// display state only; it reports whether the speed was redrawn.
func (w *Wind) BriefingUpdate(crt *rng.CRT, tick uint32) bool {
	if w == nil || crt == nil {
		return false
	}
	w.BriefingCountdown--
	if w.BriefingCountdown >= 1 {
		return false
	}
	w.Strength += -2 + int32(crt.Rand()%5)
	if w.Strength < w.Min {
		w.Strength = w.Min
	}
	if w.Strength > w.Max {
		w.Strength = w.Max
	}
	w.BriefingCountdown = int32(crt.Rand() % 63)
	w.publish(tick)
	return true
}

// Jitter is the phase-8 callback: the COMPLETE scheduled wind redraw
// [01 §4.4][01 §7.3][R-CORE-01 §4.4.1]. DET-03: the earlier split (Jitter
// drew only the CRT interval here and a phase-9 "Field" drew the sim
// strength/heading) is wrong — phase 8 owns the whole chain and phase 9 is
// the meteor shower, not a wind pass.
//
// It runs every sub-tick. The deadline gate is strict: while `tick <
// NextChange` the redraw is not due and only the one-tick Changed burst flag
// clears. When due it consumes, in order: one CRT interval draw
// `((crt*10)/0x8000+5)*30`, then the sim strength
// `simRand(maxWind-minWind)+minWind`, then — only when the strength is
// nonzero — the sim heading `simRand(0x10000)`, and finally the vectors,
// scalar, and one-tick change flag [01 §7.3]. Battle entry zeroes
// NextChange, so with the strict gate the first chain fires at tick 1 and
// battle entry itself consumes no draws [R-CORE-02].
func (w *Wind) Jitter(tick uint32, crt *rng.CRT, sim *rng.Simulation) bool {
	if w == nil || crt == nil || sim == nil {
		return false
	}
	if tick < w.NextChange {
		w.Changed = false
		return false
	}
	// CRT interval draw, drawn before the new speed and heading [01 §7.3] —
	// 64-bit multiply/divide path.
	w.NextChange = tick + windInterval(crt)
	// New strength: simRand(maxWind-minWind) + minWind [01 §7.3]. Exclusive
	// span here vs briefing display's inclusive max-min+1. Bound<2 returns 0
	// without advancing per [01 §7.1] contract.
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
// [01 §4.4]. [R-WIND-01] closes the axis question this function once left
// open: the FIRST word is the X term −2·speed·sin(heading) and
// the SECOND word is the Z term −2·speed·cos(heading) — one shared 512-entry
// sine table (entry k = 8192·sin(2πk/512)) serves both axes, the cosine
// reading the same table a quarter turn (128 entries) ahead, and the product
// rounds to nearest (half-up) before the −2 amplitude is stored. Verified in
// two independent consumer families: the strip-5/9 smoke drift applies the
// first word to world X and the second to world Z, and the feature fire-spread
// probe accumulates them into its X- and Z-cell coordinates.
//
// Axis-swap provenance: nothing consumed these vectors in a direction-
// sensitive way while the old cos→X / sin→Z assignment stood. The only
// readers — phase-3 ballistic/dropped drift [06 §6.4] and the feature
// fire-spread probe [03 §5.1.2] — predate the finding, and both of their tests
// author DirX/DirZ directly rather than through windVectors, so the swap
// changes which table feeds each axis for those consumers but no test outcome.
func windVectors(strength int32, heading uint16) (int32, int32) {
	if strength == 0 {
		return 0, 0
	}
	a := numeric.Angle(heading)
	amp := int32(-2 * strength)
	// MulRound is (a*b + 4096) >> 13: round to nearest before truncation [04 §5.1].
	// [R-WIND-01]: sin feeds X, cos feeds Z.
	return numeric.MulRound(amp, numeric.Sin(a)), numeric.MulRound(amp, numeric.Cos(a))
}
