package movement

// The per-handle tables of System are dense rows indexed by pool handle [I5].
// These three helpers are the whole of their storage discipline. A row grows
// on write so the handle is addressable, and reads answer the zero value for a
// handle the row does not address — which is what a map read of an absent key
// gave, and what every caller already tests for. Rows grow independently; no
// row's length is ever inferred from another's. BindWorld pre-sizes them all to
// the pool's capacity so the growth path is a composition cost and not a
// per-unit one.
//
// Nothing iterates a row. Every access is by handle, so the order questions of
// [I1] do not arise here.

// growHandleRow returns a row that addresses idx, keeping what it holds.
func growHandleRow[T any](row []T, idx int) []T {
	if idx < len(row) {
		return row
	}
	if idx < cap(row) {
		return row[:idx+1]
	}
	grown := make([]T, idx+1, (idx+1)*2)
	copy(grown, row)
	return grown
}

// growHandleTables makes every per-handle row address the handle. Each row is
// tested against its own length rather than against a shared witness, so a row
// that a caller has emptied on its own is grown again like any other.
func (s *System) growHandleTables(idx int) {
	if s == nil || idx < 0 {
		return
	}
	s.Routes = growHandleRow(s.Routes, idx)
	s.Steers = growHandleRow(s.Steers, idx)
	s.Collisions = growHandleRow(s.Collisions, idx)
	s.Flights = growHandleRow(s.Flights, idx)
	s.profiles = growHandleRow(s.profiles, idx)
	s.profileNames = growHandleRow(s.profileNames, idx)
	s.sessions = growHandleRow(s.sessions, idx)
	s.prevMoveTier = growHandleRow(s.prevMoveTier, idx)
	s.prevSFXBand = growHandleRow(s.prevSFXBand, idx)
	s.pathFailures = growHandleRow(s.pathFailures, idx)
	s.activeOrders = growHandleRow(s.activeOrders, idx)
	s.arrivalHandles = growHandleRow(s.arrivalHandles, idx)
	s.moveGoals = growHandleRow(s.moveGoals, idx)
}

// handleRow reads one dense per-handle row. A handle the row does not address
// has never had state written for it, so the answer is the zero value — which
// is exactly what a map read of an absent key gave, and what every caller of
// these rows already tests for. The bounds test is what makes it safe to read
// a row before BindWorld has sized it: composition publishes a fingerprint
// before the first Step binds the world.
func handleRow[T any, H ~uint16 | ~int](row []T, h H) T {
	if int(h) >= 0 && int(h) < len(row) {
		return row[h]
	}
	var zero T
	return zero
}

// setHandleRow writes one dense per-handle row, growing it first so that the
// handle is addressable. Rows grow independently: nothing reads a row expecting
// another row's length, and a row a caller has emptied is grown again like any
// other. This is the only write path.
func setHandleRow[T any, H ~uint16 | ~int](row *[]T, h H, v T) {
	if int(h) < 0 {
		return
	}
	*row = growHandleRow(*row, int(h))
	(*row)[h] = v
}
