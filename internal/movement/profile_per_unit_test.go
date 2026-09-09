package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// flatTerrain builds a flat map at the given byte height.
func flatTerrain(t *testing.T, w, h int32, height uint8) *world.Terrain {
	t.Helper()
	tr := &world.Terrain{CellW: w, CellH: h, SeaLevel: 0, Plot: make([]world.PlotCell, w*h)}
	for i := range tr.Plot {
		tr.Plot[i].SetFeature(world.PlotFeatureNone)
		tr.Plot[i].SetHeight(height)
		tr.Plot[i].SetMinHeight(height)
		tr.Plot[i].SetMaxHeight(height)
	}
	return tr
}

// profileTestWorld keeps one units.World across a test's units so handles are
// distinct.
var profileTestWorld *units.World

// newTestUnit spawns a unit whose definition names the given movement class.
func newTestUnit(t *testing.T, name, class string, fx, fz int32) *units.Unit {
	t.Helper()
	if profileTestWorld == nil {
		profileTestWorld = newMovementFixtureWorld(32)
	}
	def := &content.UnitDef{
		UnitName:      name,
		MovementClass: class,
		FootprintX:    fx,
		FootprintZ:    fz,
		MaxVelocity:   3 * 65536,
		TurnRate:      500,
		MaxDamage:     100,
	}
	x := world.CellToWorld(2)
	z := world.CellToWorld(2)
	h, err := profileTestWorld.Create(def, 0, x, 0, z)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return profileTestWorld.Unit(h)
}

// Two movement classes in one world must not share a profile. A session-wide
// profile pathed a ship, a hover and a Krogoth as the same 1x1 ground scout:
// invalid passability, wrong route bias, and occupancy stamps too small for
// large units.
func TestTwoMovementClassesInOneWorldKeepTheirOwnProfiles(t *testing.T) {
	terrain := flatTerrain(t, 64, 64, 100)
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, grid)
	sys.SetClasses(map[string]*content.MovementClass{
		// A 1x1 ground scout that cannot enter water at all.
		"tank1": {FootprintX: 1, FootprintZ: 1, MaxSlope: 50, BadSlope: 25},
		// A 4x4 ship that lives in deep water.
		"boat4": {FootprintX: 4, FootprintZ: 4, MinWaterDepth: 10, MaxWaterDepth: 255},
	})

	scout := newTestUnit(t, "ARMFLEA", "TANK1", 1, 1)
	ship := newTestUnit(t, "ARMTSHIP", "BOAT4", 4, 4)
	sys.EnsureUnit(scout)
	sys.EnsureUnit(ship)

	scoutProfile := sys.ProfileFor(scout.Handle)
	shipProfile := sys.ProfileFor(ship.Handle)
	if scoutProfile.FootPrintX != 1 || shipProfile.FootPrintX != 4 {
		t.Fatalf("footprints collapsed to one profile: scout %d ship %d",
			scoutProfile.FootPrintX, shipProfile.FootPrintX)
	}
	if scoutProfile.MinWaterDepth == shipProfile.MinWaterDepth {
		t.Fatalf("water thresholds collapsed: both %d", scoutProfile.MinWaterDepth)
	}

	// Occupancy is stamped at each unit's own footprint, so the ship really
	// occupies a 4x4 block.
	if got := sys.Collisions[ship.Handle].FootPrintX; got != 4 {
		t.Fatalf("ship collision footprint %d, want 4", got)
	}
	if got := sys.Collisions[scout.Handle].FootPrintX; got != 1 {
		t.Fatalf("scout collision footprint %d, want 1", got)
	}

	// And the path bias each unit searches under follows its own footprint.
	if b := int32(shipProfile.FootPrintX / 2); b != 2 {
		t.Fatalf("ship path bias %d, want 2", b)
	}
}

