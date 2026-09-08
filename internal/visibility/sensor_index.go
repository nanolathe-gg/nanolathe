package visibility

import "sort"

// sensorIndexCellRaw is the established 128-world-unit sensor index cell,
// represented in raw 16.16 coordinates [03 R-VIS-01 §5].
const sensorIndexCellRaw int64 = 128 << 16

// sensorCandidateIndex is reusable scratch for the sensor phase. It does not
// change contact admission: every returned input index still reaches the
// original exact predicate in sensors.go.
//
// The exact visitor narrows a raw coordinate difference to signed 32 bits
// before it squares. That permits wrapped values which a normal geometric
// square cannot bound. enabled is consequently set only for a snapshot whose
// two coordinate spans prove every pairwise narrowed difference and sum of
// squares stay representable; every other snapshot uses the exhaustive walk
// [03 R-VIS-01 §5].
type sensorCandidateIndex struct {
	enabled         bool
	forceExhaustive bool // test-only baseline control; never set in production.

	minX, minZ int64
	width      int
	height     int
	heads      []int
	counts     []int
	next       []int
	order      []int
	candidates []int
}

// rebuild takes the supplied sensor snapshot as its sole position source. It
// deliberately does not reuse a movement-owned index, whose update phase is
// different from SensorTick [03 R-VIS-01 §4].
func (x *sensorCandidateIndex) rebuild(units []SensorUnit) {
	x.enabled = false
	if x.forceExhaustive || len(units) == 0 {
		return
	}

	minX, maxX := units[0].X.Raw(), units[0].X.Raw()
	minZ, maxZ := units[0].Z.Raw(), units[0].Z.Raw()
	for i := 1; i < len(units); i++ {
		px, pz := units[i].X.Raw(), units[i].Z.Raw()
		if px < minX {
			minX = px
		}
		if maxX < px {
			maxX = px
		}
		if pz < minZ {
			minZ = pz
		}
		if maxZ < pz {
			maxZ = pz
		}
	}

	// A signed raw delta is exact only while every snapshot pair differs by at
	// most MaxInt32. At that same bound, the two non-negative high-word square
	// terms cannot overflow their signed addition. Beyond it, a wrapped result
	// can pass a radius predicate from outside any ordinary square.
	const maxSafeRawSpan = int64(1<<31 - 1)
	if !rawSpanAtMost(minX, maxX, maxSafeRawSpan) || !rawSpanAtMost(minZ, maxZ, maxSafeRawSpan) {
		return
	}
	// rawSpanAtMost establishes this subtraction is representable.
	spanX, spanZ := maxX-minX, maxZ-minZ
	width := int(spanX/sensorIndexCellRaw) + 1
	height := int(spanZ/sensorIndexCellRaw) + 1
	cellCount := width * height
	// A very sparse, wide snapshot would spend more clearing empty cells than
	// it can save. Its exhaustive fallback is still the exact production walk.
	if cellCount > 4096 && cellCount > len(units)*8 {
		return
	}
	// In a compact formation nearly every radius query spans enough occupants
	// that the ordered merge buys nothing. Keep the established direct walk in
	// that case instead of paying index traversal on every emitter.
	if len(units) >= cellCount*8 {
		return
	}
	x.prepareOrder(len(units))

	if cap(x.heads) < cellCount {
		x.heads = make([]int, cellCount)
	} else {
		x.heads = x.heads[:cellCount]
	}
	if cap(x.counts) < cellCount {
		x.counts = make([]int, cellCount)
	} else {
		x.counts = x.counts[:cellCount]
	}
	for i := range x.heads {
		x.heads[i] = -1
		x.counts[i] = 0
	}
	if cap(x.next) < len(units) {
		x.next = make([]int, len(units))
	} else {
		x.next = x.next[:len(units)]
	}
	if cap(x.candidates) < len(units) {
		x.candidates = make([]int, 0, len(units))
	} else {
		x.candidates = x.candidates[:0]
	}
	x.minX, x.minZ = minX, minZ
	x.width, x.height = width, height
	// Link in reverse so each individual cell already yields ascending input
	// indexes. Queries sort only when they combine more than one cell.
	for i := len(units) - 1; i >= 0; i-- {
		cx := int((units[i].X.Raw() - minX) / sensorIndexCellRaw)
		cz := int((units[i].Z.Raw() - minZ) / sensorIndexCellRaw)
		cell := cz*width + cx
		x.next[i] = x.heads[cell]
		x.heads[cell] = i
		x.counts[cell]++
	}
	x.enabled = true
}

