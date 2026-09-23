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
	// index is the per-cell table. A session store binds the SESSION's table
	// here rather than keeping one of its own: that table already carries
	// status, direction and node identity, so a second per-cell table would
	// duplicate every touched coordinate.
	index *cellIndex
	scale int32 // h scale quantum for F computation (see Scale)
}

// newSessionNodeStore binds node lookup to the search's existing per-cell
// entry table. The table already carries status, direction and node identity,
// so a second Cell-keyed index would duplicate every touched coordinate.
//
// nodes is reused storage; its contents are discarded and only its capacity is
// kept. A nil slice allocates.
func newSessionNodeStore(scale int32, index *cellIndex, nodes []Node) *NodeStore {
	return &NodeStore{
		nodes: append(nodes[:0], Node{}), // reserve 0
		index: index,
		scale: scale,
	}
}

// Scale returns the current h scale.
func (ns *NodeStore) Scale() int32 { return ns.scale }

// SetScale updates the h scale for future allocations. Existing nodes
// keep their already-computed F (h is write-once) [04 §7.2] C7.
func (ns *NodeStore) SetScale(scale int32) { ns.scale = scale }

// Pooling this store across searches did not pay and was reverted: allocated
// bytes and objects per tick did not move, and the median tick was 2% slower.
// Go's current map implementation releases a large map's storage on clear, so
// the pooled per-cell table re-grew regardless, and the node array's growth is
// dominated by searches that expand more nodes than the previous search on the
// same unit. All the reuse added was the clear.
//
// The successor that note named — a dense generation-stamped table indexed by
// cell rather than a map — is built and landed (workspace.go). It answers the
// two things the design turned on:
//
// Ownership. The table is not a bare generation counter shared by whoever
// asks. The Scheduler owns exactly one, lends it to the search it has admitted
// and refuses it to every other, so a second search cannot read the first's
// slots — it keeps its own map and produces the same route. The generation is
// only what makes the hand-over O(1) instead of O(table).
//
// Size against locality. Sized to a whole map the table is a few hundred
// thousand slots while a search touches a few hundred cells scattered across
// it, so the trade had to be measured rather than argued. Measured on the
// benchmark scene it comes out ahead on every figure: median tick -3%, p95 -4%,
// p99 -22%, and allocated bytes per tick -37%.
//
// One part of the same idea landed earlier and is not open either: the OPEN
// SET's node-to-slot index, which was a map[NodeID]int and is now a dense row
// (see Heap). Node identities are dense by construction, so that one needed no
// generation stamp and no owner.

// Reset clears all nodes but retains the scale.
func (ns *NodeStore) Reset() {
	ns.nodes = ns.nodes[:1]
	// Emptying the table rather than iterating it keeps a simulation-visible
	// reset free of map iteration while retaining the stable node identity
	// contract [04 §7.2]. For a session this is also the status table, and it
	// is the SAME table object the Session reads, so lookup and status cannot
	// continue against different storage after a lifecycle reset.
	ns.index.reset()
}

// Len returns the number of allocated nodes (excluding invalid 0).
func (ns *NodeStore) Len() int { return len(ns.nodes) - 1 }