// A definition naming a class the table does not hold keeps its complete FBI
// scratch profile. Retail's degraded path is not a content error, and each
// distinct scratch profile gets its own layer identity [02 §5][04 §6.1].
func TestMissingMovementClassUsesUnitScratch(t *testing.T) {
	terrain := flatTerrain(t, 32, 32, 100)
	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	sys.SetClasses(map[string]*content.MovementClass{})

	first := newTestUnit(t, "ARMFLEA", "NOSUCHCLASS", 1, 1)
	first.Def.MaxWaterDepth, first.Def.MinWaterDepth = 1, -10000
	first.Def.MaxSlope, first.Def.BadSlope = 20, 10
	first.Def.MaxWaterSlope, first.Def.BadWaterSlope = 30, 15
	second := newTestUnit(t, "ARMFLEA2", "NOSUCHCLASS2", 1, 1)
	second.Def.MaxWaterDepth, second.Def.MinWaterDepth = 20, -10000
	second.Def.MaxSlope, second.Def.BadSlope = 40, 20
	second.Def.MaxWaterSlope, second.Def.BadWaterSlope = 50, 25
	sys.EnsureUnit(first)
	sys.EnsureUnit(second)

	wantFirst := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 1, MinWaterDepth: -10000, MaxSlope: 20, BadSlope: 10, MaxWaterSlope: 30, BadWaterSlope: 15}
	wantSecond := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 20, MinWaterDepth: -10000, MaxSlope: 40, BadSlope: 20, MaxWaterSlope: 50, BadWaterSlope: 25}
	if got := sys.ProfileFor(first.Handle); got != wantFirst {
		t.Fatalf("first unresolved profile %+v, want %+v", got, wantFirst)
	}
	if got := sys.ProfileFor(second.Handle); got != wantSecond {
		t.Fatalf("second unresolved profile %+v, want %+v", got, wantSecond)
	}
	if sys.profileNames[first.Handle] == sys.profileNames[second.Handle] {
		t.Fatalf("distinct scratch profiles aliased layer identity %q", sys.profileNames[first.Handle])
	}
	// At a 20-deep water cell, the two unresolved units classify differently.
	terrain.SeaLevel = 120
	for _, c := range terrain.Plot {
		c.SetHeight(100)
		c.SetMinHeight(100)
		c.SetMaxHeight(100)
	}
	if sys.ProfileFor(first.Handle).IsPassable(terrain, 2, 2) {
		t.Fatal("first unresolved scratch profile admitted water deeper than MaxWaterDepth")
	}
	if !sys.ProfileFor(second.Handle).IsPassable(terrain, 2, 2) {
		t.Fatal("second unresolved scratch profile rejected water within MaxWaterDepth")
	}
}

// A definition naming no class at all retains its complete unit-local scratch
// profile. Aircraft and buildings do not normally use the ground lattice, but
// consumers that do must still see the FBI record [02 §5][04 §6.1].
func TestNoMovementClassUsesUnitScratch(t *testing.T) {
	terrain := flatTerrain(t, 32, 32, 100)
	scratch := Profile{FootPrintX: 3, FootPrintZ: 3, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(terrain, Profile{}, NewOccupancyGrid())

	u := newTestUnit(t, "ARMFIG", "", 3, 3)
	u.Def.MaxWaterDepth = scratch.MaxWaterDepth
	u.Def.MinWaterDepth = scratch.MinWaterDepth
	u.Def.MaxSlope = int32(scratch.MaxSlope)
	u.Def.BadSlope = int32(scratch.BadSlope)
	u.Def.MaxWaterSlope = int32(scratch.MaxWaterSlope)
	u.Def.BadWaterSlope = int32(scratch.BadWaterSlope)
	sys.EnsureUnit(u)

	if sys.ProfileFor(u.Handle) != scratch {
		t.Fatalf("profile %+v, want the unit scratch profile %+v", sys.ProfileFor(u.Handle), scratch)
	}
}

// The search callback must read the requesting unit's profile, not a shared
// one. A ship's request over water is admitted; the scout's is not.
func TestSearchPassabilityFollowsTheRequestingUnit(t *testing.T) {
	// Dry land at 200 with sea level at 120, and a water channel carved down to
	// 100 along x == 8 — 20 units deep.
	terrain := flatTerrain(t, 32, 32, 200)
	terrain.SeaLevel = 120
	for z := int32(0); z < terrain.CellH; z++ {
		c := &terrain.Plot[z*terrain.CellW+8]
		c.SetHeight(100)
		c.SetMinHeight(100)
		c.SetMaxHeight(100)
	}

	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	sys.SetClasses(map[string]*content.MovementClass{
		// A ground class that can wade at most 1 deep. Unauthored keys carry
		// the startup template, so a class that must never fire the shallow
		// gate leaves minwaterdepth out (template −10000) and authors a small
		// positive maxwaterdepth [02 §5 "Movement class record"][04 §6.1
		// R-DOC04-A].
		"tank1": {FootprintX: 1, FootprintZ: 1, MaxSlope: 255, BadSlope: 255, MaxWaterDepth: 1, MinWaterDepth: -10000},
		// A ship class that needs at least 5 of water under it; maxwaterdepth
		// omitted carries the template 10000 (no depth ceiling).
		"boat4": {FootprintX: 1, FootprintZ: 1, MaxSlope: 255, BadSlope: 255, MinWaterDepth: 5, MaxWaterDepth: 10000},
	})
	scout := newTestUnit(t, "ARMFLEA", "TANK1", 1, 1)
	ship := newTestUnit(t, "ARMTSHIP", "BOAT4", 1, 1)
	sys.EnsureUnit(scout)
	sys.EnsureUnit(ship)

	water := path.Cell{X: 8, Z: 4}
	scoutOK := sys.ProfileFor(scout.Handle).IsPassable(terrain, water.X, water.Z)
	shipOK := sys.ProfileFor(ship.Handle).IsPassable(terrain, water.X, water.Z)
	if scoutOK == shipOK {
		t.Fatalf("the water cell reads the same to a tank and a ship (%v); "+
			"one shared profile is exactly this bug", scoutOK)
	}
	if scoutOK {
		t.Fatalf("a ground class limited to 1 depth entered 20-deep water")
	}
	if !shipOK {
		t.Fatalf("a ship class could not enter water")
	}
}
