package combat

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
		var rd, sd int32
		if u.Def != nil {
			rd = u.Def.RadarDistance
			sd = u.Def.SonarDistance
		}
		sensorUnits = append(sensorUnits, visibility.SensorUnit{
			ID: uint16(u.Handle), Owner: visibility.PlayerID(u.Owner), Status: sp,
			X: u.X, Y: u.Y, Z: u.Z, Alive: true, Hidden: u.Hidden, Active: u.Activated,
			RadarDistance: rd, SonarDistance: sd, DecloakDeadline: new(uint32),
		})
	}
	f.vis.SensorTick(tick, 2, sensorUnits)
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

// Registry hostility reads exactly one row: the registry/scanning owner's
// declaration toward the candidate owner's ally group, which is the owner slot
// in a single-player seat [06 §3.1]. It does not combine player declarations
// [05 R-SHARE-01 §1].
func TestTargetRegistryUsesScanningOwnerAllianceRow(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		scannerDeclaresCandidate bool
		candidateDeclaresScanner bool
		wantMember               bool
	}{
		{"neither declares", false, false, true},
		{"candidate declares", false, true, true},
		{"scanner declares", true, false, false},
		{"both declare", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRegistryFixture(t, false)
			// Keep the projected probe within this fixture's visibility grid.
			f.enemy.Y = 0
			second, err := f.world.Create(f.enemy.Def, f.enemy.Owner, f.enemy.X+numeric.FixedFromInt(16), 0, f.enemy.Z)
			if err != nil {
				t.Fatal(err)
			}
			f.econ.Players[f.shooter.Owner].Allies[f.enemy.Owner] = tc.scannerDeclaresCandidate
			f.econ.Players[f.enemy.Owner].Allies[f.shooter.Owner] = tc.candidateDeclaresScanner
			f.sensorTick(1)
			if !directlyVisibleAtRebuild(f.shooter.Owner, f.enemy, f.vis) {
				t.Fatal("fixture is wrong: enemy must be visible to exercise primary membership")
			}

			s := &Service{}
			s.rebuildTargetRegistry(targetRegistryPeriod, f.shooter.Owner, f.world, f.vis, f.terrain, f.econ)
			var want []pool.Handle
			if tc.wantMember {
				want = []pool.Handle{f.enemy.Handle, second}
			}
			primary := s.targets.primaryList(f.shooter.Owner)
			secondary := s.targets.secondaryList(f.shooter.Owner)
			if !slices.Equal(primary, want) || !slices.Equal(secondary, want) {
				t.Fatalf("primary/secondary lists=(%v,%v), want slot order %v", primary, secondary, want)
			}
		})
	}
}

// SlotAcquisitionAdmits is the reaction path's physical gate. Its input is
// not a registry candidate, so cached list hostility and visibility must not
// be introduced here [06 §3.1].
func TestSlotAcquisitionAdmitsOnlyAppliesPhysicalGate(t *testing.T) {
	f := newRegistryFixture(t, true) // hidden and therefore not directly visible
	f.shooter.InstallWeapon(0, &content.WeaponDef{Range: 1000, LineOfSight: true})
	f.econ.Players[f.shooter.Owner].Allies[f.enemy.Owner] = true
	if !SlotAcquisitionAdmits(f.shooter, 0, f.enemy, f.world, nil, f.terrain, f.econ, nil) {
		t.Fatal("the physical gate applied registry alliance or visibility membership [06 §3.1]")
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
		rebuildEverySlot(s, tick, f.world, f.vis, f.terrain, f.econ)
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
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("fixture is wrong: a plainly visible hostile must be on the primary list")
	}
	f.enemy.Hidden = true // cloaked after the rebuild filed it
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("a listed entry that cloaks mid-window is still acquirable until the next rebuild [06 §3.1]")
	}
	// The next rebuild drops it.
	rebuildEverySlot(s, 2*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); ok {
		t.Fatal("the rebuild at tick 60 drops the now-cloaked hostile from the primary list")
	}
	// A dead entry is dropped by the per-attempt liveness re-test, without
	// waiting for a rebuild [06 §3.1].
	rebuildEverySlot(s, 3*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	f.enemy.Hidden = false
	rebuildEverySlot(s, 4*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok {
		t.Fatal("the decloaked hostile is back on the list")
	}
	f.enemy.Dying = true
	if _, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); ok {
		t.Fatal("a death-latched entry is dropped by the per-attempt liveness test, not by the rebuild")
	}
}

