package hud

// Selection, control groups, and build pages [07 §9] C9, C10 [GAP T22].
//
// This package implements the HUD-side of retail selection semantics.
// Drag selection reuses the truth table primitives from internal/client/select.go
// (read-only use per plan) and adds the GUI dirty-bit observation. Control
// groups and build pages use the flag-bit layouts and gating described in
// [07 §9]. Iteration over the owner's range is stable ascending (I1) in every
// function that walks a slice.
//
// The drag truth table is duplicated locally to avoid importing the heavy
// client package (which pulls the Ebitengine window backend) into hud tests [07 §9] C9.

const (
	SelectionFlag     uint32 = 0x10       // [07 §9] membership bit
	InterfaceDirtyBit uint32 = 0x10       // [07 §9] battle-interface dirty bit (coincidentally same value, different field)
	BulkClearMask     uint32 = 0xFFFFFF2F // [07 §9] bulk pre-clear clears 0x10 plus 0x40,0x80 companion bits
	CtrlFFlag         uint32 = 0x80000000 // [07 §9] CTRL_F filter key flag

	PagePagedBit   uint32 = 1 << 22    // 0x400000 [07 §9]
	PageBitsMask   uint32 = 0x03800000 // bits 23-25 [07 §9]
	PageClearBits  uint32 = 0xFC7FFFFF // clears bits 23-25 [07 §9]
	PageClearPaged uint32 = 0xFFBFFFFF // clears bit 22 [07 §9]
)

// DragRect is an inclusive presentation-space rectangle [07 §9] C9.
// Construct via NormalizeDragRect so MinX<=MaxX, MinY<=MaxY holds.
type DragRect struct {
	MinX, MinY int32
	MaxX, MaxY int32
}

