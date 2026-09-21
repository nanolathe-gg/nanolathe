package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The sight-distance caller is the opportunity scan of [04 R-STANCE-01 §3] —
// the idle/loiter arms of `Standby`, `Standby_Mine`, `Patrol` and the three
// VTOL rows — which "runs the shared unit-level target search … with its range
// argument taken from the definition's `sightdistance`" on slot 0. Two clauses
// of [06 §3.2] belong to that caller and to no other:
//
//   - check 4, "for the sight-distance caller only, the candidate's definition
//     index must be clear of the `nochasecategory` mask", between check 3 and
//     check 5; and
//   - the separation of the §3.1 filter radius (the caller's sight distance)
//     from the physical gate's range clause, which is always "an inclusive
//     signed 32-bit compare against the slot weapon's `range`"
//     [06 R-WPN-05 §1] clause 5 [06 R-WPN-05 §9].
//
// Both also fix the shared stream: a rejection at either clause removes that
// candidate's SCORING draw while leaving its sampling draw and its place in the
// fifty-pick limit intact [06 §3.2 "Draw consequence"], so the draw counts are
// part of the contract, not decoration.

// sightScanFixture is a standby-shaped shooter with one hostile in its
// registry's primary list. sight and reach are whole world units; the shooter
// stands at the origin cell and the hostile `reach` world units away on X.
func sightScanFixture(t *testing.T, reach int32, weaponRange int32) (*Service, *units.World, *world.Terrain, *units.Unit, *units.Unit) {
	t.Helper()
	terrain := &world.Terrain{CellW: 200, CellH: 200, Gravity: numeric.Fixed(0)}
	terrain.Plot = make([]world.PlotCell, 200*200)
	w := newCombatFixtureWorld(10, nil)

	// `standingfireorder` 2 is fire at will, the only value that opens the
	// opportunity scan [04 R-STANCE-01 §3]; the model-top word keeps both ends
	// of the gate's non-water height clause above sea level [06 R-WPN-05 §1].
	shooterDef := &content.UnitDef{UnitName: "standby", MaxDamage: 100, Limit: -1, StandingFireOrder: 2, ModelTopFixed: 16 << 16}
	// The hostile authors `shootme` (check 2's candidate disjunct) because this
	// fixture binds no player table, so its shooter reads as "not a computer"
	// [06 §3.2]. Its definition mask is the identity bit the shooter's
	// `nochasecategory` set names below — the masks are "indexed by
	// unit-definition index" [06 §3.1].
	hostileDef := &content.UnitDef{UnitName: "aircraft", MaxDamage: 100, Limit: -1, ModelTopFixed: 16 << 16, ShootMe: true, UnitMask: content.MaskForID(5)}

	shooterH, err := w.Create(shooterDef, 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(10), numeric.FixedFromInt(1000))
	if err != nil {
		t.Fatal(err)
	}
	hostileH, err := w.Create(hostileDef, 1, numeric.FixedFromInt(int64(1000+reach)), numeric.FixedFromInt(10), numeric.FixedFromInt(1000))
	if err != nil {
		t.Fatal(err)
	}
	shooter, hostile := w.Unit(shooterH), w.Unit(hostileH)
	shooter.Flags |= units.ArmedStatus
	shooter.InstallWeapon(0, &content.WeaponDef{ID: 77, Range: weaponRange, Turret: true, LineOfSight: true, WeaponVelocity: 100 * 65536 / 30})

	s := &Service{Visibility: func(visibility.PlayerID, visibility.Target) bool { return true }}
	// The registry is filled directly: the cadence, not the candidate set, is
	// what a rebuild would decide here [06 §3.1].
	s.targets.primary[shooter.Owner] = []pool.Handle{hostile.Handle}
	return s, w, terrain, shooter, hostile
}

