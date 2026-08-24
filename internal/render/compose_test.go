package render

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// mockObj implements StripObject for lifecycle tests [03 §1] C4.
type mockObj struct {
	id        int
	shouldRem bool
	updated   int
	latch     bool // when true, first Update makes ShouldRemove true next tick
	latchSet  bool
}

func (m *mockObj) ShouldRemove(_ uint32) bool { return m.shouldRem }
func (m *mockObj) Update(_ uint32) {
	m.updated++
	if m.latch && !m.latchSet {
		m.latchSet = true
		m.shouldRem = true
	}
}

// TestStripOrder verifies exactly ten strips each closed by barrier 0..9
// in the [03 §1] order with correct gating [03 §1] C1.
func TestStripOrder(t *testing.T) {
	// helper to record draws and barriers
	run := func(mode int, keyDown bool, opts uint8) ([]int, []string) {
		var c Composer
		c.KeyDown = keyDown
		c.OptionsByte = opts
		c.Fog = nil
		var order []string
		var barriers []int
		c.Hooks.Terrain = func() { order = append(order, "terrain") }
		c.Hooks.Minimap = func() { order = append(order, "minimap") }
		c.Hooks.Clip = func() { order = append(order, "clip") }
		c.Hooks.DrawStrip = func(idx int) { order = append(order, "strip"+string(rune('0'+idx))) }
		c.Hooks.BucketBuild = func() { order = append(order, "bucket") }
		c.Hooks.FeaturePass = func() { order = append(order, "feature") }
		c.Hooks.UnitTraversal = func(kind string) { order = append(order, "traversal:"+kind) }
		c.Hooks.Projectiles = func() { order = append(order, "projectiles") }
		c.Hooks.Effects = func() { order = append(order, "effects") }
		c.Hooks.KeyOverlay = func() { order = append(order, "keyOverlay") }
		c.Hooks.UnitLabel = func(unit snapshot.UnitView) { order = append(order, "label") }
		c.Hooks.OverlayA = func() { order = append(order, "overlayA") }
		c.Hooks.OverlayB = func() { order = append(order, "overlayB") }
		c.Hooks.Fog = func() { order = append(order, "fog") }
		c.Hooks.Selection = func() { order = append(order, "selection") }
		c.Hooks.Interface = func() { order = append(order, "interface") }
		c.Hooks.Barrier = func(pass int) {}
		c.Hooks.Shake = func() { order = append(order, "shake") }
		c.ShakeHook = func() { order = append(order, "shakeHook") }
		// capture barriers directly
		origBarrier := c.Hooks.Barrier
		c.Hooks.Barrier = func(pass int) {
			if origBarrier != nil {
				origBarrier(pass)
			}
		}
		c.Frame(&snapshot.Frame{Units: []snapshot.UnitView{{Slot: 1, Owner: 0}}}, 0, mode)
		barriers = append([]int(nil), c.Barriers...)
		return barriers, order
	}

	// Mode 0: only unconditional passes draw + strip 8 always [03 §1].
	// Unconditional strips: 0,1,2,3,4,5 and strip 8
	barriers0, order0 := run(0, false, 0)
	want0 := []int{0, 1, 2, 3, 4, 5, 8}
	if !reflect.DeepEqual(barriers0, want0) {
		t.Fatalf("mode0 barriers %v want %v", barriers0, want0)
	}
	// Ensure gated strips/effects not present in order0
	for _, wantAbsent := range []string{"strip6", "strip7", "strip9", "projectiles", "effects", "fog"} {
		for _, o := range order0 {
			if o == wantAbsent {
				t.Fatalf("mode0 order contains gated %q: %v", wantAbsent, order0)
			}
		}
	}
	// Ensure terrain/minimap/clip and feature and traversals still present (some traversals are unconditional mid)
	foundTerrain := false
	for _, o := range order0 {
		if o == "terrain" {
			foundTerrain = true
		}
	}
	if !foundTerrain {
		t.Fatalf("mode0 missing terrain in %v", order0)
	}
	// Ensure bucket/feature present even with mode0 (they are before gated group) [03 §1]
	hasBucket := false
	hasFeature := false
	for _, o := range order0 {
		if o == "bucket" {
			hasBucket = true
		}
		if o == "feature" {
			hasFeature = true
		}
	}
	if !hasBucket || !hasFeature {
		t.Fatalf("mode0 bucket/feature missing %v", order0)
	}
	// Ensure fog never covers selection/interface [03 §1] C2: fog before selection/interface when mode!=0,
	// and when mode0 fog absent but selection/interface still present.
	hasFog := false
	hasSel := false
	hasIface := false
	for _, o := range order0 {
		if o == "fog" {
			hasFog = true
		}
		if o == "selection" {
			hasSel = true
		}
		if o == "interface" {
			hasIface = true
		}
	}
	if hasFog {
		t.Fatalf("mode0 should have no fog")
	}
	if !hasSel || !hasIface {
		t.Fatalf("mode0 missing selection/interface %v", order0)
	}

	// Mode nonzero: all strips 0..9 fire in order [03 §1].
	barriers1, order1 := run(1, true, OptLabels|OptOverlayA|OptOverlayB)
	want1 := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if !reflect.DeepEqual(barriers1, want1) {
		t.Fatalf("mode1 barriers %v want %v", barriers1, want1)
	}
	// Verify strict numeric order
	for i := 1; i < len(barriers1); i++ {
		if barriers1[i] <= barriers1[i-1] {
			t.Fatalf("barriers not strictly increasing %v", barriers1)
		}
	}
	// Verify projectiles/effects between strips 6 and 7 [03 §1] C2
	idx6, idxProj, idxEff, idx7, idx8 := -1, -1, -1, -1, -1
	for i, o := range order1 {
		switch o {
		case "strip6":
			idx6 = i
		case "projectiles":
			idxProj = i
		case "effects":
			idxEff = i
		case "strip7":
			idx7 = i
		case "strip8":
			idx8 = i
		}
	}
	if idx6 == -1 || idxProj == -1 || idxEff == -1 || idx7 == -1 || idx8 == -1 {
		t.Fatalf("missing gated elements in order1 %v (6=%d proj=%d eff=%d 7=%d 8=%d)", order1, idx6, idxProj, idxEff, idx7, idx8)
	}
	if !(idx6 < idxProj && idxProj < idxEff && idxEff < idx7 && idx7 < idx8) {
		t.Fatalf("projectiles/effects not between strips 6 and 7: %v", order1)
	}
	// Verify fog after world drawing before selection/interface [03 §1] C1 C2
	idxFog, idxSel, idxIface := -1, -1, -1
	for i, o := range order1 {
		switch o {
		case "fog":
			idxFog = i
		case "selection":
			idxSel = i
		case "interface":
			idxIface = i
		}
	}
	if idxFog == -1 {
		t.Fatalf("mode1 missing fog")
	}
	if idxSel == -1 || idxIface == -1 {
		t.Fatalf("missing selection/interface")
	}
	if !(idxFog < idxSel && idxSel < idxIface) {
		t.Fatalf("fog must be before selection/interface [03 §1] C2 got fog=%d sel=%d iface=%d order %v", idxFog, idxSel, idxIface, order1)
	}
	// Verify key overlay and labels and optional overlays present when gated and opts set [03 §1] step9
	foundKey, foundLabel, foundA, foundB := false, false, false, false
	for _, o := range order1 {
		if o == "keyOverlay" {
			foundKey = true
		}
		if o == "label" {
			foundLabel = true
		}
		if o == "overlayA" {
			foundA = true
		}
		if o == "overlayB" {
			foundB = true
		}
	}
	if !foundKey || !foundLabel || !foundA || !foundB {
		t.Fatalf("mode1 step9 overlays missing key=%v label=%v A=%v B=%v order %v", foundKey, foundLabel, foundA, foundB, order1)
	}

	// Verify gating of optional overlays and strip9 when mode0 they are absent even with opts set
	_, order0opts := run(0, true, OptOverlayA|OptOverlayB)
	for _, bad := range []string{"strip9", "overlayA", "overlayB", "fog"} {
		for _, o := range order0opts {
			if o == bad {
				t.Fatalf("mode0 with opts should not contain %q but got %v", bad, order0opts)
			}
		}
	}
	// But keyOverlay should still appear with mode0 if keyDown (it is outside render-mode gate for key part)
	foundKey0 := false
	for _, o := range order0opts {
		if o == "keyOverlay" {
			foundKey0 = true
		}
	}
	if !foundKey0 {
		t.Fatalf("key overlay under key predicate should appear even with mode0 [03 §1] got %v", order0opts)
	}
}

