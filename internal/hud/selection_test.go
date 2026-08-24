package hud

import (
	"testing"
)

// TestTruthTableMatrix verifies the [07 §9] C9 truth table for drag selection
// via the HUD wrapper (duplicated from client primitive).
func TestTruthTableMatrix(t *testing.T) {
	cases := []struct {
		old, inside, additive bool
		want                  bool
	}{
		{false, false, false, false},
		{false, true, false, true},
		{true, false, false, false},
		{true, true, false, true},
		{false, false, true, false},
		{false, true, true, true},
		{true, false, true, true},
		{true, true, true, false},
	}
	for _, c := range cases {
		got := NextSelected(c.old, c.inside, c.additive)
		if got != c.want {
			t.Fatalf("NextSelected old=%t inside=%t additive=%t got %t want %t [07 §9] C9", c.old, c.inside, c.additive, got, c.want)
		}
	}
	// Exhaustive via flags.
	for _, old := range []bool{false, true} {
		for _, ins := range []bool{false, true} {
			for _, add := range []bool{false, true} {
				flags := uint32(0)
				if old {
					flags |= SelectionFlag
				}
				next := NextFlags(flags, ins, add)
				want := NextSelected(old, ins, add)
				if IsSelected(next) != want {
					t.Fatalf("NextFlags table mismatch old %t ins %t add %t", old, ins, add)
				}
			}
		}
	}
}