// sightScanDraws runs one opportunity-scan-shaped acquisition and reports the
// result together with the simulation draws it spent. A sight distance of zero
// stands for the autonomous caller, which supplies no radius at all.
func sightScanDraws(s *Service, w *units.World, terrain *world.Terrain, shooter *units.Unit, sight uint32) (pool.Handle, bool, uint64) {
	random := rng.NewSimulation(9)
	before := random.Draws()
	h, ok := s.AcquireWeaponTarget(shooter, 0, sight, w, nil, terrain, nil, nil, &random)
	return h, ok, random.Draws() - before
}

// Check 4 of the picked-candidate order [06 §3.2]. 70 stock mobile definitions
// author a non-`none` no-chase value — every ground combat unit names `VTOL` —
// and every one of them defaults to `Standby`, so this is the clause that keeps
// an idle unit at its post when an aircraft crosses its sight radius.
//
// Only the sight-distance caller applies it: the shooter below is unchanged
// between the two runs except for its own `nochasecategory` set.
func TestSightDistanceCallerRejectsNoChaseCandidateWithoutScoringDraw(t *testing.T) {
	// One candidate, well inside both the sight radius and the weapon range,
	// so nothing but check 4 can separate the two runs. With a single
	// candidate the sampling draw has bound one and does not advance the
	// stream, which leaves the scoring draw alone visible in the count.
	const sight = 280

	s, w, terrain, shooter, hostile := sightScanFixture(t, 250, 1000)
	h, ok, draws := sightScanDraws(s, w, terrain, shooter, sight)
	if !ok || h != hostile.Handle {
		t.Fatalf("with an empty no-chase set the aircraft is an ordinary candidate: got %d ok=%v", h, ok)
	}
	if draws != 1 {
		t.Fatalf("an admitted candidate spends exactly its scoring draw: got %d [06 §3.2]", draws)
	}

	s, w, terrain, shooter, _ = sightScanFixture(t, 250, 1000)
	shooter.Def.NoChaseCategoryMask = content.MaskForID(5)
	if _, ok, draws = sightScanDraws(s, w, terrain, shooter, sight); ok {
		t.Fatal("the sight-distance caller acquired a candidate inside its `nochasecategory` set [06 §3.2] check 4")
	}
	if draws != 0 {
		t.Fatalf("a check-4 rejection removes the scoring draw: got %d draws [06 §3.2 \"Draw consequence\"]", draws)
	}

	// The autonomous caller supplies no radius and never applies check 4, so
	// the same shooter and the same candidate acquire through that path.
	if _, ok, _ = sightScanDraws(s, w, terrain, shooter, 0); !ok {
		t.Fatal("check 4 leaked into the autonomous caller, which [06 §3.2] restricts to the sight-distance caller")
	}

	// The reaction offer's admission is the §3.1 physical gate alone
	// [06 R-WPN-04 §2 part 3], so it keeps bypassing check 4 as well.
	if !SlotAcquisitionAdmits(shooter, 0, w.Unit(s.targets.primary[0][0]), w, nil, terrain, nil, nil) {
		t.Fatal("the reaction offer applied the shooter's no-chase set; that clause is the sight-distance caller's alone")
	}
}