// candidatesFor returns a conservative, duplicate-free set of input indexes
// in original input order. Its nil result requests the existing exhaustive
// walk. The final distance test remains the caller's responsibility.
func (x *sensorCandidateIndex) candidatesFor(source *SensorUnit, radius int32) []int {
	if !x.enabled || radius < 0 || radius > 32767 {
		return nil
	}
	// For an admitted point, floor(rawDelta² / 2³²) <= radius². Therefore
	// abs(rawDelta) is strictly less than (radius+1) raw world units. The
	// expanded square may include extras, but cannot exclude an admitted point.
	extent := (int64(radius) + 1) << 16
	const minInt64 = -1 << 63
	const maxInt64 = 1<<63 - 1
	if source.X.Raw() < minInt64+extent || source.X.Raw() > maxInt64-extent ||
		source.Z.Raw() < minInt64+extent || source.Z.Raw() > maxInt64-extent {
		return nil
	}
	minX, maxX := source.X.Raw()-extent, source.X.Raw()+extent
	minZ, maxZ := source.Z.Raw()-extent, source.Z.Raw()+extent
	minCellX := int((minX - x.minX) / sensorIndexCellRaw)
	maxCellX := int((maxX - x.minX) / sensorIndexCellRaw)
	minCellZ := int((minZ - x.minZ) / sensorIndexCellRaw)
	maxCellZ := int((maxZ - x.minZ) / sensorIndexCellRaw)
	// Go divides negative integers toward zero. Correct it here because index
	// cells are floor buckets and a query can extend below the snapshot bound.
	if minX < x.minX && (minX-x.minX)%sensorIndexCellRaw != 0 {
		minCellX--
	}
	if minZ < x.minZ && (minZ-x.minZ)%sensorIndexCellRaw != 0 {
		minCellZ--
	}
	if maxX < x.minX && (maxX-x.minX)%sensorIndexCellRaw != 0 {
		maxCellX--
	}
	if maxZ < x.minZ && (maxZ-x.minZ)%sensorIndexCellRaw != 0 {
		maxCellZ--
	}
	if maxCellX < 0 || maxCellZ < 0 || minCellX >= x.width || minCellZ >= x.height {
		return x.candidates[:0]
	}
	if minCellX < 0 {
		minCellX = 0
	}
	if minCellZ < 0 {
		minCellZ = 0
	}
	if maxCellX >= x.width {
		maxCellX = x.width - 1
	}
	if maxCellZ >= x.height {
		maxCellZ = x.height - 1
	}
	if minCellX == 0 && maxCellX == x.width-1 && minCellZ == 0 && maxCellZ == x.height-1 {
		return x.order
	}
	candidateCount := 0
	for z := minCellZ; z <= maxCellZ; z++ {
		for cell := z*x.width + minCellX; cell <= z*x.width+maxCellX; cell++ {
			candidateCount += x.counts[cell]
		}
	}
	// Sorting a large merged set costs more than preserving the original full
	// scan. Returning every input remains a conservative ordered candidate set
	// and keeps compact formations out of the broad phase's worst case.
	if candidateCount*4 >= len(x.order) {
		return x.order
	}
	x.candidates = x.candidates[:0]
	for z := minCellZ; z <= maxCellZ; z++ {
		for cell := z*x.width + minCellX; cell <= z*x.width+maxCellX; cell++ {
			for i := x.heads[cell]; i >= 0; i = x.next[i] {
				x.candidates = append(x.candidates, i)
			}
		}
	}
	sort.Ints(x.candidates)
	return x.candidates
}

func (x *sensorCandidateIndex) prepareOrder(n int) {
	if cap(x.order) < n {
		x.order = make([]int, n)
	} else {
		x.order = x.order[:n]
	}
	for i := range x.order {
		x.order[i] = i
	}
}

// rawSpanAtMost compares max-min without forming a potentially overflowing
// signed subtraction. Once it returns true, max-min is safe to materialize.
func rawSpanAtMost(min, max, limit int64) bool {
	if min > max {
		return false
	}
	if min >= 0 {
		return max-min <= limit
	}
	return max <= min+limit
}
