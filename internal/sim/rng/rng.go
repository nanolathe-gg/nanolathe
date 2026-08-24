// Package rng provides the two deterministic random streams used by the
// retail engine. [01 §7.1][01 §7.2][01 §7.3]
package rng

const (
	retailParkMillerMultiplier uint32 = 16807
	retailParkMillerQuotient   uint32 = 127773
	retailParkMillerModulus    uint32 = 0x7fffffff
	retailSeedXOR              uint32 = 0x66e29572
)

// RetailSimulation is the original engine's shared simulation generator.
// Retail couples every simulation consumer through one state cell. [01 §7.1]
type RetailSimulation struct {
	State uint32
	Draws uint64
}

// NewRetailSimulation applies the seed transform performed by the original
// executable before its first simulation draw. [01 §7.1][01 §2.1]
func NewRetailSimulation(seed uint32) RetailSimulation {
	return RetailSimulation{State: (seed ^ retailSeedXOR) | 1}
}

// RetailSimulationFromState restores the generator's exact 32-bit state.
// It is useful for known-vector verification and, eventually, save/hash state.
func RetailSimulationFromState(state uint32) RetailSimulation {
	return RetailSimulation{State: state}
}

// Uint32n matches the original bounded helper: bounds below two return zero
// without advancing the globally shared stream; all other bounds advance and
// return the unsigned remainder. [01 §7.1][01 §7.3]
// For bounds above 32767 the value is sampled by 15-bit chunk concatenation
// before the final modulo, mirroring the retail inlined helper.
func (s *RetailSimulation) Uint32n(bound uint32) uint32 {
	if bound < 2 {
		return 0
	}
	if bound <= 0x7FFF {
		// The binary's 32-bit expression is algebraically the Park-Miller Schrage
		// update. Keeping the uint32 wrap makes this faithful even for a restored
		// state outside the generator's ordinary 1..m-1 invariant. [01 §7.1]
		next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
		if int32(next) < 1 {
			next += retailParkMillerModulus
		}
		s.State = next
		s.Draws++
		return next % bound
	}
	// [01 §7.1][01 §7.3] bounds above 32767 sampled by 15-bit chunk concatenation.
	var result uint32
	var mask uint32 = 0x7FFF
	next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
	if int32(next) < 1 {
		next += retailParkMillerModulus
	}
	s.State = next
	s.Draws++
	result = next & 0x7FFF
	for mask < bound {
		next = s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
		if int32(next) < 1 {
			next += retailParkMillerModulus
		}
		s.State = next
		s.Draws++
		result = (result << 15) | (next & 0x7FFF)
		mask = (mask << 15) | 0x7FFF
		if mask == 0xFFFFFFFF {
			break
		}
	}
	return result % bound
}

// RetailCRT reproduces the MSVCRT rand() generator the retail executable
// uses outside the simulation stream — meteor shower scheduling and per-hit
// geometry among them (environmental-tails.md §5.3): state = state*214013 +
// 2531011, result (state>>16)&0x7FFF. [01 §7.2]
type RetailCRT struct {
	State uint32
	Draws uint64
}

// NewRetailCRT seeds the generator directly; retail seeds it from world
// identity, so OpenTA derives the value from the match seed. [01 §2.1]
func NewRetailCRT(seed uint32) RetailCRT {
	return RetailCRT{State: seed}
}

// Rand advances the state once and returns the value in [0, 0x7FFF]. [01 §7.2]
func (r *RetailCRT) Rand() int32 {
	r.State = r.State*214013 + 2531011
	r.Draws++
	return int32((r.State >> 16) & 0x7FFF)
}

// Uint32n is the CRT equivalent of the bounded helper: bounds below two return
// zero without advancing, bounds up to 32767 use a single Rand() draw, and
// bounds above 32767 use the 15-bit chunk concatenation helper before modulo. [01 §7.2]
func (r *RetailCRT) Uint32n(bound uint32) uint32 {
	if bound < 2 {
		return 0
	}
	if bound <= 0x7FFF {
		r.State = r.State*214013 + 2531011
		r.Draws++
		return uint32((r.State>>16)&0x7FFF) % bound
	}
	var result uint32
	var mask uint32 = 0x7FFF
	r.State = r.State*214013 + 2531011
	r.Draws++
	result = (r.State >> 16) & 0x7FFF
	for mask < bound {
		r.State = r.State*214013 + 2531011
		r.Draws++
		result = (result << 15) | ((r.State >> 16) & 0x7FFF)
		mask = (mask << 15) | 0x7FFF
		if mask == 0xFFFFFFFF {
			break
		}
	}
	return result % bound
}

