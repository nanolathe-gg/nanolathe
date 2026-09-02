// WU-19-22 locks two contracts that had no reader before it.
//
//   - The autonomous target scan admits a slot only when the owning player's
//     controller type is 2 (computer) or the weapon is not `commandfire`
//     [06 §3.2]. Without it a human commander's disintegrator hunted and fired
//     on its own.
//   - The area enumeration excludes the record's shooter, and that exclusion is
//     the whole of retail's self-damage policy [06 §9.3][06 R-DMG-01 §9]. A
//     null shooter matches nobody. Without it a unit standing in its own blast
//     took its own damage and then overwrote its last-damage provenance with
//     its own owner, so the kill credit of [06 §12.1] landed on the victim.
package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// commandFireProbe builds the shared fixture: one shooter owned by player 0,
// one hostile owned by player 1 inside the weapon's range, and a service whose
// control-byte accessor answers with the value the caller names for player 0.
// The weapon always returns a nonzero Aim so the only thing the fixture can be
// blocked by is the gate under test.
func commandFireProbe(t *testing.T, commandFire bool, control uint8) (*Service, *units.Unit, *units.World, *world.Terrain, *content.Catalog) {
	t.Helper()
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{0x10021001, 1, 0x10065000} // push 1; return [04 §4.3]
	vm := cob.NewVM(progWithAim(code, "AimPrimary", 0))
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(77)
	weapon.CommandFire = commandFire
	weapon.Tolerance = wideDriftTolerance
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetNone}
	slot.Flags |= 0x02
	slot.Reload = 0
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	svc := &Service{ControlByte: func(owner uint8) uint8 {
		if owner == 0 {
			return control
		}
		return ControlByteComputer
	}}
	_ = target
	return svc, shooter, w, terrain, cat
}

// simRNGPtr is a seeded simulation stream the fixtures can hand to
// StepWeaponsForUnit and then read the draw count back from (I4).
func simRNGPtr(seed uint32) *rng.Simulation {
	s := rng.NewSimulation(seed)
	return &s
}

// visitsPerProbe is "N slot visits": enough revisits that a scan running on any
// cadence would have installed a target by now.
const visitsPerProbe = 40

func runProbeVisits(svc *Service, shooter *units.Unit, w *units.World, terrain *world.Terrain, cat *content.Catalog, sim *rng.Simulation) (acquired bool) {
	vis := allVisibleService(terrain)
	for tick := uint32(1); tick <= visitsPerProbe; tick++ {
		svc.StepWeaponsForUnit(shooter, tick, w, vis, terrain, nil, cat, sim, nil)
		if shooter.SlotAt(0).Target.Kind == units.TargetUnit && shooter.SlotAt(0).Target.Unit != 0 {
			acquired = true
		}
	}
	return acquired
}

// TestCommandFireNeverAutoAcquiresForAHuman is the contract sentence of
// [06 §3.2]: "either the owning player's controller type is 2 (computer) or the
// weapon is **not** `commandfire`", whose stated consequence is that a human
// player's units never acquire autonomously with a command-fire weapon and a
// computer player's do.
func TestCommandFireNeverAutoAcquiresForAHuman(t *testing.T) {
	cases := []struct {
		name        string
		commandFire bool
		control     uint8
		wantAcquire bool
	}{
		{"commandfire under a human", true, ControlByteHuman, false},
		{"commandfire under a computer", true, ControlByteComputer, true},
		{"ordinary weapon under a human", false, ControlByteHuman, true},
		{"ordinary weapon under a computer", false, ControlByteComputer, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, shooter, w, terrain, cat := commandFireProbe(t, tc.commandFire, tc.control)
			got := runProbeVisits(svc, shooter, w, terrain, cat, simRNGPtr(1))
			if got != tc.wantAcquire {
				t.Fatalf("after %d slot visits acquired=%v, want %v [06 §3.2]", visitsPerProbe, got, tc.wantAcquire)
			}
			if !tc.wantAcquire && svc.Count() != 0 {
				t.Fatalf("a command-fire weapon under controller %d fired %d projectiles with no order [06 §3.2]", tc.control, svc.Count())
			}
		})
	}
}

