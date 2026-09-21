package hud

import "github.com/nanolathe-gg/nanolathe/internal/content"

// Selection, control groups, and build pages [07 §9] C9, C10 [GAP T22].
//
// This package implements the HUD-side of retail selection semantics that the
// session's input boundary drives: control groups and build pages, using the
// flag-bit layouts and gating described in [07 §9]. Iteration over the owner's
// range is stable ascending (I1) in every function that walks a slice.
//
// Drag selection itself is not here. The committed-frame rectangle walk is
// internal/client's SnapshotUnitHandlesInBand, whose band is the two recorded
// world endpoints projected at test time [07 §9], and the membership writes are
// the session's HumanSelectionReplace / Toggle / Clear commands, which is the
// one path a shipped build takes [07 §9] C9.

const (
	SelectionFlag     uint32 = 0x10       // [07 §9] membership bit
	InterfaceDirtyBit uint32 = 0x10       // [07 §9] battle-interface dirty bit (coincidentally same value, different field)
	CtrlFFlag         uint32 = 0x80000000 // [07 §9] CTRL_F filter key flag

	PagePagedBit   uint32 = 1 << 22    // 0x400000 [07 §9]
	PageBitsMask   uint32 = 0x03800000 // bits 23-25 [07 §9]
	PageClearBits  uint32 = 0xFC7FFFFF // clears bits 23-25 [07 §9]
	PageClearPaged uint32 = 0xFFBFFFFF // clears bit 22 [07 §9]
)

// CategoryMaskBytes is the width of the authored CTRL_F type-filter bitset in
// bytes. Retail's registry entry is sixteen 32-bit words — 512 bits — indexed
// by the zero-extended definition id, and the recall filter tests
// word[id>>5] & (1<<(id&31)) [07 §9]. That is the same width the catalog
// compiler builds (content.CategoryMaskWords), so the two packages must agree
// or a definition id above 255 falls outside the filter here while the catalog
// still carries its bit. Byte indexing (byte id>>3, bit id&7) selects exactly
// the same bit as the word form under the little-endian word layout.
const CategoryMaskBytes = content.CategoryMaskWords * 4

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

// RememberedPage returns the page number bits 23-25 still hold, whether or not
// the page-shown bit is set. Selecting page 0 clears bit 22 and leaves those
// bits alone [07 §9], so the field is where a builder showing the orders page
// remembers the build page it was on.
func RememberedPage(flags uint32) int { return int((flags & PageBitsMask) >> 23) }

// SelectUnit is the minimal unit view the HUD selection logic operates on
// [07 §9] C9, C10. Flags carries runtime bits including SelectionFlag (0x10)
// and CtrlFFlag (0x80000000). Group is a single stored value 0..9 (0 = none)
// rather than membership in several groups [07 §9]. DefID is the catalog
// definition id (0 = none, scanned as nonzero for group assignment); it also
// indexes the 512-bit CTRL_F category mask [07 §9].
type SelectUnit struct {
	Flags uint32
	Group uint8
	DefID uint16 // 0 = no definition, 1..511 valid for CTRL_F mask
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

// TypeFilterPasses reports whether a definition id passes the authored 512-bit
// CTRL_F category mask [07 §9] C9. Definition id 0 is the catalog's null
// sentinel and is never a member, so it is admitted unfiltered.
//
// Retail has no bound test on the far side: it indexes the sixteen-word entry
// with the zero-extended definition id and would read past the entry for an id
// of 512 or more, so the executable defines no outcome there. This build
// rejects instead of inventing one, because admitting would silently widen a
// filtered recall.
//
// TODO(question): what a definition id of 512 or more should do. The decider is
// the catalog's maximum definition count: internal/content's category compiler
// refuses to compile a catalog with 512 or more definitions (the mask domain is
// 1..511), so no id this function can be handed today reaches the branch. If a
// future catalog raises that ceiling, retail offers no answer and the choice
// here must be revisited rather than read as an established contract.
func TypeFilterPasses(defID uint16, mask [CategoryMaskBytes]byte) bool { // [07 §9] C9
	if defID == 0 {
		return true
	}
	if int(defID) >= CategoryMaskBytes*8 {
		return false
	}
	return mask[defID/8]&(1<<(defID%8)) != 0
}

// RecallGroup implements digit group recall [07 §9] C9 with the preserve/toggle
// argument (shiftHeld) and the CTRL_F filter keyed on flag 0x80000000 [07 §9] C9.
// Iteration is stable ascending (I1). Eligibility is DefID !=0 (nonzero catalog
// definition id). When preserve is clear, nonmembers are cleared; when set,
// nonmembers are preserved and matching members are toggled (additive toggle
// table) [07 §9] C9. If any matching member carries CtrlFFlag, the authored
// 512-bit mask filters which matching units remain selected [07 §9] C9.
// Returns whether selection changed and the post count.
func RecallGroup(units []*SelectUnit, group int, preserve bool, mask [CategoryMaskBytes]byte, dirty *uint32) (changed bool, selectedCount int) { // [07 §9] C9
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
