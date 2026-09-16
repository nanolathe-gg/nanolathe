package path

// Test-only seams. Production runs one search shape only — the incremental
// Session the scheduler resumes under a work budget [04 §7.3] — so the
// whole-search convenience, the standalone node store and the entry-table
// census below live here rather than in package source, where they read as
// shipped entry points that nothing ships.

// SearchResult is what one completed search produced: the route points, the
// terminating status, the status the search notified along the way, and the
// counters the scheduler charges work against.
type SearchResult struct {
	Points     []Point
	Status     Status
	Notified   Status
	Popped     int
	SetupSteps int
	Seeded     bool
}

// Search runs cfg to completion in one call and returns its result. It is the
// same session the scheduler drives, resumed with a budget no search reaches.
func Search(cfg SearchConfig) SearchResult {
	s := NewSession(cfg)
	if !s.done {
		s.Resume(1 << 30)
	}
	return SearchResult{Points: s.resultPoints, Status: s.resultStatus, Notified: s.notified, Popped: s.popped, SetupSteps: s.setupSteps, Seeded: s.seeded}
}

// NewNodeStore returns an empty store with the given h scale and a per-cell
// table of its own. Scale is the per-player quantum used as (h*scale)>>16
// [04 §7.2] C6; pass 65536 for unweighted (1.0). A session binds its own entry
// table instead, through newSessionNodeStore.
func NewNodeStore(scale int32) *NodeStore {
	index := newMapIndex()
	return &NodeStore{
		nodes: make([]Node, 1), // reserve 0
		index: &index,
		scale: scale,
	}
}

// straightRunLen recomputes a node's straight-run length from its parent
// chain. Expansion never needs it: it carries the run forward in the node's
// own Run word as it allocates [04 R-PATH-01 §3], and this walk exists only so
// a test can state the same length for a chain it assembled by hand.
func straightRunLen(ns *NodeStore, id NodeID) int {
	if id == invalidNodeID {
		return 0
	}
	if run := ns.Get(id).Run; run != 0 {
		return int(run)
	}
	n := ns.Get(id)
	if n.Dir == DirNone {
		return 0
	}
	count := 1
	for parent := n.Parent; parent != invalidNodeID; parent = ns.Get(parent).Parent {
		if ns.Get(parent).Dir != n.Dir {
			break
		}
		count++
	}
	return count
}

// len reports how many cells the table holds. The map answers with its own
// length; the dense table counts what this lending wrote, because its slots
// outlive the search that wrote them.
func (ix *cellIndex) len() int {
	if ix.ws == nil {
		return len(ix.m)
	}
	return ix.n + len(ix.overflow)
}
