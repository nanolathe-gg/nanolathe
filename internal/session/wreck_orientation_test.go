package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// wreckOrientationSession composes a battle carrying one resurrector and one
// mobile victim whose corpse is a 3D wreck, named so the corpse's key truncated
// at its first underscore resolves the victim's own definition
// [05 R-WORK-01 §7 phase 3].
func wreckOrientationSession(t *testing.T) *Session {
	t.Helper()
	cat := minimalCatalogForStrict()
	mk := func(name string, f func(*content.UnitDef)) *content.UnitDef {
		d := &content.UnitDef{
			UnitName: name, ObjectName: name, MaxDamage: 100, Limit: -1,
			SightDistance: 64, MovementClass: "testmove",
			FootprintX: 1, FootprintZ: 1, BMCode: 1, CanMove: true,
			MaxVelocity: 1 << 16, TurnRate: 100, Acceleration: 1 << 10, BrakeRate: 1 << 10,
		}
		f(d)
		d.CanonicalKey = content.CanonicalKey(name)
		cat.Units[d.CanonicalKey] = d
		return d
	}
	mk("wreckcon", func(d *content.UnitDef) {
		d.Builder = true
		d.CanResurrect = true
		d.WorkerTime = 300
		d.BuildDistance = 512 << 16
	})
	mk("wreckvictim", func(d *content.UnitDef) {
		d.BuildTime = 100
		d.Corpse = "wreckvictim_dead"
	})
	corpse := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100}
	corpse.CanonicalKey = content.CanonicalKey("wreckvictim_dead")
	corpse.Object = "wreckvictim_dead.3do" // a 3D wreck, not a sprite
	corpse.Reclaimable = true
	cat.Features[corpse.CanonicalKey] = corpse
	installFixtureCOB(cat)

	s := &Session{
		Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(),
		LocalOwner: 0, Snapshot: &frame.Buffer{},
	}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	for i := range s.Econ.Players {
		s.Econ.Players[i].Exists = true
		s.Econ.Players[i].ControllerState = 1
		s.Econ.Players[i].Stock[0] = 1e6
		s.Econ.Players[i].Stock[1] = 1e6
		s.Econ.Players[i].Capacity[0] = 2e6
		s.Econ.Players[i].Capacity[1] = 2e6
	}
	s.Econ.SeedDeadlines(0)
	s.Clock = &clock.State{Requested: 10, Active: 10}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	return s
}

