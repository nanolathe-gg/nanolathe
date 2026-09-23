package path

// The search's per-cell table [04 §7.2].
//
// A search touches a few hundred cells and asks about each of them many times:
// the ray, the popped cell, every neighbour of every expansion, and
// the node store's own identity lookup all read or write the same row. That row
// was a map[Cell]entry per session, and hashing a two-int32 key in those inner
// loops was the largest remaining map traffic in the authoritative tick, with
// the map's growth one of its larger allocations.
//
// A dense table indexed by cell removes both, at the cost of a table sized to
// the map rather than to the working set. That trade cannot be assumed, which
// is why the table is owned rather than allocated per search: the Scheduler
// keeps ONE, hands it to the search it has admitted, and takes it back when
// that search is finished with it. A search that cannot have it — because
// another still holds it, or because the request carries no bounds — keeps the
// map, and answers identically.
//
// Generations, not clearing. Each lending takes the next generation and every
// slot stamped with an older one reads as empty, so handing the table over is
// O(1) rather than O(map). The generation alone would not be safe: it makes a
// second search silently share the first's slots. The LENDING is what makes it
// safe — the table is handed to exactly one search and refused to every other
// — and the generation is only what makes the hand-over cheap.

// cellSlot is one cell's entry plus the generation that wrote it.
type cellSlot struct {
	gen uint32
	e   entry
}

// Workspace is the scheduler-owned per-cell table. Its zero value is an
// unsized, unlent table.
type Workspace struct {
	origin Cell
	w, h   int32
	slots  []cellSlot
	gen    uint32
	lent   bool
	// The node array and the open set's two rows of the search that holds the
	// lending. They travel with the table for the same reason the table is
	// owned here: every search used to grow its own from empty, and that
	// growth was the largest allocation in the authoritative tick. A search
	// takes them with the table and hands them back, emptied, on release;
	// only their capacity outlives it, and every element a search reads it
	// has written first, so the retained contents are never observed.
	nodes         []Node
	heapEntries   []heapEntry
	heapPositions []int32
}

// workspaceSlotLimit caps the table a workspace will size itself to. No retail
// map comes near it — the largest is 512 cells on a side, and this admits four
// thousand — and past it a search keeps the map rather than asking for a
// gigabyte of table (I11's bounds-check exception).
const workspaceSlotLimit = 16 << 20

// lend sizes the table to cover the search's bounds and marks it taken. It
// reports false when the table is already out with another search, when the
// bounds are unusable, or when they are larger than this build will size a
// table to; the caller then keeps the map.
//
// The rectangle is grown by one cell on every side because the expansion reads
// and writes the neighbours of an in-bounds cell, and those can be one cell
// outside. Anything still outside goes to the overflow map, which no reachable
// search fills.
func (w *Workspace) lend(bounds Rect, hasBounds bool) bool {
	if w == nil || w.lent || !hasBounds {
		return false
	}
	min := Cell{X: bounds.Min.X - 1, Z: bounds.Min.Z - 1}
	width := int64(bounds.Max.X-bounds.Min.X) + 3
	height := int64(bounds.Max.Z-bounds.Min.Z) + 3
	if width <= 0 || height <= 0 || width*height > workspaceSlotLimit {
		return false
	}
	if !w.covers(min, int32(width), int32(height)) {
		w.origin, w.w, w.h = min, int32(width), int32(height)
		w.slots = make([]cellSlot, width*height)
		w.gen = 0
	}
	w.gen++
	if w.gen == 0 {
		// The generation wrapped, so every stamp in the table is ambiguous.
		// Clearing is what makes generation 1 mean empty again.
		clear(w.slots)
		w.gen = 1
	}
	w.lent = true
	return true
}

// covers reports whether the current table addresses every cell of the
// requested rectangle. A table that already covers it is kept rather than
// reallocated: which cells land in the table and which in the overflow map is
// storage only, and both answer identically.
func (w *Workspace) covers(min Cell, width, height int32) bool {
	if w.slots == nil {
		return false
	}
	return min.X >= w.origin.X && min.Z >= w.origin.Z &&
		int64(min.X)+int64(width) <= int64(w.origin.X)+int64(w.w) &&
		int64(min.Z)+int64(height) <= int64(w.origin.Z)+int64(w.h)
}

