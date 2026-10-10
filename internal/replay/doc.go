// Package replay is Nanolathe's local replay file: its versioned chunked
// format, the recorders that write it — one observing a single-player
// session, one wrapping an online battle's relay client — the composition
// that rebuilds a recorded battle for playback, and the synchronous player
// that runs the recorded pumps and checks every recorded unit checksum
// (docs/DESIGN_MULTIPLAYER.md §10).
//
// A replay is not retail evidence and has no retail counterpart. It holds
// the battle's agreed configuration and the inputs that reached phase 1 —
// commands with their stream positions, the host's pump boundaries
// (§4.5) — plus the 30-tick unit checksum (§9.2) the playback must
// reproduce. Nothing here runs inside a tick or changes what a tick
// computes: a recorder only copies what the session or the relay hands it,
// and the player drives the session through its ordinary recorded and
// granted pump entry points.
package replay
