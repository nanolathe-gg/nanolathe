package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestLandingCoarseAcceptIsTheOwnerSlotBit locks the coarse early accept of
// [04 R-AIR-01 §6a] as corrected by [04 R-AIR-01 §14.2]: the bit tested is the
// AIRCRAFT OWNER's slot bit in the mapping word grid of [03 R-LAYER §1], and a
// tile that is NOT mapped for that owner is landable outright.
//
// The index arithmetic is locked with it, including the asymmetry §6a states
// explicitly: both axes add `fx >> 2`, the Z term does NOT use `fz`.
func TestLandingCoarseAcceptIsTheOwnerSlotBit(t *testing.T) {
	// An unmapped tile for owner 3 — bit 3 clear — accepts; the same word with
	// bit 3 set does not, and the walk runs.
	const unmapped uint16 = 0x03F7 // every slot but 3
	if airMappingBitSet(unmapped, 3) {
		t.Fatalf("owner 3 bit reported set in %#04x", unmapped)
	}
	if !airMappingBitSet(unmapped|1<<3, 3) {
		t.Fatalf("owner 3 bit reported clear in %#04x", unmapped|1<<3)
	}
	// Only the ten usable slot bits exist [03 R-LAYER §1].
	if airMappingBitSet(0xFFFF, 10) {
		t.Fatal("slot 10 has no bit in the mapping word")
	}

	// tile = (cellX>>1 + fx>>2, cellZ>>1 + fx>>2), with fx on BOTH axes.
	if tx, tz := airMappingTile(9, 5, 4); tx != 5 || tz != 3 {
		t.Fatalf("mapping tile = (%d,%d), want (5,3)", tx, tz)
	}
	// A wider footprint shifts both terms by the same quarter, which is the
	// asymmetry: fz never enters.
	if tx, tz := airMappingTile(0, 0, 8); tx != 2 || tz != 2 {
		t.Fatalf("mapping tile with fx=8 = (%d,%d), want (2,2)", tx, tz)
	}

	// With no binding there is no grid, and landable falls through to the full
	// per-cell walk rather than early-accepting on an invented word.
	if _, ok := (*System)(nil).landingMappingWord(nil, 0, 0, 1); ok {
		t.Fatal("landingMappingWord reported a grid; none is bound")
	}
}

// TestLandingCoarseAcceptShortCircuitsTheWalk exercises the bound port end to
// end: a position the per-cell walk REFUSES (water under a non-amphibious
// aircraft, the substantive rule of [04 R-AIR-01 §6a]) is landable anyway when
// the tile under the anchor is not mapped for the aircraft's owner, and is
// refused again once that owner's bit is present [04 R-AIR-01 §14.2].
func TestLandingCoarseAcceptShortCircuitsTheWalk(t *testing.T) {
	sys, _, u := airFixture(t)
	if u.Def.Amphibious {
		t.Fatal("fixture aircraft is amphibious; the water rule would not apply")
	}
	// Sink one cell below sea level so the walk's depth test refuses it.
	const cell int32 = 12
	x, z := world.CellToWorld(cell), world.CellToWorld(cell)
	anchorX, anchorZ := world.PlacementAnchor(x, z, 1, 1)
	idx := anchorZ*sys.Terrain.CellW + anchorX
	sys.Terrain.Plot[idx].SetHeight(0)
	sys.Terrain.Plot[idx].SetMinHeight(0)
	sys.Terrain.Plot[idx].SetMaxHeight(0)

	if sys.landable(u, x, z) {
		t.Fatal("water cell is landable with no mapping grid; the walk should refuse it")
	}

	wantX, wantZ := airMappingTile(anchorX, anchorZ, 1)
	var word uint16
	var sawX, sawZ int32
	orders.QueueOfUnit(u).Binding().World = &orders.WorldQueryAdapter{
		MappingWord: func(tileX, tileZ int32) (uint16, bool) {
			sawX, sawZ = tileX, tileZ
			return word, true
		},
	}

	// Unmapped for owner 0: landable outright, with no feature, yard,
	// occupancy, depth or slope test at all.
	word = 0x03FE // every slot but 0
	if !sys.landable(u, x, z) {
		t.Fatal("an unmapped tile must be landable without the walk [04 R-AIR-01 §14.2]")
	}
	if sawX != wantX || sawZ != wantZ {
		t.Fatalf("read tile (%d,%d), want (%d,%d)", sawX, sawZ, wantX, wantZ)
	}

	// Mapped for owner 0: the walk runs, and the water rule refuses.
	word = 0x0001
	if sys.landable(u, x, z) {
		t.Fatal("a mapped tile must run the walk, which refuses water under a non-amphibious aircraft")
	}
}

// guardSeekFixture builds a seeker aircraft plus one candidate of the given
// owner and flight capability, both registered with the same system.
func guardSeekFixture(t *testing.T, candOwner uint8, candFlies bool, declares func(from, toward uint8) bool) (*System, *units.Unit, *units.Unit) {
	t.Helper()
	sys, w, seeker := airFixture(t)
	seeker.Def.SightDistance = 400
	// The alliance row A read the visitor needs reaches movement through the
	// queue binding's world adapter, the same seam the air-base registry's
	// rebuild uses [05 R-SHARE-01 §1].
	if q := orders.QueueOfUnit(seeker); q != nil && q.Binding() != nil {
		q.Binding().World = &orders.WorldQueryAdapter{DeclaresAlliance: declares}
	}

	def := setScratchMovement(&content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("guardward")},
		UnitName:         "guardward",
		CanFly:           candFlies,
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		MaxVelocity:      65536,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127})
	x, z := world.CellToWorld(9), world.CellToWorld(8)
	h, err := w.Create(def, candOwner, x, sys.Terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create ward: %v", err)
	}
	ward := w.Unit(h)
	sys.EnsureUnit(ward)
	return sys, seeker, ward
}

