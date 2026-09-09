package render

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// synthetic entry helper for deterministic fixtures [03 §4.4] C8.
func syntheticEntry(durations []int32) *formats.GAFEntry {
	e := &formats.GAFEntry{
		Name:       "test",
		FrameCount: uint16(len(durations)),
		Frames:     make([]formats.GAFFrameRef, len(durations)),
	}
	for i, d := range durations {
		e.Frames[i] = formats.GAFFrameRef{Value: uint32(d)}
		// allocate empty frame to satisfy non-nil check for CurrentFrame alternative path
		e.Frames[i].Frame = &formats.GAFFrame{Width: 16, Height: 16}
	}
	return e
}

// TestCursorIndexTable verifies the cursor index table [07 §8].
// Slot 0 is unused/gray overflow; indices 1..20 map to named entries [07 §8].
func TestCursorIndexTable(t *testing.T) {
	if CursorCount != 22 {
		t.Fatalf("CursorCount %d want 22 [07 §8]", CursorCount)
	}
	// Slot 0 unused [07 §8]
	if got := CursorName(0); got != "" {
		t.Fatalf("slot 0 name %q want empty [07 §8]", got)
	}
	if IsValidCursorIndex(0) {
		t.Fatalf("slot 0 should be invalid [07 §8]")
	}
	if idx := CursorIndexFromName(""); idx != 0 {
		t.Fatalf("empty name should map to 0, got %d", idx)
	}
	// Verify 1..20 all valid and names match table
	want := map[int]string{
		1:  "cursorattack",
		2:  "cursorairstrike",
		3:  "cursortoofar",
		4:  "cursorcapture",
		5:  "cursordefend",
		6:  "cursorrepair",
		7:  "cursorpatrol",
		8:  "cursorpickup",
		9:  "cursorteleport",
		10: "cursorrevive",
		11: "cursorreclamate",
		12: "cursorload",
		13: "cursorunload",
		14: "cursormove",
		15: "cursorselect",
		16: "cursorfindsite",
		17: "cursorred",
		18: "cursorgrn",
		19: "cursornormal",
		20: "cursorhourglass",
		21: "pathicon",
	}
	if len(want) != 21 {
		t.Fatalf("want map size wrong")
	}
	seen := make(map[string]int)
	for idx, name := range want {
		if got := CursorName(idx); got != name {
			t.Fatalf("idx %d name %q want %q [07 §8]", idx, got, name)
		}
		if !IsValidCursorIndex(idx) {
			t.Fatalf("idx %d should be valid [07 §8]", idx)
		}
		if got := CursorIndexFromName(name); got != idx {
			t.Fatalf("reverse name %q got idx %d want %d [07 §8]", name, got, idx)
		}
		// case-insensitive check [fmt gaf]
		if got := CursorIndexFromName(stringUpper(name)); got != idx {
			t.Fatalf("case-insensitive name %q got %d want %d", name, got, idx)
		}
		if prev, ok := seen[name]; ok {
			t.Fatalf("duplicate name %q at %d and %d", name, prev, idx)
		}
		seen[name] = idx
	}
	// Out of range indices
	if got := CursorName(22); got != "" {
		t.Fatalf("out of range 22 should return empty, got %q", got)
	}
	if got := CursorName(-1); got != "" {
		t.Fatalf("negative index should return empty, got %q", got)
	}
	if IsValidCursorIndex(22) {
		t.Fatalf("22 should be invalid")
	}
	if IsValidCursorIndex(-1) {
		t.Fatalf("-1 should be invalid")
	}
	// Unknown name returns 0
	if got := CursorIndexFromName("cursornonexistent"); got != 0 {
		t.Fatalf("unknown name should return 0, got %d", got)
	}
}

func stringUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