// release returns the table. The slots keep whatever the search left; the next
// lending's generation is what makes them read as empty.
func (w *Workspace) release() {
	if w != nil {
		w.lent = false
	}
}

// slot maps a cell onto its index, or reports that the table does not address
// it.
func (w *Workspace) slot(c Cell) (int, bool) {
	x, z := c.X-w.origin.X, c.Z-w.origin.Z
	if x < 0 || z < 0 || x >= w.w || z >= w.h {
		return 0, false
	}
	return int(z)*int(w.w) + int(x), true
}

// cellIndex is the search's per-cell table, whichever storage it has. Every
// read of a cell no writer has touched answers the zero entry, which is what a
// map read of an absent key answered.
type cellIndex struct {
	ws       *Workspace
	gen      uint32
	m        map[Cell]entry
	overflow map[Cell]entry
}

// newMapIndex is the storage a search keeps when it has no workspace.
func newMapIndex() cellIndex { return cellIndex{m: make(map[Cell]entry)} }

// bindWorkspace takes the workspace if it is free, and otherwise leaves the
// index on its map.
func (ix *cellIndex) bindWorkspace(ws *Workspace, bounds Rect, hasBounds bool) {
	if !ws.lend(bounds, hasBounds) {
		return
	}
	ix.ws, ix.gen, ix.m, ix.overflow = ws, ws.gen, nil, nil
}

// release hands the workspace back and leaves the index on a fresh map, so an
// index that outlives its lending cannot read another search's slots.
func (ix *cellIndex) release() {
	if ix.ws == nil {
		return
	}
	ix.ws.release()
	ix.ws, ix.overflow = nil, nil
	ix.m = make(map[Cell]entry)
}

// reset empties the table without disturbing the lending.
func (ix *cellIndex) reset() {
	if ix.ws != nil {
		// A fresh generation is the reset; the slots need not be touched.
		ix.ws.gen++
		if ix.ws.gen == 0 {
			clear(ix.ws.slots)
			ix.ws.gen = 1
		}
		ix.gen = ix.ws.gen
		ix.overflow = nil
		return
	}
	ix.m = make(map[Cell]entry)
}

func (ix *cellIndex) get(c Cell) entry { return ix.load(ix.slotOf(c), c) }

func (ix *cellIndex) set(c Cell, e entry) { ix.store(ix.slotOf(c), c, e) }

// slotOf is the cell's table slot, or nil when the index keeps the cell in a
// map (no workspace, or a cell the table does not address). The expansion
// resolves a neighbour's slot once and then reads and writes it through load
// and store, instead of recomputing the address on every access. A slot
// pointer stays valid for the whole lending: the table is sized when it is
// lent and never reallocated while a search holds it.
//
// The unsigned comparison is the same test as slot's two signed ones: a
// negative offset converts to a value no width reaches.
func (ix *cellIndex) slotOf(c Cell) *cellSlot {
	ws := ix.ws
	if ws == nil {
		return nil
	}
	x, z := uint32(c.X-ws.origin.X), uint32(c.Z-ws.origin.Z)
	if x >= uint32(ws.w) || z >= uint32(ws.h) {
		return nil
	}
	return &ws.slots[int(z)*int(ws.w)+int(x)]
}

// load reads the cell's entry through its resolved slot. A slot another
// generation wrote reads as the zero entry, the answer a map read of an absent
// key gave.
func (ix *cellIndex) load(sl *cellSlot, c Cell) entry {
	if sl != nil {
		if sl.gen == ix.gen {
			return sl.e
		}
		return entry{}
	}
	return ix.getMap(c)
}

// store writes the cell's entry through its resolved slot.
func (ix *cellIndex) store(sl *cellSlot, c Cell, e entry) {
	if sl != nil {
		*sl = cellSlot{gen: ix.gen, e: e}
		return
	}
	ix.setMap(c, e)
}

//go:noinline
func (ix *cellIndex) getMap(c Cell) entry {
	if ix.ws == nil {
		return ix.m[c]
	}
	return ix.overflow[c]
}

//go:noinline
func (ix *cellIndex) setMap(c Cell, e entry) {
	if ix.ws == nil {
		ix.m[c] = e
		return
	}
	if ix.overflow == nil {
		ix.overflow = make(map[Cell]entry)
	}
	ix.overflow[c] = e
}
