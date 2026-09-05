package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// floorDivInt is the sign-corrected division of [03 §2.1] I3, used here only to
// place fixture footprints the way the occupancy stamper does.
func floorDivInt(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// stampOccupancyPlane writes u's footprint rectangle into one of the plot
// cell's two occupancy words, exactly as the movement stamper does for a
// committed mover [04 R-COLL-01 §4][03 §2.2]: the anchor is
// floorDiv(position + 8 - footprint·8, 16) and the rectangle runs
// anchor .. anchor+footprint-1 [04 R-COLL-01 §1].
//
// A combat fixture assembles a world with no movement system and no
// construction service, so nothing else fills these words; the area sweep of
// [06 §9.3] reads them and nothing else, so a fixture unit that is not stamped
// is not a blast candidate at all.
func stampOccupancyPlane(t *testing.T, terrain *world.Terrain, u *units.Unit, air bool) {
	t.Helper()
	footX, footZ := int32(1), int32(1)
	if u.Def != nil {
		if u.Def.FootprintX > 0 {
			footX = u.Def.FootprintX
		}
		if u.Def.FootprintZ > 0 {
			footZ = u.Def.FootprintZ
		}
	}
	ax := int32(floorDivInt(int64(u.X.Int())+8-int64(footX)*8, 16))
	az := int32(floorDivInt(int64(u.Z.Int())+8-int64(footZ)*8, 16))
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			cell := terrain.PlotAt(ax+dx, az+dz)
			if cell == nil {
				t.Fatalf("fixture unit %d has footprint cell (%d,%d) off the map", u.Handle, ax+dx, az+dz)
			}
			if air {
				cell.SetOccupantB(int16(u.Handle))
				continue
			}
			cell.SetOccupantA(int16(u.Handle))
		}
	}
}

// stampGroundOccupancy files u in the ground plane, the word every mode-1
// mover and every building-class unit writes [04 R-COLL-01 §4].
func stampGroundOccupancy(t *testing.T, terrain *world.Terrain, u *units.Unit) {
	t.Helper()
	stampOccupancyPlane(t, terrain, u, false)
}

// stampAirOccupancy files u in the air plane, the word mode-2 movers write
// [04 R-COLL-01 §4].
func stampAirOccupancy(t *testing.T, terrain *world.Terrain, u *units.Unit) {
	t.Helper()
	stampOccupancyPlane(t, terrain, u, true)
}

// splashFixture returns an empty hundred-cell world with a plot to stamp into.
func splashFixture(t *testing.T) (*Service, *units.World, *world.Terrain) {
	t.Helper()
	// Enough per-player slots for the twenty-one candidates the full-memory
	// case needs [I5].
	w := newCombatFixtureWorld(64, nil)
	terrain := &world.Terrain{CellW: 100, CellH: 100, Plot: make([]world.PlotCell, 100*100)}
	svc := &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
	return svc, w, terrain
}

// splashUnit creates a live fixture unit at a world position with a hundred
// hit points, so one ten-point blast is legible as a ten-point drop.
func splashUnit(t *testing.T, w *units.World, def *content.UnitDef, owner uint8, x, y, z int64) *units.Unit {
	t.Helper()
	h, err := w.Create(def, owner, numeric.FixedFromInt(x), numeric.FixedFromInt(y), numeric.FixedFromInt(z))
	if err != nil {
		t.Fatalf("create fixture unit: %v", err)
	}
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	return u
}

// flashRecorder records the ordered damage-flash targets, which is the order
// [06 §9.1] step 4 applies packets in and therefore the order the area sweep
// collected its recipients.
func flashRecorder(svc *Service) *[]pool.Handle {
	var seen []pool.Handle
	svc.Events = func(ev Event) {
		if ev.Kind == EventDamageFlash {
			seen = append(seen, ev.Target)
		}
	}
	return &seen
}

// TestBlastReachesAUnitByItsFootprintNotItsCentre locks the candidate set of
// [06 §9.3]: the cell's two occupancy words name the candidates, so a unit is
// reachable on every cell its footprint holds. An eight-by-eight unit is hit
// fifty-five world units off its centre — outside the cell its centre sits in,
// inside its sixty-four-unit half footprint — with a blast of radius sixteen.
//
// The previous traversal rebuilt the live-unit slice for every blast cell and
// admitted only units whose CENTRE cell matched, so this impact did nothing.
func TestBlastReachesAUnitByItsFootprintNotItsCentre(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	def := &content.UnitDef{UnitName: "splashbig", MaxDamage: 100, Limit: -1, FootprintX: 8, FootprintZ: 8}
	u := splashUnit(t, w, def, 1, 1000, 0, 1000)
	stampGroundOccupancy(t, terrain, u)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 32, DamageDefault: 10, EdgeEffectiveness: 0}
	impact := Vec3{X: numeric.FixedFromInt(1055), Y: u.Y, Z: u.Z}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

	if u.Health != 90 {
		t.Fatalf("footprint-edge impact left health %d, want 90: the candidate set is the occupancy words, not the centre cell [06 §9.3]", u.Health)
	}
}