// Find locates the NodeID for a cell.
func (ns *NodeStore) Find(cell Cell) (NodeID, bool) {
	e := ns.index.get(cell)
	if e.id() == invalidNodeID {
		return invalidNodeID, false
	}
	return e.id(), true
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
	if e := ns.index.get(cell); e.id() != invalidNodeID {
		return e.id()
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
	e := ns.index.get(cell)
	e.node = heapID(id)
	ns.index.set(cell, e)
	return id
}

// Alloc is a lower-level allocation that takes a raw h directly.
// H is stored write-once; if cell already exists the existing node
// is returned without modifying H, G, F, or parent. Use TryRelax
// to update G/F.
func (ns *NodeStore) Alloc(cell Cell, g, h int32, parent NodeID, dir uint8) NodeID {
	if e := ns.index.get(cell); e.id() != invalidNodeID {
		return e.id()
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
	e := ns.index.get(cell)
	e.node = heapID(id)
	ns.index.set(cell, e)
	return id
}

// allocFresh opens a node for a cell the caller has just read as having none,
// with its run and terrain term, open. It is Alloc for the expansion loop,
// which already holds the cell's entry and writes it once, complete, after
// the allocation: Alloc's own lookup and its identity-only write are the two
// table accesses that final write supersedes, so the stored entry is the same
// [04 §7.2] C7.
func (ns *NodeStore) allocFresh(cell Cell, g, h int32, parent NodeID, dir uint8, run, terrain uint16) NodeID {
	id := NodeID(len(ns.nodes))
	ns.nodes = append(ns.nodes, Node{
		Cell:        cell,
		G:           g,
		H:           h,
		F:           g + hScaled(h, ns.scale),
		TerrainTerm: terrain,
		Run:         run,
		Parent:      parent,
		Dir:         dir,
		Open:        true,
		hSet:        true,
	})
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
	id heapID
	f  int32
}

// heapID is a node identity as the open set stores it. Node identities are
// dense and a search allocates far fewer than 2^31, so four bytes hold every
// one; an eight-byte entry keeps twice the heap in each cache line.
type heapID = int32

// Heap is the binary open heap. An expansion marks its root spent but leaves
// it in place until neighbour work decides whether to reuse or discard it
// [04 R-PATH-01 §1]. positions lets a lowering relaxation find and remove a
// displaced spent root without changing the node store's stable identities.
type Heap struct {
	entries []heapEntry
	// positions is the open set's node-to-slot index, dense and addressed by
	// NodeID because the node store hands out identities 1, 2, 3, ... with no
	// gaps. A slot holds the entry's index in entries, or absent to mean the
	// node is not in the heap. It replaced a map[NodeID]int, which hashed a
	// key on every swap, push and removal; nothing about the heap's shape,
	// its comparisons or its equal-key tie-breaking depends on the index
	// structure, so the pop order is the same.
	//
	// position() returns exactly what a read of that map returned: the index
	// and whether the node is present, with a zero index for an absent node.
	// Fix relies on that zero — a spent root that is not in the heap must not
	// be removed — so the two results are kept together rather than collapsed
	// into a sentinel comparison at the call sites.
	positions []int32
	spent     NodeID
}

// absentPosition marks a node that is not in the heap.
const absentPosition int32 = -1

// position is the index lookup, with the absent case reported as the zero
// index and false, the way the map read it replaced did.
func (h *Heap) position(id NodeID) (int, bool) {
	if id < 0 || int(id) >= len(h.positions) {
		return 0, false
	}
	v := h.positions[id]
	if v == absentPosition {
		return 0, false
	}
	return int(v), true
}

// setPosition files a node at an index, growing the dense row to reach it.
func (h *Heap) setPosition(id NodeID, index int) {
	if id < 0 {
		return
	}
	for int(id) >= len(h.positions) {
		h.positions = append(h.positions, absentPosition)
	}
	h.positions[id] = int32(index)
}

// clearPosition removes a node from the index.
func (h *Heap) clearPosition(id NodeID) {
	if id < 0 || int(id) >= len(h.positions) {
		return
	}
	h.positions[id] = absentPosition
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
	h.positions = h.positions[:0]
	h.spent = invalidNodeID
}

// Push inserts a new open node with key f. Every open node has one heap entry;
// strictly improving paths use Fix rather than a stale duplicate.
func (h *Heap) Push(id NodeID, f int32) {
	h.entries = append(h.entries, heapEntry{id: heapID(id), f: f})
	index := len(h.entries) - 1
	h.setPosition(id, index)
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
	return NodeID(top.id), top.f, true
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
	h.spent = NodeID(top.id)
	return NodeID(top.id), top.f, true
}

// Open records one newly opened neighbour. The first after BeginExpand
// replaces the spent root and sifts down; later ones append and sift up.
func (h *Heap) Open(id NodeID, f int32) {
	if h.spent == invalidNodeID {
		h.Push(id, f)
		return
	}
	h.clearPosition(h.spent)
	h.entries[0] = heapEntry{id: heapID(id), f: f}
	h.setPosition(id, 0)
	h.spent = invalidNodeID
	h.down(0)
}

// Peek returns the smallest entry without removing it.
func (h *Heap) Peek() (NodeID, int32, bool) {
	if len(h.entries) == 0 {
		return 0, 0, false
	}
	e := h.entries[0]
	return NodeID(e.id), e.f, true
}

// Fix records F for a relaxation that NodeStore already accepted on strictly
// lower G. It always writes the supplied F and sifts up, including a wrapped
// delta that makes F higher. If that sifting displaces the spent root, it
// removes that root from its new position using retail's last-entry
// replacement and sift-down transaction [04 R-PATH-01 §1]. If the id is not
// open, Fix returns false.
func (h *Heap) Fix(id NodeID, newF int32) bool {
	index, ok := h.position(id)
	if !ok {
		return false
	}
	h.entries[index].f = newF
	h.up(index)
	if spentIndex, _ := h.position(h.spent); h.spent != invalidNodeID && spentIndex != 0 {
		h.removeSpent()
	}
	return true
}

// Contains reports whether id is present in the heap.
func (h *Heap) Contains(id NodeID) bool {
	_, ok := h.position(id)
	return ok
}

// up and down move the sifted entry through a hole rather than by pairwise
// swaps: each level copies one entry instead of exchanging two and rewriting
// both positions, and the entry is written once where it stops. The
// comparisons, and so the final arrangement, are exactly the swap form's.
func (h *Heap) up(i int) {
	x := h.entries[i]
	for i > 0 {
		p := (i - 1) / 2
		if !(x.f < h.entries[p].f) {
			break
		}
		h.entries[i] = h.entries[p]
		h.positions[h.entries[i].id] = int32(i)
		i = p
	}
	h.entries[i] = x
	h.positions[x.id] = int32(i)
}

func (h *Heap) down(i int) {
	n := len(h.entries)
	x := h.entries[i]
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
		if x.f <= h.entries[child].f {
			break
		}
		h.entries[i] = h.entries[child]
		h.positions[h.entries[i].id] = int32(i)
		i = child
	}
	h.entries[i] = x
	h.positions[x.id] = int32(i)
}

func (h *Heap) removeSpent() {
	spent := h.spent
	h.spent = invalidNodeID
	index, ok := h.position(spent)
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
	h.clearPosition(NodeID(removed.id))
	if index == last {
		h.entries = h.entries[:last]
		return
	}
	replacement := h.entries[last]
	h.entries[index] = replacement
	h.entries = h.entries[:last]
	h.setPosition(NodeID(replacement.id), index)
	h.down(index)
}
