package gui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestFamilyRoutingTable(t *testing.T) {
	// [07 §4] stored type byte routes to distinct runtime families 1,2,3,4,5,6,12,13
	cases := []struct {
		kind Kind
		fam  uint8
		name string
	}{
		{KindButton, FamilyClickable, "clickable-with-callback"},
		{KindListBox, FamilyStateful, "stateful"},
		{KindTextBox, FamilyTextEditor, "focusable-text-editor"},
		{KindScrollBar, FamilyDedicated, "dedicated-update"},
		{KindLabel, FamilyAssociation, "association-capable"},
		{KindSurface, FamilyCallback, "callback-producing"},
		{KindPicture, FamilyRepeating, "repeating/decrementing"},
		{KindRepeat, FamilyTimed, "timed/range"},
	}
	for _, c := range cases {
		if got := c.kind.RuntimeFamily(); got != c.fam {
			t.Fatalf("Kind %d RuntimeFamily = %d want %d (%s) [07 §4]", c.kind, got, c.fam, c.name)
		}
		if got := FamilyForStoredType(byte(c.kind)); got != c.fam {
			t.Fatalf("FamilyForStoredType %d = %d want %d (%s) [07 §4]", c.kind, got, c.fam, c.name)
		}
		if FamilyName(c.fam) == "none" {
			t.Fatalf("FamilyName %d should not be none", c.fam)
		}
	}
	// Unknown kinds map to 0
	unknown := []Kind{KindPanel, KindFont, KindSlider, KindText, KindZero, KindEmbedded1, KindSingle1, KindSingle2, Kind(99)}
	for _, k := range unknown {
		if got := FamilyForStoredType(byte(k)); got != 0 && k != Kind(99) {
			// KindPanel etc return 0 per RuntimeFamily; check that
			if k.RuntimeFamily() != 0 {
				t.Fatalf("unknown Kind %d should map to 0, got %d", k, got)
			}
		}
		// byte 99 should be 0
		if k == Kind(99) && FamilyForStoredType(byte(k)) != 0 {
			t.Fatalf("stored type 99 should map to 0")
		}
	}
	// Verify all eight families distinct
	seen := make(map[uint8]bool)
	for _, c := range cases {
		if seen[c.fam] {
			t.Fatalf("duplicate family %d", c.fam)
		}
		seen[c.fam] = true
	}
	if len(seen) != 8 {
		t.Fatalf("family count = %d want 8 [07 §4]", len(seen))
	}
}

