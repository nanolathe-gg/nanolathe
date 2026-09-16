package hud

import (
	"testing"
)

// TestGroupAssignRecallTogglePreserve covers group assignment and recall
// preserve/toggle semantics [07 §9] C9 stable ascending and nonzero DefID scan.
func TestGroupAssignRecallTogglePreserve(t *testing.T) {
	makeUnits := func() []*SelectUnit {
		return []*SelectUnit{
			{Flags: SelectionFlag, Group: 0, DefID: 1},
			{Flags: 0, Group: 2, DefID: 2},
			{Flags: 0, Group: 0, DefID: 3},
			{Flags: SelectionFlag, Group: 2, DefID: 4},
			{Flags: SelectionFlag, Group: 0, DefID: 0}, // DefID 0 skipped for assign
		}
	}
	// Assign group 2: selected with DefID !=0 get group 2, unselected with group 2 cleared.
	units := makeUnits()
	var d uint32
	if !AssignGroup(units, 2, &d) {
		t.Fatalf("AssignGroup should report changed")
	}
	if units[0].Group != 2 {
		t.Fatalf("selected unit 0 should receive group 2")
	}
	if units[1].Group != 0 {
		t.Fatalf("unselected unit 1 with group 2 should be cleared")
	}
	if units[3].Group != 2 {
		t.Fatalf("selected unit 3 should keep/receive group 2")
	}
	if units[4].Group != 0 {
		t.Fatalf("DefID 0 unit should be untouched")
	}
	if d&InterfaceDirtyBit == 0 {
		t.Fatalf("assign should set dirty")
	}
	// Invalid group no-op.
	if AssignGroup(units, 10, nil) {
		t.Fatalf("invalid group should not change")
	}
	// Recall with preserve false: only group members selected, nonmembers cleared [07 §9] C9.
	units = []*SelectUnit{
		{Flags: 0, Group: 1, DefID: 10},
		{Flags: SelectionFlag, Group: 2, DefID: 11},
		{Flags: 0, Group: 1, DefID: 12},
		{Flags: SelectionFlag, Group: 0, DefID: 13},
	}
	var d2 uint32
	changed, cnt := RecallGroup(units, 1, false, [32]byte{}, &d2)
	if !changed {
		t.Fatalf("Recall preserve false should change")
	}
	if cnt != 2 {
		t.Fatalf("recall preserve false count %d want 2", cnt)
	}
	if !isSelected(units[0].Flags) || isSelected(units[1].Flags) || !isSelected(units[2].Flags) || isSelected(units[3].Flags) {
		t.Fatalf("preserve false recall failed: %v %v %v %v", isSelected(units[0].Flags), isSelected(units[1].Flags), isSelected(units[2].Flags), isSelected(units[3].Flags))
	}
	if d2&InterfaceDirtyBit == 0 {
		t.Fatalf("recall should set dirty")
	}
	// Recall with preserve true: matching toggle, nonmatching preserve [07 §9] C9.
	units = []*SelectUnit{
		{Flags: SelectionFlag, Group: 1, DefID: 20}, // selected match -> toggle off
		{Flags: 0, Group: 1, DefID: 21},             // deselected match -> toggle on
		{Flags: SelectionFlag, Group: 2, DefID: 22}, // selected nonmatch -> preserve selected
		{Flags: 0, Group: 0, DefID: 23},             // deselected nonmatch -> preserve deselected
	}
	var d3 uint32
	changed, cnt = RecallGroup(units, 1, true, [32]byte{}, &d3)
	if !changed {
		t.Fatalf("preserve true toggle should change")
	}
	if isSelected(units[0].Flags) {
		t.Fatalf("toggle should clear already selected match")
	}
	if !isSelected(units[1].Flags) {
		t.Fatalf("toggle should set deselected match")
	}
	if !isSelected(units[2].Flags) {
		t.Fatalf("nonmatch should preserve selected")
	}
	if isSelected(units[3].Flags) {
		t.Fatalf("nonmatch should preserve deselected")
	}
	if cnt != 2 { // 1 toggled on + preserved selected nonmember =2
		t.Fatalf("preserve true count %d want 2", cnt)
	}
	// Ineligible DefID 0 preserved regardless.
	units = []*SelectUnit{
		{Flags: SelectionFlag, Group: 1, DefID: 0},
		{Flags: 0, Group: 1, DefID: 1},
	}
	RecallGroup(units, 1, false, [32]byte{}, nil)
	if !isSelected(units[0].Flags) {
		t.Fatalf("ineligible DefID 0 should be preserved even though group matches")
	}
}

// setTypeMaskBit sets definition id's bit in the 256-bit CTRL_F category
// mask: byte id/8, bit id%8 [07 §9].
func setTypeMaskBit(mask *[32]byte, defID uint16) {
	mask[defID/8] |= 1 << (defID % 8)
}

