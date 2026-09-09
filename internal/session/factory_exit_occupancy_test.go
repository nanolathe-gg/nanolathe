package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Exercise the real unit/mover writers and core restore before completion.
// A restored carried aircraft keeps its ground footprint through detach;
// the ordinary mode transition then releases the pad [04 R-FAC-02 §3, §6]
// [08 R-SAVE-02 §6, §8]. Terrain/account file loading is outside this fixture.
func TestRestoredFactoryAircraftRetainsPadUntilModeChange(t *testing.T) {
	src, ids := newRestoreCoreFixture(t, 2)
	terrain := completedOccupancySessionTerrain(16, 16)
	profile := movement.Profile{FootPrintX: 2, FootPrintZ: 2}
	src.Movement = movement.NewSystem(terrain, profile, movement.NewOccupancyGrid())
	src.Movement.BindWorld(src.Units)
	builder := src.Units.Unit(ids[0].handle)
	product := src.Units.Unit(ids[1].handle)
	bd, pd := *builder.Def, *product.Def
	bd.BMCode, bd.WorkerTime = 0, 750
	bd.FootprintX, bd.FootprintZ = 2, 2
	pd.BMCode, pd.CanFly, pd.BuildTime = 1, true, 100
	pd.FootprintX, pd.FootprintZ = 2, 2
	builder.Def, product.Def = &bd, &pd
	builder.HasMover = false
	product.HasMover = true
	builder.Flags |= units.BuildingClassStatus
	builder.X, builder.Z = world.CellToWorld(2), world.CellToWorld(2)
	product.X, product.Z = world.CellToWorld(6), world.CellToWorld(6)
	product.Remaining, product.Health = .25, 50
	for _, u := range []*units.Unit{builder, product} {
		u.FootprintSizeX, u.FootprintSizeZ = 2, 2
		src.Movement.EnsureUnit(u)
		anchor := src.Movement.Collisions[u.Handle].CachedAnchor
		u.CachedOccupancyX, u.CachedOccupancyZ = int16(anchor.X), int16(anchor.Z)
	}
	if !movement.AttachFactoryProduct(src.Units, builder.Handle, product.Handle, 0) {
		t.Fatal("attach source product")
	}
	stable := func(h pool.Handle) (uint16, bool) {
		for _, id := range ids {
			if h == id.handle {
				return id.stableID, true
			}
		}
		return 0, false
	}
	image := &save.BattleImage{}
	for _, id := range ids {
		u := src.Units.Unit(id.handle)
		data, err := units.RetailUnitImage(u, 0, stable, stable, units.RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		image.Units.Records = append(image.Units.Records, save.UnitRecord{StableID: id.stableID, Data: data})
	}
	mover, err := src.Movement.RetailMoverImage(product.Handle)
	if err != nil {
		t.Fatal(err)
	}
	image.Units.Other = []save.RawBox{{Name: unitBoxName(ids[1].stableID, "mob"), Data: mover}}
	dst, dstIDs := newRestoreCoreFixture(t, 2)
	dst.Units.Unit(dstIDs[0].handle).Def = &bd
	dst.Units.Unit(dstIDs[1].handle).Def = &pd
	dstTerrain := completedOccupancySessionTerrain(16, 16)
	dst.Movement = movement.NewSystem(dstTerrain, profile, movement.NewOccupancyGrid())
	if err := RestoreRetailBattleCore(&RetailBattleStage{Session: dst, StableUnit: stableUnitMap(dstIDs), Image: image}); err != nil {
		t.Fatal(err)
	}
	builder, product = dst.Units.Unit(dstIDs[0].handle), dst.Units.Unit(dstIDs[1].handle)
	if product.Remaining != .25 || product.Attachment.Carrier != builder.Handle {
		t.Fatalf("restored construction relation: remaining=%v carrier=%d", product.Remaining, product.Attachment.Carrier)
	}
	coll := dst.Movement.Collisions[product.Handle]
	if coll == nil {
		t.Fatal("restored product has no collision owner")
	}
	anchor := coll.CachedAnchor
	assertPad := func(ground, air int16) {
		t.Helper()
		for z := anchor.Z; z < anchor.Z+2; z++ {
			for x := anchor.X; x < anchor.X+2; x++ {
				cell := dstTerrain.PlotAt(x, z)
				if cell.OccupantA() != ground || cell.OccupantB() != air {
					t.Fatalf("pad (%d,%d): ground=%d air=%d, want %d/%d", x, z, cell.OccupantA(), cell.OccupantB(), ground, air)
				}
			}
		}
	}
	assertPad(int16(product.Handle), 0)
	build := construction.NewService(dstTerrain, nil, dst.Units, dst.Econ)
	build.Movement = dst.Movement
	if !build.Assist(builder, product, 100) {
		t.Fatal("final construction quantum refused")
	}
	if product.Remaining != 0 || product.Health != 75 || product.Attachment.Carrier != 0 {
		t.Fatalf("completion: remaining=%v health=%d carrier=%d", product.Remaining, product.Health, product.Attachment.Carrier)
	}
	// No intervening CompleteUnit or mover visit can hide a transient clear.
	assertPad(int16(product.Handle), 0)
	if !dst.Movement.SetMoverMode(product, 2) {
		t.Fatal("ordinary takeoff mode edge did not run")
	}
	assertPad(0, int16(product.Handle))
	dst.Movement.ForgetUnit(product.Handle)
	assertPad(0, 0)
}