// NormalizeDragRect sorts drag endpoints into an inclusive rect [07 §9] C9.
// Each axis is sorted independently (if right < left swap, if bottom < top swap).
func NormalizeDragRect(ax, ay, bx, by int32) DragRect { // [07 §9] C9
	minX, maxX := ax, bx
	if maxX < minX {
		minX, maxX = maxX, minX
	}
	minY, maxY := ay, by
	if maxY < minY {
		minY, maxY = maxY, minY
	}
	return DragRect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

// Contains reports whether (x,y) is inside r inclusive [07 §9] C9.
func (r DragRect) Contains(x, y int32) bool { // [07 §9] C9
	return x >= r.MinX && x <= r.MaxX && y >= r.MinY && y <= r.MaxY
}

// BuildPage holds a page number and the builder's page count [07 §9] C10.
// The plan's Public API requires this type and EncodePageBits.
type BuildPage struct {
	Page  int // 0..7, page 0 means unpaged (bit 22 clear)
	Count int // builder definition page-count byte, >0 when builder is valid
}

// EncodePageBits encodes a page number into unit-flag bits 23-25 with bit 22
// as the paged indicator [07 §9] C10. Page 0 clears bit 22 and leaves bits
// 23-25 alone; page >0 sets bit 22 and writes (page &7)<<23 after clearing
// both fields. Bits are cleared with masks 0xFC7FFFFF (bits 23-25) and
// 0xFFBFFFFF (bit 22) [07 §9].
func EncodePageBits(flags uint32, page int) uint32 { // [07 §9] C10
	if page <= 0 {
		// Page 0: clear paged indicator, leave bits 23-25 alone [07 §9].
		return flags & PageClearPaged
	}
	page &= 7
	// Non-zero page: clear both fields then set paged indicator and page bits [07 §9].
	flags &= PageClearBits
	flags &= PageClearPaged
	flags |= PagePagedBit
	flags |= uint32(page&7) << 23
	return flags
}

// DecodePage returns the page number encoded in flags [07 §9] C10.
// When the paged bit (22) is clear, the page is 0 regardless of bits 23-25.
func DecodePage(flags uint32) int { // [07 §9] C10
	if flags&PagePagedBit == 0 {
		return 0
	}
	return int((flags & PageBitsMask) >> 23)
}

// IsPaged reports whether the paged indicator bit 22 is set [07 §9] C10.
func IsPaged(flags uint32) bool { return flags&PagePagedBit != 0 }

// SelectUnit is the minimal unit view the HUD selection logic operates on
// [07 §9] C9, C10. Flags carries runtime bits including SelectionFlag (0x10)
// and CtrlFFlag (0x80000000). Group is a single stored value 0..9 (0 = none)
// rather than membership in several groups [07 §9]. DefID is the catalog
// definition id (0 = none, scanned as nonzero for group assignment); it also
// indexes the 256-bit CTRL_F category mask [07 §9].
type SelectUnit struct {
	Flags uint32
	Group uint8
	DefID uint16 // 0 = no definition, 1..255 valid for CTRL_F mask
}

// IsSelected reports whether flags carries SelectionFlag (0x10) [07 §9] C9.
func IsSelected(flags uint32) bool { return flags&SelectionFlag != 0 }

// SetSelected sets the selection membership bit [07 §9] C9.
func SetSelected(flags uint32) uint32 { return flags | SelectionFlag }

// ClearSelected clears the selection membership bit [07 §9] C9.
func ClearSelected(flags uint32) uint32 { return flags &^ SelectionFlag }

// ToggleSelected toggles the selection membership bit [07 §9] C9.
func ToggleSelected(flags uint32) uint32 { return flags ^ SelectionFlag }

// NextSelected is the pure truth table for one eligible unit [07 §9] C9.
// additive==false (modifier clear): selected = inside
// additive==true  (modifier set):   selected = inside ? !old : old
func NextSelected(oldSelected, inside, additive bool) bool { // [07 §9] C9
	if additive {
		if inside {
			return !oldSelected
		}
		return oldSelected
	}
	return inside
}

// NextFlags applies the truth table directly to a flags word [07 §9] C9.
func NextFlags(flags uint32, inside, additive bool) uint32 { // [07 §9] C9
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

// ApplyDragSelection applies the [07 §9] C9 truth table to units in place
// with stable ascending iteration (I1) [07 §9]. Eligible units are those that
// pass the shared predicate; in this HUD placeholder caller supplies
// eligibility via isEligible func when non-nil; the default (isEligible == nil)
// treats every non-nil entry as eligible, matching the Gate-1 placeholder that
// collapses the full active-state/float/parent predicate behind a bool.
//
// Truth table [07 §9] C9:
//
//	modifier clear (additive==false): inside => set (|=0x10), outside => clear (pre-clear &=0xFFFFFF2F)
//	modifier set   (additive==true):  inside => toggle (^=0x10), outside => preserve
//
// Bulk changes also set the battle-interface dirty bit 0x10 (separate field)
// when any membership bit changes [07 §9] C9. Returns whether any selection
// bit changed and the post count of selected units (including ineligible that
// were already selected, counted but not mutated).
func ApplyDragSelection(units []*SelectUnit, rect DragRect, additive bool, dirty *uint32, getPos func(*SelectUnit) (int32, int32), isEligible func(*SelectUnit) bool) (changed bool, selectedCount int) { // [07 §9] C9
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u == nil {
			continue
		}
		eligible := true
		if isEligible != nil {
			eligible = isEligible(u)
		}
		if !eligible {
			if u.Flags&SelectionFlag != 0 {
				selectedCount++
			}
			continue
		}
		var inside bool
		if getPos != nil {
			x, y := getPos(u)
			inside = rect.Contains(x, y)
		}
		old := u.Flags&SelectionFlag != 0
		next := NextSelected(old, inside, additive)
		if !additive {
			u.Flags &= BulkClearMask
		}
		if next != old {
			changed = true
			if next {
				u.Flags |= SelectionFlag
			} else {
				u.Flags &^= SelectionFlag
			}
		}
		if u.Flags&SelectionFlag != 0 {
			selectedCount++
		}
	}
	if changed && dirty != nil {
		*dirty |= InterfaceDirtyBit // [07 §9] C9
	}
	return changed, selectedCount
}

// ApplyDragSelectionFlags is a convenience for parallel flag/pos slices
// [07 §9] C9 with dirty-bit observation. xs, ys are presentation positions;
// eligible may be nil meaning all eligible.
func ApplyDragSelectionFlags(flags []uint32, xs, ys []int32, rect DragRect, additive bool, eligible []bool, dirty *uint32) (changed bool, selectedCount int) { // [07 §9] C9
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
		if !additive {
			flags[i] &= BulkClearMask // [07 §9] C9 bulk pre-clear
		}
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
	if changed && dirty != nil {
		*dirty |= InterfaceDirtyBit
	}
	return changed, selectedCount
}

// AssignGroup implements Ctrl+digit group assignment [07 §9] C9.
// It scans local unit slots with nonzero DefID in stable ascending order
// (I1). Selected units (flag 0x10) receive the requested group value; unselected
// units already carrying that group have it zeroed [07 §9]. Returns whether any
// group value changed. The CreateSquad cue would play here (not modeled).
func AssignGroup(units []*SelectUnit, group int, dirty *uint32) bool { // [07 §9] C9
	if group < 1 || group > 9 {
		return false
	}
	g := uint8(group)
	changed := false
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u == nil || u.DefID == 0 {
			continue // nonzero catalog definition id scanner condition [07 §9] C9
		}
		selected := u.Flags&SelectionFlag != 0
		if selected {
			if u.Group != g {
				u.Group = g
				changed = true
			}
		} else {
			if u.Group == g {
				u.Group = 0
				changed = true
			}
		}
	}
	if changed && dirty != nil {
		*dirty |= InterfaceDirtyBit // bulk change also refreshes UI state [07 §9] C9
	}
	return changed
}