// TestRegistryUsesSensorSonarAndModelHull locks the direct-visibility adapter
// at the actual sensor-to-registry seam. The sensor callback grants sonar to a
// fully submerged hostile, while a hull whose model top crosses the sea plane
// needs no sonar at all. Both cases use the definition's min/max record and
// the completed sensor status; alliance state cannot manufacture either
// admission [06 §3.1][03 R-VIS-01 §5].
func TestRegistryUsesSensorSonarAndModelHull(t *testing.T) {
	run := func(modelTop int32, sonar bool) (listed bool, status uint32) {
		f := newRegistryFixture(t, false)
		f.terrain.SeaLevel = 20
		f.enemy.X = numeric.FixedFromInt(30)
		f.enemy.Y = numeric.FixedFromInt(15)
		f.enemy.Z = numeric.FixedFromInt(30)
		f.enemy.Def.ModelTopFixed = modelTop << 16
		f.enemy.Def.ModelTop = modelTop
		if sonar {
			f.shooter.Def.SonarDistance = 500
		}
		f.sensorTick(1)
		status = *f.status[f.enemy.Handle]
		s := &Service{}
		rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
		for _, h := range s.targets.primaryList(0) {
			if h == f.enemy.Handle {
				return true, status
			}
		}
		return false, status
	}

	if listed, status := run(0, false); listed || status&visibility.SonarBit != 0 {
		t.Fatalf("fully submerged hostile without sonar listed=%v status=%#x", listed, status)
	}
	if listed, status := run(0, true); !listed || status&visibility.SonarBit == 0 {
		t.Fatalf("sensor-granted sonar did not admit submerged hostile: listed=%v status=%#x", listed, status)
	}
	if listed, status := run(10, false); !listed || status&visibility.SonarBit != 0 {
		t.Fatalf("hull top crossing the sea plane needs no sonar: listed=%v status=%#x", listed, status)
	}

	// The same hull admission reaches the normal per-slot acquisition path: the
	// list is filed at rebuild and the subsequent attempt retains it rather than
	// collapsing the target back to its centre point.
	f := newRegistryFixture(t, false)
	f.terrain.SeaLevel = 20
	f.enemy.X, f.enemy.Y, f.enemy.Z = numeric.FixedFromInt(30), numeric.FixedFromInt(15), numeric.FixedFromInt(30)
	f.enemy.Def.ModelTop, f.enemy.Def.ModelTopFixed = 10, 10<<16
	f.shooter.InstallWeapon(0, &content.WeaponDef{ID: 99, Range: 500})
	f.sensorTick(1)
	s := &Service{}
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if h, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok || h != f.enemy.Handle {
		t.Fatalf("hull-admitted primary target acquisition = %d,%v want %d,true", h, ok, f.enemy.Handle)
	}
}

