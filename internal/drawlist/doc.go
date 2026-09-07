// Package drawlist is the recorded form of one committed-frame walk.
//
// There is exactly one committed-frame ordering in the tree,
// drawCommittedFrame, and it is not duplicated per executor: it records a
// List, and two executors replay that same List. The classic (software)
// executor writes physical palette indices into a byte surface; the modern
// (GPU) executor replays the identical List through Ebitengine in
// palette-index space and expands to RGB once at the end. The simulation
// cannot tell which executor ran. See docs/DESIGN_GPU_RENDERER.md §2.1.
//
// This package is pure Go: no Ebitengine, no device, no pointer into a live
// simulation pool, and no read of the committed frame during replay. Its only
// carried state is physical PALETTE.PAL indices and immutable resource
// references. That is what makes "replay the same List through both executors
// and compare" a complete parity test rather than a sample of one
// [I6].
//
// Contracts owned here (docs/DESIGN_GPU_RENDERER.md §3):
//
//   - C-G1 One walk. drawCommittedFrame is the only committed-frame ordering;
//     it records and does not know which executor replays [03 §1].
//   - C-G2 Physical indices only. Every byte in a record is a physical
//     PALETTE.PAL index; logical GUI/primitive/FNT colours are resolved
//     through the logical map before recording [03 §4.3].
//   - C-G3 Replay preserves order. List.Replay visits commands in exact record
//     order and calls the matching Sink method for each [I1].
package drawlist