// TestGuardCandidateVisitorAdmitsAlliesOnly locks the visitor's three clauses
// with the polarity [04 R-AIR-01 §14.4] established: the CANDIDATE owner's
// alliance row A indexed by the SEEKER's slot must be nonzero, the candidate
// must not be `canfly`, and it must not be the seeker itself.
func TestGuardCandidateVisitorAdmitsAlliesOnly(t *testing.T) {
	// Row A: owner 1 declares toward owner 0; owner 2 declares toward nobody.
	declares := func(from, toward uint8) bool { return from == toward || (from == 1 && toward == 0) }

	sys, seeker, ward := guardSeekFixture(t, 0, false, declares)
	if !airGuardVisitorAdmits(seeker, ward, declares) {
		t.Fatal("a ground unit of the seeker's own side was refused")
	}
	if got := sys.airGuardCandidate(seeker); got != ward {
		t.Fatalf("enumeration returned %v, want the own-side ground ward", got)
	}
	if airGuardVisitorAdmits(seeker, seeker, declares) {
		t.Fatal("the seeker admitted itself")
	}

	// An ally by declaration is admitted; the row is read from the CANDIDATE's
	// side, so owner 1's declaration toward slot 0 is what counts.
	sysAlly, allySeeker, allyWard := guardSeekFixture(t, 1, false, declares)
	if !airGuardVisitorAdmits(allySeeker, allyWard, declares) {
		t.Fatal("a declared ally's ground unit was refused")
	}
	if got := sysAlly.airGuardCandidate(allySeeker); got != allyWard {
		t.Fatalf("enumeration returned %v, want the allied ground ward", got)
	}

	// An enemy is refused: owner 2 declares nothing toward slot 0.
	sysFoe, foeSeeker, foeWard := guardSeekFixture(t, 2, false, declares)
	if airGuardVisitorAdmits(foeSeeker, foeWard, declares) {
		t.Fatal("an enemy ground unit was admitted")
	}
	if got := sysFoe.airGuardCandidate(foeSeeker); got != nil {
		t.Fatalf("enumeration returned %v for an enemy-only field, want none", got)
	}

	// A flyer is refused whatever its side.
	sysAir, airSeeker, airWard := guardSeekFixture(t, 0, true, declares)
	if airGuardVisitorAdmits(airSeeker, airWard, declares) {
		t.Fatal("a canfly candidate was admitted")
	}
	if got := sysAir.airGuardCandidate(airSeeker); got != nil {
		t.Fatalf("enumeration returned %v for a flyers-only field, want none", got)
	}

	// Out of `sightdistance` is not enumerated at all.
	sysFar, farSeeker, _ := guardSeekFixture(t, 0, false, declares)
	farSeeker.Def.SightDistance = 1
	if got := sysFar.airGuardCandidate(farSeeker); got != nil {
		t.Fatalf("enumeration returned %v beyond sightdistance, want none", got)
	}
}

// TestAirToAirCloseRangeArmRearmsWithoutAMarker locks the arm [04 R-AIR-01 §8]
// omits and [04 R-AIR-01 §14.5] supplies: arrival bits clear, counter below
// 0x5A, range to the target at or below 0xA0 world units — no new payload is
// installed, the deadline is tick+45, the gate takes 0x100E8, and the leg holds.
func TestAirToAirCloseRangeArmRearmsWithoutAMarker(t *testing.T) {
	sys, w, u := airFixture(t)

	def := setScratchMovement(&content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("dogfightfoe")},
		UnitName:         "dogfightfoe",
		CanFly:           true,
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		MaxVelocity:      2 * 65536,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127})
	// 64 world units east of the seeker, comfortably inside the 0xA0 test.
	x := u.X + numeric.FixedFromInt(64)
	h, err := w.Create(def, 1, x, u.Y, u.Z)
	if err != nil {
		t.Fatalf("create foe: %v", err)
	}
	foe := w.Unit(h)
	sys.EnsureUnit(foe)

	n := pushAirOrder(t, u, "AirToAir", foe.X, foe.Z)
	n.Target = foe.Handle
	n.Phase = 1
	n.Param1 = 0x2D // below 0x5A, so the give-up arm is not the one taken
	n.DynamicGate = 0

	const tick = 700
	code := sys.legAirToAir(u, n, 0, tick) // arrival bits clear
	if code != 2 {
		t.Fatalf("code = %d, want 2 (hold)", code)
	}
	if n.DynamicGate&airLegGateStrike != airLegGateStrike {
		t.Fatalf("gate = %#x, want %#x OR-ed in", n.DynamicGate, airLegGateStrike)
	}
	if got := n.Deadline; got != tick+45 {
		t.Fatalf("deadline = %d, want %d", got, tick+45)
	}
	// No payload was installed: whatever was bound stays bound, and nothing was
	// bound here [04 R-AIR-01 §14.5].
	if c := sys.FlightCommandFor(u.Handle, u); c != nil && c.Payload != nil {
		t.Fatalf("close-range arm installed a payload %T, want none", c.Payload)
	}
	// The counter still advanced, as the arm's shared prologue does.
	if n.Param1 == 0x2D {
		t.Fatal("the scratch counter was not updated before the range test")
	}
}
