package client

// Committed-frame selection geometry and picking.
//
// Retail contract [07 §9] C6:
//
//   - Drag endpoints recorded in world coordinates are converted to presentation
//     coordinates by subtracting the camera position and adding the fixed
//     view-pane origin offsets (128 horizontally, 32 vertically); each axis is
//     then sorted independently (if right < left swap, if bottom < top swap) and
//     both boundaries are tested inclusively (min <= x <= max && min <= y <= max).
//
//   - Selection membership is bit 0x10 of unit runtime flags.
//
//   - Iteration over the owner's unit range is ascending and stable.
//
//   - Truth table for eligible units:
//
//     | Modifier | inside rect            | outside rect                                |
//     |----------|------------------------|---------------------------------------------|
//     | clear 0  | set (flags \|= 0x10)   | clear (bulk pre-clear flags &= 0xFFFFFF2F) |
//     | set 1    | toggle (flags ^= 0x10) | preserve                                    |
//
//   - Eligibility predicate tests authoritative fields: active-state bit 0x20,
//     exact single-precision value == 1.0, no disqualifying state reference
//     (zero), and either no parent or parent flags carry 0x40000000 [07 §9].
//     Bulk changes also clear the selected-builder single-select id, refresh
//     aggregate command/UI state, and set battle-interface dirty bit 0x10.
//
// Gate 1 has no simulation unit pool. This file implements the truth table as
// pure functions over presentation coordinates and flag bits so the contract
// can be unit-tested and later wired to the phase-6 pool without changing the
// table. Deterministic iteration is preserved (I1) by iterating slices in
// ascending index order, which maps to ascending slot order when the pool is
// present.
//
// Selection mutation is owned by HUD/UI semantics; this package only exposes
// geometry and deterministic picking.

import (
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// Rect is an alias for the HUD's canonical drag rectangle. Client owns only
// projection and picking; selection mutation lives in internal/hud.
type Rect = hud.DragRect

// NormalizeRect sorts drag endpoints into an inclusive presentation rect [07 §9].
//
// Each axis is sorted independently (if right < left swap, if bottom < top swap)
// after the world→presentation conversion has been applied. The result is the
// exact inclusive test used by retail.
func NormalizeRect(ax, ay, bx, by int32) Rect { // [07 §9]
	return hud.NormalizeDragRect(ax, ay, bx, by)
}

func rectEmpty(r Rect) bool { return r.MinX > r.MaxX || r.MinY > r.MaxY }
