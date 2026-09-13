package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The corpse origin is the victim's committed occupancy pair; its extent is
// the selected feature's, and its rendered position stays exact [05 R-FEAT-01
// §13]. A centre-cell stamp wrongly occupies the adjacent movers' cells.
func TestFactoryCorpseLeavesAdjacentMoversFree(t *testing.T) {
	for _, size := range []int32{2, 3} {
		t.Run(fmt.Sprintf("footprint_%d", size), func(t *testing.T) {
			s := newLoopTestSession(t, 0)
			wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "factorywreck"}, FootprintX: 6, FootprintZ: 4, Blocking: true, Damage: 100, Object: "factorywreck"}
			s.Catalog.Features[wreck.CanonicalKey] = wreck
			factory := *s.Catalog.Units["armcom"]
			factory.BMCode, factory.CanMove, factory.MaxVelocity = 0, false, 0
			factory.FootprintX, factory.FootprintZ, factory.Corpse = 6, 4, wreck.CanonicalKey
			factory.YardMap = "oooooooooooooooooooooooo"
			fh, err := s.Units.Create(&factory, 0, numeric.FixedFromInt(192), numeric.FixedFromInt(10), numeric.FixedFromInt(160))
			if err != nil {
				t.Fatal(err)
			}
			victim := s.Units.Unit(fh)
			victim.Remaining = 0
			s.Movement.EnsureUnit(victim)
			origin, _, _, ok := s.Movement.CommittedFootprint(fh)
			if !ok {
				t.Fatal("factory has no committed origin")
			}
			mover := *s.Catalog.Units["armcom"]
			mover.BMCode, mover.CanMove, mover.Corpse = 1, true, ""
			mover.FootprintX, mover.FootprintZ = size, size
			mover.MovementClass = ""
			mover.MaxWaterDepth, mover.MinWaterDepth = 10000, -10000
			mover.MaxSlope, mover.BadSlope, mover.MaxWaterSlope, mover.BadWaterSlope = 255, 255, 255, 255
			mover.MaxVelocity, mover.Acceleration, mover.BrakeRate, mover.TurnRate = 65536, 6553, 6553, 2048
			mx := numeric.FixedFromInt(15*16 + int64(size)*8)
			mz := numeric.FixedFromInt(10*16 + int64(size)*8)
			mh, err := s.Units.Create(&mover, 0, mx, numeric.FixedFromInt(10), mz)
			if err != nil {
				t.Fatal(err)
			}
			moving := s.Units.Unit(mh)
			s.Movement.EnsureUnit(moving)
			victim.Health = -1
			victim.SetScript(cob.NewVM(makeKilledProg(1)))
			s.Units.Destroy(fh, units.DeathKilled)
			s.finalizePhase2Death(fh, 1)
			id := orders.Lookup("Move_Ground")
			q := orders.QueueForUnit(moving)
			q.Push(id, orders.NewMoveNode(id, mx+numeric.FixedFromInt(80), mz, 0, mh, false))
			for tick := uint32(2); tick <= 240; tick++ {
				s.Step(int32(tick))
			}
			if moving.X <= mx+numeric.FixedFromInt(32) {
				t.Fatalf("adjacent mover stayed blocked at %d, started %d", moving.X, mx)
			}
			corpse := s.Features.InstanceAt(int(origin.X), int(origin.Z))
			if corpse == nil || corpse.X != numeric.FixedFromInt(192) || corpse.Z != numeric.FixedFromInt(160) {
				t.Fatalf("corpse failed to preserve origin and exact position: %+v", corpse)
			}
		})
	}
}

// Before a movement surface exists, including restored no-mover state, death
// still consumes the unit's saved pair. It does not require an occupied plane.
func TestCorpseUsesRetainedAnchorWithoutMovementSurface(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		forced, restored, movement bool
		mode                       uint8
	}{
		{name: "ordinary allocator"},
		{name: "forced allocator", forced: true},
		{name: "restored pair", restored: true},
		{name: "air plane", movement: true, mode: 2},
		{name: "carried no plane", movement: true, mode: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestSession(t, 0)
			wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "retainedwreck"}, FootprintX: 1, FootprintZ: 2, Damage: 100, Object: "retainedwreck"}
			s.Catalog.Features[wreck.CanonicalKey] = wreck
			def := *s.Catalog.Units["armcom"]
			def.BMCode, def.FootprintX, def.FootprintZ, def.Corpse = 2, 6, 4, wreck.CanonicalKey
			if tc.movement {
				def.BMCode, def.CanFly = 1, true
			}
			var h pool.Handle
			var err error
			if tc.forced {
				start, _, _ := s.Units.SliceForPlayer(0)
				h, err = s.Units.CreateWithForcedSlot(&def, 0, numeric.FixedFromInt(192), numeric.FixedFromInt(80), numeric.FixedFromInt(160), pool.Handle(start))
			} else {
				h, err = s.Units.Create(&def, 0, numeric.FixedFromInt(192), numeric.FixedFromInt(80), numeric.FixedFromInt(160))
			}
			if err != nil {
				t.Fatal(err)
			}
			u := s.Units.Unit(h)
			want := world.Cell{X: 9, Z: 8}
			if u.CachedOccupancyX != int16(want.X) || u.CachedOccupancyZ != int16(want.Z) {
				t.Fatal("allocator did not retain footprint anchor")
			}
			if tc.restored {
				want = world.Cell{X: 4, Z: 6}
				u.CachedOccupancyX, u.CachedOccupancyZ = int16(want.X), int16(want.Z)
				stable := func(h pool.Handle) (uint16, bool) { return uint16(h), true }
				image, err := units.RetailUnitImage(u, 0, stable, stable, units.RetailUnitWriterScratch{})
				if err != nil {
					t.Fatal(err)
				}
				u.CachedOccupancyX, u.CachedOccupancyZ = 0, 0
				if err := units.RetailUnitBase(u, image); err != nil {
					t.Fatal(err)
				}
			}
			if tc.movement {
				u.Move.Mode, u.Move.ModeMirror, u.RestoredMoveMode = tc.mode, tc.mode, true
				s.Movement.EnsureUnit(u)
				want = world.Cell{X: 5, Z: 7}
				if err := s.Movement.RestoreOccupancy(h, int16(want.X), int16(want.Z)); err != nil {
					t.Fatal(err)
				}
			}
			u.Health = -1
			u.SetScript(cob.NewVM(makeKilledProg(1)))
			s.Units.Destroy(h, units.DeathKilled)
			s.finalizePhase2Death(h, 1)
			if corpse := s.Features.InstanceAt(int(want.X), int(want.Z)); corpse == nil {
				t.Fatal("death lost saved footprint anchor")
			}
		})
	}
}