// TestUnitLabelGating verifies labels gated on options byte and owner==local slot [03 §1] C1.
func TestUnitLabelGating(t *testing.T) {
	f := &snapshot.Frame{
		Units: []snapshot.UnitView{
			{Slot: 10, Owner: 0},
			{Slot: 11, Owner: 1},
			{Slot: 12, Owner: 0},
		},
	}
	// With OptLabels unset, no labels even if owner matches
	var c Composer
	c.LocalSlot = 0
	c.OptionsByte = 0
	count := 0
	c.Hooks.UnitLabel = func(unit snapshot.UnitView) { count++ }
	c.Frame(f, 0, 1)
	if count != 0 {
		t.Fatalf("labels without opt bit should be 0 got %d", count)
	}
	// With OptLabels set, only owner==local slot labels
	c.OptionsByte = OptLabels
	count = 0
	c.Frame(f, 0, 1)
	if count != 2 {
		t.Fatalf("labels with opt and local 0 want 2 (owners 0,0) got %d", count)
	}
	// Change local slot
	c.LocalSlot = 1
	count = 0
	c.Frame(f, 0, 1)
	if count != 1 {
		t.Fatalf("labels local 1 want 1 got %d", count)
	}
}

// TestYBucketOrdering verifies per-row screen-Y bucket insertion [03 §1] C3.
func TestYBucketOrdering(t *testing.T) {
	var b YBuckets
	// Insert in enumeration order with varying rows unsorted
	// enumeration: ids 0..5 with rows 10,5,10,7,5,10
	inserts := []struct{ row, id int }{
		{10, 0},
		{5, 1},
		{10, 2},
		{7, 3},
		{5, 4},
		{10, 5},
	}
	for _, ins := range inserts {
		b.Insert(ins.row, ins.id)
	}
	// Rows sorted: 5,7,10
	rows := b.Rows()
	wantRows := []int{5, 7, 10}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("rows %v want %v", rows, wantRows)
	}
	// Paint = Y-sorted rows with in-row enumeration order, no depth test [03 §1] C3
	ordered := b.Ordered()
	// row5: ids 1,4 in enumeration order; row7: 3; row10: 0,2,5
	wantOrdered := []int{1, 4, 3, 0, 2, 5}
	if !reflect.DeepEqual(ordered, wantOrdered) {
		t.Fatalf("ordered %v want %v", ordered, wantOrdered)
	}
	// Verify no depth test: two entries in same row inserted in enumeration
	// order must stay that order even if second has "greater depth".
	// Simulate depth variation by inserting same row but varying a phantom depth value;
	// bucket ignores depth, so order is enumeration.
	var b2 YBuckets
	b2.Insert(20, 100) // enumeration first, depth small
	b2.Insert(20, 200) // enumeration second, depth large — should still be 100,200
	ord2 := b2.Ordered()
	if !reflect.DeepEqual(ord2, []int{100, 200}) {
		t.Fatalf("in-row enumeration order not preserved, got %v", ord2)
	}
	// Also verify Composer's bucket building preserves enumeration order
	var c Composer
	f := &snapshot.Frame{Units: []snapshot.UnitView{}}
	// Use non-camera fallback row: row = wz - (wy>>1). Craft units with same row.
	f.Units = []snapshot.UnitView{
		{Slot: 1, X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(10 << 16)},
		{Slot: 2, X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(10 << 16)},
		{Slot: 3, X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(5 << 16)},
	}
	c.Frame(f, 0, 0)
	// Buckets should contain 3 entries, rows sorted, in-row order by enumeration
	// Units enumerated 0,1,2: row for slot1=10,row slot2=10,row slot3=5 => ordered should be slot3, slot1, slot2
	orderedComp := c.Buckets.Ordered()
	// Buckets store Slot ids as ints
	wantComp := []int{3, 1, 2}
	if !reflect.DeepEqual(orderedComp, wantComp) {
		t.Fatalf("composer buckets ordered %v want %v rows %v buckets %v", orderedComp, wantComp, c.Buckets.Rows(), c.Buckets.buckets)
	}
}

