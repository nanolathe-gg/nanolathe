// Package movement implements ground route publication, storage, and smoothing.
//
// Research: [04 §7.3] scheduler budget, publication, route storage, pruning,
// save form, and export helper; [04 §7.5] smoothing; [04 §7.1] lattice and
// half-footprint bias. Invariants I10 (citations), I13 (route save is the
// explicit-layout exception).
package movement

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Point is a signed integer waypoint coordinate in the route lattice.
//
// Alias decision: per PLAN_07/WU-07-6, internal/path has not yet published a
// canonical Point/Cell type (another agent owns path/types.go concurrently).
// To avoid coupling we define Point inside movement and note that path's type,
// when it lands, may be unified via alias (type Cell = Point) without layout
// change. The conversion from cell to signed world coordinates uses the
// request's half-footprint bias [04 §7.1][04 §7.3] C13.
type Point struct {
	X int32
	Z int32
}

// Route holds up to 20 waypoints plus active/dirty bits [04 §7.3] C14–C16.
// Points are packed 4-byte X/Z pairs in the publisher; the save form uses
// signed 16-bit pairs capped at 3 [04 §7.3] C16.
type Route struct {
	Points [20]Point
	Count  uint8
	Active bool
	Dirty  bool
}

// Publish publishes a waypoint list per [04 §7.3] C14.
//
//   - Any count above 20 is clamped to 20 FIRST.
//   - A nonempty publication writes count+points and sets active+dirty.
//   - A ZERO publication clears active, sets dirty, writes NEITHER count NOR
//     array — stale bytes stay physically present. Queries gate on the active
//     bit, so an implementation may invalidate without zeroing.
func (r *Route) Publish(points []Point) { // [04 §7.3] C14
	if r == nil {
		return
	}
	if len(points) > 20 { // [04 §7.3] C14 clamp above 20 to 20 first
		points = points[:20]
	}
	if len(points) == 0 { // [04 §7.3] C14 zero publication
		r.Active = false
		r.Dirty = true
		return // count and array untouched
	}
	// nonempty publication: write count and points, set active+dirty
	r.Count = uint8(len(points))
	copy(r.Points[:], points)
	r.Active = true
	r.Dirty = true
}

// Prune implements waypoint pruning [04 §7.3] C15.
//
// When count exceeds one, compare mover's signed integer position against stored
// point index 1; if dx²+dz² ≤ 25 shift later points down, decrement, clear
// active below two points, set dirty. The threshold 25 is the integer 5-unit
// radius squared.
func (r *Route) Prune(pos Point) { // [04 §7.3] C15
	if r == nil || r.Count <= 1 {
		return
	}
	target := r.Points[1]
	dx := int64(pos.X) - int64(target.X)
	dz := int64(pos.Z) - int64(target.Z)
	if dx*dx+dz*dz > 25 { // [04 §7.3] C15
		return
	}
	// shift later points down one slot [04 §7.3] C15
	copy(r.Points[0:], r.Points[1:r.Count])
	r.Count--
	if r.Count < 2 {
		r.Active = false // clear active below two points [04 §7.3] C15
	}
	r.Dirty = true // set dirty [04 §7.3] C15
}

// At is the route export helper per [04 §7.3] C17.
// It is NOT an active-route predicate: for each requested index below the
// stored count it reads that point, and for excess indices it REPEATS THE LAST
// STORED POINT; it performs no active-bit check, and a zero count selects
// index −1, reading adjacent non-point fields rather than yielding an empty
// result. Callers must gate on the active bit themselves.
//
// Go cannot read adjacent struct memory without unsafe; at zero count we return
// a deterministic zero Point and document the divergence:
// TODO(question): retail reads adjacent non-point fields at zero count (index
// −1); what is the adjacent field layout for Route and what value does that
// reinterpreted read produce on each compiler? We return Point{} deterministically
// because callers gate on Active and the read is unobserved in normal use.
func (r *Route) At(index int) Point { // [04 §7.3] C17
	if r == nil {
		return Point{}
	}
	if r.Count == 0 {
		// Retail reads adjacent fields at index -1 [04 §7.3] C17.
		// See TODO(question) above; return zero value deterministically.
		return Point{}
	}
	if index < int(r.Count) {
		return r.Points[index]
	}
	// excess indices repeat last stored point [04 §7.3] C17
	return r.Points[r.Count-1]
}

