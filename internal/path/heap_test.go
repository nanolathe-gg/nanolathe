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

func TestHeapExpansionTransactionExamples(t *testing.T) {
	tests := []struct {
		name    string
		initial []heapEntry
		work    func(*Heap)
		want    []NodeID
		next    NodeID
	}{
		{
			name:    "spent root removal does not preserve equal insertion order",
			initial: []heapEntry{{1, 1}, {2, 2}, {3, 2}, {4, 2}},
			work:    func(*Heap) {},
			want:    []NodeID{4, 2, 3},
			next:    4,
		},
		{
			name:    "first newly opened neighbour replaces the spent root",
			initial: []heapEntry{{1, 1}, {2, 2}, {3, 2}, {4, 2}},
			work:    func(h *Heap) { h.Open(5, 2) },
			want:    []NodeID{5, 2, 3, 4},
			next:    5,
		},
		{
			name:    "sift down selects the left equal child",
			initial: []heapEntry{{1, 1}, {2, 2}, {3, 2}, {4, 3}},
			work:    func(*Heap) {},
			want:    []NodeID{2, 4, 3},
			next:    2,
		},
		{
			name:    "relaxation displaces and removes the spent root",
			initial: []heapEntry{{1, 5}, {2, 10}, {3, 11}, {4, 12}},
			work: func(h *Heap) {
				if !h.Fix(4, 4) {
					t.Fatal("relaxation node missing")
				}
				h.Open(5, 4)
			},
			want: []NodeID{4, 5, 3, 2},
			next: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := heapFrom(test.initial)
			if id, _, ok := h.BeginExpand(); !ok || id != 1 {
				t.Fatalf("first expansion = %d, %v; want root 1", id, ok)
			}
			test.work(&h)
			if id, _, ok := h.BeginExpand(); !ok || id != test.next {
				t.Fatalf("next expansion = %d, %v; want %d", id, ok, test.next)
			}
			got := heapIDs(h)
			if len(got) != len(test.want) {
				t.Fatalf("heap len = %d, want %d: %v", len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("heap[%d] = %d, want %d; heap %v", i, got[i], test.want[i], got)
				}
			}
		})
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

func TestHeapFixKeepsWrappedRelaxationCurrent(t *testing.T) {
	ns := NewNodeStore(65536)
	// Reducing G across the signed range wraps its delta. Retail accepts the
	// lower G, stores the resulting higher F, then sifts up without a second
	// F comparison [04 R-PATH-01 §1].
	n := ns.Alloc(Cell{0, 0}, 100, -200, 0, DirNone)
	other := ns.Alloc(Cell{1, 0}, 0, 0, 0, DirNone)
	var h Heap
	h.Push(n, ns.Get(n).F)
	h.Push(other, ns.Get(other).F)
	if !ns.TryRelax(n, int32(-1<<31), 0, DirNone) {
		t.Fatal("lower wrapped G must relax")
	}
	if !h.Fix(n, ns.Get(n).F) {
		t.Fatal("relaxed node must remain in the heap")
	}
	if ns.Get(n).F != 2147483448 {
		t.Fatalf("wrapped F = %d, want 2147483448", ns.Get(n).F)
	}
	id, f, ok := h.Peek()
	if !ok || id != n || f != ns.Get(n).F {
		t.Fatalf("heap entry = (%d, %d, %v), want current node F %d", id, f, ok, ns.Get(n).F)
	}
	if id, f, ok = h.Pop(); !ok || id != n || f != ns.Get(n).F {
		t.Fatalf("pop = (%d, %d, %v), want current node F %d", id, f, ok, ns.Get(n).F)
	}
}

func TestHeapPopOrderHandBuiltGraph(t *testing.T) {
	// Hand-built small graph using NodeStore + Heap to simulate A* open-list ordering.
	// Nodes: start S at (0,0) G=0 H=30 F=30, neighbors A(1,0) G16 F46, B(0,1) G16 F26, C(1,1) diagonal G22 F42 etc.
	// We push in arbitrary order and verify pop returns ascending F.
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

func heapFrom(entries []heapEntry) Heap {
	h := Heap{entries: append([]heapEntry(nil), entries...)}
	for i, entry := range h.entries {
		h.setPosition(entry.id, i)
	}
	return h
}

func heapIDs(h Heap) []NodeID {
	ids := make([]NodeID, len(h.entries))
	for i, entry := range h.entries {
		ids[i] = entry.id
	}
	return ids
}
