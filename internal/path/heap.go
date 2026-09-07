package path

// heap.go implements the A* open list and node store for ground
// path search [04 §7.2].
//
// Heap key is f = g + hScaled where hScaled = (h*scale)>>16 with a
// signed 64-bit product and arithmetic shift [04 §7.2] C6. The heap
// compares signed f values only. Equal keys have no insertion-order
// tiebreaker; the left child wins an equal-child sift-down [04 R-PATH-01 §1].
//
// NodeStore holds per-cell nodes with write-once h [04 §7.2] C7 and
// strict less-g parent replacement [04 §7.2] C5. Relaxation adjusts f
// by the g delta alone without recomputing h [04 §7.2] C7.
//
// Capacity is unbounded Go slices; retail's heap OOM policy is
// unknown [04 §11] and route caps (20 published, 64 reconstruction
// ring) are enforced by the publisher, not the heap.

// NodeID indexes a node in the store. Zero is the reserved null identity, so a
// real node is always 1 or higher.
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

// heapEntry is a heap element keyed on F.
type heapEntry struct {
	id NodeID
	f  int32
}

// Heap is the binary open heap. An expansion marks its root spent but leaves
// it in place until neighbour work decides whether to reuse or discard it
// [04 R-PATH-01 §1]. positions lets a lowering relaxation find and remove a
// displaced spent root without changing the node store's stable identities.
type Heap struct {
	entries   []heapEntry
	positions map[NodeID]int
	spent     NodeID
}

// Len returns the number of entries.
func (h *Heap) Len() int { return len(h.entries) }

// IsEmpty reports whether the heap is empty.
func (h *Heap) IsEmpty() bool { return len(h.entries) == 0 }

// HasCandidate reports whether an unspent node remains available to expand.
func (h *Heap) HasCandidate() bool {
	return len(h.entries) != 0 && !(h.spent != invalidNodeID && len(h.entries) == 1)
}

// Clear removes all entries and transaction state.
func (h *Heap) Clear() {
	h.entries = h.entries[:0]
	clear(h.positions)
	h.spent = invalidNodeID
}

// Push inserts a new open node with key f. Every open node has one heap entry;
// strictly improving paths use Fix rather than a stale duplicate.
func (h *Heap) Push(id NodeID, f int32) {
	h.ensurePositions()
	h.entries = append(h.entries, heapEntry{id: id, f: f})
	index := len(h.entries) - 1
	h.positions[id] = index
	h.up(index)
}

// Pop removes and returns the smallest entry. If the heap is empty
// ok is false.
func (h *Heap) Pop() (NodeID, int32, bool) {
	if h.spent != invalidNodeID {
		h.removeSpent()
	}
	if len(h.entries) == 0 {
		return 0, 0, false
	}
	top := h.entries[0]
	h.removeAtDown(0)
	return top.id, top.f, true
}

// BeginExpand selects the current root but deliberately retains it in the
// heap. The next newly opened neighbour can replace it in place; otherwise it
// is removed before the following selection [04 R-PATH-01 §1].
func (h *Heap) BeginExpand() (NodeID, int32, bool) {
	if h.spent != invalidNodeID {
		h.removeSpent()
	}
	if len(h.entries) == 0 {
		return invalidNodeID, 0, false
	}
	top := h.entries[0]
	h.spent = top.id
	return top.id, top.f, true
}

// Open records one newly opened neighbour. The first after BeginExpand
// replaces the spent root and sifts down; later ones append and sift up.
func (h *Heap) Open(id NodeID, f int32) {
	if h.spent == invalidNodeID {
		h.Push(id, f)
		return
	}
	h.ensurePositions()
	delete(h.positions, h.spent)
	h.entries[0] = heapEntry{id: id, f: f}
	h.positions[id] = 0
	h.spent = invalidNodeID
	h.down(0)
}

// Peek returns the smallest entry without removing it.
func (h *Heap) Peek() (NodeID, int32, bool) {
	if len(h.entries) == 0 {
		return 0, 0, false
	}
	e := h.entries[0]
	return e.id, e.f, true
}

// Fix records F for a relaxation that NodeStore already accepted on strictly
// lower G. It always writes the supplied F and sifts up, including a wrapped
// delta that makes F higher. If that sifting displaces the spent root, it
// removes that root from its new position using retail's last-entry
// replacement and sift-down transaction [04 R-PATH-01 §1]. If the id is not
// open, Fix returns false.
func (h *Heap) Fix(id NodeID, newF int32) bool {
	index, ok := h.positions[id]
	if !ok {
		return false
	}
	h.entries[index].f = newF
	h.up(index)
	if h.spent != invalidNodeID && h.positions[h.spent] != 0 {
		h.removeSpent()
	}
	return true
}

// Contains reports whether id is present in the heap.
func (h *Heap) Contains(id NodeID) bool {
	_, ok := h.positions[id]
	return ok
}

func (h *Heap) less(i, j int) bool {
	return h.entries[i].f < h.entries[j].f
}

func (h *Heap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap) down(i int) {
	n := len(h.entries)
	for {
		l := 2*i + 1
		r := l + 1
		if l >= n {
			break
		}
		child := l
		// A right child wins only when strictly smaller; equality selects left.
		if r < n && h.entries[r].f < h.entries[l].f {
			child = r
		}
		if h.entries[i].f <= h.entries[child].f {
			break
		}
		h.swap(i, child)
		i = child
	}
}

func (h *Heap) ensurePositions() {
	if h.positions == nil {
		h.positions = make(map[NodeID]int)
	}
}

func (h *Heap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.positions[h.entries[i].id] = i
	h.positions[h.entries[j].id] = j
}

func (h *Heap) removeSpent() {
	spent := h.spent
	h.spent = invalidNodeID
	index, ok := h.positions[spent]
	if ok {
		h.removeAtDown(index)
	}
}

// removeAtDown removes one element by filling its slot from the heap tail and
// sifting that replacement down. The spent-root transaction specifies down
// even when the removed element was displaced below the root.
func (h *Heap) removeAtDown(index int) {
	last := len(h.entries) - 1
	removed := h.entries[index]
	delete(h.positions, removed.id)
	if index == last {
		h.entries = h.entries[:last]
		return
	}
	replacement := h.entries[last]
	h.entries[index] = replacement
	h.entries = h.entries[:last]
	h.positions[replacement.id] = index
	h.down(index)
}
