// Package rng provides the two deterministic random streams used by the
// retail engine [01 §7.1], [01 §7.2], [01 §7.3].
//
// There are exactly two streams and they are not interchangeable:
//
//   - Simulation is the process-wide Park–Miller stream. Every authoritative
//     gameplay draw comes from it, and call order — not entity identity — is
//     what isolates consumers [01 §7.1]. It yields 31 bits per draw.
//   - CRT is the per-thread MSVCRT stream used for meteor geometry [06 §6.5],
//     screen shake [03 §5.6], audio variants [03 §8.3] and briefing wind
//     [01 §7.3]. It yields 15 bits per draw, so bounds above 32,767 need the
//     chunk-concatenation helper below.
//
// No other stream exists. Do not add per-entity, per-player or name-seeded
// substreams: they look deterministic and reproduce nothing retail does (I4).
package rng

const (
	retailParkMillerMultiplier uint32 = 16807
	retailParkMillerQuotient   uint32 = 127773
	retailParkMillerModulus    uint32 = 0x7fffffff
	retailSeedXOR              uint32 = 0x66e29572
)

// Simulation is the process-wide Park–Miller Schrage stream [01 §7.1].
type Simulation struct {
	State uint32
	draws uint64
}

// NewSimulation applies the seed transform retail performs at battle entry
// before its first simulation draw: the counter value XORed with a fixed
// constant and forced odd [01 §7.1], [01 §2.1].
func NewSimulation(seed uint32) Simulation {
	return Simulation{State: (seed ^ retailSeedXOR) | 1}
}

// SimulationFromState restores the generator's exact 32-bit state, for
// known-vector verification and debug repros. Retail does not save RNG state
// [08 "Scheduler and random state in saves"].
func SimulationFromState(state uint32) Simulation {
	return Simulation{State: state}
}

// step performs one Lehmer update via Schrage's method and returns the new
// state [01 §7.1].
//
// The binary's 32-bit expression is algebraically Schrage: with a·q = m − r,
// state·a + (state/q)·0x80000001 ≡ a·lo − r·hi (mod 2³²). Keeping the uint32
// wrap makes this faithful even for a restored state outside the generator's
// ordinary 1..m−1 invariant.
func (s *Simulation) step() uint32 {
	next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
	if int32(next) < 1 {
		next += retailParkMillerModulus
	}
	s.State = next
	s.draws++
	return next
}

// Uint32n is the bounded simulation helper [01 §7.1]: for a bound of at least
// two, update the state with the Lehmer recurrence, add the modulus when the
// intermediate value is nonpositive, then return the unsigned remainder modulo
// the bound. Bounds below two return zero WITHOUT advancing the stream — a
// stream that advances here desynchronizes every later consumer (I4).
//
// The bound test is a SIGNED 32-bit compare in retail [01 §7.1]: a bound whose
// top bit is set reads as negative and is caught by "below two" exactly like 0
// or 1, returning 0 with no draw. A caller that narrows a wider quantity (a
// distance-squared term, for instance) to this 32-bit bound can legitimately
// produce such a value, so this must not be an unsigned compare against the
// literal 2.
//
// There is deliberately no chunk-concatenation path here. That helper belongs
// to the CRT stream, which yields only 15 bits per draw [01 §7.2]; Park–Miller
// already yields 31 bits, so every bound up to the modulus is covered by a
// single draw. Adding one would make simRand(0x10000) — the wind heading of
// [01 §7.3] and [05 "Wind generation"] — cost two draws instead of one and
// return a different value.
func (s *Simulation) Uint32n(bound uint32) uint32 {
	if int32(bound) < 2 {
		return 0
	}
	return s.step() % bound
}

// Draws returns the number of draws consumed. It is a debug counter and never
// participates in simulation state (I4).
func (s *Simulation) Draws() uint64 { return s.draws }

// CRT is the MSVCRT stream: state = state*214013 + 2531011, result
// (state>>16) & 32767 [01 §7.2].
type CRT struct {
	State uint32
	draws uint64
}

