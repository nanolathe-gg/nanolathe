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
// There is deliberately no chunk-concatenation path here. That helper belongs
// to the CRT stream, which yields only 15 bits per draw [01 §7.2]; Park–Miller
// already yields 31 bits, so every bound up to the modulus is covered by a
// single draw. Adding one would make simRand(0x10000) — the wind heading of
// [01 §7.3] and [05 "Wind generation"] — cost two draws instead of one and
// return a different value.
func (s *Simulation) Uint32n(bound uint32) uint32 {
	if bound < 2 {
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
// Bounds above 32,767 use the chunk-concatenation loop of [01 §7.2], followed
// literally: mask and result both start at 0x7FFF; while the mask is below
// the needed bound, shift both left by 15 bits, OR the mask with 0x7FFF again,
// and OR another fresh draw into the result; the final result modulo the
// bound is the sample. One draw is consumed per iteration.
//
// TODO(question): the merged pre-review code instead seeded result from a
// fresh draw (one extra draw per call, different value). The research text's
// "starting with mask and result both 0x7FFF" is explicit about result's
// initialization, so this implementation follows the letter; if
// disassembly evidence ever shows an initial draw, change exactly this loop
// and its vectors. No stock content draws a CRT bound above 32767 today
// (briefing wind span+1 tops out at 2000), so nothing observable hinges on
// it until a consumer appears [06 §6.5].
//
// Divergence (I11): a bound of zero would fault on retail's modulo. We consume
// the draw — retail evaluates rand() before the divide — and return zero
// rather than panicking.
func (c *CRT) Uint32n(bound uint32) uint32 {
	if bound <= 0x7FFF {
		v := uint32(c.Rand())
		if bound == 0 {
			return 0
		}
		return v % bound
	}
	result := uint32(0x7FFF)
	mask := uint32(0x7FFF)
	for mask < bound {
		result = (result << 15) | uint32(c.Rand())
		mask = (mask << 15) | 0x7FFF
		if mask == 0xFFFFFFFF {
			break
		}
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
