package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestCombatTailAppendNeverFillsHoles locks [06 §5.1], [01 §6.1] append-at-tail contract.
// Reserve handles 1..5, mark handle 2 dead; next Reserve returns 6 rather than filling hole.
// Full active span still rejects allocation even though a hole exists.
func TestCombatTailAppendNeverFillsHoles(t *testing.T) {
	var s Service
	for i := 0; i < 5; i++ {
		h, ok := s.Reserve()
		if !ok || h == 0 {
			t.Fatalf("reserve %d failed", i)
		}
		// Tag stable order via WeaponID.
		s.Records[int(h)-1].WeaponID = int32(i + 1)
	}
	// Mark handle 2 dead; count still 5 [06 §5.1].
	s.MarkDead(pool.Handle(2))
	if s.Count() != 5 {
		t.Fatalf("MarkDead changed count to %d want 5", s.Count())
	}
	if !s.IsDead(2) {
		t.Fatal("dead flag not set for handle 2")
	}
	// Next reserve appends at tail (handle 6), never fills hole [01 §6.1].
	h, ok := s.Reserve()
	if !ok {
		t.Fatalf("reserve after hole failed")
	}
	if h != pool.Handle(6) {
		t.Fatalf("Reserve after dead hole returned handle %d want 6 (must append at tail, never fill holes)", h)
	}
	if s.Count() != 6 {
		t.Fatalf("count after append = %d want 6", s.Count())
	}
	// Verify hole still dead.
	if !s.IsDead(2) {
		t.Fatal("hole should remain dead until compact")
	}
	// Fill to capacity and verify pool-full rejection even with holes.
	// Fill remaining slots to capacity.
	for s.Count() < ProjectileCapacity {
		if _, ok := s.Reserve(); !ok {
			t.Fatalf("fill to capacity failed at count %d", s.Count())
		}
	}
	if s.Count() != ProjectileCapacity {
		t.Fatalf("count = %d want %d", s.Count(), ProjectileCapacity)
	}
	// Mark one interior dead but still at capacity; next reserve must fail [06 §5.1].
	s.MarkDead(pool.Handle(10))
	s.MarkDead(pool.Handle(20))
	if s.Count() != ProjectileCapacity {
		t.Fatalf("MarkDead changed count")
	}
	if h, ok := s.Reserve(); ok || h != 0 {
		t.Fatalf("reserve past capacity with dead holes succeeded: handle %d", h)
	}
}

// TestCombatCountSemanticsAfterCompact verifies retirement sets dead flag without
// decrementing count [06 §5.1] and Compact publishes reduced count after scan [06 §5.2].
func TestCombatCountSemanticsAfterCompact(t *testing.T) {
	var s Service
	for i := 0; i < 5; i++ {
		h, _ := s.Reserve()
		s.Records[int(h)-1].WeaponID = int32(i * 10)
	}
	if s.Count() != 5 {
		t.Fatalf("count %d want 5", s.Count())
	}
	s.MarkDead(2)
	s.MarkDead(4)
	if s.Count() != 5 {
		t.Fatalf("MarkDead should not decrement count, got %d", s.Count())
	}
	// Compact removes dead, preserves order, reduces count [06 §5.2].
	s.Compact(nil)
	if s.Count() != 3 {
		t.Fatalf("count after compact = %d want 3", s.Count())
	}
	// Survivors should be handles 1,3,5 originally (WeaponIDs 0,20,40) in order.
	want := []int32{0, 20, 40}
	for i, w := range want {
		if s.Records[i].WeaponID != w {
			t.Fatalf("stable order violated at new slot %d: got WeaponID %d want %d", i+1, s.Records[i].WeaponID, w)
		}
	}
}