// TestCtrlFFilter verifies the secondary CTRL_F branch [07 §9] C9.
func TestCtrlFFilter(t *testing.T) {
	// Two group-3 members with DefIDs 5 and 7. Mask admits 5 only.
	units := []*SelectUnit{
		{Flags: 0, Group: 3, DefID: 5},
		{Flags: 0, Group: 3, DefID: 7},
		{Flags: 0, Group: 3, DefID: 10},
	}
	var mask [32]byte
	setTypeMaskBit(&mask, 5)
	// No flag 0x80000000 anywhere: filter inactive, both members recall without filtering.
	changed, cnt := RecallGroup(units, 3, false, mask, nil)
	if !changed || cnt != 3 {
		t.Fatalf("filter inactive should select all 3, got cnt %d changed %v", cnt, changed)
	}
	if !isSelected(units[0].Flags) || !isSelected(units[1].Flags) || !isSelected(units[2].Flags) {
		t.Fatalf("unfiltered recall should select all")
	}
	// Activate filter: set 0x80000000 on one matching unit. Now mask should filter.
	units = []*SelectUnit{
		{Flags: CtrlFFlag, Group: 3, DefID: 5}, // carries flag 0x80000000
		{Flags: 0, Group: 3, DefID: 7},
		{Flags: 0, Group: 3, DefID: 5},
		{Flags: 0, Group: 2, DefID: 5}, // nonmatch group, should not be selected even if passes mask
	}
	mask = [32]byte{}
	setTypeMaskBit(&mask, 5)
	_, cnt = RecallGroup(units, 3, false, mask, nil)
	if cnt != 2 {
		t.Fatalf("filtered recall count %d want 2 (only DefID 5 matches)", cnt)
	}
	if !isSelected(units[0].Flags) || isSelected(units[1].Flags) || !isSelected(units[2].Flags) || isSelected(units[3].Flags) {
		t.Fatalf("filtered recall failed: %v %v %v %v", isSelected(units[0].Flags), isSelected(units[1].Flags), isSelected(units[2].Flags), isSelected(units[3].Flags))
	}
	// Filter with preserve true: filtered-out matches are considered nonmembers and thus preserved, not toggled.
	units = []*SelectUnit{
		{Flags: SelectionFlag, Group: 3, DefID: 5}, // selected, flagged, passes mask -> toggle off
		{Flags: SelectionFlag, Group: 3, DefID: 7}, // selected, but filtered out -> treated as nonmatch -> preserve (stay selected)
		{Flags: 0, Group: 3, DefID: 7},             // deselected filtered out -> preserve deselected
		{Flags: CtrlFFlag, Group: 3, DefID: 7},     // carries flag but DefID 7 not in mask -> still nonmatch? Actually it carries flag and is mismatch, but it's still a match group, filtered out -> nonmatch.
	}
	mask = [32]byte{}
	setTypeMaskBit(&mask, 5) // only 5 admitted
	RecallGroup(units, 3, true, mask, nil)
	if isSelected(units[0].Flags) {
		t.Fatalf("preserve true filtered passing should toggle off")
	}
	if !isSelected(units[1].Flags) {
		t.Fatalf("filtered nonmember should preserve selected")
	}
	if isSelected(units[2].Flags) {
		t.Fatalf("filtered deselected should preserve deselected")
	}
	// TypeFilterPasses helper directly.
	if !TypeFilterPasses(5, mask) || TypeFilterPasses(7, mask) {
		t.Fatalf("TypeFilterPasses wrong")
	}
	if !TypeFilterPasses(300, mask) { // beyond 255 always passes
		t.Fatalf("beyond 255 should pass")
	}
}

// TestDigitRoutingGate verifies the battle-mode/Alt gate [07 §9] C10.
func TestDigitRoutingGate(t *testing.T) {
	cases := []struct {
		mode     byte
		alt      bool
		wantPage bool
	}{
		{0, false, true},
		{0, true, false},
		{1, false, false},
		{1, true, true},
		{2, false, true}, // 2 &1 ==0 so same as 0
		{3, true, true},  // 3 &1 ==1
		{0xFF, false, false},
		{0xFF, true, true},
	}
	for _, c := range cases {
		got := RoutesToPage(c.mode, c.alt)
		if got != c.wantPage {
			t.Fatalf("RoutesToPage mode %d alt %v got %v want %v [07 §9] C10", c.mode, c.alt, got, c.wantPage)
		}
	}
	// Digit conversion. The group arm of the gate takes the digit itself:
	// battleSession.routeDigit hands it straight to DispatchGroupRecall.
	if DigitToPage(1) != 0 || DigitToPage(9) != 8 || DigitToPage(0) != 0 || DigitToPage(10) != 0 {
		t.Fatalf("DigitToPage wrong")
	}
}

