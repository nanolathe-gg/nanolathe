// Package rng provides named, deterministic random streams for simulation state.
package rng

import "sort"

const (
	retailParkMillerMultiplier uint32 = 16807
	retailParkMillerQuotient   uint32 = 127773
	retailParkMillerModulus    uint32 = 0x7fffffff
	retailSeedXOR              uint32 = 0x66e29572
)

// RetailSimulation is the original engine's shared simulation generator.
// It is intentionally separate from Stream: retail couples every simulation
// consumer through one state cell, while OpenTA currently owns independent
// named streams. Callers must not substitute this type until the original
// binary's complete caller order is known.
type RetailSimulation struct {
	State uint32
	Draws uint64
}

// NewRetailSimulation applies the seed transform performed by the original
// executable before its first simulation draw.
func NewRetailSimulation(seed uint32) RetailSimulation {
	return RetailSimulation{State: (seed ^ retailSeedXOR) | 1}
}

// RetailSimulationFromState restores the generator's exact 32-bit state.
// It is useful for known-vector verification and, eventually, save/hash state.
func RetailSimulationFromState(state uint32) RetailSimulation {
	return RetailSimulation{State: state}
}

// Uint32n matches the original bounded helper: bounds below two return zero
// without advancing the globally shared stream; all other bounds advance once
// and return the unsigned remainder.
func (s *RetailSimulation) Uint32n(bound uint32) uint32 {
	if bound < 2 {
		return 0
	}
	// The binary's 32-bit expression is algebraically the Park-Miller Schrage
	// update. Keeping the uint32 wrap makes this faithful even for a restored
	// state outside the generator's ordinary 1..m-1 invariant.
	next := s.State*retailParkMillerMultiplier + (s.State/retailParkMillerQuotient)*uint32(0x80000001)
	if int32(next) < 1 {
		next += retailParkMillerModulus
	}
	s.State = next
	s.Draws++
	return next % bound
}

// RetailCRT reproduces the MSVCRT rand() generator the retail executable
// uses outside the simulation stream — meteor shower scheduling and per-hit
// geometry among them (environmental-tails.md §5.3): state = state*214013 +
// 2531011, result (state>>16)&0x7FFF.
type RetailCRT struct {
	State uint32
	Draws uint64
}

// NewRetailCRT seeds the generator directly; retail seeds it from world
// identity, so OpenTA derives the value from the match seed.
func NewRetailCRT(seed uint32) RetailCRT {
	return RetailCRT{State: seed}
}

// Rand advances the state once and returns the value in [0, 0x7FFF].
func (r *RetailCRT) Rand() int32 {
	r.State = r.State*214013 + 2531011
	r.Draws++
	return int32((r.State >> 16) & 0x7FFF)
}

// Stream is a small deterministic generator. Its state is portable and may be
// included in a save or state hash.
type Stream struct {
	Name  string
	State uint64
	Draws uint64
}

func NewStream(name string, seed uint64) Stream {
	return Stream{Name: name, State: seed}
}

// Uint64 uses SplitMix64. It is not a cryptographic generator.
func (s *Stream) Uint64() uint64 {
	s.State += 0x9e3779b97f4a7c15
	z := s.State
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	s.Draws++
	return z ^ (z >> 31)
}

func (s *Stream) Uint32() uint32 { return uint32(s.Uint64() >> 32) }

func (s *Stream) Bool() bool { return s.Uint64()&1 != 0 }

// Intn returns a value in [0,n). A non-positive bound returns zero without a draw.
func (s *Stream) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	b := uint64(n)
	limit := ^uint64(0) - (^uint64(0) % b)
	for {
		v := s.Uint64()
		if v < limit {
			return int(v % b)
		}
	}
}

// Streams owns independent named streams. Names are sorted for stable snapshots.
type Streams struct {
	seed    uint64
	streams map[string]*Stream
}

func New(seed uint64) *Streams { return &Streams{seed: seed, streams: make(map[string]*Stream)} }

func (s *Streams) Get(name string) *Stream {
	if s.streams == nil {
		s.streams = make(map[string]*Stream)
	}
	v, ok := s.streams[name]
	if !ok {
		v := NewStream(name, seedFor(s.seed, seedFromName(name)))
		s.streams[name] = &v
		return &v
	}
	return v
}

func (s *Streams) Seed(name string, seed uint64) {
	if s.streams == nil {
		s.streams = make(map[string]*Stream)
	}
	v := NewStream(name, seed)
	s.streams[name] = &v
}

func (s *Streams) Snapshot() []Stream {
	keys := make([]string, 0, len(s.streams))
	for k := range s.streams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Stream, 0, len(keys))
	for _, k := range keys {
		out = append(out, *s.streams[k])
	}
	return out
}

func seedFromName(name string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(name); i++ {
		h = (h ^ uint64(name[i])) * 1099511628211
	}
	return h
}
func seedFor(seed, salt uint64) uint64 { return seed ^ (salt + 0x9e3779b97f4a7c15 + seed<<6 + seed>>2) }
