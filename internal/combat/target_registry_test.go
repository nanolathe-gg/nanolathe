package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The secondary list receives the SAME distance/liveness test as the primary
// walk and NO visibility re-test: "the secondary list was populated from the
// *seen* bit at rebuild, and that is the only sensor test it ever receives"
// [06 §3.1] (refinement of 2026-09-02).
//
// The re-test that used to stand in AcquireTarget made the fallback list a
// second copy of the primary list, so the gate could never produce a target the
// primary walk had not; this locks that it cannot come back.
func TestSecondaryListGetsNoVisibilityRetest(t *testing.T) {
	cand := Candidate{
		Handle:  7,
		X:       numeric.FixedFromInt(10),
		Z:       numeric.FixedFromInt(10),
		Y:       numeric.FixedFromInt(100),
		Hostile: true,
	}
	base := func() Acquisition {
		return Acquisition{
			ShooterX: numeric.FixedFromInt(0),
			ShooterZ: numeric.FixedFromInt(0),
			ShooterY: numeric.FixedFromInt(100),
			SeaLevel: numeric.FixedFromInt(0),
			Range:    1000,
			// The predicate that gates the primary list rejects everything, so
			// any hit can only have come through the secondary list.
			Visible: func(Candidate) bool { return false },
		}
	}

	// The primary list is what the caller hands in; an empty one is the only
	// way the secondary list is ever reached [06 §3.1]. The Visible closure is
	// installed to prove the attempt never consults it.
	acq := base()
	if _, ok := AcquireTarget(nil, acq); ok {
		t.Fatal("an empty primary list with the gate clear acquires nothing")
	}

	acq = base()
	acq.Secondary = []Candidate{cand}
	if _, ok := AcquireTarget(nil, acq); ok {
		t.Fatal("with the gate clear the secondary list is not consulted at all [06 §3.1]")
	}

	acq = base()
	acq.Secondary = []Candidate{cand}
	acq.HasUpgrade = true
	h, ok := AcquireTarget(nil, acq)
	if !ok || h != cand.Handle {
		t.Fatalf("gate set and primary empty: secondary entry wins with no visibility re-test, got %d ok=%v", h, ok)
	}

	// The distance test still applies, on the same inclusive truncated metric
	// the primary walk uses [06 §3.1].
	acq = base()
	acq.Range = 5
	acq.Secondary = []Candidate{cand}
	acq.HasUpgrade = true
	if _, ok := AcquireTarget(nil, acq); ok {
		t.Fatal("a secondary entry outside the weapon range is filtered out like a primary one")
	}

	// A non-empty primary result is never displaced by the secondary list, and
	// the secondary list is not retried when the primary produced entries
	// [06 §3.1].
	acq = base()
	primary := cand
	primary.Handle = 3
	acq.Secondary = []Candidate{cand}
	acq.HasUpgrade = true
	if h, ok := AcquireTarget([]Candidate{primary}, acq); !ok || h != primary.Handle {
		t.Fatalf("primary result wins, got %d ok=%v", h, ok)
	}
}

// registryFixture builds a two-side world with the local player 0 owning a
// radar emitter and player 2 owning one hostile unit, plus a real sensor pass
// so the seen bit under test is the one the sensor phase writes
// [03 §3.4][R-VIS-01 §4].
type registryFixture struct {
	world   *units.World
	terrain *world.Terrain
	vis     *visibility.Service
	econ    *economy.Service
	status  map[pool.Handle]*uint32

	shooter *units.Unit
	enemy   *units.Unit
}