// TestRemovalBeforeUpdate verifies removal evaluated BEFORE update and stable compaction [03 §1] C4.
func TestRemovalBeforeUpdate(t *testing.T) {
	var s Strip
	a := &mockObj{id: 0}
	b := &mockObj{id: 1, shouldRem: true}
	cObj := &mockObj{id: 2}
	d := &mockObj{id: 3, shouldRem: true}
	s.Append(a)
	s.Append(b)
	s.Append(cObj)
	s.Append(d)
	if len(s.Objects) != 4 {
		t.Fatalf("setup len %d want 4", len(s.Objects))
	}
	s.Update(1)
	if len(s.Objects) != 2 {
		t.Fatalf("after update len %d want 2 survivors", len(s.Objects))
	}
	// Survivors keep order [03 §1] C4
	if s.Objects[0] != a || s.Objects[1] != cObj {
		t.Fatalf("survivor order wrong got ids %d %d want 0 2", s.Objects[0].(*mockObj).id, s.Objects[1].(*mockObj).id)
	}
	if b.updated != 0 || d.updated != 0 {
		t.Fatalf("removed objects should not be updated b=%d d=%d", b.updated, d.updated)
	}
	if a.updated != 1 || cObj.updated != 1 {
		t.Fatalf("survivors should be updated once a=%d c=%d", a.updated, cObj.updated)
	}
	// Terminal condition created during update noticed only NEXT invocation [03 §1] C4
	var s2 Strip
	e := &mockObj{id: 10, latch: true}
	fObj := &mockObj{id: 11}
	s2.Append(e)
	s2.Append(fObj)
	s2.Update(10) // first update: e.Update sets shouldRem=true but not removed this tick
	if len(s2.Objects) != 2 {
		t.Fatalf("latch first update should not remove, len %d want 2", len(s2.Objects))
	}
	if e.updated != 1 || !e.shouldRem {
		t.Fatalf("latch first update e updated=%d shouldRem=%v want 1,true", e.updated, e.shouldRem)
	}
	// Second invocation: removal BEFORE update so e should be removed without second Update
	s2.Update(11)
	if len(s2.Objects) != 1 {
		t.Fatalf("latch second update len %d want 1", len(s2.Objects))
	}
	if s2.Objects[0] != fObj {
		t.Fatalf("remaining should be f")
	}
	if e.updated != 1 {
		t.Fatalf("latch object should not be updated second time, got %d want 1", e.updated)
	}
	if fObj.updated != 2 {
		t.Fatalf("f should be updated twice, got %d", fObj.updated)
	}
}

