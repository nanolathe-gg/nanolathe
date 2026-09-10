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
	// index is the standalone store's per-cell table. A session store uses
	// indexRef to bind this same logical table to Session.entries. Keeping the
	// node ID in that table avoids a second Cell-keyed map for the working set.
	index map[Cell]entry
	// indexRef lets Reset replace a session's shared table without leaving the
	// Session pointing at the old map.
	indexRef *map[Cell]entry
	scale    int32 // h scale quantum for F computation (see Scale)
}

// NewNodeStore returns an empty store with the given h scale.
// Scale is the per-player quantum used as (h*scale)>>16 [04 §7.2] C6.
// Pass 65536 for unweighted (1.0) when no scheduler is present.
func NewNodeStore(scale int32) *NodeStore {
	index := make(map[Cell]entry)
	return &NodeStore{
		nodes: make([]Node, 1), // reserve 0
		index: index,
		scale: scale,
	}
}

// newSessionNodeStore binds node lookup to the search's existing per-cell
// entry table. The table already carries status, direction and node identity,
// so a second Cell-keyed index would duplicate every touched coordinate.
func newSessionNodeStore(scale int32, entries *map[Cell]entry) *NodeStore {
	return &NodeStore{
		nodes:    make([]Node, 1),
		indexRef: entries,
		scale:    scale,
	}
}

// Scale returns the current h scale.
func (ns *NodeStore) Scale() int32 { return ns.scale }

// SetScale updates the h scale for future allocations. Existing nodes
// keep their already-computed F (h is write-once) [04 §7.2] C7.
func (ns *NodeStore) SetScale(scale int32) { ns.scale = scale }

// TODO(question): pooling this store across searches did not pay. The
// scheduler holds one session per unit and replaces it whenever the goal or
// activation changes, and Alloc's growth was 37% of everything the simulation
// allocated after the publication boundary was fixed, so reuse looked
// worthwhile. It was implemented, measured and reverted: allocated bytes and
// objects per tick did not move at all (0.378 MB / 1475 objects either way) and
// the median tick was 2% SLOWER across three runs. The reason is that Go's
// current map implementation releases a large map's storage on clear, so the
// pooled per-cell table re-grows regardless, and the node array's growth is
// dominated by searches that expand more nodes than the previous search on the
// same unit. Reuse only added the clear. What would change the answer: a dense
// generation-stamped node table indexed by cell instead of the map — which is a
// different search data structure, not a pooling change, and would have to be
// proven not to alter visit order or open-set tie-breaking before it could be
// gated on the fingerprint.
//
// That table was designed and not built, and the two things the design turns
// on are recorded here so the next attempt starts from them rather than from
// the profile again.
//
// Ownership. A generation-stamped table is shared: a search takes the next
// generation and every slot stamped with an older one reads as empty. That is
// only sound while exactly one search holds live per-cell state. It does hold
// today, and by a chain that lives in internal/movement rather than here: the
// scheduler latches one request at a time and keeps it until the search
// reports done, the caller drops its session on done, a goal or activation
// change replaces the session before it is resumed, and the one cancel path
// (System.CancelPathRequest) clears the cached session in the same call that
// cancels the request. So no suspended session can be resumed after another
// search has run. Nothing in this package enforces that, and a shared table
// would make a future change to that cache corrupt a search silently rather
// than fail — so the table needs an owner that hands it out and takes it back,
// not a bare generation counter.
//
// Size against locality. Sized to a full map the table is a few hundred
// thousand slots, while a search touches a few hundred cells scattered across
// it. The map's working set is a few kilobytes and stays in cache; the dense
// table's is one cache line per touched cell spread over megabytes. The
// hashing this would remove measures about six per cent of the tick, so the
// win is not large enough to assume the locality trade comes out ahead — it
// has to be measured on the scene, not argued.
//
// One part of the same idea did pay and is no longer open: the OPEN SET's
// node-to-slot index, which was a map[NodeID]int and is now a dense row (see
// Heap). Node identities are dense by construction, so that one needed no
// generation stamp and no owner.

// Reset clears all nodes but retains the scale.
func (ns *NodeStore) Reset() {
	ns.nodes = ns.nodes[:1]
	// Replacing the index avoids map iteration in a simulation-visible reset
	// while retaining the stable node identity contract [04 §7.2]. For a
	// session this is also the status table, so rebinding through indexRef
	// updates the Session's sole map reference; lookup and status cannot
	// continue against different maps after a lifecycle reset.
	if ns.indexRef != nil {
		*ns.indexRef = make(map[Cell]entry)
		return
	}
	ns.index = make(map[Cell]entry)
}

// Len returns the number of allocated nodes (excluding invalid 0).
func (ns *NodeStore) Len() int { return len(ns.nodes) - 1 }

// Find locates the NodeID for a cell.
func (ns *NodeStore) Find(cell Cell) (NodeID, bool) {
	index := ns.lookup()
	e, ok := index[cell]
	if !ok || e.node == invalidNodeID {
		return invalidNodeID, false
	}
	return e.node, true
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
	index := ns.lookup()
	if e, ok := index[cell]; ok && e.node != invalidNodeID {
		return e.node
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
	e := index[cell]
	e.node = id
	index[cell] = e
	return id
}

// Alloc is a lower-level allocation that takes a raw h directly.
// H is stored write-once; if cell already exists the existing node
// is returned without modifying H, G, F, or parent. Use TryRelax
// to update G/F.
func (ns *NodeStore) Alloc(cell Cell, g, h int32, parent NodeID, dir uint8) NodeID {
	index := ns.lookup()
	if e, ok := index[cell]; ok && e.node != invalidNodeID {
		return e.node
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
	e := index[cell]
	e.node = id
	index[cell] = e
	return id
}

func (ns *NodeStore) lookup() map[Cell]entry {
	if ns.indexRef != nil {
		return *ns.indexRef
	}
	return ns.index
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
	h.entries = append(h.entries, heapEntry{id: id, f: f})
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
	h.clearPosition(h.spent)
	h.entries[0] = heapEntry{id: id, f: f}
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
	return e.id, e.f, true
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

func (h *Heap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.setPosition(h.entries[i].id, i)
	h.setPosition(h.entries[j].id, j)
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
	h.clearPosition(removed.id)
	if index == last {
		h.entries = h.entries[:last]
		return
	}
	replacement := h.entries[last]
	h.entries[index] = replacement
	h.entries = h.entries[:last]
	h.setPosition(replacement.id, index)
	h.down(index)
}