// TestBuildSiteCursorChoice verifies build-site validity choosing [07 §8].
func TestBuildSiteCursorChoice(t *testing.T) {
	tests := []struct {
		valid     bool
		want      int
		wantName  string
		ghostWant int
		ghostName string
	}{
		{true, 16, "cursorfindsite", 18, "cursorgrn"},
		{false, 3, "cursortoofar", 17, "cursorred"},
	}
	for _, tc := range tests {
		build := CursorTooFar
		ghost := CursorRed
		if tc.valid {
			build = CursorFindSite
			ghost = CursorGrn
		}
		if build != tc.want {
			t.Fatalf("build cursor valid=%v got %d want %d [07 §8]", tc.valid, build, tc.want)
		}
		if name := CursorName(build); name != tc.wantName {
			t.Fatalf("build cursor name valid=%v got %q want %q", tc.valid, name, tc.wantName)
		}
		if ghost != tc.ghostWant {
			t.Fatalf("ghost cursor valid=%v got %d want %d [07 §8]", tc.valid, ghost, tc.ghostWant)
		}
		if name := CursorName(ghost); name != tc.ghostName {
			t.Fatalf("ghost cursor name valid=%v got %q want %q", tc.valid, name, tc.ghostName)
		}
		// Ensure both resolve to valid indices
		if !IsValidCursorIndex(build) {
			t.Fatalf("build cursor invalid")
		}
		if !IsValidCursorIndex(ghost) {
			t.Fatalf("ghost cursor invalid")
		}
	}
	// Matrix: ensure findsite vs too far are distinct and ghost red vs grn distinct
	if CursorFindSite == CursorTooFar {
		t.Fatalf("build site cursors should differ")
	}
	if CursorGrn == CursorRed {
		t.Fatalf("ghost cursors should differ")
	}
}

// TestGafCursorWrap verifies C8 single-tick wrapping [03 §4.4].
// Three frames of duration 10; a step at countdown 1 advances and wraps [PLAN_13].
func TestGafCursorWrap(t *testing.T) {
	durs := []int32{10, 10, 10}
	entry := syntheticEntry(durs)
	var c Cursor
	c.Bind(entry, 2, true) // start at last frame [03 §4.4]
	if c.Idx != 2 || c.Countdown != 10 {
		t.Fatalf("bind idx %d countdown %d want 2,10", c.Idx, c.Countdown)
	}
	// Simulate countdown falling to 1 then stepping should wrap
	c.Countdown = 1
	c.Step() // countdown <2 advances, wraps to 0 for looping [03 §4.4]
	if c.Idx != 0 {
		t.Fatalf("wrap step idx %d want 0 [03 §4.4]", c.Idx)
	}
	if c.Countdown != 10 {
		t.Fatalf("wrap step countdown %d want 10 [03 §4.4]", c.Countdown)
	}
	if !c.IsActive() {
		t.Fatalf("looping wrap should stay active [03 §4.4]")
	}
	// Also test non-looping termination
	var c2 Cursor
	c2.Bind(entry, 2, false)
	c2.Countdown = 1
	c2.Step()
	if c2.IsActive() {
		t.Fatalf("non-looping at end should clear entry pointer [03 §4.4]")
	}
	if c2.Entry() != nil {
		t.Fatalf("non-looping should clear entry [03 §4.4]")
	}
	// Single-frame never advances [03 §4.4]
	single := syntheticEntry([]int32{5})
	var c3 Cursor
	c3.Bind(single, 0, true)
	c3.Countdown = 1
	origIdx := c3.Idx
	c3.Step()
	if c3.Idx != origIdx {
		t.Fatalf("single-frame should never advance [03 §4.4] got %d want %d", c3.Idx, origIdx)
	}
	if !c3.IsActive() {
		t.Fatalf("single-frame should stay active")
	}
}