// Export is an alias for At kept for callers that prefer the plan's
// "export helper" name [04 §7.3] C17.
func (r *Route) Export(index int) Point { // [04 §7.3] C17
	return r.At(index)
}

// EncodeRoute serializes a route into its save form [04 §7.3] C16.
// Inactive routes serialize a 2-bit count of zero; active routes serialize
// min(count,3) followed by that many signed 16-bit X/Z pairs. Bytes cross a
// boundary so explicit layout is allowed per I13. Encoding uses little-endian
// int16 per pair, low 2 bits of the first byte hold the count (value 0..3).
func EncodeRoute(r *Route) []byte { // [04 §7.3] C16, I13 exception
	if r == nil || !r.Active {
		// inactive: 2-bit count zero [04 §7.3] C16
		return []byte{0}
	}
	cnt := int(r.Count)
	if cnt > 3 {
		cnt = 3 // [04 §7.3] C16 min(count,3)
	}
	buf := make([]byte, 1+cnt*4)
	buf[0] = byte(cnt & 0x3) // 2-bit count [04 §7.3] C16
	off := 1
	for i := 0; i < cnt; i++ {
		// signed 16-bit X/Z pairs [04 §7.3] C16
		x := int16(r.Points[i].X)
		z := int16(r.Points[i].Z)
		binary.LittleEndian.PutUint16(buf[off:], uint16(x))
		binary.LittleEndian.PutUint16(buf[off+2:], uint16(z))
		off += 4
	}
	return buf
}

// DecodeRoute decodes the save form produced by EncodeRoute [04 §7.3] C16.
// It returns an error on truncated input. An inactive (count 0) encoding
// yields an inactive route with count 0; counts are always masked to 2 bits.
func DecodeRoute(data []byte) (*Route, error) { // [04 §7.3] C16
	if len(data) == 0 {
		return nil, errors.New("movement: empty route save")
	}
	cnt := int(data[0] & 0x3) // 2-bit count [04 §7.3] C16
	if cnt == 0 {
		return &Route{Active: false, Dirty: false, Count: 0}, nil
	}
	if len(data) < 1+cnt*4 {
		return nil, fmt.Errorf("movement: route save truncated: need %d have %d", 1+cnt*4, len(data))
	}
	r := &Route{Active: true, Dirty: false, Count: uint8(cnt)}
	off := 1
	for i := 0; i < cnt; i++ {
		x := int16(binary.LittleEndian.Uint16(data[off:]))
		z := int16(binary.LittleEndian.Uint16(data[off+2:]))
		r.Points[i] = Point{X: int32(x), Z: int32(z)}
		off += 4
	}
	return r, nil
}

