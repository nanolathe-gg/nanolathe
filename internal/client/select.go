// Package client — drag-rectangle selection placeholder (WU-04A-6, Gate 1).
//
// Retail contract [07 §9] C6:
//
//   - Drag endpoints recorded in world coordinates are converted to presentation
//     coordinates by subtracting the camera position and adding the fixed
//     view-pane origin offsets (128 horizontally, 32 vertically); each axis is
//     then sorted independently (if right < left swap, if bottom < top swap) and
//     both boundaries are tested inclusively (min <= x <= max && min <= y <= max).
//   - Selection membership is bit 0x10 of unit runtime flags.
//   - Iteration over the owner's unit range is ascending and stable.
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
// TODO: wire to phase-6 pool. When the unit pool lands, replace the
// Selectable/Eligible placeholder with iteration over the owning player's
// inclusive unit range at fixed 280-byte stride, compute eligibility from
// authoritative fields, and apply the same NextSelected/ApplyDragSelection
// logic. The bulk pre-clear path should use mask 0xFFFFFF2F across that range
// before the set-inside pass [07 §9].
package client

// View-pane origin offsets for presentation conversion [07 §9][03 §2.5].
// The observed beam path uses 128,32; the drag-rect conversion uses the same
// fixed offsets to map world→presentation by camera subtraction + origin.
const (
	ViewOriginX int32 = 128 // [07 §9][03 §2.5]
	ViewOriginY int32 = 32  // [07 §9][03 §2.5]
)

// Selection membership and bulk-clear masks [07 §9].
const (
	// SelectionFlag is the selection membership bit in unit runtime flags.
	SelectionFlag uint32 = 0x10 // [07 §9]

	// SelectionBulkClearMask is the retail bulk pre-clear mask applied across
	// the whole pool before iteration when the modifier is clear. It clears
	// 0x10 (selection) plus 0x40 and 0x80 (companion presentation bits) [07 §9].
	// The placeholder ApplyDragSelection clears only 0x10; the full mask is
	// retained here for the phase-6 wiring.
	SelectionBulkClearMask uint32 = 0xFFFFFF2F // [07 §9]

	// toggle modifier is bit 2 of the drag parameter word [07 §9].
	SelectionToggleBit uint32 = 1 << 2
)

// Rect is an inclusive presentation-space rectangle [07 §9].
// Both boundaries are tested inclusively: min <= x <= max && min <= y <= max.
// Construct via NormalizeRect so the invariant MinX<=MaxX, MinY<=MaxY holds.
type Rect struct {
	MinX, MinY int32
	MaxX, MaxY int32
}

