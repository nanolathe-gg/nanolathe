// Package ai is the skirmish planner: the authored profile and its directive
// grammar, the per-player manager and its tasks, the strategic refresh, build
// candidate selection, site placement, and the classifier that files units
// into task groups [08 "Established AI-facing data and rooted planner"]
// [08 R-AI-01] [08 R-AI-03].
//
// One manager belongs to one player slot. The session ticks it inside phase 5,
// before that player's settlement deadline; the manager issues ordinary orders
// through the same queue a human commander uses and has no privileged path
// into the simulation.
package ai