// ReconstructRoute reconstructs a published waypoint list from a predecessor
// chain per [04 §7.3] C13.
//
// Expected input shape (so WU-07-3 path/search.go can call it):
//
//	start, goal — packed cell coordinates in the TNT attribute-cell lattice
//	  (one cell = 16 map pixels [04 §7.1]). These are lattice indices, not yet
//	  world coordinates.
//	parentOf — predecessor lookup: given a cell, returns its parent cell and
//	  true if the cell was visited and has a parent. The search's heap/node
//	  store owns the packed predecessor representation (per-node parent direction
//	  or parent coordinate); this function abstracts over that storage via a
//	  closure so movement does not depend on path's concrete slice/map layout.
//	  Typical capture: func(c Point) (Point,bool) { idx:=cellIndex(c); p:=pred[idx]; ... }.
//	bias — half-footprint bias in signed integer units to add to each cell when
//	  converting to signed world coordinates [04 §7.1][04 §7.3]. Waypoints come
//	  from cell coordinates plus the profile's half-footprint bias [04 §7.1]; the
//	  emitter converts each cell to signed world coordinates using the request's
//	  half-footprint bias [04 §7.3] C13.
//
// Algorithm per [04 §7.3] C13: walk predecessors backward from the goal cell,
// storing the packed cell into a 64-entry ring at index&63 each time the
// direction CHANGES (wraparound overwrites the oldest), then append the start
// cell. Emission walks masked indices downward, newest first, converts each cell
// to signed world coordinates by adding bias, and publishes
// min(directionChanges+1,64) points. More than 63 direction changes therefore
// survive as the most recent 63 change-points plus the start cell.
//
// TODO(question): exact world scaling of bias. Research establishes the lattice
// is 16 pixels per cell [04 §7.1] and that waypoints are cell+half-footprint
// bias [04 §7.1][04 §7.3], but the integer domain of the stored waypoints vs
// the pruning integer position (dx²+dz²≤25) suggests the saved points and prune
// comparison use map-pixel or cell integer units, not 16.16 Fixed [04 §7.3] C15.
// Current helper adds bias directly (world = cell+bias) in the same integer
// domain as the input cells. If retail instead computes world = CellToWorld(cell)
// + bias*worldUnitsPerPixel, that is a one-line change at the emission site.
func ReconstructRoute(start, goal Point, parentOf func(Point) (Point, bool), bias Point) []Point { // [04 §7.3] C13
	var ring [64]Point
	next := 0
	changes := 0
	// Sentinel invalid direction so first step always counts as a change
	// [04 §7.3] C13: storing each time direction changes; first comparison must store goal
	prevDx, prevDz := int32(1<<30), int32(1<<30)
	cur := goal
	if cur != start {
		for cur != start {
			parent, ok := parentOf(cur)
			if !ok {
				break // broken chain
			}
			dx := cur.X - parent.X
			dz := cur.Z - parent.Z
			if dx != prevDx || dz != prevDz {
				ring[next&63] = cur // [04 §7.3] C13 index &63
				next++
				changes++
				prevDx, prevDz = dx, dz
			}
			cur = parent
		}
	}
	// append start cell [04 §7.3] C13
	ring[next&63] = start
	next++
	total := changes + 1
	if total > 64 {
		total = 64 // [04 §7.3] C13 min(directionChanges+1,64)
	}
	if total <= 0 {
		total = 1
	}
	points := make([]Point, total)
	for i := 0; i < total; i++ {
		idx := (next - 1 - i) & 63 // masked downward, newest first [04 §7.3] C13
		cell := ring[idx]
		// Convert cell to signed world coordinates using half-footprint bias [04 §7.3] C13
		points[i] = Point{X: cell.X + bias.X, Z: cell.Z + bias.Z}
	}
	return points
}

// isCollinear reports whether b lies on the segment a–c in integer lattice
// coordinates [04 §7.5] C19 collinear removal.
func isCollinear(a, b, c Point) bool { // [04 §7.5] C19
	// cross product (b-a) × (c-a) == 0
	return int64(b.X-a.X)*int64(c.Z-a.Z) == int64(b.Z-a.Z)*int64(c.X-a.X)
}

