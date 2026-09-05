// Package architecture holds the repository's structural guards: source-level
// checks that a boundary the design depends on has not quietly been crossed.
//
// Every check here parses the tree rather than running the engine. They pin
// who may touch the two retail random streams (DET-01), that the headless
// command carries no desktop dependency, that only the platform adapter
// reaches Ebitengine, that authoritative packages import no host or
// nondeterministic runtime facility, and the shrink-only parity-drift
// ratchets (PROC-03) on authoritative map iteration and float64 counts
// [01 §7.1] [01 §7.2] [I4] [I6].
//
// The package has no runtime API: it is all tests, and this file exists only
// to carry the package summary.
package architecture