func newRegistryFixture(t *testing.T, enemyHidden bool) *registryFixture {
	t.Helper()
	terrain := &world.Terrain{CellW: 100, CellH: 100, Gravity: numeric.Fixed(0)}
	terrain.Plot = make([]world.PlotCell, 100*100)
	w := newCombatFixtureWorld(10, nil)
	vis := visibility.New(terrain, 0)
	vis.SetLocal(0)

	// The shooter is also the radar emitter: pass 2 runs over the viewing
	// player's own active units, and its contact callback applies no cloak test,
	// so a hidden hostile inside the circle still gets the seen bit.
	defShooter := &content.UnitDef{UnitName: "shooter", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1, RadarDistance: 500}
	defEnemy := &content.UnitDef{UnitName: "enemy", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1}
	sh, _ := w.Create(defShooter, 0, numeric.FixedFromInt(10), numeric.FixedFromInt(100), numeric.FixedFromInt(10))
	en, _ := w.Create(defEnemy, 2, numeric.FixedFromInt(30), numeric.FixedFromInt(100), numeric.FixedFromInt(10))

	f := &registryFixture{world: w, terrain: terrain, vis: vis, econ: &economy.Service{}, status: map[pool.Handle]*uint32{}}
	for i := 0; i < 10; i++ {
		f.econ.Players[i].Allies[i] = true
	}
	f.shooter = w.Unit(sh)
	f.shooter.Activated = true
	f.enemy = w.Unit(en)
	f.enemy.Hidden = enemyHidden
	return f
}

// sensorTick runs one sensor pass so the fixture's seen bits are the phase's
// own output rather than a hand-set value.
func (f *registryFixture) sensorTick(tick uint32) {
	var sensorUnits []visibility.SensorUnit
	for _, u := range f.world.Iter() {
		sp, ok := f.status[u.Handle]
		if !ok {
			sp = new(uint32)
			f.status[u.Handle] = sp
		}
		var rd int32
		if u.Def != nil {
			rd = u.Def.RadarDistance
		}
		sensorUnits = append(sensorUnits, visibility.SensorUnit{
			ID: uint16(u.Handle), Owner: visibility.PlayerID(u.Owner), Status: sp,
			X: u.X, Y: u.Y, Z: u.Z, Alive: true, Hidden: u.Hidden, Active: u.Activated,
			RadarDistance: rd, DecloakDeadline: new(uint32),
		})
	}
	f.vis.SensorTick(tick, 2, func(a, b visibility.PlayerID) bool { return a == b }, sensorUnits)
}

// The registry rebuild is gated on `lastRebuild + 30 <= currentTick` [06 §3.1],
// so a side's gate and list stay as the last rebuild left them for up to thirty
// ticks — including for the first thirty ticks of a battle, before any rebuild
// has run.
func TestTargetRegistryRebuildCadence(t *testing.T) {
	f := newRegistryFixture(t, true)
	upgrade := &content.UnitDef{UnitName: "targ", MaxDamage: 100, Limit: -1, IsTargetingUpgrade: true}
	uh, _ := f.world.Create(upgrade, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(100), numeric.FixedFromInt(12))
	f.world.Unit(uh).Activated = true
	f.sensorTick(1)

	s := &Service{}
	s.rebuildTargetRegistry(1, 0, f.world, f.vis, f.terrain, f.econ)
	if s.targetingUpgradeGateFor(0) {
		t.Fatal("no rebuild is due before tick 30, so the gate is still clear [06 §3.1]")
	}
	s.rebuildTargetRegistry(29, 0, f.world, f.vis, f.terrain, f.econ)
	if s.targetingUpgradeGateFor(0) {
		t.Fatal("tick 29 is still inside the first window")
	}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	if !s.targetingUpgradeGateFor(0) {
		t.Fatal("the first rebuild is due at tick 30 and sets the gate")
	}
	if got := s.targets.secondaryList(0); len(got) != 1 || got[0] != f.enemy.Handle {
		t.Fatalf("the secondary list is the hostile seen-bit list, got %v want [%d]", got, f.enemy.Handle)
	}

	// Staleness is the contract: deactivating the upgrade does not clear the
	// gate until the next rebuild is due.
	f.world.Unit(uh).Activated = false
	s.rebuildTargetRegistry(31, 0, f.world, f.vis, f.terrain, f.econ)
	if !s.targetingUpgradeGateFor(0) {
		t.Fatal("a side's registry is not rebuilt again until 30 ticks have passed")
	}
	s.rebuildTargetRegistry(60, 0, f.world, f.vis, f.terrain, f.econ)
	if s.targetingUpgradeGateFor(0) {
		t.Fatal("the rebuild at tick 60 clears the gate the deactivated upgrade no longer opens")
	}
}

