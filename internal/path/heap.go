package path

// heap.go implements the A* open list and node store for ground
// path search [04 §7.2].
//
// Heap key is f = g + hScaled where hScaled = (h*scale)>>16 with a
// signed 64-bit product and arithmetic shift [04 §7.2] C6. The heap
// compares strictly less so equal keys preserve insertion order [04 §7.2]
// C5. A monotonic insertion counter provides the tiebreaker.
//
// NodeStore holds per-cell nodes with write-once h [04 §7.2] C7 and
// strict less-g parent replacement [04 §7.2] C5. Relaxation adjusts f
// by the g delta alone without recomputing h [04 §7.2] C7.
//
// Capacity is unbounded Go slices; retail's heap OOM policy is
// unknown [04 §11] and route caps (20 published, 64 reconstruction
// ring) are enforced by the publisher, not the heap.

type NodeID int

const invalidNodeID NodeID = 0

// Node holds per-cell A* state.
type Node struct {
	Cell Cell
	G    int32 // cost from start
	H    int32 // write-once raw heuristic [04 §7.2] C7
	F    int32 // G + HScaled (HScaled = (H*scale)>>16)
	// TerrainTerm is the passability tier captured when the node was opened.
	// Run is the number of consecutive steps in Dir ending at this node
	// [R-PATH-01 §3]. Both are retained across relaxations.
	TerrainTerm uint16
	Run         uint16
	// Parent is the predecessor node ID (0 for start/root).
	Parent NodeID
	// Dir is the travel direction from parent (0..7) or 0xFF for root.
	Dir uint8
	// Open / Closed status [04 §7.2].
	Open   bool
	Closed bool
	// hSet records whether H has been evaluated.
	hSet bool
}

// NodeStore is the allocated-node table. Cell is the key, NodeID
// is the stable identity. Node 0 is invalid, like pool slot 0.
type NodeStore struct {
	nodes []Node // index by NodeID; nodes[0] is zero
	index map[Cell]NodeID
	scale int32 // h scale quantum for F computation (see Scale)
}

// NewNodeStore returns an empty store with the given h scale.
// Scale is the per-player quantum used as (h*scale)>>16 [04 §7.2] C6.
// Pass 65536 for unweighted (1.0) when no scheduler is present.
func NewNodeStore(scale int32) *NodeStore {
	return &NodeStore{
		nodes: make([]Node, 1), // reserve 0
		index: make(map[Cell]NodeID),
		scale: scale,
	}
}

// Scale returns the current h scale.
func (ns *NodeStore) Scale() int32 { return ns.scale }

// SetScale updates the h scale for future allocations. Existing nodes
// keep their already-computed F (h is write-once) [04 §7.2] C7.
func (ns *NodeStore) SetScale(scale int32) { ns.scale = scale }

// Reset clears all nodes but retains the scale.
func (ns *NodeStore) Reset() {
	ns.nodes = ns.nodes[:1]
	// Replacing the index avoids map iteration in a simulation-visible reset
	// while retaining the stable node identity contract [04 §7.2].
	ns.index = make(map[Cell]NodeID)
}

// Len returns the number of allocated nodes (excluding invalid 0).
func (ns *NodeStore) Len() int { return len(ns.nodes) - 1 }

// Find locates the NodeID for a cell.
func (ns *NodeStore) Find(cell Cell) (NodeID, bool) {
	id, ok := ns.index[cell]
	return id, ok
}

// Get returns the node for id. Caller must ensure id is valid.
func (ns *NodeStore) Get(id NodeID) *Node {
	return &ns.nodes[id]
}

// hScaled computes (h*scale)>>16 with signed 64 product and
// arithmetic shift, no floating point [04 §7.2] C6.
func hScaled(h, scale int32) int32 {
	return int32((int64(h) * int64(scale)) >> 16)
}

// Ensure returns the node for cell, allocating if absent. On first
// allocation H is evaluated via goal.H(cell) and stored write-once;
// F is set to g + hScaled [04 §7.2] C7. If the cell already exists
// the stored H and F (minus any later g relaxation) are left intact
// even if goal now returns a different h — the caller can mutate
// goal between calls to verify the write-once contract.
func (ns *NodeStore) Ensure(cell Cell, g int32, parent NodeID, dir uint8, goal Goal) NodeID {
	if id, ok := ns.index[cell]; ok {
		return id
	}
	var h int32
	if goal != nil {
		h = goal.H(cell)
	}
	hs := hScaled(h, ns.scale)
	f := g + hs
	id := NodeID(len(ns.nodes))
	ns.nodes = append(ns.nodes, Node{
		Cell:   cell,
		G:      g,
		H:      h,
		F:      f,
		Parent: parent,
		Dir:    dir,
		hSet:   true,
	})
	ns.index[cell] = id
	return id
}

// Alloc is a lower-level allocation that takes a raw h directly.
// H is stored write-once; if cell already exists the existing node
// is returned without modifying H, G, F, or parent. Use TryRelax
// to update G/F.
func (ns *NodeStore) Alloc(cell Cell, g, h int32, parent NodeID, dir uint8) NodeID {
	if id, ok := ns.index[cell]; ok {
		return id
	}
	hs := hScaled(h, ns.scale)
	f := g + hs
	id := NodeID(len(ns.nodes))
	ns.nodes = append(ns.nodes, Node{
		Cell:   cell,
		G:      g,
		H:      h,
		F:      f,
		Parent: parent,
		Dir:    dir,
		hSet:   true,
	})
	ns.index[cell] = id
	return id
}