// NewCRT seeds the generator directly. Retail seeds it at process start from
// local/system time and time-zone conversion, at effectively one-second
// resolution [01 §2.1], [01 §7.2].
func NewCRT(seed uint32) CRT { return CRT{State: seed} }

// CRTFromState restores the exact 32-bit CRT state.
func CRTFromState(state uint32) CRT { return CRT{State: state} }

// Rand advances the state once and returns the value in [0, 0x7FFF] [01 §7.2].
func (c *CRT) Rand() int32 {
	c.State = c.State*214013 + 2531011
	c.draws++
	return int32((c.State >> 16) & 0x7FFF)
}

// Uint32n samples the CRT stream modulo bound.
//
// Retail's CRT call sites are raw inline `rand() % n` expressions — the
// briefing wind speed of [01 §7.3] is written exactly that way — so unlike the
// simulation helper there is NO zero-bound guard here: a bound of one consumes
// its draw and returns zero. Skipping that draw shifts every later CRT
// consumer by one.
//
// The widening sampler of [01 §7.2] is followed exactly: the mask starts at
// 0x7FFF and the result starts as ONE fresh fifteen-bit draw; while the mask is
// below the bound, both the mask and the result shift left fifteen bits and both
// take 0x7FFF in their low bits; the sample is the unsigned remainder of the
// result modulo the bound.
//
// Exactly ONE draw is consumed per call at every bound. The widening loop ORs
// the same constant into the result that it ORs into the mask — it never takes
// a second draw — so above 32,767 the sample's low fifteen bits are all ones
// and only its top bits carry entropy. That is retail, and it is the behavior
// being cloned (I11), not a transcription slip: the sampler is inlined at both
// of its sites in the same shape, and both are shuffles whose bound is a
// growing prefix length. Correction landed 2026-09-04 (WU-19-155); the earlier
// text here and in [01 §7.2] had the result starting at the constant 0x7FFF
// and a fresh draw ORed in per iteration, which is one to two draws per call
// and a different value.
//
// Divergence (I11): a bound of zero would fault on retail's unsigned divide. We
// consume the draw — retail draws before the divide — and return zero rather
// than panicking.
func (c *CRT) Uint32n(bound uint32) uint32 {
	result := uint32(c.Rand()) & 0x7FFF
	mask := uint32(0x7FFF)
	for mask < bound {
		if mask == 0xFFFFFFFF {
			break
		}
		mask = (mask << 15) | 0x7FFF
		result = (result << 15) | 0x7FFF
	}
	if bound == 0 {
		return 0
	}
	return result % bound
}

// Draws returns the number of draws consumed on this stream. I4.
func (c *CRT) Draws() uint64 { return c.draws }

// Global holds the two process-wide streams.
//
// Both are nil until SeedGlobal runs. That is deliberate: a stream that is
// silently usable before it is seeded produces a run that looks deterministic
// and reproduces nothing. Sim packages should take *Simulation / *CRT
// explicitly rather than reaching for these.
var Global struct {
	Sim *Simulation
	Crt *CRT
}

// SeedGlobal installs both process-wide streams and must run before any
// consumer draws.
//
// The simulation seed goes through the battle-entry transform
// (t ^ 0x66e29572) | 1; retail derives t from QueryPerformanceCounter. The CRT
// seed is applied directly; retail seeds it separately at process start from
// the time source [01 §2.1], [01 §7.1], [01 §7.2]. Passing a fixed pair
// reproduces a run, which matters because RNG state is not saved and is
// reseeded on load [08 "Scheduler and random state in saves"].
func SeedGlobal(simSeed, crtSeed uint32) {
	sim := NewSimulation(simSeed)
	crt := NewCRT(crtSeed)
	Global.Sim = &sim
	Global.Crt = &crt
}

// SeedGlobalIfUnset seeds both streams only when the global simulation stream
// is nil, so library-embedded battle entry always has streams without
// disturbing callers that seed explicitly [01 §7]. Deterministic default.
func SeedGlobalIfUnset(simSeed, crtSeed uint32) {
	if Global.Sim == nil {
		SeedGlobal(simSeed, crtSeed)
	}
}
