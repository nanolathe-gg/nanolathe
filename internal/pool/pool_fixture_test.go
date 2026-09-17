// Test-only pool helpers.
//
// Neither form ships. Production sizes the pool through CapacityForLimit and
// reads the maintained Used count; these are the reference forms the pool's own
// tests hold those against [P0-16].

package pool

// UsableCapacityForLimit returns the usable slot count excluding the null
// sentinel: limit*10 [P0-16].
func UsableCapacityForLimit(limit int) int {
	if limit < 0 {
		limit = 0
	}
	return limit * 10
}

// countUsed recomputes the allocated-slot count by scanning. Production reads
// Used; this is the reference the maintained count is tested against.
func (p *Units) countUsed() int {
	if p == nil || p.alive == nil {
		return 0
	}
	n := 0
	for i := 1; i < len(p.alive); i++ {
		if p.alive[i] && p.defID[i] != 0 {
			n++
		}
	}
	return n
}