// NormalizeRect sorts drag endpoints into an inclusive presentation rect [07 §9].
//
// Each axis is sorted independently (if right < left swap, if bottom < top swap)
// after the world→presentation conversion has been applied. The result is the
// exact inclusive test used by retail.
func NormalizeRect(ax, ay, bx, by int32) Rect { // [07 §9]
	minX, maxX := ax, bx
	if maxX < minX {
		minX, maxX = maxX, minX
	}
	minY, maxY := ay, by
	if maxY < minY {
		minY, maxY = maxY, minY
	}
	return Rect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

// Contains reports whether (x,y) is inside r inclusive [07 §9].
func (r Rect) Contains(x, y int32) bool { // [07 §9]
	return x >= r.MinX && x <= r.MaxX && y >= r.MinY && y <= r.MaxY
}

// IsEmpty reports whether r is empty. A rect with Min > Max on any axis is
// empty. NormalizeRect never produces an empty rect, but callers that build
// Rect literals should check.
func (r Rect) IsEmpty() bool {
	return r.MinX > r.MaxX || r.MinY > r.MaxY
}

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

// IsSelected reports whether flags carries SelectionFlag (0x10) [07 §9].
func IsSelected(flags uint32) bool { return flags&SelectionFlag != 0 }

// SetSelected sets the selection bit [07 §9].
func SetSelected(flags uint32) uint32 { return flags | SelectionFlag }

// ClearSelected clears the selection bit [07 §9].
// Retail bulk pre-clear uses SelectionBulkClearMask (0xFFFFFF2F) which also
// clears bits 0x40/0x80; this helper clears only 0x10 for the placeholder.
func ClearSelected(flags uint32) uint32 { return flags &^ SelectionFlag }

// ToggleSelected toggles the selection bit [07 §9].
func ToggleSelected(flags uint32) uint32 { return flags ^ SelectionFlag }

// NextSelected is the pure truth table for one eligible unit [07 §9] C6.
//
//   additive == false (modifier clear): selected = inside
//   additive == true  (modifier set):   selected = inside ? !old : old
//
// This is the complete C6 contract for a single unit. Bulk application must
// iterate in ascending stable order (I1) and apply this per unit.
func NextSelected(oldSelected, inside, additive bool) bool { // [07 §9] C6
	if additive {
		if inside {
			return !oldSelected
		}
		return oldSelected
	}
	return inside
}

// NextFlags applies the truth table directly to a flags word [07 §9] C6.
// Ineligible units should not call this; they are preserved by the caller.
// For additive==false it sets or clears 0x10 based on inside; for
// additive==true it toggles when inside and preserves otherwise.
func NextFlags(flags uint32, inside, additive bool) uint32 { // [07 §9] C6
	old := flags&SelectionFlag != 0
	next := NextSelected(old, inside, additive)
	if next == old {
		return flags
	}
	if next {
		return flags | SelectionFlag
	}
	return flags &^ SelectionFlag
}

// Selectable is the placeholder selectable unit for Gate 1 [07 §9].
//
// X,Y are presentation coordinates (inclusive test against Rect). Flags holds
// runtime flag bits including SelectionFlag (0x10). Eligible indicates whether
// the unit passes the authoritative eligibility predicate (active 0x20, float
// 1.0, state ref, parent 0x40000000) [07 §9]. For Gate 1 the caller sets
// Eligible directly; phase-6 wiring will compute it from the pool.
//
// Slot ordering: the slice index is the stable ascending order that maps to the
// owner's inclusive unit-range slot order when the pool is present (I1) [07 §9].
type Selectable struct {
	X, Y     int32
	Flags    uint32
	Eligible bool
}

// ApplyDragSelection applies the C6 truth table to units in place [07 §9] C6.
//
//   - additive==false: eligible inside are set (|=0x10), eligible outside are
//     cleared (&^0x10). Implemented as NextSelected==inside, which is equivalent
//     to the retail bulk pre-clear (&=0xFFFFFF2F) followed by set-inside.
//   - additive==true: eligible inside toggle (^=0x10), eligible outside preserved.
//
// Iteration is ascending index order (stable) [07 §9][I1]. Ineligible units are
// preserved regardless of rect or modifier. Returns whether any selection bit
// changed and the number of selected eligible units after the update.
//
// This is the Gate-1 placeholder that will be wired to the phase-6 unit pool.
// The predicate variant ApplyDragSelectionFunc should be used when the source
// is not a slice.
func ApplyDragSelection(units []Selectable, rect Rect, additive bool) (changed bool, selectedCount int) { // [07 §9] C6
	// Stable ascending iteration [07 §9][I1].
	for i := 0; i < len(units); i++ {
		if !units[i].Eligible {
			if units[i].Flags&SelectionFlag != 0 {
				selectedCount++
			}
			continue
		}
		inside := rect.Contains(units[i].X, units[i].Y)
		old := units[i].Flags&SelectionFlag != 0
		next := NextSelected(old, inside, additive)
		if next != old {
			changed = true
			if next {
				units[i].Flags |= SelectionFlag
			} else {
				units[i].Flags &^= SelectionFlag
			}
		}
		if units[i].Flags&SelectionFlag != 0 {
			selectedCount++
		}
	}
	return changed, selectedCount
}

// ApplyDragSelectionFunc is the predicate-based variant for pool wiring [07 §9] C6.
//
// n is the number of units in the owner's inclusive range. Iteration is
// 0..n-1 ascending (stable) [07 §9][I1]. Callbacks:
//
//   eligible(i) reports eligibility (active 0x20, float 1.0, etc.) [07 §9].
//   inside(i) reports whether the unit's presentation position is inside rect.
//   isSelected(i) reports current selection bit.
//   set(i)/clear(i)/toggle(i) mutate the selection bit.
//
// Ineligible units are not mutated. Returns changed and post-count of selected
// units (including ineligible that were already selected, which are counted but
// not mutated).
func ApplyDragSelectionFunc(n int, eligible func(int) bool, inside func(int) bool, isSelected func(int) bool, set func(int), clear func(int), toggle func(int), additive bool) (changed bool, selectedCount int) { // [07 §9] C6
	for i := 0; i < n; i++ {
		if !eligible(i) {
			if isSelected(i) {
				selectedCount++
			}
			continue
		}
		in := inside(i)
		old := isSelected(i)
		var next bool
		if additive {
			if in {
				next = !old
			} else {
				next = old
			}
		} else {
			next = in
		}
		if next != old {
			changed = true
			if in {
				// additive paths use toggle, non-additive uses set/clear.
				if additive {
					toggle(i)
				} else if next {
					set(i)
				} else {
					clear(i)
				}
			} else {
				// outside with additive==false was already handled as next==in==false
				// but we distinguish for clarity.
				if !additive {
					clear(i)
				}
			}
			// For the toggle-outside case (additive && !in) we already returned
			// without mutating, so no action.
		}
		// Count post-state. For mutated units next is authoritative; for
		// preserved outside-additive units old==next==isSelected(i).
		if next {
			selectedCount++
		} else if additive && !in && old {
			// preserved selected outside in additive mode: next==old but old read
			// before mutation is still the post state.
			selectedCount++
		}
	}
	return changed, selectedCount
}

// ApplyDragSelectionFlags operates directly on parallel slices of flags and
// presentation positions [07 §9] C6. It is a convenience for callers that have
// not yet built Selectable structs (e.g., tests). Lengths must match; excess
// entries are ignored. Eligible is optional: if nil all units are treated as
// eligible.
func ApplyDragSelectionFlags(flags []uint32, xs, ys []int32, rect Rect, additive bool, eligible []bool) (changed bool, selectedCount int) { // [07 §9] C6
	n := len(flags)
	if len(xs) < n {
		n = len(xs)
	}
	if len(ys) < n {
		n = len(ys)
	}
	if eligible != nil && len(eligible) < n {
		n = len(eligible)
	}
	for i := 0; i < n; i++ {
		isEligible := true
		if eligible != nil {
			isEligible = eligible[i]
		}
		if !isEligible {
			if flags[i]&SelectionFlag != 0 {
				selectedCount++
			}
			continue
		}
		inside := rect.Contains(xs[i], ys[i])
		old := flags[i]&SelectionFlag != 0
		next := NextSelected(old, inside, additive)
		if next != old {
			changed = true
			if next {
				flags[i] |= SelectionFlag
			} else {
				flags[i] &^= SelectionFlag
			}
		}
		if flags[i]&SelectionFlag != 0 {
			selectedCount++
		}
	}
	return changed, selectedCount
}