// TestCompactionMarkerRepair locks C12: three cases of [06 §5.2] including
// "sources before the first hole never enter the repair table".
func TestCompactionMarkerRepair(t *testing.T) {
	// Case A: source before first hole linking to target that moves — no repair, stale.
	t.Run("sourceBeforeFirstHoleStale", func(t *testing.T) {
		var s Service
		// Create 5 records: handles 1..5.
		for i := 0; i < 5; i++ {
			h, _ := s.Reserve()
			s.Records[int(h)-1].WeaponID = int32(i + 1)
		}
		// Index mapping: 0:h1,1:h2,2:h3,3:h4,4:h5.
		// Kill handle 2 (index1) => firstHole =1. Source at index0 (handle1) before hole links to target at index3 (handle4) which will move.
		s.Records[0].TargetProjectile = pool.Handle(4) // source before hole -> target after hole
		// Need some distinct follower links for others to avoid confusion.
		s.MarkDead(pool.Handle(2)) // hole at index1
		// Capture link before compact.
		beforeLink := s.Records[0].TargetProjectile

		s.Compact(nil)

		// After compact: oldCount 5, dead {index1}, survivors indices 0,2,3,4 -> new positions 0,1,2,3.
		// Target old handle 4 (index3) moves to new index 2 (new handle 3).
		// Source at index0 was before firstHole, never enters repair table [06 §5.2], so link stays stale at old handle 4.
		if s.Count() != 4 {
			t.Fatalf("count %d want 4", s.Count())
		}
		afterLink := s.Records[0].TargetProjectile
		if afterLink != beforeLink {
			t.Fatalf("source before first hole was repaired: before %d after %d want stale %d (no repair) [06 §5.2]", beforeLink, afterLink, beforeLink)
		}
		// The stale handle 4 now aliases a different live record (new handle 4 is old handle 5) or stale bytes — that aliasing is retail [06 §5.2].
		if afterLink != pool.Handle(4) {
			t.Fatalf("stale link should remain handle 4, got %d", afterLink)
		}
		// Verify target actually survived and moved: find live record with marker ==3.
		found := false
		for i := 0; i < s.Count(); i++ {
			if int(s.Records[i].OldMarker) == 3 {
				found = true
				if pool.Handle(i+1) != pool.Handle(3) {
					t.Fatalf("target with old marker 3 should now be handle 3, got %d", i+1)
				}
				break
			}
		}
		if !found {
			t.Fatal("target with old marker 3 not found after compact")
		}
	})

	// Case B: moved source linking to surviving moved target — repaired.
	t.Run("movedSourceToSurvivingTargetRepaired", func(t *testing.T) {
		var s Service
		for i := 0; i < 5; i++ {
			h, _ := s.Reserve()
			s.Records[int(h)-1].WeaponID = int32(i + 1)
		}
		// Kill handle 2 (index1) => firstHole 1.
		// Source at index2 (handle3) after hole, linking to target at index4 (handle5) after hole, both will move.
		s.Records[2].TargetProjectile = pool.Handle(5)
		s.MarkDead(pool.Handle(2))
		s.Compact(nil)
		if s.Count() != 4 {
			t.Fatalf("count %d want 4", s.Count())
		}
		// After compact, survivor mapping: old indices 0->0,2->1,3->2,4->3.
		// Source old index2 -> new index1, target old index4 -> new index3 (handle4).
		// Repair should rewrite source link to handle 4 [06 §5.2].
		sourceNewIdx := 1
		got := s.Records[sourceNewIdx].TargetProjectile
		want := pool.Handle(4)
		if got != want {
			t.Fatalf("moved source not repaired: got %d want %d", got, want)
		}
		// Also verify target's old marker is 4.
		if int(s.Records[3].OldMarker) != 4 {
			t.Fatalf("target marker = %d want 4", s.Records[3].OldMarker)
		}
	})

	// Case C: moved source linking to removed target — stale.
	t.Run("movedSourceToRemovedTargetStale", func(t *testing.T) {
		var s Service
		for i := 0; i < 5; i++ {
			h, _ := s.Reserve()
			s.Records[int(h)-1].WeaponID = int32(i + 1)
		}
		// Kill handle2 and handle3 (indices1,2) — target is handle3 which will be removed.
		// Source at index4 (handle5) linking to target handle3.
		s.Records[4].TargetProjectile = pool.Handle(3)
		s.MarkDead(pool.Handle(2))
		s.MarkDead(pool.Handle(3))
		s.Compact(nil)
		if s.Count() != 3 {
			t.Fatalf("count %d want 3", s.Count())
		}
		// Survivor mapping: old indices 0->0,3->1,4->2. Source old index4 -> new index2.
		// Target old index2 was dead, removed. No live record carries marker 2, so no rewrite [06 §5.2]; source retains stale handle3.
		sourceNewIdx := 2
		got := s.Records[sourceNewIdx].TargetProjectile
		want := pool.Handle(3)
		if got != want {
			t.Fatalf("removed-target link should stay stale: got %d want %d", got, want)
		}
		// Verify no live record carries marker 2.
		for i := 0; i < s.Count(); i++ {
			if int(s.Records[i].OldMarker) == 2 {
				t.Fatalf("found dead target marker 2 in live span at %d", i)
			}
		}
	})
}