// "Friendly" in the counting branch means the SAME PLAYER, not the same ally
// group: an ally's targeting-upgrade unit never opens the gate for you
// [06 §3.1] (refinement of 2026-09-02, point 2). An allied unit is neither
// hostile nor own and is skipped entirely, so it reaches neither list.
func TestTargetRegistryGateIsOwnerOnly(t *testing.T) {
	f := newRegistryFixture(t, true)
	f.econ.Players[0].Allies[1] = true
	f.econ.Players[1].Allies[0] = true
	upgrade := &content.UnitDef{UnitName: "targ", MaxDamage: 100, Limit: -1, IsTargetingUpgrade: true}
	ah, _ := f.world.Create(upgrade, 1, numeric.FixedFromInt(12), numeric.FixedFromInt(100), numeric.FixedFromInt(12))
	f.world.Unit(ah).Activated = true
	f.sensorTick(1)

	s := &Service{}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	if s.targetingUpgradeGateFor(0) {
		t.Fatal("an ally's upgrade must not open player 0's gate [06 §3.1]")
	}
	for _, h := range s.targets.secondaryList(0) {
		if h == f.world.Unit(ah).Handle {
			t.Fatal("an allied unit is skipped entirely; it is not a secondary candidate")
		}
	}
	s.rebuildTargetRegistry(30, 1, f.world, f.vis, f.terrain, f.econ)
	if !s.targetingUpgradeGateFor(1) {
		t.Fatal("the upgrade's own player gets the gate")
	}
}

// End to end: a cloaked hostile inside the local player's radar circle carries
// the seen bit but fails the direct-visibility predicate's cloak reject, so it
// is on the secondary list and not on the primary one. It is acquirable only
// while the scanning player's own targeting-upgrade unit holds the gate open
// [06 §3.1].
func TestSecondaryListAcquiresCloakedRadarContactOnlyBehindTheGate(t *testing.T) {
	run := func(withUpgrade bool) (pool.Handle, bool) {
		f := newRegistryFixture(t, true)
		if withUpgrade {
			upgrade := &content.UnitDef{UnitName: "targ", MaxDamage: 100, Limit: -1, IsTargetingUpgrade: true}
			uh, _ := f.world.Create(upgrade, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(100), numeric.FixedFromInt(12))
			f.world.Unit(uh).Activated = true
		}
		wdef := &content.WeaponDef{ID: 99, Range: 1000}
		f.shooter.InstallWeapon(0, wdef)
		f.shooter.SlotAt(0).Flags |= 0x02
		f.sensorTick(1)

		s := &Service{}
		s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
		if !s.targets.seenBit(f.enemy.Handle) {
			t.Fatal("fixture is wrong: the radar pass must set the hostile's seen bit")
		}
		return s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ)
	}

	if h, ok := run(false); ok {
		t.Fatalf("without the gate a cloaked radar contact is not acquirable, got %d", h)
	}
	h, ok := run(true)
	if !ok {
		t.Fatal("with the gate open the cloaked radar contact is acquired off the secondary list")
	}
	if h == 0 {
		t.Fatal("acquisition returned the null handle")
	}
}

// The primary list is filed at the rebuild with the direct-visibility
// predicate evaluated THERE, so "an acquisition can therefore see a list up to
// thirty ticks stale" cuts both ways: a unit that becomes visible inside a
// window is not acquirable until the next rebuild files it [06 §3.1].
// onProjectedGrid moves the fixture's two units to a world Y whose half-height
// shear leaves them inside the visibility grid, so the direct-visibility
// predicate's four hull probes can succeed [03 §3.2] step 5. The fixture's own
// Y of 100 shears every probe off the north edge, which is exactly what the
// cloaked-radar-contact test wants and exactly what a primary-list test cannot
// use.
func (f *registryFixture) onProjectedGrid() {
	f.shooter.Y = numeric.FixedFromInt(20)
	f.enemy.Y = numeric.FixedFromInt(20)
}

