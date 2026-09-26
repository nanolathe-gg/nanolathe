// Package aikit is the research framework for Modern computer players: a
// per-player host that runs a stateful "brain" behind the existing ai.Planner
// seam, a fair observation of the world (own units plus only what the owner
// can see or detect on radar, with remembered sightings), a command executor
// that reaches the same order and construction producers a human uses, and
// content-driven unit roles and map analysis every brain shares.
//
// Status: research prototype. No rule set binds it as its own step; Strict
// 3.1 and Community 3.9 keep ai.RetailPlanner and Modern keeps
// ai.ModernPlanner for every Classic computer player. A brain runs for a
// computer player the lobby marks Modern, in any rule set, through the think
// step mods/aikit installs (session.RegisterModernAI), or directly by the
// arena harness, where a computer player without a brain runs
// ai.ModernPlanner.
//
// Determinism. A brain's Think is a pure function of the observation it is
// handed and of its own state: it never reads live simulation objects and
// never draws from either random stream. Its commands take effect a fixed
// number of ticks later (the persona's reaction latency). That is what lets
// the host run Think on another goroutine without changing the game: the
// asynchronous and the synchronous host produce the same tick sequence.
// All decision arithmetic is integer, per docs/INVARIANTS.md I2.
package aikit
