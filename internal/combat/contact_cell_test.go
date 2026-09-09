package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Contact tests for the projectile–unit ladder steps 3 and 4 [06 §8.1] and the
// closure that states them at implementable precision [06 R-DMG-01 §7].
//
// Fixture note: these stamp the plot cells' occupancy words directly through
// world.PlotCell.SetOccupantA/SetOccupantB. The internal test package cannot
// drive movement.OccupancyGrid because internal/movement imports
// internal/combat; the external twin TestStamperWritesTheCellsTheContactScan-
// Reads (contact_stamp_test.go) runs the real stamper and asserts it writes
// exactly the cells these fixtures write, and the session-level PT5/PT6 tests
// exercise the two together end to end.

const contactCellsPerSide = 32

// contactModelTop is an authored 16.16 model top: 40 world units. The walk
// itself is [fmt 3do]; the number here is a fixture value, not a retail one.
const contactModelTop = int32(40 << 16)

// newContactFixture returns a world and a terrain whose plot cells are empty:
// no occupant in either word and the no-feature sentinel 0xFFFF, so every gate
// below the unit slots in the ladder is quiet unless a test arms it.
func newContactFixture(t *testing.T) (*units.World, *world.Terrain) {
	t.Helper()
	ter := &world.Terrain{
		CellW:       contactCellsPerSide,
		CellH:       contactCellsPerSide,
		FeatureDefs: []*content.FeatureDef{{Height: 16}},
	}
	ter.Plot = make([]world.PlotCell, contactCellsPerSide*contactCellsPerSide)
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return newCombatFixtureWorld(10, nil), ter
}

// contactDef is a definition carrying only what the contact scan reads: the
// full 16.16 model top [06 R-DMG-01 §7].
func contactDef(top int32) *content.UnitDef {
	return &content.UnitDef{UnitName: "contactfixture", MaxDamage: 100, Limit: -1, ModelTopFixed: top}
}

// cellCentre is the 16.16 point at the middle of cell c on one axis, so a
// projectile placed there is unambiguously inside that cell and nowhere near
// the boundary the shift would round differently.
func cellCentre(c int32) numeric.Fixed {
	return world.CellToWorld(c) + numeric.Fixed(8*65536)
}

// stampGroundRect and stampAirRect write one identity over a footprint
// rectangle, which is what the occupancy stamper does for a mode-1 and a
// mode-2 mover respectively [04 R-COLL-01 §4].
func stampGroundRect(ter *world.Terrain, anchorX, anchorZ, fx, fz int32, id pool.Handle) {
	forEachRectCell(ter, anchorX, anchorZ, fx, fz, func(c *world.PlotCell) { c.SetOccupantA(int16(id)) })
}

func stampAirRect(ter *world.Terrain, anchorX, anchorZ, fx, fz int32, id pool.Handle) {
	forEachRectCell(ter, anchorX, anchorZ, fx, fz, func(c *world.PlotCell) { c.SetOccupantB(int16(id)) })
}

func forEachRectCell(ter *world.Terrain, anchorX, anchorZ, fx, fz int32, fn func(*world.PlotCell)) {
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			if c := ter.PlotAt(anchorX+dx, anchorZ+dz); c != nil {
				fn(c)
			}
		}
	}
}

// contactAt runs the two unit slots for a projectile point at the centre of
// cell (cx, cz) at height y, fired by side.
func contactAt(w *units.World, ter *world.Terrain, side uint8, cx, cz int32, y numeric.Fixed) pool.Handle {
	p := &Projectile{
		ShooterSide: side,
		Pos:         Vec3{X: cellCentre(cx), Y: y, Z: cellCentre(cz)},
	}
	return contactUnitInCell(p, w, ter, cx, cz)
}

// TestContactIsTheStampedRectangleNotARadius locks the XY gate: a projectile
// contacts a unit exactly when its cell is one of the cells the occupancy
// stamper wrote, up to footprintX × footprintZ of them, and misses in the
// adjacent cell however close the unit's centre is [06 R-DMG-01 §7]. The old
// build kept a 24-world-unit planar radius, which admitted the second case and
// refused the first for anything larger than a two-cell footprint.
func TestContactIsTheStampedRectangleNotARadius(t *testing.T) {
	w, ter := newContactFixture(t)
	// A 3×2 rectangle anchored at (4,4): cells x∈[4,7), z∈[4,6).
	const ax, az, fx, fz = 4, 4, 3, 2
	// The unit sits at the rectangle's north-west corner, so the far corner
	// cell is 32 world units away — well outside any planar 24 radius.
	h, err := w.Create(contactDef(contactModelTop), 1, cellCentre(ax), 0, cellCentre(az))
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(ter, ax, az, fx, fz, h)

	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			if got := contactAt(w, ter, 0, ax+dx, az+dz, 0); got != h {
				t.Fatalf("cell (%d,%d) inside the stamped rectangle: contact = %d, want %d", ax+dx, az+dz, got, h)
			}
		}
	}
	// One cell east of the rectangle, and one cell south: unstamped, so no
	// contact even though the unit's own cell is a single step away.
	if got := contactAt(w, ter, 0, ax+fx, az, 0); got != 0 {
		t.Fatalf("cell east of the rectangle contacted %d, want none", got)
	}
	if got := contactAt(w, ter, 0, ax, az+fz, 0); got != 0 {
		t.Fatalf("cell south of the rectangle contacted %d, want none", got)
	}
}