func TestPrimaryListStalenessDelaysNewlyVisibleCandidate(t *testing.T) {
	f := newRegistryFixture(t, true) // the hostile starts cloaked
	f.onProjectedGrid()
	wdef := &content.WeaponDef{ID: 99, Range: 1000}
	f.shooter.InstallWeapon(0, wdef)
	f.shooter.SlotAt(0).Flags |= 0x02

	s := &Service{}
	acquire := func(tick uint32) (pool.Handle, bool) {
		f.sensorTick(tick)
		s.stepTargetRegistries(tick, f.world, f.vis, f.terrain, f.econ)
		return s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ)
	}

	if _, ok := acquire(targetRegistryPeriod); ok {
		t.Fatal("a cloaked hostile fails the rebuild's cloak clause and is not on the primary list")
	}
	// It decloaks one tick after the rebuild that refused it.
	f.enemy.Hidden = false
	for tick := targetRegistryPeriod + 1; tick < 2*targetRegistryPeriod; tick++ {
		if h, ok := acquire(tick); ok {
			t.Fatalf("acquired %d at tick %d: a unit can be newly visible for up to thirty ticks before it enters the primary list [06 §3.1]", h, tick)
		}
	}
	if _, ok := acquire(2 * targetRegistryPeriod); !ok {
		t.Fatal("the rebuild at tick 60 files the decloaked hostile, which is then acquirable")
	}
}

// A listed candidate that goes dark inside the window stays acquirable: the
// per-attempt filter re-tests liveness and nothing else, and the visibility
// predicate is not re-run [06 §3.1].
func TestPrimaryListEntryStaysAcquirableAfterItGoesDark(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	wdef := &content.WeaponDef{ID: 99, Range: 1000}
	f.shooter.InstallWeapon(0, wdef)
	f.shooter.SlotAt(0).Flags |= 0x02
	f.sensorTick(1)

	s := &Service{}
	s.stepTargetRegistries(targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("fixture is wrong: a plainly visible hostile must be on the primary list")
	}
	f.enemy.Hidden = true // cloaked after the rebuild filed it
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("a listed entry that cloaks mid-window is still acquirable until the next rebuild [06 §3.1]")
	}
	// The next rebuild drops it.
	s.stepTargetRegistries(2*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); ok {
		t.Fatal("the rebuild at tick 60 drops the now-cloaked hostile from the primary list")
	}
	// A dead entry is dropped by the per-attempt liveness re-test, without
	// waiting for a rebuild [06 §3.1].
	s.stepTargetRegistries(3*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	f.enemy.Hidden = false
	s.stepTargetRegistries(4*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("the decloaked hostile is back on the list")
	}
	f.enemy.Dying = true
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); ok {
		t.Fatal("a death-latched entry is dropped by the per-attempt liveness test, not by the rebuild")
	}
}

// The cadence belongs to the ten player slots, not to the units that happen to
// be stepped: the per-player phase "iterates the ten player slots in order"
// [08 "Dispatch gates and order sinks"][06 §3.1]. A side that owns no live unit
// therefore still has its registry rebuilt.
func TestRegistrySweepRebuildsEverySide(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	f.sensorTick(1)
	s := &Service{}
	s.stepTargetRegistries(targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)

	// Player 3 owns nothing at all; the hostile pair is still classified for it.
	if got := len(s.targets.primaryList(3)); got != 2 {
		t.Fatalf("a unit-less side's primary list holds both hostiles, got %d", got)
	}
	// The sweep is idempotent within a tick, so a second stepped unit does not
	// re-run a rebuild the first one performed.
	f.enemy.Hidden = true
	s.stepTargetRegistries(targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if got := len(s.targets.primaryList(3)); got != 2 {
		t.Fatalf("the sweep ran twice inside one tick, got %d", got)
	}
}