// TestPageClampAndEncoding verifies page bits 23-25 with bit 22 paged
// guarded by builder page count [07 §9] C10.
func TestPageClampAndEncoding(t *testing.T) {
	// EncodePageBits tests vectors [07 §9] C10.
	tests := []struct {
		flags uint32
		page  int
		want  uint32
	}{
		{0, 0, 0 & PageClearPaged},  // page 0 clears bit22 only
		{0xFFFFFFFF, 0, 0xFFBFFFFF}, // page 0 from all bits clears only 0x400000
		{0, 1, PagePagedBit | (1 << 23)},
		{0, 7, PagePagedBit | (7 << 23)},
		{0, 8, PagePagedBit | (0 << 23)},                                   // page &7 wraps (8->0) but paged still set for >0
		{0x00400000 | (3 << 23), 0, 0xFFBFFFFF & (0x00400000 | (3 << 23))}, // page 0 leaves bits 23-25 alone
	}
	for _, tc := range tests {
		got := EncodePageBits(tc.flags, tc.page)
		if got != tc.want {
			t.Fatalf("EncodePageBits flags %08x page %d got %08x want %08x [07 §9] C10", tc.flags, tc.page, got, tc.want)
		}
	}
	// Verify page 0 leaves bits 23-25 alone.
	flags := uint32(PagePagedBit | (5 << 23) | 0x1234)
	after0 := EncodePageBits(flags, 0)
	if after0&PageBitsMask != (5 << 23) {
		t.Fatalf("page 0 should leave bits 23-25 alone: got %08x want %08x preserved", after0, 5<<23)
	}
	if after0&PagePagedBit != 0 {
		t.Fatalf("page 0 should clear paged bit")
	}
	// Non-zero page clears and sets.
	flags = 0xFFBFFFFF // paged clear, page bits cleared
	got := EncodePageBits(flags, 3)
	if !IsPaged(got) || DecodePage(got) != 3 {
		t.Fatalf("encode 3 failed: %08x decode %d", got, DecodePage(got))
	}
	// DecodePage returns 0 when paged clear.
	if DecodePage(0) != 0 || DecodePage(5<<23) != 0 {
		t.Fatalf("Decode without paged should be 0")
	}
	// SetBuildPage validation and clamp [07 §9] C10.
	builder := &SelectUnit{Flags: 0, DefID: 50}
	var d uint32
	if SetBuildPage(nil, 2, 4, &d) {
		t.Fatalf("nil builder should fail")
	}
	// DefID 0 invalid.
	badBuilder := &SelectUnit{Flags: 0, DefID: 0}
	if SetBuildPage(badBuilder, 1, 4, &d) {
		t.Fatalf("DefID 0 builder should fail")
	}
	// pageCount guard.
	if SetBuildPage(builder, 1, 0, &d) {
		t.Fatalf("pageCount 0 should fail")
	}
	// Clamp: request page 5 with count 3 => clamped to 2.
	builder.Flags = 0
	d = 0
	if !SetBuildPage(builder, 5, 3, &d) {
		t.Fatalf("clamp should succeed")
	}
	if DecodePage(builder.Flags) != 2 {
		t.Fatalf("clamped page want 2 got %d flags %08x", DecodePage(builder.Flags), builder.Flags)
	}
	if d&InterfaceDirtyBit == 0 {
		t.Fatalf("dirty not set on page change")
	}
	// No change when same page.
	d = 0
	if SetBuildPage(builder, 2, 3, &d) {
		t.Fatalf("same page should not report change")
	}
	if d != 0 {
		t.Fatalf("dirty should not be set when no change")
	}
	// Clamp overflow beyond 7: page &7 after clamp to 7 max.
	builder.Flags = 0
	SetBuildPage(builder, 10, 20, nil)
	if DecodePage(builder.Flags) != 7 {
		t.Fatalf("page >7 should clamp to 7, got %d", DecodePage(builder.Flags))
	}
	// Wrap via digit.
	builder.Flags = 0
	SetBuildPage(builder, DigitToPage(9), 10, nil) // digit 9 => page 8 => 8&7=0? Actually ClampPage caps to 7, then Encode wraps? Wait Clamp caps to 7 then Encode page&7 => 7. So digit 9 with large count should be page 8 clamped to 7.
	if DecodePage(builder.Flags) != 7 {
		t.Fatalf("digit 9 large count page want 7 got %d", DecodePage(builder.Flags))
	}
	// Digit 1 with count 1 => page 0 => paged clear.
	builder.Flags = PagePagedBit | (3 << 23)
	SetBuildPage(builder, DigitToPage(1), 1, nil)
	if IsPaged(builder.Flags) {
		t.Fatalf("page 0 should clear paged")
	}
}

// isSelected is the test's readability form of the membership-bit test that
// production inlines at its call sites [07 §9] C9.
func isSelected(flags uint32) bool { return flags&SelectionFlag != 0 }