// Simulation is the Park–Miller Schrage stream used for authoritative
// gameplay. It is the same arithmetic as RetailSimulation but exposed under
// the plan's canonical name and with a Draws() accessor that never participates
// in simulation input (I4). [01 §7.1]
type Simulation struct {
	State uint32
	draws uint64
}

// NewSimulation applies the retail seed transform (t ^ 0x66e29572)|1. [01 §7.1][01 §2.1]
func NewSimulation(seed uint32) Simulation {
	return Simulation{State: (seed ^ retailSeedXOR) | 1}
}

// SimulationFromState restores the generator's exact 32-bit state.
func SimulationFromState(state uint32) Simulation {
	return Simulation{State: state}
}

// Uint32n implements the bounded Park–Miller helper with the same contracts as
// RetailSimulation: bound<2 returns 0 without advancing, bounds up to 32767
// advance once and return remainder, bounds above 32767 use 15-bit chunk
// concatenation before modulo. [01 §7.1][01 §7.3]
func (s *Simulation) Uint32n(bound uint32) uint32 {
	if bound < 2 {
		return 0
	}
	if bound <= 0x7FFF {
		next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
		if int32(next) < 1 {
			next += retailParkMillerModulus
		}
		s.State = next
		s.draws++
		return next % bound
	}
	var result uint32
	var mask uint32 = 0x7FFF
	next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
	if int32(next) < 1 {
		next += retailParkMillerModulus
	}
	s.State = next
	s.draws++
	result = next & 0x7FFF
	for mask < bound {
		next = s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
		if int32(next) < 1 {
			next += retailParkMillerModulus
		}
		s.State = next
		s.draws++
		result = (result << 15) | (next & 0x7FFF)
		mask = (mask << 15) | 0x7FFF
		if mask == 0xFFFFFFFF {
			break
		}
	}
	return result % bound
}

// Draws returns the number of draws consumed. It is a debug counter and never
// participates in simulation state (I4).
func (s *Simulation) Draws() uint64 { return s.draws }

// CRT is the per-thread MSVCRT stream (*214013+2531011) used for meteor
// geometry, screen shake, audio variants and briefing wind. [01 §7.2]
type CRT struct {
	State uint32
	draws uint64
}

// NewCRT seeds the generator directly. Retail seeds it at process start from
// time/time-zone conversion at one-second resolution. [01 §2.1]
func NewCRT(seed uint32) CRT { return CRT{State: seed} }

// CRTFromState restores the exact 32-bit CRT state.
func CRTFromState(state uint32) CRT { return CRT{State: state} }

// Rand advances the state once and returns the value in [0, 0x7FFF]. [01 §7.2]
func (c *CRT) Rand() int32 {
	c.State = c.State*214013 + 2531011
	c.draws++
	return int32((c.State >> 16) & 0x7FFF)
}

// Uint32n implements the CRT bounded helper with 15-bit chunk handling for
// bounds above 32767. [01 §7.2]
func (c *CRT) Uint32n(bound uint32) uint32 {
	if bound < 2 {
		return 0
	}
	if bound <= 0x7FFF {
		c.State = c.State*214013 + 2531011
		c.draws++
		return uint32((c.State>>16)&0x7FFF) % bound
	}
	var result uint32
	var mask uint32 = 0x7FFF
	c.State = c.State*214013 + 2531011
	c.draws++
	result = (c.State >> 16) & 0x7FFF
	for mask < bound {
		c.State = c.State*214013 + 2531011
		c.draws++
		result = (result << 15) | ((c.State >> 16) & 0x7FFF)
		mask = (mask << 15) | 0x7FFF
		if mask == 0xFFFFFFFF {
			break
		}
	}
	return result % bound
}

// Draws returns the number of draws consumed on this stream. I4.
func (c *CRT) Draws() uint64 { return c.draws }

// Global holds the two process-wide streams. Simulation is seeded at battle
// entry as (t ^ 0x66e29572)|1; CRT is seeded at process start. [01 §2.1][01 §7.1]
var Global struct {
	Sim *Simulation
	Crt *CRT
}

func init() {
	sim := NewSimulation(0)
	crt := NewCRT(1)
	Global.Sim = &sim
	Global.Crt = &crt
}