// TestStableCompaction verifies survivors keep insertion order after mixed removals [03 §1] C4.
func TestStableCompaction(t *testing.T) {
	var s Strip
	objs := make([]*mockObj, 6)
	for i := 0; i < 6; i++ {
		objs[i] = &mockObj{id: i, shouldRem: i%2 == 1} // remove odd ids
		s.Append(objs[i])
	}
	s.Update(1)
	if len(s.Objects) != 3 {
		t.Fatalf("len %d want 3", len(s.Objects))
	}
	want := []int{0, 2, 4}
	for i, wantID := range want {
		if s.Objects[i].(*mockObj).id != wantID {
			t.Fatalf("stable order i=%d got %d want %d", i, s.Objects[i].(*mockObj).id, wantID)
		}
	}
}

// TestStripEviction verifies pre-insert >400 destroys oldest first, steady state ≤401 [03 §1] C4.
func TestStripEviction(t *testing.T) {
	var s Strip
	// Fill to 401 via steady appends
	for i := 0; i < 401; i++ {
		s.Append(&mockObj{id: i})
	}
	if len(s.Objects) != 401 {
		t.Fatalf("after 401 appends len %d want 401", len(s.Objects))
	}
	if s.Objects[0].(*mockObj).id != 0 || s.Objects[400].(*mockObj).id != 400 {
		t.Fatalf("initial order corrupted")
	}
	// Next append should destroy oldest (0) then append 401
	s.Append(&mockObj{id: 401})
	if len(s.Objects) != 401 {
		t.Fatalf("after eviction len %d want 401 steady", len(s.Objects))
	}
	if s.Objects[0].(*mockObj).id != 1 {
		t.Fatalf("oldest eviction failed, first id %d want 1", s.Objects[0].(*mockObj).id)
	}
	if s.Objects[400].(*mockObj).id != 401 {
		t.Fatalf("last id %d want 401", s.Objects[400].(*mockObj).id)
	}
	// Survivor order equals insertion order among survivors [03 §1]
	for i, obj := range s.Objects {
		wantID := i + 1
		if obj.(*mockObj).id != wantID {
			t.Fatalf("survivor order i=%d id %d want %d", i, obj.(*mockObj).id, wantID)
		}
	}
	// Append many more and ensure steady state stays 401 and oldest evicted FIFO
	for i := 402; i < 500; i++ {
		s.Append(&mockObj{id: i})
		if len(s.Objects) != 401 {
			t.Fatalf("steady state violated at i=%d len %d", i, len(s.Objects))
		}
	}
	// After appending up to 499, remaining should be ids 99..499 inclusive (401 entries)
	if s.Objects[0].(*mockObj).id != 99 {
		t.Fatalf("after many evictions first id %d want 99", s.Objects[0].(*mockObj).id)
	}
	if s.Objects[400].(*mockObj).id != 499 {
		t.Fatalf("last id %d want 499", s.Objects[400].(*mockObj).id)
	}
}