// TestRegistryVisibilityUsesHullCornersAndObserverSource keeps the combat
// adapters from reconstructing a centre-point query or silently collapsing the
// visibility service's byte/word source choice. The target's centre and the
// other three probes are dark; only its min-X/min-Z hull corner is lit. Its
// bottom is exactly at sea level, so this also locks the strict underwater
// comparison [03 §3.2][06 §3.1].
func TestRegistryVisibilityUsesHullCornersAndObserverSource(t *testing.T) {
	newFixture := func(mode visibility.Mode) *registryFixture {
		f := newRegistryFixture(t, false)
		f.vis.SetMode(mode)
		f.terrain.SeaLevel = 0
		f.shooter.Y = 0
		f.enemy.X = numeric.FixedFromInt(32)
		f.enemy.Y = 0
		f.enemy.Z = numeric.FixedFromInt(48)
		// The odd X and wider Z footprints make the accumulated probe walk
		// span four distinct projection cells: (0,0), (1,0), (1,2), (0,2).
		f.enemy.Def.FootprintX = 3
		f.enemy.Def.FootprintZ = 5
		for p := 0; p < 10; p++ {
			for i := range f.vis.ByteGrid(visibility.PlayerID(p)) {
				f.vis.ByteGrid(visibility.PlayerID(p))[i] = 0
			}
		}
		for i := range f.vis.WordMask() {
			f.vis.WordMask()[i] = 0
		}
		return f
	}
	contains := func(s *Service, owner uint8, h pool.Handle) bool {
		for _, got := range s.targets.primaryList(owner) {
			if got == h {
				return true
			}
		}
		return false
	}

	// Current-coverage mode reads the queried observer's byte grid. A lit
	// corner lets both the registry and normal weapon acquisition see the
	// target, even though its centre is dark.
	f := newFixture(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	f.vis.ByteGrid(0)[0] = 1
	f.shooter.InstallWeapon(0, &content.WeaponDef{ID: 99, Range: 1000, WaterWeapon: true})
	f.shooter.SlotAt(0).Flags |= 0x02
	f.sensorTick(1)
	s := &Service{}
	s.rebuildTargetRegistry(targetRegistryPeriod, 0, f.world, f.vis, f.terrain, f.econ)
	if !contains(s, 0, f.enemy.Handle) {
		t.Fatal("lit hull corner at equal sea level did not enter the primary registry")
	}
	if got, ok := s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ); !ok || got != f.enemy.Handle {
		t.Fatalf("corner-admitted target acquisition = %d,%v want %d,true", got, ok, f.enemy.Handle)
	}

	// Player 3 must not borrow local player 0's current-coverage byte grid.
	// Once player 3's corresponding cell is lit, its regular registry rebuild
	// files the same hostile.
	f = newFixture(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	f.vis.ByteGrid(0)[0] = 1
	f.sensorTick(1)
	s = &Service{}
	s.rebuildTargetRegistry(targetRegistryPeriod, 3, f.world, f.vis, f.terrain, f.econ)
	if contains(s, 3, f.enemy.Handle) {
		t.Fatal("byte-grid observer 3 borrowed local player 0 coverage")
	}
	f.vis.ByteGrid(3)[0] = 1
	f.sensorTick(2)
	s.rebuildTargetRegistry(2*targetRegistryPeriod, 3, f.world, f.vis, f.terrain, f.econ)
	if !contains(s, 3, f.enemy.Handle) {
		t.Fatal("byte-grid observer 3 did not use its own lit coverage")
	}

	// With current coverage disabled, the predicate instead reads the local
	// player's word bit even while the registry is rebuilding player 3.
	f = newFixture(visibility.ModeHistoryEnabled)
	f.vis.WordMask()[0] = 1 << 0
	f.sensorTick(1)
	s = &Service{}
	s.rebuildTargetRegistry(targetRegistryPeriod, 3, f.world, f.vis, f.terrain, f.econ)
	if !contains(s, 3, f.enemy.Handle) {
		t.Fatal("word-grid observer 3 did not use the local player's word bit")
	}
}

// The cadence belongs to the ten player slots, not to the units that happen to
// be stepped: the per-player phase "iterates the ten player slots in order"
// [08 "Dispatch gates and order sinks"][06 §3.1], and the gate is null-checked
// on the slot's STRATEGIC STATE, "never controller-checked" — a state the
// per-player reset constructs "for every slot whose controller is not 3
// (remote), human slots included" [06 §3.1 "Which slots draw"]. A slot with
// strategic state and no live unit of its own therefore still has its registry
// rebuilt.
func TestRegistrySweepRebuildsEverySide(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	f.sensorTick(1)
	s := &Service{}
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)

	// Player 3 owns nothing at all; the hostile pair is still classified for it.
	if got := len(s.targets.primaryList(3)); got != 2 {
		t.Fatalf("a unit-less side's primary list holds both hostiles, got %d", got)
	}
	// The mirror cadence word makes a duplicate call inside one window a no-op,
	// so a second visit to the same slot does not re-run a rebuild the first
	// one performed [06 §3.1].
	f.enemy.Hidden = true
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if got := len(s.targets.primaryList(3)); got != 2 {
		t.Fatalf("the rebuild ran twice inside one window, got %d", got)
	}
}