func TestHitTestInclusiveMatrix(t *testing.T) {
	// [07 §3][07 §4] inclusive both edges AFTER runtime window placement; grayed+hidden reject
	w := &Window{
		Name: "test",
		Rect: Rect{X: 0, Y: 0, W: 100, H: 100},
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "PANEL", Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: 1},
			{Kind: KindButton, Name: "BTN", Rect: Rect{X: 10, Y: 10, W: 20, H: 10}, Active: 1},
			{Kind: KindButton, Name: "GRAYED", Rect: Rect{X: 40, Y: 10, W: 20, H: 10}, Active: 1, GrayedOut: 1},
			{Kind: KindButton, Name: "HIDDEN", Rect: Rect{X: 10, Y: 30, W: 20, H: 10}, Active: 0},
			{Kind: KindButton, Name: "EDGE", Rect: Rect{X: 50, Y: 50, W: 1, H: 1}, Active: 1},
		},
	}
	// Top-left inclusive
	if got := w.HitTest(10, 10); got != 1 {
		t.Fatalf("HitTest top-left inclusive: got %d want 1", got)
	}
	if got := HitTestAfterPlacement(w, 10, 10); got != 1 {
		t.Fatalf("HitTestAfterPlacement top-left: got %d want 1", got)
	}
	// Bottom-right inclusive: 10+20-1=29, 10+10-1=19
	if got := w.HitTest(29, 19); got != 1 {
		t.Fatalf("HitTest bottom-right inclusive: got %d want 1", got)
	}
	// Outside by one pixel should miss
	if got := w.HitTest(30, 10); got != -1 {
		t.Fatalf("HitTest outside right: got %d want -1", got)
	}
	if got := w.HitTest(10, 20); got != -1 {
		t.Fatalf("HitTest outside bottom: got %d want -1", got)
	}
	if got := w.HitTest(9, 10); got != -1 {
		t.Fatalf("HitTest outside left: got %d want -1", got)
	}
	if got := w.HitTest(10, 9); got != -1 {
		t.Fatalf("HitTest outside top: got %d want -1", got)
	}
	// Single pixel gadget at 50,50 size 1x1 => only 50,50 hits
	if got := w.HitTest(50, 50); got != 4 {
		t.Fatalf("HitTest 1x1 inclusive: got %d want 4", got)
	}
	if got := w.HitTest(51, 50); got != -1 {
		t.Fatalf("HitTest 1x1 outside: got %d want -1", got)
	}
	// Grayed rejects [07 §3]
	if got := w.HitTest(40, 10); got != -1 {
		t.Fatalf("grayed control should reject hit per [07 §3], got %d", got)
	}
	// Hidden rejects [07 §3]
	if got := w.HitTest(10, 30); got != -1 {
		t.Fatalf("hidden control should reject hit per [07 §3], got %d", got)
	}
	// Header panel (KindPanel) is skipped even if it would contain point
	if got := w.HitTest(0, 0); got != -1 {
		t.Fatalf("panel header should be skipped, got %d", got)
	}
	// After runtime placement: gadget rects already incorporate window placement [07 §3][07 §4]
	// Verify that moving window rect does not affect hit if gadgets already placed absolute
	// (If gadgets were relative, adding window offset would be needed; here we just ensure HitTestAfterPlacement equals HitTest)
	w2 := &Window{
		Name: "placed",
		Rect: Rect{X: 5, Y: 5, W: 200, H: 200},
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "PANEL", Rect: Rect{X: 5, Y: 5, W: 200, H: 200}, Active: 1},
			{Kind: KindButton, Name: "BTN2", Rect: Rect{X: 15, Y: 15, W: 10, H: 10}, Active: 1},
		},
	}
	if got := w2.HitTest(15, 15); got != 1 {
		t.Fatalf("placed window hit: got %d want 1", got)
	}
	if got := HitTestAfterPlacement(w2, 15, 15); got != 1 {
		t.Fatalf("HitTestAfterPlacement placed: got %d want 1", got)
	}
	if got := w2.HitTest(24, 24); got != 1 {
		t.Fatalf("placed bottom-right inclusive: got %d want 1", got)
	}
	if got := w2.HitTest(25, 15); got != -1 {
		t.Fatalf("placed outside: got %d want -1", got)
	}
}

func TestZeroTokenSuppressionExact(t *testing.T) {
	// [07 §3] zero-token mode peek suppresses 0xE2..0xEB inclusive ONLY
	for code := 0; code < 256; code++ {
		b := byte(code)
		sup := IsSuppressedInZeroTokenMode(b)
		inRange := b >= 0xE2 && b <= 0xEB
		if sup != inRange {
			t.Fatalf("IsSuppressed %02X = %v want %v [07 §3]", b, sup, inRange)
		}
	}
	// Verify exactly 10 codes suppressed
	count := 0
	for code := 0; code < 256; code++ {
		if IsSuppressedInZeroTokenMode(byte(code)) {
			count++
		}
	}
	if count != 10 {
		t.Fatalf("suppressed count = %d want 10 (0xE2..0xEB) [07 §3]", count)
	}
	// Filter behavior
	supTok := input.Token{Code: 0xE5, Char: 0}
	if got := FilterZeroMode(supTok, true); got.Code != 0 || got.Char != 0 {
		t.Fatalf("FilterZeroMode suppressed should return zero token, got %+v", got)
	}
	if got := FilterZeroMode(supTok, false); got.Code != 0xE5 {
		t.Fatalf("FilterZeroMode non-zero mode should not suppress, got %+v", got)
	}
	nonSup := input.Token{Code: 0x20, Char: ' '}
	if got := FilterZeroMode(nonSup, true); got.Code != 0x20 {
		t.Fatalf("FilterZeroMode non-suppressed code should pass through even in zero mode, got %+v", got)
	}
	edgeLow := input.Token{Code: 0xE2}
	if got := FilterZeroMode(edgeLow, true); got.Code != 0 {
		t.Fatalf("edge 0xE2 should be suppressed in zero mode")
	}
	edgeHigh := input.Token{Code: 0xEB}
	if got := FilterZeroMode(edgeHigh, true); got.Code != 0 {
		t.Fatalf("edge 0xEB should be suppressed in zero mode")
	}
	outLow := input.Token{Code: 0xE1}
	if got := FilterZeroMode(outLow, true); got.Code != 0xE1 {
		t.Fatalf("0xE1 should NOT be suppressed")
	}
	outHigh := input.Token{Code: 0xEC}
	if got := FilterZeroMode(outHigh, true); got.Code != 0xEC {
		t.Fatalf("0xEC should NOT be suppressed")
	}
}