// TestBlastVisitsTheGroundSlotBeforeTheAirSlot locks the within-cell order of
// [06 §9.3]: "unit slot zero, unit slot one, then the feature/terrain
// candidate" — the ground occupancy word first and the air word second
// [03 §2.2][04 R-COLL-01 §4].
func TestBlastVisitsTheGroundSlotBeforeTheAirSlot(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	def := &content.UnitDef{UnitName: "splashsmall", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1}
	// The air occupant belongs to the earlier player and therefore holds the
	// LOWER pool slot, so a sweep walking the unit pool instead of the cell's
	// two words reaches it first [I5][I1].
	air := splashUnit(t, w, def, 1, 808, 0, 808)
	ground := splashUnit(t, w, def, 2, 808, 0, 808)
	stampGroundOccupancy(t, terrain, ground)
	stampAirOccupancy(t, terrain, air)
	seen := flashRecorder(svc)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 64, DamageDefault: 10, EdgeEffectiveness: 0}
	svc.ExplodeWeaponAt(w, terrain, weapon, Vec3{X: ground.X, Y: ground.Y, Z: ground.Z}, 0, 1)

	if len(*seen) != 2 {
		t.Fatalf("the cell's two occupancy words produced %d recipients, want 2 [06 §9.3]", len(*seen))
	}
	if (*seen)[0] != ground.Handle || (*seen)[1] != air.Handle {
		t.Fatalf("recipient order %v, want ground %d before air %d [06 §9.3]", *seen, ground.Handle, air.Handle)
	}
}

// TestBlastRemembersAMultiCellUnitOnce locks the deduplication memory of
// [06 §9.3]: a unit standing on two cells of the blast rectangle is a candidate
// twice and is damaged once, because the memory is consulted before the radius
// test and remembers it on the first sighting.
func TestBlastRemembersAMultiCellUnitOnce(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	def := &content.UnitDef{UnitName: "splashwide", MaxDamage: 100, Limit: -1, FootprintX: 2, FootprintZ: 1}
	u := splashUnit(t, w, def, 1, 816, 1000, 808)
	stampGroundOccupancy(t, terrain, u)
	seen := flashRecorder(svc)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 64, DamageDefault: 10, EdgeEffectiveness: 0}
	impact := Vec3{X: numeric.FixedFromInt(808), Y: numeric.FixedFromInt(1000), Z: numeric.FixedFromInt(808)}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

	if len(*seen) != 1 || u.Health != 90 {
		t.Fatalf("a two-cell unit took %d packets and ended at health %d, want one packet and 90 [06 §9.3]", len(*seen), u.Health)
	}
}

// TestBlastStopsRememberingWhenTheMemoryIsFull locks the other half of the same
// rule [06 §9.3]: the memory holds at most twenty units, an out-of-radius first
// sighting still consumes an entry, and "a candidate encountered when the
// memory is full is still processed but not remembered, so a later occurrence
// is processed again".
//
// Twenty-one one-cell units sit in the blast rectangle's first three rows a
// thousand world units below the impact, so every one of them is a candidate
// the memory records and the radius test then rejects. The two-cell victim in
// the impact row is therefore never remembered, and takes one packet per cell
// it holds.
func TestBlastStopsRememberingWhenTheMemoryIsFull(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	small := &content.UnitDef{UnitName: "splashfiller", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1}
	var fillers []*units.Unit
	for cz := int64(47); cz <= 49; cz++ {
		for cx := int64(47); cx <= 53; cx++ {
			f := splashUnit(t, w, small, 1, cx*16+8, 0, cz*16+8)
			stampGroundOccupancy(t, terrain, f)
			fillers = append(fillers, f)
		}
	}
	if len(fillers) != 21 {
		t.Fatalf("fixture built %d fillers, want 21 — one more than the memory holds [06 §9.3]", len(fillers))
	}
	wide := &content.UnitDef{UnitName: "splashwide", MaxDamage: 100, Limit: -1, FootprintX: 2, FootprintZ: 1}
	victim := splashUnit(t, w, wide, 1, 816, 1000, 808)
	stampGroundOccupancy(t, terrain, victim)
	seen := flashRecorder(svc)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 64, DamageDefault: 10, EdgeEffectiveness: 0}
	impact := Vec3{X: numeric.FixedFromInt(808), Y: numeric.FixedFromInt(1000), Z: numeric.FixedFromInt(808)}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

	for i, f := range fillers {
		if f.Health != 100 {
			t.Fatalf("filler %d took damage at health %d; it is a thousand units below the impact and fails the radius test [06 §9.3]", i, f.Health)
		}
	}
	if len(*seen) != 2 || victim.Health != 80 {
		t.Fatalf("with the memory full the two-cell victim took %d packets and ended at health %d, want two packets and 80 [06 §9.3]", len(*seen), victim.Health)
	}
}