// TestCombatEntrySpanCapture validates C11: projectile phase captures span count once at entry;
// clones appended during pass are NOT visited this tick [01 §6.2], [06 §5.2]. Compactor reads current count and does include them.
func TestCombatEntrySpanCapture(t *testing.T) {
	var s Service
	for i := 0; i < 3; i++ {
		h, _ := s.Reserve()
		s.Records[int(h)-1].WeaponID = int32(i + 100)
	}
	// Simulate phase entry: capture count.
	visited := []pool.Handle{}
	s.ForEachAliveInEntrySpan(func(h pool.Handle, p *Projectile) {
		visited = append(visited, h)
		// Append clones during iteration — these should be outside captured span [06 §5.2].
		if len(visited) == 2 {
			nh, ok := s.Reserve()
			if !ok {
				t.Fatalf("reserve clone during entry span failed")
			}
			s.Records[int(nh)-1].WeaponID = 999
			nh2, _ := s.Reserve()
			s.Records[int(nh2)-1].WeaponID = 1000
		}
	})
	if len(visited) != 3 {
		t.Fatalf("entry-span capture visited %d handles want 3 (clones excluded) [06 §5.2]", len(visited))
	}
	for _, h := range visited {
		if h > 3 {
			t.Fatalf("visited clone handle %d inside entry span", h)
		}
	}
	if s.Count() != 5 {
		t.Fatalf("count after appending clones = %d want 5", s.Count())
	}
	// Compaction must include clones (reads current count) [01 §6.2], [06 §5.2].
	// Mark first handle dead, compact, verify survivors include clones.
	s.MarkDead(pool.Handle(1))
	// Need to ensure we test that clones are included in compaction survivors.
	// Give clones distinct markers after their dead before compact? Already set.
	s.Compact(nil)
	if s.Count() != 4 {
		t.Fatalf("count after compact including clones = %d want 4", s.Count())
	}
	// Survivors should be original handles 2,3 plus two clones (WeaponIDs 999,1000 lost? Actually 1 was dead, so survivors are 2: WeaponID 101, 3:102, plus 999,1000)
	// Order stable: survivors originally at indices 1,2,3,4 after oldCount 5, hole at 0, slide preserves order.
	found999 := false
	found1000 := false
	for i := 0; i < s.Count(); i++ {
		if s.Records[i].WeaponID == 999 {
			found999 = true
		}
		if s.Records[i].WeaponID == 1000 {
			found1000 = true
		}
	}
	if !found999 || !found1000 {
		t.Fatalf("clones not preserved through compaction: found999=%v found1000=%v", found999, found1000)
	}
}

// TestCombatFollowCameraRepair verifies that follow handle is repaired when followed survivor moves [01 §6.1], [06 §5.2].
func TestCombatFollowCameraRepair(t *testing.T) {
	var s Service
	for i := 0; i < 5; i++ {
		h, _ := s.Reserve()
		s.Records[int(h)-1].WeaponID = int32(i)
	}
	follow := pool.Handle(5) // follow last record
	s.MarkDead(pool.Handle(2))
	s.MarkDead(pool.Handle(3))
	// Before compact: handles 1,2(dead),3(dead),4,5. Follow at 5 (index4).
	s.Compact(&follow)
	if s.Count() != 3 {
		t.Fatalf("count %d want 3", s.Count())
	}
	// Survivor mapping: old 0->0,3->1,4->2. Follow old index4 -> new index2 => handle 3.
	if follow != pool.Handle(3) {
		t.Fatalf("follow repair got %d want 3", follow)
	}
	// Follow of removed target should clear to null [01 §6.1] via pool logic.
	gone := pool.Handle(2) // dead handle 2 removed
	// Need to resurrect scenario: after previous compact, handle 2 already gone. Create new pool with follow to dead.
	var s2 Service
	for i := 0; i < 3; i++ {
		h, _ := s2.Reserve()
		s2.Records[int(h)-1].WeaponID = int32(i)
	}
	gone = pool.Handle(2)
	s2.MarkDead(gone)
	s2.Compact(&gone)
	if gone != 0 {
		t.Fatalf("follow to removed record = %d want 0", gone)
	}
	// Sources before first hole following same verification as marker repair etc are separate.
}

// TestCombatStableOrder ensures compaction preserves survivor relative order [06 §5.2].
func TestCombatStableOrder(t *testing.T) {
	var s Service
	for i := 0; i < 10; i++ {
		h, _ := s.Reserve()
		s.Records[int(h)-1].WeaponID = int32(i * 10)
	}
	// Kill every other: handles 2,4,6,8
	s.MarkDead(pool.Handle(2))
	s.MarkDead(pool.Handle(4))
	s.MarkDead(pool.Handle(6))
	s.MarkDead(pool.Handle(8))
	s.Compact(nil)
	if s.Count() != 6 {
		t.Fatalf("count %d want 6", s.Count())
	}
	last := int32(-1)
	for i := 0; i < s.Count(); i++ {
		v := s.Records[i].WeaponID
		if v <= last {
			t.Fatalf("stable order violated at new slot %d: WeaponID %d after %d", i+1, v, last)
		}
		last = v
	}
	// Verify OldMarker still holds original indices for survivors.
	// Survivors originally at indices 0,2,4,6,8,9 (handles 1,3,5,7,9,10) => markers 0,2,4,6,8,9
	wantMarkers := []int16{0, 2, 4, 6, 8, 9}
	for i, w := range wantMarkers {
		if s.Records[i].OldMarker != w {
			t.Fatalf("marker at new slot %d = %d want %d", i+1, s.Records[i].OldMarker, w)
		}
	}
}
