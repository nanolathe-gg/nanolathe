// Package movement implements ground route publication and storage.
//
// Research: [04 §7.3] scheduler budget, publication, route storage, pruning,
// save form, and export helper; [04 §7.1] lattice and half-footprint bias.
// Invariants I10 (citations), I13 (route save is the explicit-layout
// exception).
package movement

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/path"
)

// Point is the canonical published route coordinate from internal/path.
// The conversion from cell to signed world coordinates uses the request's
// half-footprint bias [04 §7.1][04 §7.3] C13.
type Point = path.Point

// Route holds up to 20 waypoints plus active/dirty bits [04 §7.3] C14–C16.
// Points are packed 4-byte X/Z pairs in the publisher; the save form uses
// signed 16-bit pairs capped at 3 [04 §7.3] C16.
type Route struct {
	Points [20]Point
	Count  uint8
	Active bool
	Dirty  bool
	// WantsRepath and LastRequestTick are the route follower's request-poll
	// state. They are runtime state, not part of the compact route save form
	// implemented below [04 R-MOV-01 §3][04 R-MOV-01 §7].
	WantsRepath     bool
	LastRequestTick uint32
	Status          path.Status // last publish status [04 §7.2] 0 success, 0x100 already, 0x200 rejected
	// StaticRevision is Nanolathe runtime metadata. It is not part of the
	// retail route save encoding [04 §7.3].
	StaticRevision uint64
}

// Publish publishes a waypoint list per [04 §7.3] C14.
//
//   - Any count above 20 is clamped to 20 FIRST.
//   - A nonempty publication writes count+points and sets active+dirty.
//   - A ZERO publication clears active, sets dirty, writes NEITHER count NOR
//     array — stale bytes stay physically present. Queries gate on the active
//     bit, so an implementation may invalidate without zeroing.
func (r *Route) Publish(points []Point) { // [04 §7.3] C14
	r.PublishAtRevision(points, 0)
}

// PublishAtRevision publishes a route and records the static-obstacle
// revision that produced it. Zero publication retains stale route bytes and
// metadata, matching the retail inactive-route behavior [04 §7.3].
func (r *Route) PublishAtRevision(points []Point, revision uint64) { // [04 §7.3]
	if r == nil {
		return
	}
	if len(points) > 20 { // [04 §7.3] C14 clamp above 20 to 20 first
		points = points[:20]
	}
	if len(points) == 0 { // [04 §7.3] C14 zero publication
		r.Active = false
		r.Dirty = true
		r.WantsRepath = false
		return // count and array untouched
	}
	// nonempty publication: write count and points, set active+dirty
	r.Count = uint8(len(points))
	copy(r.Points[:], points)
	r.Active = true
	r.Dirty = true
	r.WantsRepath = false
	r.StaticRevision = revision
}

// NeedsStaticReplan reports whether an active route was produced against an
// older static obstacle revision. Mobile occupancy is intentionally absent
// from this check [04 §8.2][docs/SPEC_CONFLICTS SC22].
func (r *Route) NeedsStaticReplan(current uint64) bool {
	return r != nil && r.Active && r.Count > 0 && r.StaticRevision != current
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
// [04 R-PATH-01 §13] closes the marker that stood here, which asked which
// adjacent field index −1 lands on and what value the read produces. The
// helper is the ground follower's point fill, and the follower's layout is, in
// order: the controller's method table, the bound goal payload, the OWNING
// UNIT'S REFERENCE, the twenty packed 4-byte points, then the count and the
// flag bits. Index −1 therefore lands on the owning-unit reference — X becomes
// its low 16 bits shifted into 16.16, Z its high 16 bits, Y zero.
//
// That reference is a heap address. The value is not reproducible between runs,
// so there is NO CONTRACT to clone; and the read is unreachable from the one
// reader the document names, because ground steering asks the follower for a
// waypoint first and the has-waypoint bit is cleared below two points
// [04 R-MOV-01 §3]. Returning a fixed zero triple at zero count is not a
// divergence from anything observable, so Point{} stays.
func (r *Route) At(index int) Point { // [04 §7.3] C17
	if r == nil {
		return Point{}
	}
	if r.Count == 0 {
		// Index −1 reads the owning-unit reference in retail: a heap address,
		// no contract, and unreachable from the follower's own reader
		// [04 R-PATH-01 §13].
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