// TestBlastMeasuresToTheDefinitionsBoundingRecord locks the box of [06 §9.3]:
// "lo = unit.pos.axis + definition.boundsMin.axis, hi = unit.pos.axis +
// definition.boundsMax.axis", whose Y half is the model-top walk over a zero
// minimum [02 R-CAT-01 §7].
//
// A target eighty world units tall is hit forty units above its base with a
// radius-sixteen blast. The impact is inside its body, so the distance is zero
// and the damage is undiminished. The retired box ended a flat sixteen units
// above the position, which put this impact twenty-four units away and outside
// the radius.
func TestBlastMeasuresToTheDefinitionsBoundingRecord(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	tall := &content.UnitDef{
		UnitName: "splashtall", MaxDamage: 100, Limit: -1,
		FootprintX: 1, FootprintZ: 1, ModelTopFixed: 80 << 16,
	}
	u := splashUnit(t, w, tall, 1, 808, 0, 808)
	stampGroundOccupancy(t, terrain, u)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 32, DamageDefault: 10, EdgeEffectiveness: 0}
	impact := Vec3{X: u.X, Y: numeric.FixedFromInt(40), Z: u.Z}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

	if u.Health != 90 {
		t.Fatalf("an impact inside an eighty-unit-tall body left health %d, want 90: the box is the definition's bounding record [06 §9.3][02 R-CAT-01 §7]", u.Health)
	}
}

// TestBlastDoesNotInventHeightForAFlatTarget is the converse: a definition
// whose model-top walk reported zero has no vertical extent at all
// [02 R-CAT-01 §7], so an impact twenty world units above it is twenty units
// away and a radius-sixteen blast misses. The retired sixteen-unit box would
// have put the same impact four units away and inside.
func TestBlastDoesNotInventHeightForAFlatTarget(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	flat := &content.UnitDef{
		UnitName: "splashflat", MaxDamage: 100, Limit: -1,
		FootprintX: 1, FootprintZ: 1, ModelTopFixed: 0,
	}
	u := splashUnit(t, w, flat, 1, 808, 0, 808)
	stampGroundOccupancy(t, terrain, u)

	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 32, DamageDefault: 10, EdgeEffectiveness: 0}
	impact := Vec3{X: u.X, Y: numeric.FixedFromInt(20), Z: u.Z}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

	if u.Health != 100 {
		t.Fatalf("a flat target took %d damage from an impact twenty units above it; no height is invented [06 §9.3][02 R-CAT-01 §7]", 100-u.Health)
	}
}

// TestBlastRadiusComparisonIsStrict locks the acceptance test of [06 §9.3]: "a
// recipient is accepted only when that value is STRICTLY less than R". Edge
// effectiveness one holds the falloff at one, so the only thing under test is
// the comparison. The flat target's box gives a distance equal to the impact's
// height above it.
func TestBlastRadiusComparisonIsStrict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height int64
		want   int32
	}{
		{"one below the radius", 15, 90},
		{"exactly the radius", 16, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, w, terrain := splashFixture(t)
			flat := &content.UnitDef{
				UnitName: "splashflat", MaxDamage: 100, Limit: -1,
				FootprintX: 1, FootprintZ: 1, ModelTopFixed: 0,
			}
			u := splashUnit(t, w, flat, 1, 808, 0, 808)
			stampGroundOccupancy(t, terrain, u)

			weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 32, DamageDefault: 10, EdgeEffectiveness: 1}
			impact := Vec3{X: u.X, Y: numeric.FixedFromInt(tc.height), Z: u.Z}
			svc.ExplodeWeaponAt(w, terrain, weapon, impact, 0, 1)

			if u.Health != tc.want {
				t.Fatalf("distance %d against radius 16 left health %d, want %d: the test is strictly less than [06 §9.3]", tc.height, u.Health, tc.want)
			}
		})
	}
}