// rayPassable reports whether every lattice cell on the line a→b (inclusive)
// passes the passability predicate [04 §7.5] C19. Each intermediate cell must
// pass the same passability test.
func rayPassable(a, b Point, isPassable func(Point) bool) bool { // [04 §7.5] C19
	if isPassable == nil {
		return false
	}
	dx := b.X - a.X
	dz := b.Z - a.Z
	adx := dx
	if adx < 0 {
		adx = -adx
	}
	adz := dz
	if adz < 0 {
		adz = -adz
	}
	sx := int32(1)
	if dx < 0 {
		sx = -1
	}
	sz := int32(1)
	if dz < 0 {
		sz = -1
	}
	x, z := a.X, a.Z
	err := adx - adz
	for {
		if !isPassable(Point{X: x, Z: z}) {
			return false
		}
		if x == b.X && z == b.Z {
			break
		}
		e2 := 2 * err
		if e2 > -adz {
			err -= adz
			x += sx
		}
		if e2 < adx {
			err += adx
			z += sz
		}
	}
	return true
}

// removeCollinear removes collinear intermediate points in place [04 §7.5] C19.
// It preserves endpoints and compacts the array, decrementing Count.
func (r *Route) removeCollinear() { // [04 §7.5] C19
	if r == nil || r.Count < 3 {
		return
	}
	// Build filtered list preserving non-collinear points
	write := 0
	// Keep first point
	filtered := make([]Point, 0, r.Count)
	filtered = append(filtered, r.Points[0])
	for i := 1; i < int(r.Count)-1; i++ {
		a := filtered[write]
		b := r.Points[i]
		c := r.Points[i+1]
		if isCollinear(a, b, c) {
			// b is collinear between a and c — skip it [04 §7.5] C19
			continue
		}
		filtered = append(filtered, b)
		write++
	}
	// Keep last point
	filtered = append(filtered, r.Points[r.Count-1])
	if len(filtered) == int(r.Count) {
		return
	}
	copy(r.Points[:], filtered)
	r.Count = uint8(len(filtered))
	r.Dirty = true
	_ = write
}

// Smooth removes collinear points then does bidirectional ray checks with the
// same passability test; a shortcut is accepted for legality only — cost is
// never compared [04 §7.5] C19.
//
// isPassable is the injected passability callback func(cell) bool so this
// package need not depend on path/search. Each intermediate cell must pass the
// same passability test for a shortcut to be accepted [04 §7.5].
func (r *Route) Smooth(isPassable func(Point) bool) { // [04 §7.5] C19
	if r == nil || r.Count < 3 {
		return
	}
	// Step 1: remove collinear points [04 §7.5] C19
	r.removeCollinear()
	if r.Count < 3 {
		return
	}
	if isPassable == nil {
		return
	}
	// Step 2: bidirectional ray checks, legality only, cost never compared [04 §7.5] C19
	// Forward pass: try farthest reachable shortcut from each anchor.
	// After a successful shortcut, restart from the same anchor to allow chaining.
	i := 0
	for i < int(r.Count)-2 {
		farthest := -1
		for j := int(r.Count) - 1; j > i+1; j-- {
			if rayPassable(r.Points[i], r.Points[j], isPassable) {
				farthest = j
				break // farthest reachable — cost not compared [04 §7.5] C19
			}
		}
		if farthest != -1 {
			// Remove (i+1 .. farthest-1) [04 §7.5] ray shortcut
			copy(r.Points[i+1:], r.Points[farthest:])
			r.Count -= uint8(farthest - i - 1)
			r.Dirty = true
			// do not advance i; try to extend this shortcut further on next iter
			continue
		}
		i++
	}
	// Backward pass: same from the tail, to catch shortcuts the forward pass
	// missed due to greedy farthest-first ordering; still legality-only.
	i = int(r.Count) - 1
	for i > 1 {
		farthest := -1
		for j := 0; j < i-1; j++ {
			if rayPassable(r.Points[j], r.Points[i], isPassable) {
				farthest = j
				break // nearest from head that can reach i
			}
		}
		if farthest != -1 {
			copy(r.Points[farthest+1:], r.Points[i:])
			removed := i - farthest - 1
			r.Count -= uint8(removed)
			r.Dirty = true
			i = int(r.Count) - 1 // restart from tail
			continue
		}
		i--
	}
}