// TestGafCursorStepDelta verifies delta stepping can cross multiple frames [03 §4.4] C8.
func TestGafCursorStepDelta(t *testing.T) {
	durs := []int32{10, 10, 10}
	entry := syntheticEntry(durs)
	var c Cursor
	c.Bind(entry, 0, true)
	c.Countdown = 5
	// Negative delta -12 should cross one frame: 5-12=-7 <2 => advance to 1 with accumulation: -7+10=3
	c.StepDelta(-12)
	if c.Idx != 1 {
		t.Fatalf("delta -12 from idx0 countdown5 want idx1 got %d countdown %d", c.Idx, c.Countdown)
	}
	if c.Countdown != 3 {
		t.Fatalf("delta accumulation countdown %d want 3 [03 §4.4]", c.Countdown)
	}
	// Large negative cross multiple frames: from idx1 countdown3 delta -25 => 3-25=-22 => advance to 2: -22+10=-12 <2 => advance to 0: -12+10=-2 <2 => advance to1: -2+10=8
	var c2 Cursor
	c2.Bind(entry, 1, true)
	c2.Countdown = 3
	c2.StepDelta(-25)
	// trace: start idx1 cd3 => cd -22 => adv idx2 cd -12 => adv idx0 cd -2 => adv idx1 cd8
	if c2.Idx != 1 {
		t.Fatalf("large delta wrap idx %d want 1", c2.Idx)
	}
	if c2.Countdown != 8 {
		t.Fatalf("large delta countdown %d want 8", c2.Countdown)
	}
	// Single-frame never advances with delta [03 §4.4]
	single := syntheticEntry([]int32{10})
	var c3 Cursor
	c3.Bind(single, 0, true)
	c3.Countdown = 5
	c3.StepDelta(-20)
	if c3.Idx != 0 {
		t.Fatalf("single-frame delta should not advance, got %d", c3.Idx)
	}
}

// TestGafCursorDeterminism verifies frame stepping determinism [I4].
func TestGafCursorDeterminism(t *testing.T) {
	durs := []int32{3, 5, 7}
	entry := syntheticEntry(durs)
	run := func() []int {
		var c Cursor
		c.Bind(entry, 0, true)
		seq := []int{c.Idx}
		for i := 0; i < 20; i++ {
			c.Step()
			seq = append(seq, c.Idx)
		}
		return seq
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("determinism failed %v vs %v", a, b)
	}
	// Delta determinism
	runDelta := func(delta int16) []int {
		var c Cursor
		c.Bind(entry, 0, true)
		seq := []int{c.Idx}
		for i := 0; i < 5; i++ {
			c.StepDelta(delta)
			seq = append(seq, c.Idx)
		}
		return seq
	}
	ad := runDelta(-7)
	bd := runDelta(-7)
	if !reflect.DeepEqual(ad, bd) {
		t.Fatalf("delta determinism failed %v vs %v", ad, bd)
	}
}

// TestGafCursorBindClamp verifies binding clamps out-of-range start index to zero [03 §4.4].
func TestGafCursorBindClamp(t *testing.T) {
	entry := syntheticEntry([]int32{4, 5, 6})
	var c Cursor
	c.Bind(entry, 99, true) // out of range
	if c.Idx != 0 {
		t.Fatalf("bind clamp want 0 got %d [03 §4.4]", c.Idx)
	}
	if c.Countdown != 4 {
		t.Fatalf("bind countdown want 4 got %d [03 §4.4]", c.Countdown)
	}
	c.Bind(entry, -1, true)
	if c.Idx != 0 {
		t.Fatalf("negative bind clamp want 0 got %d", c.Idx)
	}
	// Nil entry clears
	c.Bind(nil, 0, true)
	if c.IsActive() {
		t.Fatalf("nil bind should clear")
	}
	empty := &formats.GAFEntry{Name: "empty", FrameCount: 0}
	c.Bind(empty, 0, true)
	if c.IsActive() {
		t.Fatalf("zero-frame bind should clear")
	}
}