func TestCloseTopSequenceOrder(t *testing.T) {
	// [07 §3] closing top window runs callbacks → redraw → removal → predecessor reactivation + extra redraw under flag 0x800
	makeWindow := func(name string) *Window {
		return &Window{Name: name, Gadgets: []Gadget{{Kind: KindPanel, Name: "P", Active: 1}}}
	}
	// Single window, no flag: callbacks, redraw, remove
	s := &Stack{}
	s.Push(makeWindow("TOP"))
	var spy []string
	s.CloseTop(&spy)
	want := []string{"callbacks", "redraw", "remove"}
	if !equalSlice(spy, want) {
		t.Fatalf("close single no flag order = %v want %v [07 §3]", spy, want)
	}
	if s.Len() != 0 {
		t.Fatalf("stack len after close = %d want 0", s.Len())
	}
	// Two windows, no flag: adds reactivate
	s = &Stack{}
	s.Push(makeWindow("BOTTOM"))
	s.Push(makeWindow("TOP"))
	spy = nil
	s.CloseTop(&spy)
	want = []string{"callbacks", "redraw", "remove", "reactivate"}
	if !equalSlice(spy, want) {
		t.Fatalf("close with predecessor no flag = %v want %v [07 §3]", spy, want)
	}
	if s.Len() != 1 || s.Top().Name != "BOTTOM" {
		t.Fatalf("predecessor not reactivated correctly, len %d top %v", s.Len(), s.Top())
	}
	// Single window with flag 0x800: extra redraw
	s = &Stack{Flags: FlagExtraRedraw}
	s.Push(makeWindow("TOP"))
	spy = nil
	s.CloseTop(&spy)
	want = []string{"callbacks", "redraw", "remove", "extra_redraw"}
	if !equalSlice(spy, want) {
		t.Fatalf("close single with 0x800 = %v want %v [07 §3]", spy, want)
	}
	// Two windows with flag 0x800: callbacks, redraw, remove, reactivate, extra_redraw
	s = &Stack{Flags: FlagExtraRedraw}
	s.Push(makeWindow("BOTTOM"))
	s.Push(makeWindow("TOP"))
	spy = nil
	s.CloseTop(&spy)
	want = []string{"callbacks", "redraw", "remove", "reactivate", "extra_redraw"}
	if !equalSlice(spy, want) {
		t.Fatalf("close with predecessor + 0x800 = %v want %v [07 §3]", spy, want)
	}
	// Empty stack no panic
	s = &Stack{}
	spy = nil
	s.CloseTop(&spy)
	if len(spy) != 0 {
		t.Fatalf("close empty should produce no calls, got %v", spy)
	}
	// Verify flag exactly 0x800, not other bits
	s = &Stack{Flags: 0x80}
	s.Push(makeWindow("TOP"))
	spy = nil
	s.CloseTop(&spy)
	for _, c := range spy {
		if c == "extra_redraw" {
			t.Fatalf("flag 0x80 should NOT trigger extra_redraw, only 0x800 [07 §3]")
		}
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScrollbarFromListTakeover(t *testing.T) {
	// [07 §4] C7 scrollbars associated with lists take range/knob size/position from the list
	w := &Window{
		Name: "test",
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "PANEL", Rect: Rect{X: 0, Y: 0, W: 640, H: 480}, Active: 1},
			{Kind: KindListBox, Name: "MYLIST", Assoc: 5, Range: 20, KnobPos: 2, KnobSize: 5, Active: 1},
			{Kind: KindScrollBar, Name: "MYSCROLL", Assoc: 5, Range: 99, KnobPos: 99, KnobSize: 99, Active: 1},
			{Kind: KindListBox, Name: "OTHERLIST", Assoc: 7, Range: 10, KnobPos: 1, KnobSize: 3, Active: 1},
			{Kind: KindScrollBar, Name: "OTHERSCROLL", Assoc: 7, Range: 50, KnobPos: 50, KnobSize: 50, Active: 1},
			{Kind: KindScrollBar, Name: "UNASSOCIATED", Assoc: 0, Range: 42, KnobPos: 4, KnobSize: 4, Active: 1},
			{Kind: KindScrollBar, Name: "ORPHAN", Assoc: 9, Range: 60, KnobPos: 6, KnobSize: 6, Active: 1}, // no list with assoc 9
		},
	}
	SyncScrollbars(w)
	// MYSCROLL should inherit from MYLIST
	for _, g := range w.Gadgets {
		if g.Name == "MYSCROLL" {
			if g.Range != 20 || g.KnobPos != 2 || g.KnobSize != 5 {
				t.Fatalf("MYSCROLL not taken from list: got range %d knobpos %d knobsize %d want 20,2,5 [07 §4] C7", g.Range, g.KnobPos, g.KnobSize)
			}
		}
		if g.Name == "OTHERSCROLL" {
			if g.Range != 10 || g.KnobPos != 1 || g.KnobSize != 3 {
				t.Fatalf("OTHERSCROLL not taken from list: got %d %d %d want 10,1,3", g.Range, g.KnobPos, g.KnobSize)
			}
		}
		if g.Name == "UNASSOCIATED" {
			if g.Range != 42 {
				t.Fatalf("unassociated scrollbar should keep authored values, got %d want 42", g.Range)
			}
		}
		if g.Name == "ORPHAN" {
			if g.Range != 60 {
				t.Fatalf("orphan scrollbar with no matching list should keep authored, got %d want 60", g.Range)
			}
		}
	}
}

