package aikit

// Rand is a computer player's private random generator: PCG32 (O'Neill,
// "PCG: A Family of Simple Fast Space-Efficient Statistically Good
// Algorithms for Random Number Generation", 2014), the PCG-XSH-RR variant
// with 64-bit state and 32-bit output, seeded the way the reference
// implementation's pcg32_srandom_r is.
//
// It is the one exception to the rule that decisions draw no random numbers
// (docs/INVARIANTS.md I4, "Modern AI exception"): variety must not come from
// either simulation stream, because an asynchronous think runs on another
// core and would race the simulation for its draws. Each Host owns exactly
// one generator, seeded from the battle seed and the player slot, and only
// that player's brain draws from it, so a game stays reproducible from its
// seed and the synchronous and asynchronous hosts play identical games.
type Rand struct {
	state, inc uint64
}

// pcgMult is the 64-bit LCG multiplier of the reference implementation.
const pcgMult = 6364136223846793005

// NewRand seeds a generator: initState picks the position in the sequence
// and stream selects one of 2^63 independent sequences.
func NewRand(initState, stream uint64) Rand {
	var r Rand
	r.Seed(initState, stream)
	return r
}

// PlayerRand is the generator a Host gives its brain: the battle seed and
// the player slot pick the state, and the slot the stream, so two players
// in one battle draw from unrelated sequences and a new battle seed changes
// both. The seed and slot pass through the SplitMix64 finalizer first:
// PCG's first outputs from nearby states are visibly related (consecutive
// battle seeds clumped a first draw's distribution), and hashing the seed
// gives every battle an unrelated starting state.
func PlayerRand(battleSeed uint32, slot uint8) Rand {
	return NewRand(mix64(uint64(battleSeed)<<8|uint64(slot)), uint64(slot)+1)
}

// mix64 is the SplitMix64 output function (Steele, Lea and Flood, "Fast
// Splittable Pseudorandom Number Generators", 2014).
func mix64(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Seed resets the generator (pcg32_srandom_r).
func (r *Rand) Seed(initState, stream uint64) {
	r.state = 0
	r.inc = stream<<1 | 1
	r.Uint32()
	r.state += initState
	r.Uint32()
}

// Position is the generator's place in its sequence: the 64-bit state. The
// stream is not part of it; a player's stream is fixed by its slot
// (PlayerRand).
func (r *Rand) Position() uint64 { return r.state }

// SetPosition moves the generator to a place Position reported, on the same
// stream, so it continues the sequence from there.
func (r *Rand) SetPosition(state uint64) { r.state = state }

// Uint32 returns the next 32 uniformly distributed bits.
func (r *Rand) Uint32() uint32 {
	old := r.state
	r.state = old*pcgMult + r.inc
	xorshifted := uint32(((old >> 18) ^ old) >> 27)
	rot := uint32(old >> 59)
	return xorshifted>>rot | xorshifted<<((-rot)&31)
}

// Intn returns a uniform integer in [0, n); n <= 0 returns 0 without
// drawing. It is unbiased (Lemire's multiply-and-reject bound).
func (r *Rand) Intn(n int32) int32 {
	if n <= 0 {
		return 0
	}
	bound := uint32(n)
	m := uint64(r.Uint32()) * uint64(bound)
	if low := uint32(m); low < bound {
		threshold := -bound % bound
		for low < threshold {
			m = uint64(r.Uint32()) * uint64(bound)
			low = uint32(m)
		}
	}
	return int32(m >> 32)
}

// Range returns a uniform integer in [lo, hi] (lo when hi <= lo, without
// drawing).
func (r *Rand) Range(lo, hi int32) int32 {
	if hi <= lo {
		return lo
	}
	return lo + r.Intn(hi-lo+1)
}

// Chance reports true with probability permille/1000.
func (r *Rand) Chance(permille int32) bool {
	if permille <= 0 {
		return false
	}
	if permille >= 1000 {
		return true
	}
	return r.Intn(1000) < permille
}

// Pick returns an index drawn with probability proportional to its weight
// (negative weights count as zero), or -1 without drawing when every weight
// is zero.
func (r *Rand) Pick(weights []int32) int {
	var total int32
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	if total == 0 {
		return -1
	}
	x := r.Intn(total)
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		if x < w {
			return i
		}
		x -= w
	}
	return len(weights) - 1
}