// TestCommandFireUnoccupiedRowDoesNotAutoAcquire pins the exact-2 comparison:
// only controller type 2 opens the command-fire arm, so an unoccupied row —
// what a nil accessor reads as [06 R-DMG-01 §9] — answers like a human's.
func TestCommandFireUnoccupiedRowDoesNotAutoAcquire(t *testing.T) {
	svc, shooter, w, terrain, cat := commandFireProbe(t, true, ControlByteAbsent)
	svc.ControlByte = nil // every row reads as unoccupied [06 R-DMG-01 §8]
	if runProbeVisits(svc, shooter, w, terrain, cat, simRNGPtr(1)) {
		t.Fatalf("a command-fire weapon acquired with no player row bound; only controller type 2 admits it [06 §3.2]")
	}
	if !AutonomousScanAdmitsSlot(&content.WeaponDef{}, ControlByteAbsent) {
		t.Fatalf("an ordinary weapon must acquire for every controller [06 §3.2]")
	}
	if AutonomousScanAdmitsSlot(nil, ControlByteComputer) {
		t.Fatalf("a slot with no resolved weapon acquires nothing")
	}
}

// TestManualTargetStillFiresACommandFireWeapon is the other half of the same
// contract. The gate suppresses ACQUISITION only: a target installed by the
// manual path — `AttackSpecial` resolves command code 3, sets p1 = 2, and the
// resolved attack handler binds slot 2 from its next visit [04 R-ORD-01 §2]
// [04 R-ORD-01 §3] — reaches the ordinary shot-time gates, because forced
// installation bypasses the autonomous lists and nothing else [06 §3.2].
//
// This drives the slot-level manual-fire entry rather than the order layer:
// binding slot 2 to the target is exactly what the order handler's *bind slot k
// to unit* helper does [04 R-ORD-01 §1], and internal/orders owns its own
// `AttackSpecial` cases.
func TestManualTargetStillFiresACommandFireWeapon(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{0x10021001, 1, 0x10065000}
	vm := cob.NewVM(progWithAim(code, "AimTertiary", 0))
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(78)
	weapon.CommandFire = true
	weapon.Tolerance = wideDriftTolerance
	shooter.InstallWeapon(2, weapon) // AttackSpecial's weapon-slot selection 2 [04 R-ORD-01 §2]
	slot := shooter.SlotAt(2)
	slot.Reload = 0
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	svc := &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
	vis := allVisibleService(terrain)

	// With no manual target the human's command-fire slot stays silent.
	for tick := uint32(1); tick <= 5; tick++ {
		svc.StepWeaponsForUnit(shooter, tick, w, vis, terrain, nil, cat, simRNGPtr(1), nil)
	}
	if svc.Count() != 0 {
		t.Fatalf("command-fire slot fired %d projectiles before any order [06 §3.2]", svc.Count())
	}

	// The manual install. The order layer stores the unit id with the unit
	// marker and does not touch the slot flags [04 R-ORD-01 §1].
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	fired := false
	for tick := uint32(6); tick <= 20 && !fired; tick++ {
		svc.StepWeaponsForUnit(shooter, tick, w, vis, terrain, nil, cat, simRNGPtr(1), nil)
		fired = svc.Count() != 0
	}
	if !fired {
		t.Fatalf("a manually installed target did not fire the command-fire weapon [06 §3.2][04 R-ORD-01 §3]")
	}

	// The same holds when the tracking flag is clear, which is the state a
	// previous target's death leaves behind: the slot must not re-acquire, and
	// must not lose the installed target either.
	svc2 := &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags &^= 0x02
	slot.Reload = 0
	svc2.StepWeaponsForUnit(shooter, 21, w, vis, terrain, nil, cat, simRNGPtr(1), nil)
	if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != target.Handle {
		t.Fatalf("the acquisition gate dropped a manually installed target: %+v", slot.Target)
	}
}