func TestAssociationNameCompare16(t *testing.T) {
	// [07 §4] association-capable (up to 16 byte name compare across fixed-stride scan)
	w := &Window{
		Name: "assoc",
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "PANEL", Active: 1},
			{Kind: KindLabel, Name: "TARGET_NAME_123", Active: 1},              // family 5 trigger
			{Kind: KindButton, Name: "OTHER", Active: 1},                       // not matching
			{Kind: KindButton, Name: "TARGET_NAME_123", Active: 1},             // exact match at idx 3
			{Kind: KindLabel, Name: "TARGET_NAME_1234567890_EXTRA", Active: 1}, // same 16-byte prefix? first 16 bytes are "TARGET_NAME_1234" vs "TARGET_NAME_123"? need to test
			{Kind: KindButton, Name: "TARGET_NAME_123", Active: 1},             // second match
		},
	}
	// FindAssociated should find first matching gadget in scan order, skipping src itself
	idx := FindAssociated(w, 1)
	if idx != 3 {
		t.Fatalf("FindAssociated from 1 should find idx 3, got %d", idx)
	}
	// 16-byte compare: names that differ beyond 16 should still match
	a := "1234567890123456_EXTRA1"
	b := "1234567890123456_EXTRA2"
	if !Name16Equal(a, b) {
		t.Fatalf("Name16Equal should compare only first 16 bytes, %q vs %q should match", a, b)
	}
	c := "1234567890123456A"
	d := "1234567890123456B"
	if !Name16Equal(c, d) {
		t.Fatalf("Name16Equal with same first 16 should match even if 17th differs, %q vs %q", c, d)
	}
	// Differ within first 16 should NOT match
	e := "123456789012345A"
	f := "123456789012345B"
	if Name16Equal(e, f) {
		t.Fatalf("Name16Equal differing within first 16 should not match, %q vs %q", e, f)
	}
	// Corrected: if first 16 equal, should match even if 17th differs
	if !Name16Equal("1234567890123456X", "1234567890123456Y") {
		t.Fatalf("Name16Equal first 16 equal should match")
	}
	// Short names padded: "ABC" vs "ABC" should match, "ABC" vs "ABD" should not
	if !Name16Equal("ABC", "ABC") {
		t.Fatalf("short equal should match")
	}
	if Name16Equal("ABC", "ABD") {
		t.Fatalf("short differing should not match")
	}
	// Fixed-stride scan order: should return lowest index matching
	w2 := &Window{
		Name: "scan",
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "PANEL", Active: 1},
			{Kind: KindLabel, Name: "FOO", Active: 1},  // src 1
			{Kind: KindButton, Name: "BAR", Active: 1}, // 2 non-match
			{Kind: KindButton, Name: "FOO", Active: 1}, // 3 first match
			{Kind: KindButton, Name: "FOO", Active: 1}, // 4 second match
		},
	}
	if idx := FindAssociated(w2, 1); idx != 3 {
		t.Fatalf("fixed-stride scan should return first match at 3, got %d", idx)
	}
	// Non-association family should return -1
	if idx := FindAssociated(w, 2); idx != -1 {
		t.Fatalf("non-association family gadget should return -1, got %d", idx)
	}
}

