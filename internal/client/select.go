// Package client — committed-frame selection geometry and picking.
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
package client

import (
	"github.com/nanolathe/nanolathe/internal/hud"
)

// View-pane origin offsets for presentation conversion [07 §9][03 §2.5].
// The observed beam path uses 128,32; the drag-rect conversion uses the same
// fixed offsets to map world→presentation by camera subtraction + origin.
const (
	ViewOriginX int32 = 128 // [07 §9][03 §2.5]
	ViewOriginY int32 = 32  // [07 §9][03 §2.5]
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

// WorldToPresentation converts a world map-pixel coordinate to presentation
// space by subtracting the camera and adding the fixed origin [07 §9].
// World pixels are map pixels (world Fixed >>16). For Fixed inputs use
// FixedToPresentation.
func WorldToPresentation(world, camera, origin int32) int32 { // [07 §9]
	return world - camera + origin
}

// FixedToPresentation converts a world Fixed (16.16) coordinate to presentation
// space [07 §9][03 §2.5][I3]. It truncates toward zero (>>16 via arithmetic
// shift) before the camera subtraction, matching the retail projection's
// integer path. World Y shear (>>1) is not applied here; drag selection uses
// the X/Z ground plane via the same formula on each axis.
func FixedToPresentation(worldFixed int64, camera, origin int32) int32 { // [07 §9]
	worldPixel := int32(worldFixed >> 16)
	return worldPixel - camera + origin
}

// DragRectFromWorld builds a normalized drag rect from world map-pixel
// endpoints and camera [07 §9].
// wx0,wz0 and wx1,wz1 are world positions in map pixels; camX,camZ is the
// camera origin in the same units.
func DragRectFromWorld(wx0, wz0, wx1, wz1, camX, camZ int32) Rect { // [07 §9]
	ax := WorldToPresentation(wx0, camX, ViewOriginX)
	ay := WorldToPresentation(wz0, camZ, ViewOriginY)
	bx := WorldToPresentation(wx1, camX, ViewOriginX)
	by := WorldToPresentation(wz1, camZ, ViewOriginY)
	return NormalizeRect(ax, ay, bx, by)
}

// DragRectFromFixed is the Fixed-point variant of DragRectFromWorld [07 §9].
func DragRectFromFixed(wx0, wz0, wx1, wz1 int64, camX, camZ int32) Rect { // [07 §9]
	ax := FixedToPresentation(wx0, camX, ViewOriginX)
	ay := FixedToPresentation(wz0, camZ, ViewOriginY)
	bx := FixedToPresentation(wx1, camX, ViewOriginX)
	by := FixedToPresentation(wz1, camZ, ViewOriginY)
	return NormalizeRect(ax, ay, bx, by)
}