// TestContactGroundWordBeforeAirWord locks the fixed slot order: with both
// words of one cell populated by units that each pass their band, the ground
// word's unit impacts and the scan returns [06 §8.1] steps 3–4. Nothing
// prefers a nearer unit because no distance exists [06 R-DMG-01 §7].
func TestContactGroundWordBeforeAirWord(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 9, 9
	ground, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), 0, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	air, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), 0, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(ter, cx, cz, 1, 1, ground)
	stampAirRect(ter, cx, cz, 1, 1, air)

	// The point is at both units' base height, which both bands admit.
	if got := contactAt(w, ter, 0, cx, cz, 0); got != ground {
		t.Fatalf("both words populated: contact = %d, want the ground word's %d", got, ground)
	}
	// With the ground word cleared the same point takes the air word, so the
	// first assertion is an ordering, not an air-word blind spot.
	ter.PlotAt(cx, cz).SetOccupantA(0)
	if got := contactAt(w, ter, 0, cx, cz, 0); got != air {
		t.Fatalf("ground word cleared: contact = %d, want the air word's %d", got, air)
	}
}

// TestContactBandEdgesPerSlot locks the asymmetry of the two vertical bands:
// the ground word's occupants run base-to-top so their test is
// `point.Y < unit.Y + modelTop` with no lower bound, and the air word's
// occupants sit in an altitude band so their test is
// `unit.Y <= point.Y <= unit.Y + modelTop`, both ends inclusive
// [06 §8.1][06 R-DMG-01 §7]. All values are full 16.16.
func TestContactBandEdgesPerSlot(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 12, 12
	base := numeric.Fixed(100 * 65536)
	top := numeric.Fixed(contactModelTop)

	groundH, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), base, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(groundH).Y = base
	stampGroundRect(ter, cx, cz, 1, 1, groundH)

	groundCases := []struct {
		name string
		y    numeric.Fixed
		want pool.Handle
	}{
		{"one unit below the top", base + top - 1, groundH},
		{"exactly the top is strict", base + top, 0},
		{"far below the base has no lower gate", base - numeric.Fixed(1000*65536), groundH},
		{"one above the top", base + top + 1, 0},
	}
	for _, tc := range groundCases {
		if got := contactAt(w, ter, 0, cx, cz, tc.y); got != tc.want {
			t.Fatalf("ground slot %s: contact = %d, want %d", tc.name, got, tc.want)
		}
	}

	ter.PlotAt(cx, cz).SetOccupantA(0)
	airH, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), base, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(airH).Y = base
	stampAirRect(ter, cx, cz, 1, 1, airH)

	airCases := []struct {
		name string
		y    numeric.Fixed
		want pool.Handle
	}{
		{"exactly the base is inclusive", base, airH},
		{"exactly the top is inclusive", base + top, airH},
		{"one below the base", base - 1, 0},
		{"one above the top", base + top + 1, 0},
	}
	for _, tc := range airCases {
		if got := contactAt(w, ter, 0, cx, cz, tc.y); got != tc.want {
			t.Fatalf("air slot %s: contact = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestContactZeroModelTopDegeneratesTheBands locks the arithmetic the closure
// spells out for a model with no vertex above its origin: the ground band
// admits only points strictly below the base and the air band collapses to
// equality [06 R-DMG-01 §7]. No stock model is built that way; the test exists
// so a future "sensible" floor on the band is a failing test, not a silent
// invention.
func TestContactZeroModelTopDegeneratesTheBands(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 14, 14
	base := numeric.Fixed(50 * 65536)
	h, err := w.Create(contactDef(0), 1, cellCentre(cx), base, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Y = base
	stampGroundRect(ter, cx, cz, 1, 1, h)
	if got := contactAt(w, ter, 0, cx, cz, base); got != 0 {
		t.Fatalf("ground slot at the base with a zero model top contacted %d, want none", got)
	}
	if got := contactAt(w, ter, 0, cx, cz, base-1); got != h {
		t.Fatalf("ground slot one below the base contacted %d, want %d", got, h)
	}
	ter.PlotAt(cx, cz).SetOccupantA(0)
	stampAirRect(ter, cx, cz, 1, 1, h)
	if got := contactAt(w, ter, 0, cx, cz, base); got != h {
		t.Fatalf("air slot at the base with a zero model top contacted %d, want %d", got, h)
	}
	if got := contactAt(w, ter, 0, cx, cz, base+1); got != 0 {
		t.Fatalf("air slot one above the base with a zero model top contacted %d, want none", got)
	}
}

// TestContactOwnerGateHitsAlliesAndSpares OwnSide locks the one side test the
// scan performs: the unit's owner byte against the projectile's side byte.
// An allied unit is a valid contact — the resolver does not consult the
// alliance matrix — and only the shooter's own side is exempt; a shooter-less
// record carries the neutral side byte, which differs from every player slot
// and therefore contacts anyone [06 §8.1][06 R-DMG-01 §7][06 §6.5].
func TestContactOwnerGateHitsAlliesAndSparesOwnSide(t *testing.T) {
	w, ter := newContactFixture(t)
	cases := []struct {
		name  string
		owner uint8
		side  uint8
		hit   bool
	}{
		{"hostile owner", 1, 0, true},
		{"allied third party", 2, 0, true},
		{"the shooter's own side", 0, 0, false},
		{"neutral shooter-less record against side 0", 0, NeutralSide, true},
		{"neutral shooter-less record against side 9", 9, NeutralSide, true},
	}
	for i, tc := range cases {
		cx := int32(16 + i)
		cz := int32(16)
		h, err := w.Create(contactDef(contactModelTop), tc.owner, cellCentre(cx), 0, cellCentre(cz))
		if err != nil {
			t.Fatal(err)
		}
		stampGroundRect(ter, cx, cz, 1, 1, h)
		got := contactAt(w, ter, tc.side, cx, cz, 0)
		if tc.hit && got != h {
			t.Fatalf("%s: contact = %d, want %d", tc.name, got, h)
		}
		if !tc.hit && got != 0 {
			t.Fatalf("%s: contact = %d, want none", tc.name, got)
		}
	}
}

// TestContactLadderRunsUnitSlotsBeforeFeatures locks the ladder order of
// [06 §8.1]: the two unit slots are steps 3 and 4, feature resolution is step
// 6. Before this unit the build resolved feature, terrain and water first, so
// a unit standing on a feature cell could not be shot.
func TestContactLadderRunsUnitSlotsBeforeFeatures(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 20, 20
	// A real feature ordinal on the cell: the ladder's step 6 resolves it and
	// the fixture's zero height byte puts its top at 16 world units.
	ter.PlotAt(cx, cz).SetFeature(0)
	h, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), 0, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(ter, cx, cz, 1, 1, h)

	p := &Projectile{ShooterSide: 0, Pos: Vec3{X: cellCentre(cx), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}}
	hitUnit, hitFeature, isWater, isOffMap, _, bounce := checkCollision(p, wu1913Weapon(10), w, ter, nil, false)
	if hitUnit != h {
		t.Fatalf("unit on a feature cell: hitUnit = %d, want %d", hitUnit, h)
	}
	if hitFeature != nil || isWater || isOffMap || bounce {
		t.Fatalf("unit slot must return before the feature/terrain/water steps: feature=%v water=%v offmap=%v bounce=%v",
			hitFeature != nil, isWater, isOffMap, bounce)
	}
	// Clearing the occupancy word lets step 6 run, which proves the fixture's
	// cell really does carry a resolvable feature and the first assertion was
	// an ordering.
	ter.PlotAt(cx, cz).SetOccupantA(0)
	p2 := &Projectile{ShooterSide: 0, Pos: Vec3{X: cellCentre(cx), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}}
	if _, feature, _, _, _, _ := checkCollision(p2, wu1913Weapon(10), w, ter, nil, false); feature == nil {
		t.Fatal("with the cell unoccupied the feature step must resolve the cell's feature")
	}
}

// TestContactDrawsNoRandomness locks I4: the contact ladder consults no RNG
// stream. The scan has no candidate list to shuffle and no tie to break — the
// word order decides — so a draw appearing here would be an invention.
func TestContactDrawsNoRandomness(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 24, 24
	h, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), 0, cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(ter, cx, cz, 1, 1, h)
	sim := rng.NewSimulation(1)
	crt := rng.NewCRT(1)
	before, beforeCRT := sim.Draws(), crt.Draws()
	p := &Projectile{ShooterSide: 0, Pos: Vec3{X: cellCentre(cx), Y: 0, Z: cellCentre(cz)}}
	if got, _, _, _, _, _ := checkCollision(p, wu1913Weapon(10), w, ter, nil, false); got != h {
		t.Fatalf("fixture did not contact: %d", got)
	}
	if sim.Draws() != before || crt.Draws() != beforeCRT {
		t.Fatalf("draws sim %d->%d crt %d->%d, want none (I4)", before, sim.Draws(), beforeCRT, crt.Draws())
	}
}
