package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestUnitVisibilityTargetIsTargetFromBounds holds the session's in-place
// visibility query to visibility.TargetFromBounds, the canonical hull
// translation [06 §3.1][03 §3.2]: for units of assorted definitions, positions
// and states, filling a query field by field gives exactly the record the
// literal-and-TargetFromBounds form gave, and it overwrites every field of a
// query that held another unit's values.
func TestUnitVisibilityTargetIsTargetFromBounds(t *testing.T) {
	defs := []*content.UnitDef{
		nil, // no definition: a degenerate box at the position
		{FootprintX: 1, FootprintZ: 1},
		{FootprintX: 3, FootprintZ: 5, ModelTopFixed: 21 << 16},
		{FootprintX: 8, FootprintZ: 2, ModelTopFixed: -4 << 16}, // floored at zero
		{FootprintX: 0, FootprintZ: 7, ModelTopFixed: 0x12345},
	}
	positions := [][3]numeric.Fixed{
		{0, 0, 0},
		{64 * numeric.FixedOne, 21 * numeric.FixedOne, 96 * numeric.FixedOne},
		{-3 * numeric.FixedOne, -70 * numeric.FixedOne, 5},
		{40000 * numeric.FixedOne, 255 * numeric.FixedOne, 33000*numeric.FixedOne + 1},
	}
	junk := visibility.Target{
		UnitID: 0xffff, Owner: 9, X: 1, Y: 2, Z: 3, OriginX: 4, OriginY: 5, OriginZ: 6,
		Flying: true, OffMap: true, FootprintX: 7, FootprintZ: 8, FootprintSizeX: 9, FootprintSizeZ: 10,
		XExtent: 11, YExtent: 12, ZExtent: 13, Hidden: true, Status: 0xffffffff,
	}
	n := 0
	for _, def := range defs {
		for _, p := range positions {
			n++
			u := &units.Unit{
				Handle: pool.Handle(n * 37), Owner: uint8(n % 10), Def: def,
				X: p[0], Y: p[1], Z: p[2], Hidden: n%2 == 0,
				CachedOccupancyX: int16(n - 3), CachedOccupancyZ: int16(2 * n),
				FootprintSizeX: int16(n % 4), FootprintSizeZ: int16(n % 3),
			}
			if n%3 == 0 {
				u.Move.ModeMirror = 2
			}
			status := uint32(n) * 0x101
			min, max := def.BoundingExtents()
			want := visibility.TargetFromBounds(visibility.Target{
				UnitID: uint16(u.Handle), Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z,
				Hidden: u.Hidden, Status: status, OriginX: u.X, OriginY: u.Y, OriginZ: u.Z,
				Flying:     u.Move.ModeMirror == 2,
				FootprintX: int32(u.CachedOccupancyX), FootprintZ: int32(u.CachedOccupancyZ),
				FootprintSizeX: int32(u.FootprintSizeX), FootprintSizeZ: int32(u.FootprintSizeZ),
			}, min, max)
			if got := unitVisibilityTarget(u, status); got != want {
				t.Fatalf("unit %d: query %+v, want %+v", n, got, want)
			}
			reused := junk
			fillUnitVisibilityTarget(&reused, u, status)
			if reused != want {
				t.Fatalf("unit %d: refilled query %+v, want %+v", n, reused, want)
			}
		}
	}
	reused := junk
	fillUnitVisibilityTarget(&reused, nil, 0)
	if reused != (visibility.Target{}) || unitVisibilityTarget(nil, 0) != (visibility.Target{}) {
		t.Fatal("a missing unit must give the zero query")
	}
}