// TypeFilterPasses reports whether a definition id passes the authored 256-bit
// CTRL_F category mask [07 §9] C9. Definitions beyond 255 are unfilterable and
// always pass (retail byte-wide mask space).
func TypeFilterPasses(defID uint16, mask [32]byte) bool { // [07 §9] C9
	if defID == 0 || defID >= 256 {
		return true
	}
	return mask[defID/8]&(1<<(defID%8)) != 0
}

// RecallGroup implements digit group recall [07 §9] C9 with the preserve/toggle
// argument (shiftHeld) and the CTRL_F filter keyed on flag 0x80000000 [07 §9] C9.
// Iteration is stable ascending (I1). Eligibility is DefID !=0 (nonzero catalog
// definition id). When preserve is clear, nonmembers are cleared; when set,
// nonmembers are preserved and matching members are toggled (additive toggle
// table) [07 §9] C9. If any matching member carries CtrlFFlag, the authored
// 256-bit mask filters which matching units remain selected [07 §9] C9.
// Returns whether selection changed and the post count.
func RecallGroup(units []*SelectUnit, group int, preserve bool, mask [32]byte, dirty *uint32) (changed bool, selectedCount int) { // [07 §9] C9
	if group < 1 || group > 9 {
		// Count existing selection for return value even when group invalid.
		for i := 0; i < len(units); i++ {
			if units[i] != nil && units[i].Flags&SelectionFlag != 0 {
				selectedCount++
			}
		}
		return false, selectedCount
	}
	g := uint8(group)
	// Determine whether CTRL_F secondary branch is active [07 §9] C9:
	// keys on a matching unit that also carries runtime flag 0x80000000.
	filterActive := false
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u == nil || u.DefID == 0 || u.Group != g {
			continue
		}
		if u.Flags&CtrlFFlag != 0 {
			filterActive = true
			break
		}
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u == nil {
			continue
		}
		if u.DefID == 0 {
			if u.Flags&SelectionFlag != 0 {
				selectedCount++
			}
			continue
		}
		isMatch := u.Group == g
		if filterActive && isMatch {
			if !TypeFilterPasses(u.DefID, mask) {
				isMatch = false // filtered matching unit is treated as nonmember [07 §9] C9
			}
		}
		old := u.Flags&SelectionFlag != 0
		var next bool
		if preserve {
			if isMatch {
				next = !old // toggle inside, preserve outside [07 §9] C9
			} else {
				next = old
			}
		} else {
			next = isMatch // set if match, clear if not [07 §9] C9
		}
		if next != old {
			changed = true
			if next {
				u.Flags |= SelectionFlag
			} else {
				u.Flags &^= SelectionFlag
			}
		}
		if u.Flags&SelectionFlag != 0 {
			selectedCount++
		}
	}
	if changed && dirty != nil {
		*dirty |= InterfaceDirtyBit // [07 §9] C9
	}
	return changed, selectedCount
}

