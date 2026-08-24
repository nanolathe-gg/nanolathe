package path

import "testing"

// mutableGoal is a test Goal whose H value can be changed after
// allocation to verify the write-once contract.
type mutableGoal struct {
	h int32
}

func (m *mutableGoal) Enumerate(out []Cell) []Cell    { return out }
func (m *mutableGoal) StartSatisfied(start Cell) bool { return false }
func (m *mutableGoal) H(c Cell) int32                 { return m.h }

func TestHeapStrictLessInsertionOrder(t *testing.T) {
	var h Heap
	// Push three entries with equal f but different insertion order.
	h.Push(1, 100)
	h.Push(2, 100)
	h.Push(3, 100)
	// Pop must preserve insertion order because heap compares strictly less [04 §7.2] C5.
	for want := NodeID(1); want <= 3; want++ {
		id, _, ok := h.Pop()
		if !ok {
			t.Fatalf("pop want %d, heap empty", want)
		}
		if id != want {
			t.Fatalf("pop order: want %d got %d, equal keys must preserve insertion order", want, id)
		}
	}
	if !h.IsEmpty() {
		t.Fatalf("heap should be empty")
	}
}

func TestHeapInsertionOrderWithMixedKeys(t *testing.T) {
	var h Heap
	// Mix equal and distinct keys to ensure seq tiebreaker does not disturb f ordering.
	h.Push(10, 50)  // seq 0
	h.Push(20, 100) // seq 1
	h.Push(30, 50)  // seq 2 — same f as 10, should come after 10 but before 100
	h.Push(40, 50)  // seq 3 — same f, after 30
	id, _, _ := h.Pop()
	if id != 10 {
		t.Fatalf("first pop want 10 got %d", id)
	}
	id, _, _ = h.Pop()
	if id != 30 {
		t.Fatalf("second pop want 30 got %d", id)
	}
	id, _, _ = h.Pop()
	if id != 40 {
		t.Fatalf("third pop want 40 got %d", id)
	}
	id, _, _ = h.Pop()
	if id != 20 {
		t.Fatalf("fourth pop want 20 got %d", id)
	}
}

func TestNodeStoreEqualGNonReplacement(t *testing.T) {
	ns := NewNodeStore(65536)
	a := ns.Alloc(Cell{0, 0}, 0, 0, 0, 0xFF)
	b := ns.Alloc(Cell{1, 0}, 16, 10, a, 2)
	// Try to relax b with equal g — must not replace parent [04 §7.2] C5.
	origParent := ns.Get(b).Parent
	origG := ns.Get(b).G
	origF := ns.Get(b).F
	c := ns.Alloc(Cell{0, 1}, 16, 5, a, 4)
	if !ns.TryRelax(b, 16, c, 6) {
		// TryRelax correctly returns false for equal g
	} else {
		t.Fatalf("equal g must not replace parent")
	}
	if ns.Get(b).Parent != origParent {
		t.Fatalf("equal g replaced parent: want %d got %d", origParent, ns.Get(b).Parent)
	}
	if ns.Get(b).G != origG || ns.Get(b).F != origF {
		t.Fatalf("equal g changed G/F")
	}
	// Strictly smaller g must replace.
	if !ns.TryRelax(b, 15, c, 6) {
		t.Fatalf("strictly smaller g should replace")
	}
	if ns.Get(b).Parent != c {
		t.Fatalf("smaller g did not replace parent")
	}
	if ns.Get(b).G != 15 {
		t.Fatalf("smaller g did not update G")
	}
	// F must be adjusted by delta only (h not recomputed) [04 §7.2] C7.
	// Original F = G(16)+hScaled(10)=26, new F should be 25 (delta -1)
	if ns.Get(b).F != origF-1 {
		t.Fatalf("F not adjusted by g delta: want %d got %d", origF-1, ns.Get(b).F)
	}
}

func TestNodeStoreHEvaluatedOnce(t *testing.T) {
	ns := NewNodeStore(65536)
	g := &mutableGoal{h: 50}
	cell := Cell{5, 7}
	id := ns.Ensure(cell, 0, 0, 0xFF, g)
	firstH := ns.Get(id).H
	firstF := ns.Get(id).F
	// Mutate goal to return different h; Ensure on same cell must not recompute.
	g.h = 999
	id2 := ns.Ensure(cell, 0, 0, 0xFF, g)
	if id != id2 {
		t.Fatalf("Ensure on same cell should return same ID")
	}
	if ns.Get(id2).H != firstH {
		t.Fatalf("h evaluated more than once: first %d second %d", firstH, ns.Get(id2).H)
	}
	if ns.Get(id2).F != firstF {
		t.Fatalf("F changed after goal mutation: first %d second %d", firstF, ns.Get(id2).F)
	}
	// Alloc path also write-once.
	ns2 := NewNodeStore(65536)
	id3 := ns2.Alloc(Cell{1, 1}, 10, 20, 0, 0)
	g.h = 1
	id4 := ns2.Alloc(Cell{1, 1}, 99, 1, 0, 0)
	if id3 != id4 {
		t.Fatalf("Alloc on same cell should return same ID")
	}
	if ns2.Get(id4).H != 20 {
		t.Fatalf("Alloc H not write-once: want 20 got %d", ns2.Get(id4).H)
	}
	if ns2.Get(id4).G != 10 {
		t.Fatalf("Alloc G should remain first allocation")
	}
}