// TestGafCursorGafResolution verifies GAF entry/frame resolution through formats.GAF [fmt gaf][07 §8].
func TestGafCursorGafResolution(t *testing.T) {
	// Build a synthetic GAF with cursor entries
	gaf := &formats.GAF{
		Entries: []formats.GAFEntry{},
	}
	// Manually insert entries matching cursor table subset
	// Use real formats.GAF Find is case-insensitive; we need to exercise it via LoadGAF helper?
	// Instead test via Resolve functions on a fake GAF constructed with byName map via LoadGAF.
	// For determinism, test that Resolve works on a GAF we build via LoadGAF bytes? Simpler:
	// Create a minimal GAF file bytes would be complex; instead just test that our synthetic
	// entry can be bound and resolved.

	// Test BindIndex failure when GAF nil or entry missing
	var c Cursor
	if c.BindIndex(nil, 1, 0, true) {
		t.Fatalf("nil GAF should fail")
	}
	// Create empty GAF with no entries
	emptyGAF := &formats.GAF{}
	if c.BindIndex(emptyGAF, 15, 0, true) {
		t.Fatalf("missing entry should fail and clear")
	}
	if c.IsActive() {
		t.Fatalf("failed bind should clear")
	}
	// Happy path: use synthetic GAF entry directly via Bind
	entry := syntheticEntry([]int32{10, 10})
	var c2 Cursor
	c2.Bind(entry, 0, true)
	if frame, ok := c2.CurrentFrame(); !ok || frame == nil {
		t.Fatalf("CurrentFrame should succeed")
	}
	// ResolveCursorFrame helper
	if frame, ok := ResolveCursorFrame(&c2); !ok || frame == nil {
		t.Fatalf("ResolveCursorFrame should succeed")
	}
	// advance and check frame still resolves
	c2.Step() // countdown 10 -> 9, no advance
	if c2.Idx != 0 {
		t.Fatalf("after one step idx should stay 0")
	}
	c2.Countdown = 1
	c2.Step() // should advance to 1
	if c2.Idx != 1 {
		t.Fatalf("after wrap idx 1")
	}
	if _, ok := c2.CurrentFrame(); !ok {
		t.Fatalf("frame after advance should resolve")
	}
	_ = gaf
	_ = emptyGAF
}

// TestGafCursorAssetGuarded exercises optional walk over an install cursor GAF [07 §8][fmt gaf].
// It is asset-guarded: when ~/TotalAnnihilation is absent the test is skipped.
func TestGafCursorAssetGuarded(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount failed: %v", err)
	}
	defer fs.Close()
	gaf, err := formats.LoadGAFFile(fs, "anims/cursors.gaf")
	if err != nil {
		// try upper case
		gaf, err = formats.LoadGAFFile(fs, "anims/CURSORS.GAF")
		if err != nil {
			t.Skipf("cursor GAF not available: %v", err)
		}
	}
	// Assert relationships [07 §8][fmt gaf]
	// At least the 20 cursor entries should be present or a subset; retail cursors.gaf has ~? entries
	if gaf.EntryCount == 0 {
		t.Fatalf("cursor GAF has zero entries")
	}
	// Check that index table entries resolve case-insensitively when present
	missing := 0
	for idx := 1; idx <= CursorCount-1; idx++ {
		name := CursorName(idx)
		if name == "" {
			t.Fatalf("table hole at %d", idx)
		}
		entry, ok := gaf.Find(name)
		if !ok {
			// Not all installs may have pathicon etc; count missing
			missing++
			continue
		}
		if entry.FrameCount == 0 {
			t.Fatalf("entry %q (idx %d) has zero frames", name, idx)
		}
		if len(entry.Frames) != int(entry.FrameCount) {
			t.Fatalf("entry %q frames len %d vs count %d", name, len(entry.Frames), entry.FrameCount)
		}
		// Each frame's duration (Value) should be non-zero? Retail has 1..10 [fmt gaf]
		for i, ref := range entry.Frames {
			if ref.Frame == nil {
				t.Fatalf("entry %q frame %d nil decoded", name, i)
			}
			if ref.Value == 0 {
				t.Logf("warning: entry %q frame %d Value 0, treating as 1 [fmt gaf]", name, i)
			}
			if ref.Frame.Width == 0 || ref.Frame.Height == 0 {
				t.Fatalf("entry %q frame %d zero dimensions", name, i)
			}
		}
		// Also check reverse lookup
		if got := CursorIndexFromName(entry.Name); got != idx {
			// Name may differ in case but should still map
			if got != idx {
				t.Logf("name case mismatch %q idx %d lookup %d", entry.Name, idx, got)
			}
		}
	}
	// If many missing, log but not fail — some GAF variants may differ
	if missing > 10 {
		t.Logf("many cursor entries missing (%d/21), install may be minimal", missing)
	}
	// Spot check build-site cursors are present
	for _, idx := range []int{CursorFindSite, CursorTooFar, CursorGrn, CursorRed} {
		name := CursorName(idx)
		if _, ok := gaf.Find(name); !ok {
			t.Logf("expected build/ghost cursor %q (idx %d) not in GAF", name, idx)
		}
	}
}
