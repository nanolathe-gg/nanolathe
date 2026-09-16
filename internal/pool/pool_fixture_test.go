// Test-only pool helpers.
//
// Neither form ships. Production sizes the pool through CapacityForDefs and
// reads the maintained Used count; these are the reference forms the pool's own
// tests hold those against [P0-16].

package pool

// UsableCapacityForDefs returns the usable slot count excluding the null
// sentinel: maxDefs*10 [P0-16].
func UsableCapacityForDefs(maxDefs int) int {
	if maxDefs < 0 {
		maxDefs = 0
	}
	return maxDefs * 10
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
