package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Constructor stamps precede the saved body. Rebuilding must discard their
// yard selection and use the saved committed anchor, even for an unfinished
// structure without a mover [08 R-SAVE-02 §6, §11][04 R-COLL-01 §4].
func TestRetailRestoreReplacesConstructorYard(t *testing.T) {
	for _, remaining := range []float32{0, .5} {
		for _, open := range []bool{false, true} {
			for _, shift := range []int16{0, 4} {
				t.Run(fmt.Sprintf("remaining=%g/open=%t/shift=%d", remaining, open, shift), func(t *testing.T) {
					s, ids := newRestoreCoreFixture(t, 1)
					u := s.Units.Unit(ids[0].handle)
					terrain := completedOccupancySessionTerrain(16, 16)
					u.Def = completedOccupancyBuildingDef("restored-yard", 2, 2, "cOoc")
					u.X, u.Z = world.CellToWorld(4), world.CellToWorld(4)
					u.HasMover = false
					u.Remaining, u.YardOpen = remaining, open
					u.CachedOccupancyX, u.CachedOccupancyZ = 3+shift, 3+shift
					u.FootprintSizeX, u.FootprintSizeZ = 2, 2
					resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == u.Handle }
					body, err := units.RetailUnitImage(u, 0, resolve, resolve, units.RetailUnitWriterScratch{})
					if err != nil {
						t.Fatal(err)
					}
					s.Movement = movement.NewSystem(terrain, movement.Profile{}, movement.NewOccupancyGrid())
					s.Movement.BindWorld(s.Units)
					s.Build = construction.NewService(terrain, nil, s.Units, s.Econ)
					s.Build.Movement = s.Movement
					// Model the pre-COB constructor stamp that production staging
					// installs before RetailUnitBase replaces the logical yard bit.
					u.YardOpen = !open
					if err := s.Build.RegisterBuildingPlacement(u); err != nil {
						t.Fatal(err)
					}
					image := &save.BattleImage{Units: save.UnitImage{Records: []save.UnitRecord{{StableID: ids[0].stableID, Data: body}}}}
					if err := RestoreRetailBattleCore(&RetailBattleStage{Session: s, StableUnit: stableUnitMap(ids), Image: image}); err != nil {
						t.Fatal(err)
					}
					yard, err := world.ParseYardMap(u.Def.YardMap, 2, 2)
					if err != nil {
						t.Fatal(err)
					}
					for z := int32(1); z < 12; z++ {
						for x := int32(1); x < 12; x++ {
							var want int16
							dx, dz := x-int32(u.CachedOccupancyX), z-int32(u.CachedOccupancyZ)
							if dx >= 0 && dx < 2 && dz >= 0 && dz < 2 && yard[dz*2+dx].Selects(open) {
								want = int16(u.Handle)
							}
							grid, _ := s.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z})
							if plot := terrain.PlotAt(x, z).OccupantA(); plot != want || grid != int(want) {
								t.Fatalf("cell (%d,%d) plot=%d grid=%d, want %d", x, z, plot, grid, want)
							}
						}
					}
					rect, ok := s.Build.PlacementForProduct(u.Handle)
					if !ok || rect.MinX() != int32(u.CachedOccupancyX) || rect.MinZ() != int32(u.CachedOccupancyZ) {
						t.Fatalf("construction retained placement=%v, present=%t", rect, ok)
					}
					if !s.Build.YardOpenTransaction(u, !open) {
						t.Fatal("restored yard refused its first unobstructed port-18 transition")
					}
					for dz := int32(0); dz < 2; dz++ {
						for dx := int32(0); dx < 2; dx++ {
							cell := terrain.PlotAt(rect.MinX()+dx, rect.MinZ()+dz)
							if got := cell.OccupantA() == int16(u.Handle); got != yard[dz*2+dx].Selects(!open) {
								t.Fatal("port-18 transition did not use the retained saved anchor")
							}
						}
					}
					s.Build.ReleasePlacement(u.Handle)
					for i, cell := range terrain.Plot {
						grid, _ := s.Movement.Grid.OccupantAt(movement.Cell{X: int32(i) % terrain.CellW, Z: int32(i) / terrain.CellW})
						if cell.OccupantA() == int16(u.Handle) || grid == int(u.Handle) {
							t.Fatal("restored yard left occupied cells after teardown")
						}
					}
				})
			}
		}
	}
}

// A saved yard may occupy the position where another staged constructor
// temporarily stamped. Release the entire constructor pass first, so replay
// cannot manufacture an overlap event [08 R-SAVE-02 §11][04 R-COLL-01 §4].
func TestRetailRestoreReleasesAllConstructorYardsBeforeRebuild(t *testing.T) {
	s, ids := newRestoreCoreFixture(t, 2)
	terrain := completedOccupancySessionTerrain(16, 16)
	s.Movement = movement.NewSystem(terrain, movement.Profile{}, movement.NewOccupancyGrid())
	s.Movement.BindWorld(s.Units)
	s.Build = construction.NewService(terrain, nil, s.Units, s.Econ)
	s.Build.Movement = s.Movement
	image := &save.BattleImage{}
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), s.Units.Unit(h) != nil }
	for i, id := range ids {
		u := s.Units.Unit(id.handle)
		u.Def = completedOccupancyBuildingDef("restored-yard", 2, 2, "o")
		u.X, u.Z = world.CellToWorld(int32(4+i*4)), world.CellToWorld(4)
		u.HasMover, u.Remaining = false, .5
		u.CachedOccupancyX, u.CachedOccupancyZ = int16(7-i*4), 3
		u.FootprintSizeX, u.FootprintSizeZ = 2, 2
		body, err := units.RetailUnitImage(u, 0, resolve, resolve, units.RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		image.Units.Records = append(image.Units.Records, save.UnitRecord{StableID: id.stableID, Data: body})
		if err := s.Build.RegisterBuildingPlacement(u); err != nil {
			t.Fatal(err)
		}
	}
	if err := RestoreRetailBattleCore(&RetailBattleStage{Session: s, StableUnit: stableUnitMap(ids), Image: image}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		u := s.Units.Unit(id.handle)
		if u.Pending != 0 {
			t.Fatalf("constructor footprint generated restored overlap notice %#x", u.Pending)
		}
		if got := terrain.PlotAt(int32(u.CachedOccupancyX), int32(u.CachedOccupancyZ)).OccupantA(); got != int16(u.Handle) {
			t.Fatalf("saved yard owner=%d, want %d", got, u.Handle)
		}
	}
}