// The filter radius and the gate's range clause are two different values
// [06 §3.2][06 R-WPN-05 §1] clause 5. 29 stock mobile definitions author a
// weapon shorter than their sight distance — both commanders (laser 200, sight
// 290), the Peewee this fixture is shaped after (EMG 180, sight 280), the FAV
// (180 vs 310) — and running one value for both made every one of them acquire,
// and then chase, an enemy up to its whole sight radius away.
func TestSightDistanceFiltersByCallerRadiusAndGatesByWeaponRange(t *testing.T) {
	const sight = 280
	const weaponRange = 180

	// 250 is inside the sight radius, so the candidate is materialized and
	// takes its sampling draw, and outside the weapon range, so the gate's last
	// clause refuses it before the scoring draw.
	s, w, terrain, shooter, _ := sightScanFixture(t, 250, weaponRange)
	h, ok, draws := sightScanDraws(s, w, terrain, shooter, sight)
	if ok {
		t.Fatalf("acquired %d at 250 world units with a %d-unit weapon: the gate's range clause is the slot weapon's [06 R-WPN-05 §1] clause 5", h, weaponRange)
	}
	if draws != 0 {
		t.Fatalf("a gate rejection removes the scoring draw: got %d [06 §3.2]", draws)
	}

	// Inside the weapon range the same caller acquires, so the narrowing is the
	// gate's and not a shrunken filter.
	s, w, terrain, shooter, hostile := sightScanFixture(t, 175, weaponRange)
	if h, ok, _ = sightScanDraws(s, w, terrain, shooter, sight); !ok || h != hostile.Handle {
		t.Fatalf("the sight-distance caller refused a candidate inside the weapon range: got %d ok=%v", h, ok)
	}

	// The autonomous caller passes the slot weapon's range as its own filter
	// radius, so both values coincide there and it still acquires at 175.
	if h, ok, _ = sightScanDraws(s, w, terrain, shooter, 0); !ok || h != hostile.Handle {
		t.Fatalf("the autonomous caller lost its target: got %d ok=%v [06 §3.2]", h, ok)
	}
}

// Modern replaces the sampling and scoring of [06 §3.2], not the physical
// gates: "retains the existing medium, range and trajectory gates"
// (docs/DESIGN_WEAPONS_PROJECTILES.md "Modern threat targeting and incoming
// fire"). No Modern policy claims the no-chase category — the Modern danger
// response already honors the same mask before it proposes an attack — so both
// clauses hold for the sight-distance caller under Modern too, without any
// draw: Modern acquisition consumes no sampling or scoring RNG.
func TestModernSightDistanceCallerKeepsNoChaseAndWeaponRange(t *testing.T) {
	const sight = 280

	fixture := func(reach, weaponRange int32) (*Service, *units.World, *world.Terrain, *units.Unit, *units.Unit) {
		s, w, terrain, shooter, hostile := sightScanFixture(t, reach, weaponRange)
		s.Rules = &ModernRules{}
		s.Reaction = &ReactionSeams{Allied: func(a, b uint8) bool { return a == b }}
		// Modern admits only a candidate this weapon can actually damage.
		hostile.Def.DamageModifier = 65536
		weapon := *shooter.SlotAt(0).Weapon
		weapon.DamageDefault = 60
		weapon.ReloadTime = 30
		shooter.InstallWeapon(0, &weapon)
		return s, w, terrain, shooter, hostile
	}

	s, w, terrain, shooter, hostile := fixture(250, 1000)
	h, ok, draws := sightScanDraws(s, w, terrain, shooter, sight)
	if !ok || h != hostile.Handle {
		t.Fatalf("Modern lost an ordinary candidate: got %d ok=%v", h, ok)
	}
	if draws != 0 {
		t.Fatalf("Modern acquisition consumes no RNG: got %d draws", draws)
	}

	s, w, terrain, shooter, _ = fixture(250, 1000)
	shooter.Def.NoChaseCategoryMask = content.MaskForID(5)
	if _, ok, _ = sightScanDraws(s, w, terrain, shooter, sight); ok {
		t.Fatal("Modern chased a candidate inside the shooter's `nochasecategory` set")
	}
	if _, ok, _ = sightScanDraws(s, w, terrain, shooter, 0); !ok {
		t.Fatal("Modern applied check 4 to the autonomous caller")
	}

	s, w, terrain, shooter, _ = fixture(250, 180)
	if _, ok, _ = sightScanDraws(s, w, terrain, shooter, sight); ok {
		t.Fatal("Modern acquired beyond the slot weapon's range: the gate's range clause is retained policy")
	}
	s, w, terrain, shooter, hostile = fixture(175, 180)
	if h, ok, _ = sightScanDraws(s, w, terrain, shooter, sight); !ok || h != hostile.Handle {
		t.Fatalf("Modern refused a candidate inside the weapon range: got %d ok=%v", h, ok)
	}
}