// TestNoCommandFireWeaponConsumesTheSameDraws is I4: the gate must not move the
// simulation stream for content that has no command-fire weapon at all. The
// scan's draws are the swap-remove sampling and the per-candidate score of
// [06 §3.2], and a scenario the gate never refuses must consume exactly them.
func TestNoCommandFireWeaponConsumesTheSameDraws(t *testing.T) {
	svc, shooter, w, terrain, cat := commandFireProbe(t, false, ControlByteHuman)
	sim := simRNGPtr(12345)
	runProbeVisits(svc, shooter, w, terrain, cat, sim)
	human := sim.Draws()

	svc2, shooter2, w2, terrain2, cat2 := commandFireProbe(t, false, ControlByteComputer)
	sim2 := simRNGPtr(12345)
	runProbeVisits(svc2, shooter2, w2, terrain2, cat2, sim2)
	computer := sim2.Draws()

	if human != computer {
		t.Fatalf("an ordinary weapon drew %d under a human and %d under a computer; the gate must not touch it (I4)[06 §3.2]", human, computer)
	}
	if human == 0 {
		t.Fatalf("the acquisition scan consumed no draws at all; the fixture proves nothing [06 §3.2]")
	}
}

// TestCommandFireGateRemovesTheScanDraws is the paired positive: the refused
// slot runs no acquisition, so it consumes none of the scan's draws.
func TestCommandFireGateRemovesTheScanDraws(t *testing.T) {
	svc, shooter, w, terrain, cat := commandFireProbe(t, true, ControlByteHuman)
	sim := simRNGPtr(12345)
	runProbeVisits(svc, shooter, w, terrain, cat, sim)
	if got := sim.Draws(); got != 0 {
		t.Fatalf("a refused command-fire slot consumed %d simulation draws, want none [06 §3.2]", got)
	}
}

// ---------------------------------------------------------------------------
// The blast's shooter exclusion [06 §9.3]
// ---------------------------------------------------------------------------

// blastFixture places a shooter and a neighbour on the same spot so both are
// inside a radius the blast certainly covers, and gives each a distinct owner
// so the provenance stamp is legible.
func blastFixture(t *testing.T) (*Service, *units.World, *world.Terrain, *units.Unit, *units.Unit) {
	t.Helper()
	w := newCombatFixtureWorld(10, nil)
	terrain := &world.Terrain{CellW: 100, CellH: 100}
	terrain.Plot = make([]world.PlotCell, 100*100)
	def := &content.UnitDef{UnitName: "blastfixture", MaxDamage: 5000, Limit: -1, FootprintX: 1, FootprintZ: 1}
	at := func(owner uint8, x int64) *units.Unit {
		h, err := w.Create(def, owner, numeric.FixedFromInt(x), numeric.FixedFromInt(10), numeric.FixedFromInt(40))
		if err != nil {
			t.Fatalf("create unit: %v", err)
		}
		u := w.Unit(h)
		u.Y = numeric.FixedFromInt(10)
		u.Health = 5000
		u.MaxHealth = 5000
		return u
	}
	shooter := at(3, 40)   // owner 3 so the stamp cannot be confused with slot 0
	neighbour := at(1, 44) // four world units away, well inside the blast
	svc := &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
	return svc, w, terrain, shooter, neighbour
}

// TestShooterIsNotItsOwnBlastVictim is [06 §9.3]: "A unit candidate must be
// nonzero **and must not be the record's shooter** — the shooter is
// unconditionally excluded from every blast, which is the whole of retail's
// self-damage policy", restated in [06 R-DMG-01 §9]. The neighbour is the
// control: there is no owner or alliance test in this enumeration, so it takes
// full damage and is stamped with the shooter's side [06 §12.1].
func TestShooterIsNotItsOwnBlastVictim(t *testing.T) {
	svc, w, terrain, shooter, neighbour := blastFixture(t)
	shooter.LastDamageSide = 9 // a value nothing in this test writes
	shooter.LastDamageCause = 0
	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 200, DamageDefault: 400, EdgeEffectiveness: 0}

	impact := Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, shooter.Handle, 1)

	if shooter.Health != 5000 {
		t.Fatalf("the shooter took %d damage from its own blast; the shooter is excluded from every blast [06 §9.3]", 5000-shooter.Health)
	}
	if shooter.LastDamageSide != 9 || shooter.LastDamageCause != 0 {
		t.Fatalf("the shooter's provenance was stamped by its own blast: side %d cause %d, want 9/0 [06 §12.1]",
			shooter.LastDamageSide, shooter.LastDamageCause)
	}
	if neighbour.Health >= 5000 {
		t.Fatalf("the neighbour took no damage; the exclusion is the shooter only, with no owner or alliance test [06 §9.3]")
	}
	if neighbour.LastDamageSide != shooter.Owner {
		t.Fatalf("neighbour credited to side %d, want the shooter's side %d [06 §12.1]", neighbour.LastDamageSide, shooter.Owner)
	}
	if Cause(neighbour.LastDamageCause) != CauseOrdinary {
		t.Fatalf("neighbour cause %d, want the ordinary weapon cause [06 §12.1]", neighbour.LastDamageCause)
	}
}