// TestComposerUpdateAcrossStrips verifies Composer.Update stably updates all strips [03 §1] C4.
func TestComposerUpdateAcrossStrips(t *testing.T) {
	var c Composer
	a := &mockObj{id: 1}
	b := &mockObj{id: 2, shouldRem: true}
	c.Strips[0].Append(a)
	c.Strips[0].Append(b)
	c.Strips[5].Append(&mockObj{id: 5})
	c.Strips[9].Append(&mockObj{id: 9, shouldRem: true})
	c.Update(42)
	if len(c.Strips[0].Objects) != 1 || c.Strips[0].Objects[0].(*mockObj).id != 1 {
		t.Fatalf("strip0 update failed")
	}
	if len(c.Strips[5].Objects) != 1 {
		t.Fatalf("strip5 should remain")
	}
	if len(c.Strips[9].Objects) != 0 {
		t.Fatalf("strip9 should be empty")
	}
}

// TestProjectilesNotStripObjects verifies C2 projectiles/effects sit between strips 6 and 7 not as strip objects.
func TestProjectilesNotStripObjects(t *testing.T) {
	// Put identifiable objects in strips 6 and 7
	var c Composer
	c.Strips[6].Append(&mockObj{id: 600})
	c.Strips[7].Append(&mockObj{id: 700})
	drawnStrips := []int{}
	c.Hooks.DrawStrip = func(idx int) { drawnStrips = append(drawnStrips, idx) }
	order := []string{}
	c.Hooks.Projectiles = func() { order = append(order, "proj") }
	c.Hooks.Effects = func() { order = append(order, "eff") }
	c.Hooks.Barrier = func(pass int) { order = append(order, "b") }
	// Capture draw order via hooks and ensure projectiles not drawn via strips
	c.Frame(&snapshot.Frame{}, 0, 1)
	// Check that Strips[6] objects still exist (not cleared as projectiles)
	if len(c.Strips[6].Objects) != 1 || len(c.Strips[7].Objects) != 1 {
		t.Fatalf("strips 6/7 objects should remain, not consumed as projectiles")
	}
	// Verify order as before: strip6 barrier then proj/eff then strip7
	// We recorded barriers via c.Barriers, but order slice should show proj/eff between
	// Find positions
	var barrier6Idx, barrier7Idx int = -1, -1
	for i, b := range c.Barriers {
		if b == 6 {
			barrier6Idx = i
		}
		if b == 7 {
			barrier7Idx = i
		}
	}
	// order contains hook sequence, not barriers; but we can check that proj/eff hooks were called
	if len(order) == 0 {
		t.Fatalf("no projectiles/effects hooks called")
	}
	// barriers should be 0..9 inclusive
	if len(c.Barriers) != 10 {
		t.Fatalf("barriers %v want 10", c.Barriers)
	}
	if barrier6Idx == -1 || barrier7Idx == -1 || barrier6Idx > barrier7Idx {
		t.Fatalf("barriers 6 before 7 failed %v", c.Barriers)
	}
	// drawnStrips should include 6 and 7 as strip draws
	found6, found7 := false, false
	for _, d := range drawnStrips {
		if d == 6 {
			found6 = true
		}
		if d == 7 {
			found7 = true
		}
	}
	if !found6 || !found7 {
		t.Fatalf("strips 6,7 not drawn via strip dispatcher")
	}
}

// TestShakeHook ensures composer exposes shake hook without editing shake.go [03 §5.6].
func TestShakeHook(t *testing.T) {
	var c Composer
	called := 0
	c.ShakeHook = func() { called++ }
	c.Hooks.Shake = func() { called++ }
	c.Frame(&snapshot.Frame{}, 0, 1)
	if called == 0 {
		t.Fatalf("shake hook not called")
	}
	called = 0
	c.Frame(&snapshot.Frame{}, 0, 0)
	if called == 0 {
		t.Fatalf("shake hook should be invocable even with mode0 (presentation) [03 §5.6]")
	}
}