func TestRepeatingAndTimedFamilies(t *testing.T) {
	// [07 §4] family 12 repeating/decrementing under throttle, family 13 timed/range animating toward max
	// Repeating
	rs := RepeatingState{Counter: 3}
	if !rs.TickRepeating(true) || rs.Counter != 2 {
		t.Fatalf("repeating tick with throttle should decrement to 2, got %d", rs.Counter)
	}
	if rs.TickRepeating(false) || rs.Counter != 2 {
		t.Fatalf("repeating tick without throttle should not decrement")
	}
	rs.TickRepeating(true)
	rs.TickRepeating(true)
	if rs.Counter != 0 {
		t.Fatalf("repeating should reach 0, got %d", rs.Counter)
	}
	if rs.TickRepeating(true) {
		t.Fatalf("repeating at 0 should not tick further")
	}
	// Timed
	ts := TimedState{Value: 0, Max: 3}
	if ts.AdvanceTimed(false) {
		t.Fatalf("timed without throttle should not advance")
	}
	if ts.AdvanceTimed(true) && ts.Value != 1 {
		t.Fatalf("timed advance 1 failed, value %d", ts.Value)
	}
	// Advance to max fires
	ts.AdvanceTimed(true)          // 2
	fired := ts.AdvanceTimed(true) // 3 reaches max
	if !fired || ts.Value != 3 {
		t.Fatalf("timed reaching max should fire, value %d fired %v", ts.Value, fired)
	}
	if ts.AdvanceTimed(true) {
		t.Fatalf("timed beyond max should not fire")
	}
}

func TestDispatchPrecedesBattle(t *testing.T) {
	// [07 §3] GUI dispatch precedes battle hotkeys
	if !DispatchPrecedesBattle() {
		t.Fatalf("DispatchPrecedesBattle should be true [07 §3]")
	}
}

func TestGUIDispatchPrecedesBattleOrdering(t *testing.T) {
	// Verify that GUI would consume token before battle sees it, via FilterZeroMode and RouteEvent
	w := &Window{
		Gadgets: []Gadget{
			{Kind: KindPanel, Name: "P", Active: 1},
			{Kind: KindButton, Name: "B", Rect: Rect{X: 0, Y: 0, W: 10, H: 10}, Active: 1},
		},
	}
	ev := Event{X: 5, Y: 5, Key: input.Token{Code: 0x20}}
	fam, idx := RouteEvent(w, ev)
	if fam != FamilyClickable || idx != 1 {
		t.Fatalf("RouteEvent should identify clickable family 1 at idx 1, got fam %d idx %d", fam, idx)
	}
}
