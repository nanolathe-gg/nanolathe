// Package pool implements the fixed-capacity deterministic pools that underlie
// the simulation. Retail's pool behaviour is load-bearing for determinism:
// iteration order, allocation order, and compaction order are part of the
// contract [01 §6.1], [01 §6.2].
package pool