// R2-03's regression, end to end. A unit dies with a nonzero orientation; the
// corpse placement stores that triple on the live feature record [05 "Feature
// instance and terrain cell"]; the record publishes it; and the resurrection
// order's phase 5 copies it back into the replacement unit, overwriting
// whatever the allocator seeded [05 R-WORK-01 §7 "Established — the
// transplant"]. Before this, the triple was discarded at the corpse stamp and
// a resurrected unit faced whichever way the allocator happened to point it.
func TestWreckKeepsItsOrientationThroughDeathAndResurrection(t *testing.T) {
	s := wreckOrientationSession(t)
	victimDef := s.Catalog.Units["wreckvictim"]
	conDef := s.Catalog.Units["wreckcon"]

	const (
		victimCellX = 12
		victimCellZ = 12
	)
	fellBank, fellHeading, fellPitch := uint16(0x0800), uint16(0xC000), uint16(0x0140)

	hVictim, err := s.Units.Create(victimDef, 0, world.CellToWorld(victimCellX), 0, world.CellToWorld(victimCellZ))
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	victim := s.Units.Unit(hVictim)
	victim.Move.Bank, victim.Move.Heading, victim.Move.Pitch = fellBank, fellHeading, fellPitch
	victim.Health = -10
	victim.MaxHealth = int32(victimDef.MaxDamage)
	victim.PriorSample = 80
	victim.Remaining = 0
	// Killed variant 1 is corpse-chain depth 1: the authored `Corpse`
	// [06 §12.1] C23.
	victim.SetScript(cob.NewVM(makeKilledProg(1)))
	s.Units.Destroy(hVictim, units.DeathKilled)
	s.Units.FinalizeDeath(hVictim, 1)

	wreck := s.Features.InstanceAt(victimCellX, victimCellZ)
	if wreck == nil || wreck.Def == nil || wreck.Def.CanonicalKey != "wreckvictim_dead" {
		t.Fatalf("no wreck at the victim's cell: %+v", wreck)
	}
	if wreck.Bank != fellBank || wreck.Heading != fellHeading || wreck.Pitch != fellPitch {
		t.Fatalf("wreck orientation (%d,%d,%d), want the dying unit's (%d,%d,%d) [05 \"Feature instance and terrain cell\"]",
			wreck.Bank, wreck.Heading, wreck.Pitch, fellBank, fellHeading, fellPitch)
	}

	// The publication boundary carries it, because the 3DO feature pass fills a
	// pseudo-unit with the model, the position and the slot's orientation words
	// [03 R-RAST-01 §6][I6].
	s.publishSnapshot(1)
	committed := s.Snapshot.Current()
	if committed == nil {
		t.Fatal("no committed frame")
	}
	found := false
	for _, fv := range committed.Features {
		if fv.DefName != "wreckvictim_dead" {
			continue
		}
		found = true
		if fv.Bank != fellBank || fv.Heading != fellHeading || fv.Pitch != fellPitch {
			t.Fatalf("published feature orientation (%d,%d,%d), want (%d,%d,%d)",
				fv.Bank, fv.Heading, fv.Pitch, fellBank, fellHeading, fellPitch)
		}
	}
	if !found {
		t.Fatal("the wreck never reached the committed frame")
	}

	// Now resurrect it through the real order row.
	hCon, err := s.Units.Create(conDef, 0, world.CellToWorld(victimCellX-1), 0, world.CellToWorld(victimCellZ))
	if err != nil {
		t.Fatalf("create resurrector: %v", err)
	}
	con := s.Units.Unit(hCon)
	s.Movement.EnsureUnit(con)
	// The fixture script never runs the deferred StartBuilding body, so the
	// stance byte phase 2 waits on is set here, exactly as the assist fixture
	// does [04 R-ORD-01 §5].
	con.InBuildStance = true
	id := orders.Lookup("Resurrect")
	if id == 0 {
		t.Fatal("no Resurrect row in the table")
	}
	orders.QueueForUnit(con).Push(id, orders.Node{
		Owner: hCon,
		GoalX: world.CellToWorld(victimCellX), GoalZ: world.CellToWorld(victimCellZ),
	})

	var product *units.Unit
	for tick := uint32(2); tick <= 120 && product == nil; tick++ {
		s.Clock.GlobalTick = tick
		s.stepAuthoritativePhases(tick)
		for _, u := range s.Units.Iter() {
			if u != nil && u.Def == victimDef && u.Handle != hVictim {
				product = u
				break
			}
		}
	}
	if product == nil {
		t.Fatal("the Resurrect row never allocated a replacement unit")
	}
	if product.Move.Bank != fellBank || product.Move.Heading != fellHeading || product.Move.Pitch != fellPitch {
		t.Fatalf("resurrected unit orientation (%d,%d,%d), want the wreck's (%d,%d,%d) [05 R-WORK-01 §7]",
			product.Move.Bank, product.Move.Heading, product.Move.Pitch, fellBank, fellHeading, fellPitch)
	}
	// The mover records must agree: movement's EnsureUnit seeds the steering
	// heading from the unit's own, so a transplant that ran after the
	// completion hook would leave the two disagreeing and the first movement
	// step would commit the allocator's facing back over the wreck's.
	steer := s.Movement.Steers[product.Handle]
	if steer == nil {
		t.Fatal("the resurrected unit has no mover record")
	}
	if steer.Heading != fellHeading || steer.PendingHeading != fellHeading {
		t.Fatalf("mover heading %d/%d, want %d — the transplant must precede CompleteUnit",
			steer.Heading, steer.PendingHeading, fellHeading)
	}
	// The transplant copies orientation only; the position is the allocator's,
	// from the feature's recorded position [05 R-WORK-01 §7].
	if product.X != numeric.Fixed(0) && product.X != wreck.X {
		t.Logf("resurrected at X=%d (wreck X=%d)", product.X.Raw(), wreck.X.Raw())
	}
}
