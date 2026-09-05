// Package session is the authoritative sub-tick: it owns the twelve phases of
// [01 §4.4], in order, and the publication boundary at the end of them.
//
// One session holds the world, the unit pool, and every simulation service —
// orders, movement, economy, construction, combat, visibility, the AI, the
// mission triggers — and calls each one from the phase it belongs to. Nothing
// else may step them, and no service reads another's state across a phase
// boundary except through the session.
//
// The boundary at the end of a tick is the committed frame [I6]: presentation
// samples the tick as committed, never a partial one, and never writes back.
// The save boundary is the retail bank of [08 "Save-file organization"]: a
// battle is written from the same authoritative state the tick leaves and
// restored into a staged session before it steps.
package session
