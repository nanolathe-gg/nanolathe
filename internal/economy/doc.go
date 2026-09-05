// Package economy owns the per-player and per-unit resource ledgers and the
// two-stage settlement that retail runs once per tick: producers accumulate
// into their buckets, then admission distributes what the stores can pay
// [05 "Authoritative settlement order"].
//
// Bucket amounts are float32 because retail's are; the allowlist in
// docs/INVARIANTS.md I2 names this package for that reason.
package economy