func TestNodeStoreRelaxAdjustsFByDeltaOnly(t *testing.T) {
	ns := NewNodeStore(65536)
	// F = G + hScaled, h=100, scale=65536 => hScaled=100, F= G+100
	a := ns.Alloc(Cell{0, 0}, 0, 100, 0, 0xFF)
	if ns.Get(a).F != 100 {
		t.Fatalf("initial F want 100 got %d", ns.Get(a).F)
	}
	b := ns.Alloc(Cell{1, 0}, 16, 50, a, 0)
	// b: G=16, H=50 => F=66
	if ns.Get(b).F != 66 {
		t.Fatalf("initial F want 66 got %d", ns.Get(b).F)
	}
	// Relax with strictly smaller G=10, new F should be 60 (delta -6), H still 50
	if !ns.TryRelax(b, 10, a, 1) {
		t.Fatalf("relax should succeed")
	}
	if ns.Get(b).H != 50 {
		t.Fatalf("relax must not change H")
	}
	if ns.Get(b).F != 60 {
		t.Fatalf("relax F want 60 got %d", ns.Get(b).F)
	}
	if ns.Get(b).G != 10 {
		t.Fatalf("relax G want 10 got %d", ns.Get(b).G)
	}
}

func TestHeapPopOrderHandBuiltGraph(t *testing.T) {
	// Hand-built small graph using NodeStore + Heap to simulate A* open list ordering.
	// Nodes: start S at (0,0) G=0 H=30 F=30, neighbors A(1,0) G16 F46, B(0,1) G16 F26, C(1,1) diagonal G22 F42 etc.
	// We push in arbitrary order and verify pop returns ascending F with insertion-order tiebreak.
	ns := NewNodeStore(65536)
	var h Heap
	s := ns.Alloc(Cell{0, 0}, 0, 30, 0, 0xFF)
	h.Push(s, ns.Get(s).F)
	a := ns.Alloc(Cell{1, 0}, 16, 30, s, 0) // F 46
	b := ns.Alloc(Cell{0, 1}, 16, 10, s, 2) // F 26
	c := ns.Alloc(Cell{1, 1}, 22, 20, s, 1) // F 42
	// Push in non-sorted order: a,b,c
	h.Push(a, ns.Get(a).F)
	h.Push(b, ns.Get(b).F)
	h.Push(c, ns.Get(c).F)
	// Expected pop order by F: b(26), s(30), c(42), a(46)
	want := []NodeID{b, s, c, a}
	for i, w := range want {
		id, f, ok := h.Pop()
		if !ok {
			t.Fatalf("pop %d: heap empty", i)
		}
		if id != w {
			t.Fatalf("pop %d: want node %d (f want %d) got %d (f %d)", i, w, ns.Get(w).F, id, f)
		}
	}
}

func TestHeapFixPreservesInsertionOrder(t *testing.T) {
	var h Heap
	h.Push(1, 100) // seq 0
	h.Push(2, 90)  // seq 1 -> pops first
	h.Push(3, 100) // seq 2
	// Decrease key of 3 to 90: now 2 and 3 both have f=90.
	// 2 has seq 1, 3 has seq 2, so 2 should still pop before 3 despite fix.
	if !h.Fix(3, 90) {
		t.Fatalf("Fix should find node 3")
	}
	id, _, _ := h.Pop()
	if id != 2 {
		t.Fatalf("after Fix, first pop want 2 got %d", id)
	}
	id, _, _ = h.Pop()
	if id != 1 && id != 3 {
		t.Fatalf("second pop unexpected %d", id)
	}
	// The remaining 90 entry (id 3) must come before the 100 entry (id 1) because 90<100.
	// So second pop should be 3, third 1.
	if id != 3 {
		t.Fatalf("second pop after decrease should be 3 (seq 2) before 1 (f 100), got %d", id)
	}
	id, _, _ = h.Pop()
	if id != 1 {
		t.Fatalf("third pop want 1 got %d", id)
	}
}

func TestHeapLazyInvalidationSkipsStale(t *testing.T) {
	// Verify that a heap that allows duplicates can be used with
	// lazy invalidation: push new entry for same node with smaller f,
	// pop returns smaller f first; caller skips stale by checking current F.
	ns := NewNodeStore(65536)
	var h Heap
	n := ns.Alloc(Cell{2, 2}, 100, 0, 0, 0) // F 100
	h.Push(n, ns.Get(n).F)
	// Simulate relaxation that lowers G to 50 => F 50, lazily push duplicate.
	ns.TryRelax(n, 50, 0, 0)
	h.Push(n, ns.Get(n).F) // duplicate with smaller f and later seq
	// Pop should yield the 50 entry first (smaller f), even though seq is later.
	id, f, _ := h.Pop()
	if id != n || f != 50 {
		t.Fatalf("lazy: first pop want n with f 50 got %d f %d", id, f)
	}
	// Next pop is stale (100) — a real search would skip it by comparing with ns.Get.
	id2, f2, _ := h.Pop()
	if id2 != n || f2 != 100 {
		t.Fatalf("lazy: second pop want stale 100 got %d f %d", id2, f2)
	}
	if ns.Get(n).F != 50 {
		t.Fatalf("node current F should still be 50")
	}
	// Demonstrate caller-side staleness check.
	if f2 == ns.Get(n).F {
		t.Fatalf("stale entry incorrectly matches current F")
	}
}