// TestNullShooterBlastMatchesNobody is the other arm of the same sentence: a
// null shooter matches no unit, which is what lets a meteor or a death
// explosion damage every side alike and credit nobody [06 R-DMG-01 §9].
func TestNullShooterBlastMatchesNobody(t *testing.T) {
	svc, w, terrain, shooter, neighbour := blastFixture(t)
	shooter.LastDamageSide = 9
	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 200, DamageDefault: 400, EdgeEffectiveness: 0}

	impact := Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}
	svc.ExplodeWeaponAt(w, terrain, weapon, impact, pool.Handle(0), 1)

	if shooter.Health >= 5000 || neighbour.Health >= 5000 {
		t.Fatalf("a null-shooter blast spared a unit: shooter %d neighbour %d; null matches nobody [06 R-DMG-01 §9]",
			shooter.Health, neighbour.Health)
	}
	if shooter.LastDamageSide != 9 {
		t.Fatalf("a null-shooter record stamped provenance %d; it credits nobody [06 R-DMG-01 §9][06 §12.1]", shooter.LastDamageSide)
	}
}

// TestAreaEnumerationExcludesShooterBeforeDedup locks the same rule in the
// documented enumeration of [06 §9.3]: the shooter test sits with the nonzero
// test, ahead of the twenty-entry dedup memory, so the shooter never consumes
// one of its entries.
func TestAreaEnumerationExcludesShooterBeforeDedup(t *testing.T) {
	box := func(h pool.Handle) UnitForArea {
		return UnitForArea{
			Handle: h,
			Pos:    Vec3{X: numericFromInt(0), Y: numericFromInt(0), Z: numericFromInt(0)},
			Min:    Vec3{X: numericFromInt(-2), Y: numericFromInt(-2), Z: numericFromInt(-2)},
			Max:    Vec3{X: numericFromInt(2), Y: numericFromInt(2), Z: numericFromInt(2)},
		}
	}
	weapon := &content.WeaponDef{AreaOfEffect: 40, EdgeEffectiveness: 0}
	radius := BlastRadius(weapon.AreaOfEffect)
	impact := Vec3{X: numericFromInt(0), Y: numericFromInt(0), Z: numericFromInt(0)}
	cells := func(cx, cz int32) [2]pool.Handle {
		if cx == 0 && cz == 0 {
			return [2]pool.Handle{1, 2} // slot zero then slot one [06 §9.3]
		}
		return [2]pool.Handle{0, 0}
	}
	view := func(h pool.Handle) (UnitForArea, bool) { return box(h), true }

	var visited []pool.Handle
	ApplyAreaDamage(impact, weapon, 1, 0, 0, radius, 10, 10, cells, view, false,
		func(victim pool.Handle, _ float32, _ int32) { visited = append(visited, victim) })
	if len(visited) != 1 || visited[0] != 2 {
		t.Fatalf("shooter 1 in its own blast produced victims %v, want only the other unit [06 §9.3]", visited)
	}

	visited = nil
	ApplyAreaDamage(impact, weapon, 0, 0, 0, radius, 10, 10, cells, view, false,
		func(victim pool.Handle, _ float32, _ int32) { visited = append(visited, victim) })
	if len(visited) != 2 {
		t.Fatalf("a null shooter excluded somebody: victims %v; null matches nobody [06 R-DMG-01 §9]", visited)
	}
}