// TestDragSelectionMembershipAndDirty checks membership bit 0x10 and dirty bit
// observation plus stable ascending iteration [07 §9] C9.
func TestDragSelectionMembershipAndDirty(t *testing.T) {
	// Three units at distinct presentation positions, ascending iteration 0..2 [07 §9] I1.
	u0 := &SelectUnit{Flags: 0, DefID: 1}
	u1 := &SelectUnit{Flags: SelectionFlag, DefID: 2} // initially selected
	u2 := &SelectUnit{Flags: 0, DefID: 3}
	units := []*SelectUnit{u0, u1, u2}
	getPos := func(u *SelectUnit) (int32, int32) {
		switch u {
		case u0:
			return 10, 10 // inside
		case u1:
			return 200, 200 // outside
		case u2:
			return 15, 15 // inside
		default:
			return 999, 999
		}
	}
	rect := NormalizeDragRect(0, 0, 20, 20) // contains u0 and u2 only
	var dirty uint32
	// Modifier clear: inside set, outside clear (bulk pre-clear) [07 §9] C9.
	changed, cnt := ApplyDragSelection(units, rect, false, &dirty, getPos, nil)
	if !changed {
		t.Fatalf("expected changed on clear modifier")
	}
	if dirty&InterfaceDirtyBit == 0 {
		t.Fatalf("dirty bit 0x10 not set after change [07 §9] C9")
	}
	if cnt != 2 {
		t.Fatalf("selected count after clear = %d want 2", cnt)
	}
	if !IsSelected(u0.Flags) || IsSelected(u1.Flags) || !IsSelected(u2.Flags) {
		t.Fatalf("clear table failed: u0 %v u1 %v u2 %v", IsSelected(u0.Flags), IsSelected(u1.Flags), IsSelected(u2.Flags))
	}
	// Companion bits 0x40 and 0x80 must be cleared on eligible units when modifier clear [07 §9] BulkClearMask.
	uComp := &SelectUnit{Flags: SelectionFlag | 0x40 | 0x80, DefID: 4}
	units2 := []*SelectUnit{uComp}
	getPos2 := func(u *SelectUnit) (int32, int32) { return 999, 999 } // outside
	var d2 uint32
	rect2 := NormalizeDragRect(0, 0, 10, 10)
	ApplyDragSelection(units2, rect2, false, &d2, getPos2, nil)
	if uComp.Flags&0x40 != 0 || uComp.Flags&0x80 != 0 {
		t.Fatalf("bulk clear mask 0xFFFFFF2F not applied: flags %08x [07 §9] C9", uComp.Flags)
	}
	if IsSelected(uComp.Flags) {
		t.Fatalf("outside with clear should be deselected")
	}
	// Modifier set: toggle inside, preserve outside [07 §9] C9.
	u0.Flags = SelectionFlag // selected inside will toggle off
	u1.Flags = 0             // outside preserved (was cleared)
	u2.Flags = 0             // inside not selected will toggle on
	units = []*SelectUnit{u0, u1, u2}
	var d3 uint32
	changed, cnt = ApplyDragSelection(units, rect, true, &d3, getPos, nil)
	if !changed {
		t.Fatalf("expected toggle change")
	}
	if IsSelected(u0.Flags) {
		t.Fatalf("toggle inside selected should clear")
	}
	if IsSelected(u1.Flags) {
		t.Fatalf("outside with additive should preserve deselected")
	}
	if !IsSelected(u2.Flags) {
		t.Fatalf("toggle inside deselected should set")
	}
	if cnt != 1 {
		t.Fatalf("toggle count %d want 1", cnt)
	}
	// Stable ascending iteration: mutation order must not affect stability.
	flags := []uint32{0, SelectionFlag, 0}
	xs := []int32{10, 200, 15}
	ys := []int32{10, 200, 15}
	var d4 uint32
	ch, sc := ApplyDragSelectionFlags(flags, xs, ys, rect, false, nil, &d4)
	if !ch || sc != 2 || flags[0]&SelectionFlag == 0 || flags[1]&SelectionFlag != 0 || flags[2]&SelectionFlag == 0 {
		t.Fatalf("ApplyDragSelectionFlags iteration or truth table wrong: %08x %08x %08x ch %v sc %d", flags[0], flags[1], flags[2], ch, sc)
	}
	// Dirty not set when no change.
	uA := &SelectUnit{Flags: SelectionFlag, DefID: 5}
	unitsA := []*SelectUnit{uA}
	getPosA := func(u *SelectUnit) (int32, int32) { return 5, 5 } // inside, already selected, clear modifier would keep selected
	rectA := NormalizeDragRect(0, 0, 10, 10)
	var dNo uint32
	chNo, _ := ApplyDragSelection(unitsA, rectA, false, &dNo, getPosA, nil)
	if chNo {
		t.Fatalf("no-change should not report changed")
	}
	if dNo != 0 {
		t.Fatalf("dirty set without change")
	}
	// Ineligible units preserve regardless of rect/modifier.
	uInelig := &SelectUnit{Flags: SelectionFlag, DefID: 6}
	var d5 uint32
	ApplyDragSelection([]*SelectUnit{uInelig}, rect, false, &d5, func(u *SelectUnit) (int32, int32) { return 999, 999 }, func(u *SelectUnit) bool { return false })
	if !IsSelected(uInelig.Flags) {
		t.Fatalf("ineligible should be preserved")
	}
}

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
	if !IsSelected(units[0].Flags) || IsSelected(units[1].Flags) || !IsSelected(units[2].Flags) || IsSelected(units[3].Flags) {
		t.Fatalf("preserve false recall failed: %v %v %v %v", IsSelected(units[0].Flags), IsSelected(units[1].Flags), IsSelected(units[2].Flags), IsSelected(units[3].Flags))
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
	if IsSelected(units[0].Flags) {
		t.Fatalf("toggle should clear already selected match")
	}
	if !IsSelected(units[1].Flags) {
		t.Fatalf("toggle should set deselected match")
	}
	if !IsSelected(units[2].Flags) {
		t.Fatalf("nonmatch should preserve selected")
	}
	if IsSelected(units[3].Flags) {
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
	if !IsSelected(units[0].Flags) {
		t.Fatalf("ineligible DefID 0 should be preserved even though group matches")
	}
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
	mask[5/8] |= 1 << (5 % 8)
	// No flag 0x80000000 anywhere: filter inactive, both members recall without filtering.
	changed, cnt := RecallGroup(units, 3, false, mask, nil)
	if !changed || cnt != 3 {
		t.Fatalf("filter inactive should select all 3, got cnt %d changed %v", cnt, changed)
	}
	if !IsSelected(units[0].Flags) || !IsSelected(units[1].Flags) || !IsSelected(units[2].Flags) {
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
	mask[5/8] |= 1 << (5 % 8)
	changed, cnt = RecallGroup(units, 3, false, mask, nil)
	if cnt != 2 {
		t.Fatalf("filtered recall count %d want 2 (only DefID 5 matches)", cnt)
	}
	if !IsSelected(units[0].Flags) || IsSelected(units[1].Flags) || !IsSelected(units[2].Flags) || IsSelected(units[3].Flags) {
		t.Fatalf("filtered recall failed: %v %v %v %v", IsSelected(units[0].Flags), IsSelected(units[1].Flags), IsSelected(units[2].Flags), IsSelected(units[3].Flags))
	}
	// Filter with preserve true: filtered-out matches are considered nonmembers and thus preserved, not toggled.
	units = []*SelectUnit{
		{Flags: SelectionFlag, Group: 3, DefID: 5}, // selected, flagged, passes mask -> toggle off
		{Flags: SelectionFlag, Group: 3, DefID: 7}, // selected, but filtered out -> treated as nonmatch -> preserve (stay selected)
		{Flags: 0, Group: 3, DefID: 7},             // deselected filtered out -> preserve deselected
		{Flags: CtrlFFlag, Group: 3, DefID: 7},     // carries flag but DefID 7 not in mask -> still nonmatch? Actually it carries flag and is mismatch, but it's still a match group, filtered out -> nonmatch.
	}
	mask = [32]byte{}
	mask[5/8] |= 1 << (5 % 8) // only 5 admitted
	changed, cnt = RecallGroup(units, 3, true, mask, nil)
	if IsSelected(units[0].Flags) {
		t.Fatalf("preserve true filtered passing should toggle off")
	}
	if !IsSelected(units[1].Flags) {
		t.Fatalf("filtered nonmember should preserve selected")
	}
	if IsSelected(units[2].Flags) {
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
	// Digit conversion.
	if DigitToPage(1) != 0 || DigitToPage(9) != 8 || DigitToPage(0) != 0 || DigitToPage(10) != 0 {
		t.Fatalf("DigitToPage wrong")
	}
	if DigitToGroup(5) != 5 || DigitToGroup(0) != 0 {
		t.Fatalf("DigitToGroup wrong")
	}
	// HandleDigit end-to-end routing.
	builder := &SelectUnit{Flags: 0, DefID: 100}
	units := []*SelectUnit{
		{Flags: 0, Group: 2, DefID: 1},
		{Flags: 0, Group: 2, DefID: 2},
	}
	var d uint32
	var mask [32]byte
	// mode 0 alt false => page path. digit 3 => page 2.
	isPage, changed := HandleDigit(0, false, false, 3, builder, 4, units, mask, &d)
	if !isPage {
		t.Fatalf("HandleDigit should route to page")
	}
	if !changed || DecodePage(builder.Flags) != 2 {
		t.Fatalf("HandleDigit page not set: flags %08x decode %d changed %v", builder.Flags, DecodePage(builder.Flags), changed)
	}
	if d&InterfaceDirtyBit == 0 {
		t.Fatalf("page switch should set dirty")
	}
	// mode 0 alt true => group recall path.
	builder2 := &SelectUnit{Flags: 0, DefID: 101}
	units2 := []*SelectUnit{
		{Flags: 0, Group: 2, DefID: 1},
		{Flags: 0, Group: 2, DefID: 2},
	}
	var d2 uint32
	isPage, changed = HandleDigit(0, true, false, 2, builder2, 4, units2, mask, &d2)
	if isPage {
		t.Fatalf("HandleDigit should route to group")
	}
	if !changed || !IsSelected(units2[0].Flags) || !IsSelected(units2[1].Flags) {
		t.Fatalf("HandleDigit group recall failed")
	}
	// Invalid digit no handling.
	isPage, changed = HandleDigit(0, false, false, 10, builder, 4, units, mask, nil)
	if isPage || changed {
		t.Fatalf("invalid digit should not handle")
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
	SetBuildPageForDigit(builder, 9, 10, nil) // digit 9 => page 8 => 8&7=0? Actually ClampPage caps to 7, then Encode wraps? Wait Clamp caps to 7 then Encode page&7 => 7. So digit 9 with large count should be page 8 clamped to 7.
	if DecodePage(builder.Flags) != 7 {
		t.Fatalf("digit 9 large count page want 7 got %d", DecodePage(builder.Flags))
	}
	// Digit 1 with count 1 => page 0 => paged clear.
	builder.Flags = PagePagedBit | (3 << 23)
	SetBuildPageForDigit(builder, 1, 1, nil)
	if IsPaged(builder.Flags) {
		t.Fatalf("page 0 should clear paged")
	}
}
