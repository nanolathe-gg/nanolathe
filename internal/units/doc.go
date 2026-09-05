// Package units owns the unit pool and the per-unit record: creation, the
// definition identity, the COB and model bindings a live unit carries, the
// per-unit pre-update stage of the authoritative sweep, and death
// finalization [01 §6.1] [04 §1.1] [04 R-UNIT-06].
//
// It is the record every later simulation package writes through. The sweep
// itself is driven by internal/session; this package supplies the traversal
// and the stage boundaries.
package units
