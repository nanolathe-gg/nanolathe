// Package hud is the battle HUD's geometry and state, with no drawing and no
// window.
//
// It owns the side anchor block and the bars placed against it, the selection
// truth table and control groups, the build and command pages, the command
// latch, the panel slide, the cursor choice, the footer and the queue overlay.
// Every value here is derived from the committed frame and the authored side
// data; the composer in internal/client turns them into pixels
// [07 §6] [07 §9] [07 R-HUD-03].
package hud