// TryRelax attempts to improve the path to id via newG/newParent.
// It replaces only when newG < old G (strictly less); equal g does
// not replace the parent [04 §7.2] C5. On success F is adjusted by
// the g delta alone (h is not recomputed) [04 §7.2] C7 and the
// parent/dir are updated. Returns true if the node was updated.
func (ns *NodeStore) TryRelax(id NodeID, newG int32, newParent NodeID, newDir uint8) bool {
	n := &ns.nodes[id]
	if newG >= n.G {
		return false
	}
	delta := newG - n.G
	n.G = newG
	n.F += delta
	n.Parent = newParent
	n.Dir = newDir
	return true
}

// SetOpen marks the node's open status.
func (ns *NodeStore) SetOpen(id NodeID, open bool) { ns.nodes[id].Open = open }

// SetClosed marks the node's closed status.
func (ns *NodeStore) SetClosed(id NodeID, closed bool) { ns.nodes[id].Closed = closed }

// IsOpen reports whether the node is on the open list.
func (ns *NodeStore) IsOpen(id NodeID) bool { return ns.nodes[id].Open }

// IsClosed reports whether the node is closed.
func (ns *NodeStore) IsClosed(id NodeID) bool { return ns.nodes[id].Closed }

// heapEntry is a heap element keyed on F with insertion-order tiebreaker.
type heapEntry struct {
	id  NodeID
	f   int32
	seq uint64
}

// Heap is a binary min-heap keyed on f = g + hScaled with strict-less
// ordering so equal keys preserve insertion order [04 §7.2] C5. The
// monotonic seq provides the tiebreaker.
type Heap struct {
	entries []heapEntry
	nextSeq uint64
}

// Len returns the number of entries.
func (h *Heap) Len() int { return len(h.entries) }

// IsEmpty reports whether the heap is empty.
func (h *Heap) IsEmpty() bool { return len(h.entries) == 0 }

// Clear removes all entries and resets the sequence counter.
func (h *Heap) Clear() {
	h.entries = h.entries[:0]
	h.nextSeq = 0
}

// Push inserts id with key f. Equal f values keep insertion order
// via the monotonic seq [04 §7.2] C5. Duplicates are allowed; the
// caller may use lazy invalidation (push a second entry for the same
// node and skip stale pops) or decrease-key via Fix.
func (h *Heap) Push(id NodeID, f int32) {
	e := heapEntry{id: id, f: f, seq: h.nextSeq}
	h.nextSeq++
	h.entries = append(h.entries, e)
	h.up(len(h.entries) - 1)
}

// Pop removes and returns the smallest entry. If the heap is empty
// ok is false.
func (h *Heap) Pop() (NodeID, int32, bool) {
	if len(h.entries) == 0 {
		return 0, 0, false
	}
	top := h.entries[0]
	last := len(h.entries) - 1
	h.entries[0] = h.entries[last]
	h.entries = h.entries[:last]
	if len(h.entries) > 0 {
		h.down(0)
	}
	return top.id, top.f, true
}

// Peek returns the smallest entry without removing it.
func (h *Heap) Peek() (NodeID, int32, bool) {
	if len(h.entries) == 0 {
		return 0, 0, false
	}
	e := h.entries[0]
	return e.id, e.f, true
}

// Fix updates the key for an existing entry and restores heap order.
// It implements decrease-key (or increase-key) without inserting a
// duplicate. The entry's seq is preserved so equal-key insertion
// order remains that of the original insertion [04 §7.2] C5.
// If the id is not found, Fix returns false.
func (h *Heap) Fix(id NodeID, newF int32) bool {
	for i, e := range h.entries {
		if e.id == id {
			oldF := e.f
			h.entries[i].f = newF
			if newF < oldF {
				h.up(i)
			} else if newF > oldF {
				h.down(i)
			}
			return true
		}
	}
	return false
}

// Contains reports whether id is present in the heap (linear scan).
func (h *Heap) Contains(id NodeID) bool {
	for _, e := range h.entries {
		if e.id == id {
			return true
		}
	}
	return false
}

func (h *Heap) less(i, j int) bool {
	a := h.entries[i]
	b := h.entries[j]
	if a.f != b.f {
		return a.f < b.f // strict less [04 §7.2] C5
	}
	return a.seq < b.seq // insertion-order tiebreaker
}

func (h *Heap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		h.entries[i], h.entries[p] = h.entries[p], h.entries[i]
		i = p
	}
}

func (h *Heap) down(i int) {
	n := len(h.entries)
	for {
		l := 2*i + 1
		r := l + 1
		smallest := i
		if l < n && h.less(l, smallest) {
			smallest = l
		}
		if r < n && h.less(r, smallest) {
			smallest = r
		}
		if smallest == i {
			break
		}
		h.entries[i], h.entries[smallest] = h.entries[smallest], h.entries[i]
		i = smallest
	}
}
