// Package survival holds the content- and session-independent half of the
// Survival scenario (docs/DESIGN_SURVIVAL.md): the tuning table, the
// build-tree tech tiers and wave pool, the wave planner and the connected
// region labelling the director uses to choose start and spawn cells.
//
// Nothing here is retail behaviour. Survival is a scenario composed from
// retail mechanisms; this package decides what to create and where, and the
// session creates it through the ordinary allocator and order queues. The
// planner draws only from the simulation stream it is handed, in the order
// DESIGN_SURVIVAL §6.8 records.
package survival