// RoutesToPage reports whether a digit 1..9 should select a build page rather
// than recall a control group under the exact battle-mode/Alt gate [07 §9] C10.
//
//	modeBit = battle-mode flag &1
//	alt = held-key query for token 0xFB (Alt)
//	if (!modeBit && !alt) || (modeBit && alt) => build page
//	else => group recall [07 §9] C10
func RoutesToPage(battleMode byte, altHeld bool) bool { // [07 §9] C10
	modeBit := battleMode&1 != 0
	return (!modeBit && !altHeld) || (modeBit && altHeld)
}

// DigitToPage converts a digit 1..9 token (0x31..0x39) to a build page number
// 0..8 (digit-1) [07 §9] C10.
func DigitToPage(digit int) int { // [07 §9] C10
	if digit < 1 || digit > 9 {
		return 0
	}
	return digit - 1
}

// DigitToGroup converts a digit 1..9 to a group number 1..9 [07 §9] C9.
func DigitToGroup(digit int) int { // [07 §9] C9
	if digit < 1 || digit > 9 {
		return 0
	}
	return digit
}

// ClampPage clamps a requested page to the builder's page count [07 §9] C10.
// Page count comes from the builder definition's page-count byte. If count <=0
// the page is 0. Excess pages clamp to count-1. Used before EncodePageBits.
func ClampPage(page, count int) int { // [07 §9] C10
	if count <= 0 {
		return 0
	}
	if page < 0 {
		page = 0
	}
	if page >= count {
		page = count - 1
	}
	if page > 7 {
		page = 7 // page number lives in bits 23-25 (3 bits) [07 §9] C10
	}
	return page
}

// SetBuildPage switches the selected builder's page [07 §9] C10. Validation
// requires a nonzero single-select builder identity (nonnil builder) and
// nonzero DefID. The page is clamped to the builder's page count before
// encoding. Encoding uses EncodePageBits with the guarded count [07 §9] C10.
// Sets the interface dirty bit 0x10 and would play the nextbuildmenu cue when
// changed. Returns whether flags changed.
func SetBuildPage(builder *SelectUnit, page, pageCount int, dirty *uint32) bool { // [07 §9] C10
	if builder == nil || builder.DefID == 0 {
		return false // validates selected-builder identity first [07 §9] C10
	}
	if pageCount <= 0 {
		return false // guarded by builder's page-count byte [07 §9] C10
	}
	clamped := ClampPage(page, pageCount)
	old := builder.Flags
	newFlags := EncodePageBits(old, clamped)
	if newFlags == old {
		return false
	}
	builder.Flags = newFlags
	if dirty != nil {
		*dirty |= InterfaceDirtyBit // switching sets dirty bit 0x10 [07 §9] C10
	}
	return true
}

// SetBuildPageForDigit is the digit-gated wrapper for page switching [07 §9] C10.
// Digit 1..9 maps to page digit-1; the page is clamped by pageCount before
// encoding [07 §9] C10. Validation and dirty propagation are as SetBuildPage.
func SetBuildPageForDigit(builder *SelectUnit, digit, pageCount int, dirty *uint32) bool { // [07 §9] C10
	if digit < 1 || digit > 9 {
		return false
	}
	page := DigitToPage(digit)
	return SetBuildPage(builder, page, pageCount, dirty)
}

// HandleDigit routes a digit 1..9 through the battle-mode/Alt gate to either
// build-page switching or group recall [07 §9] C10, C9. When the gate selects
// build page, it attempts SetBuildPageForDigit on the selected builder
// (single-select identity, nonzero DefID) guarded by builderPageCount; when it
// selects group recall, it calls RecallGroup with the preserve (shiftHeld)
// argument and the authored CTRL_F mask [07 §9] C9. Returns whether the digit
// was handled as page (true) or group (false), and whether any state changed.
func HandleDigit(battleMode byte, altHeld, shiftHeld bool, digit int, selectedBuilder *SelectUnit, builderPageCount int, units []*SelectUnit, mask [32]byte, dirty *uint32) (isPage bool, changed bool) { // [07 §9] C10, C9
	if digit < 1 || digit > 9 {
		return false, false
	}
	isPage = RoutesToPage(battleMode, altHeld)
	if isPage {
		changed = SetBuildPageForDigit(selectedBuilder, digit, builderPageCount, dirty)
		return true, changed
	}
	changed, _ = RecallGroup(units, DigitToGroup(digit), shiftHeld, mask, dirty)
	return false, changed
}