// TestCloakProximityFollowsTheRegistryCadence is the R14 regression at the seam:
// the sensor phase's minimum-cloak proximity pass searches the SOURCE OWNER's
// primary candidate list of [06 §3.1] through the read-only accessor, so it
// inherits that list's thirty-tick staleness in both directions
// [03 R-VIS-01 §4] pass 4.
//
// The three properties, in one run: an enemy that never enters the list never
// breaches; an enemy that becomes visible waits for the next rebuild; and an
// enemy that goes dark after it was filed keeps breaching until the rebuild that
// drops it.
func TestCloakProximityFollowsTheRegistryCadence(t *testing.T) {
	f := newRegistryFixture(t, true) // the hostile starts cloaked: off the list
	f.onProjectedGrid()
	// The source is the local player's own unit: cloak-capable by definition,
	// twenty world units from the hostile, and owned by an active controller of
	// type 1 [06 R-WPN-02 §2].
	f.shooter.Def.MinCloakDistance = 50
	f.shooter.Def.CloakCost = 10
	// Cloaked, as the review's probe had it: under the old all-hostiles scan
	// this is exactly the pairing that moved the deadline.
	f.shooter.Hidden = true
	f.econ.Players[0].Exists = true
	f.econ.Players[0].ControllerState = 1

	s := &Service{}
	var deadline uint32
	// The session builds this membership word from Service.PrimaryTargets; the
	// test builds the same word the same way so the accessor is what is under
	// test, not a copy of the registry's internals.
	step := func(tick uint32) {
		rebuildEverySlot(s, tick, f.world, f.vis, f.terrain, f.econ)
		masks := map[pool.Handle]uint16{}
		for slot := 0; slot < combatPlayerSlots; slot++ {
			for _, h := range s.PrimaryTargets(uint8(slot)) {
				masks[h] |= 1 << uint(slot)
			}
		}
		var sensorUnits []visibility.SensorUnit
		for _, u := range f.world.Iter() {
			sp, ok := f.status[u.Handle]
			if !ok {
				sp = new(uint32)
				f.status[u.Handle] = sp
			}
			su := visibility.SensorUnit{
				ID: uint16(u.Handle), Owner: visibility.PlayerID(u.Owner), Status: sp,
				X: u.X, Y: u.Y, Z: u.Z, Alive: u.Alive, Dying: u.Dying, Hidden: u.Hidden,
				Active: u.Activated, RadarDistance: u.Def.RadarDistance,
				MinCloakDistance:      u.Def.MinCloakDistance,
				CanCloak:              u.Def.CloakCost > 0,
				OwnerLocallySimulated: u.Owner == 0,
				PrimaryCandidateOf:    masks[u.Handle],
			}
			if u.Handle == f.shooter.Handle {
				su.DecloakDeadline = &deadline
			} else {
				su.DecloakDeadline = new(uint32)
			}
			sensorUnits = append(sensorUnits, su)
		}
		f.vis.SensorTick(tick, 2, sensorUnits)
	}

	// A cloaked hostile fails the rebuild's visibility clause, so it is on no
	// list and cannot suppress cloak however close it stands. This is the
	// review's probe: the old all-hostiles scan moved the deadline here.
	step(targetRegistryPeriod)
	if deadline != 0 {
		t.Fatalf("an enemy that cannot enter the primary list breached: deadline %d [R-VIS-01 §4] pass 4", deadline)
	}
	// It decloaks one tick after the rebuild that refused it: no breach until
	// the next rebuild files it.
	f.enemy.Hidden = false
	for tick := targetRegistryPeriod + 1; tick < 2*targetRegistryPeriod; tick++ {
		step(tick)
		if deadline != 0 {
			t.Fatalf("a newly visible enemy breached at tick %d, before the rebuild that files it [06 §3.1]", tick)
		}
	}
	step(2 * targetRegistryPeriod)
	if deadline != 2*targetRegistryPeriod+visibility.DecloakDeadlineAdd {
		t.Fatalf("deadline %d after the rebuild that files the enemy, want %d", deadline, 2*targetRegistryPeriod+visibility.DecloakDeadlineAdd)
	}
	// A filed entry that goes dark stays eligible until the rebuild drops it.
	f.enemy.Hidden = true
	step(2*targetRegistryPeriod + 1)
	if deadline != 2*targetRegistryPeriod+1+visibility.DecloakDeadlineAdd {
		t.Fatalf("a retained candidate must stay eligible until the next rebuild: deadline %d", deadline)
	}
	// The rebuild at tick 90 re-runs the visibility clause and drops the
	// re-cloaked enemy, so the deadline stops moving from that tick on.
	before := deadline
	step(3 * targetRegistryPeriod)
	step(3*targetRegistryPeriod + 1)
	if deadline != before {
		t.Fatalf("the rebuild at tick %d drops the re-cloaked enemy, so the deadline must stop moving: %d -> %d", 3*targetRegistryPeriod, before, deadline)
	}
}
