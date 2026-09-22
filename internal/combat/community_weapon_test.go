package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func communityWeaponService(enabled bool) *Service {
	return &Service{
		Rules:     &CommunityRules{},
		Community: community.Features{WeaponTargetKeys: enabled},
	}
}

func TestCommunityTargetKeysComparisonEdgesAndStrictBypass(t *testing.T) {
	strict := &Service{Rules: StrictRules{}, Community: community.Features{WeaponTargetKeys: true}}
	off := communityWeaponService(false)
	on := communityWeaponService(true)
	w := &content.WeaponDef{WaterWeapon: true, SurfaceFire: true}
	q := TargetAdmission{
		Weapon:      w,
		WaterWeapon: true,
		Sea:         10,
		Target:      unitGateEnd{Y: 11},
	}
	for _, tc := range []struct {
		name string
		svc  *Service
	}{{"strict", strict}, {"flag-off", off}} {
		name, svc := tc.name, tc.svc
		q.Service = svc
		if svc.rules().AdmitTarget(q) {
			t.Fatalf("%s read surfacefire; the retail water gate must reject above-sea target", name)
		}
	}
	q.Service = on
	if !on.rules().AdmitTarget(q) {
		t.Fatal("surfacefire did not bypass the ordinary above-sea rejection")
	}

	// The second stock rejection is independent: a floater bypasses the first,
	// while canhover plus half the model top still rejects. surfacefire bypasses
	// both (docs/DESIGN_COMMUNITY_PATCH.md §4.2, CP-WPN-3).
	q.Target = unitGateEnd{Y: 9, ModelTop: 4, Floater: true, CanHover: true}
	w.SurfaceFire = false
	if on.rules().AdmitTarget(q) {
		t.Fatal("untagged hover target passed the second above-sea rejection")
	}
	w.SurfaceFire = true
	if !on.rules().AdmitTarget(q) {
		t.Fatal("surfacefire did not bypass the hover above-sea rejection")
	}

	// nottoair is based on the position-committed unit mode and wins even when surfacefire
	// would otherwise admit. A landed aircraft remains eligible.
	w.NotToAir = true
	q.Target = unitGateEnd{Y: 11, MoverMode: 1, UnitMode: airborneMoverMode}
	if on.rules().AdmitTarget(q) {
		t.Fatal("nottoair lost to surfacefire for an airborne target")
	}
	q.Target.UnitMode = 1
	q.Target.MoverMode = airborneMoverMode
	if !on.rules().AdmitTarget(q) {
		t.Fatal("nottoair rejected a landed aircraft")
	}

	// nottounderwater compares the separately truncated target position and
	// model top at the convergence of every water allow path; equality rejects.
	w.NotToAir = false
	w.NotToUnderwater = true
	q.Target = unitGateEnd{Y: 6, ModelTop: 4, Floater: true, CanHover: true}
	if on.rules().AdmitTarget(q) {
		t.Fatal("nottounderwater admitted target top equal to sea level")
	}
	q.Target.Y++
	if !on.rules().AdmitTarget(q) {
		t.Fatal("nottounderwater rejected target top strictly above sea level")
	}

	// The community-patch line has no toaironly reader.
	w.NotToUnderwater = false
	w.ToAirOnly = true
	q.Target = unitGateEnd{Y: 11, MoverMode: 1}
	if !on.rules().AdmitTarget(q) {
		t.Fatal("toaironly acquired a reader despite remaining inert")
	}
}

func TestCommunityTargetGateIsUsedByTheServiceBoundary(t *testing.T) {
	unitWorld, terrain, shooter, target := newTestWorldAndUnits(t)
	terrain.SeaLevel = 5
	weapon := &content.WeaponDef{Range: 100, WaterWeapon: true, SurfaceFire: true}
	shooter.InstallWeapon(0, weapon)
	strict := &Service{Rules: StrictRules{}, Community: community.Features{WeaponTargetKeys: true}}
	if strict.CanEngageSlotTarget(shooter, target, 0, terrain) {
		t.Fatal("Strict service read surfacefire at the shared target boundary")
	}
	svc := communityWeaponService(true)
	if !svc.CanEngageSlotTarget(shooter, target, 0, terrain) {
		t.Fatal("Community service did not dispatch surfacefire through the shared target boundary")
	}
	target.Move.Mode = 1
	target.Move.ModeMirror = airborneMoverMode
	weapon.NotToAir = true
	if svc.CanEngageSlotTarget(shooter, target, 0, terrain) {
		t.Fatal("Community service admitted an airborne nottoair target")
	}
	if svc.SlotAcquisitionAdmits(shooter, 0, target, unitWorld, nil, terrain, nil, nil) {
		t.Fatal("acquisition ignored the published airborne mode")
	}
	target.Move.ModeMirror = 1
	target.Move.Mode = airborneMoverMode
	if !svc.CanEngageSlotTarget(shooter, target, 0, terrain) {
		t.Fatal("nottoair read the unpublished live mover mode")
	}
}

func TestCommunityTerrainGateRunsAfterReloadWithoutLaterEffects(t *testing.T) {
	worldUnits, terrain, shooter, _ := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{
		ID: 81, LineOfSight: true, WaterWeapon: true, Range: 100,
		NoOverWater: true, EnergyPerShot: 9, MetalPerShot: 7,
	}
	shooter.InstallWeapon(0, weapon)
	if !FireWeaponPoint(shooter, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(12), 1) {
		t.Fatal("point target setup failed")
	}
	slot := shooter.SlotAt(0)
	slot.Reload = 1
	targetBefore, aimBefore := slot.Target, slot.Aim
	econ := &economy.Service{}
	econ.Players[shooter.Owner].Stock = [2]float32{100, 100}
	stockBefore := econ.Players[shooter.Owner].Stock
	sim := rng.NewSimulation(1)
	svc := communityWeaponService(true)
	sum := svc.StepWeaponsForUnit(shooter, 1, worldUnits, nil, terrain, econ, nil, &sim, nil)
	if slot.Reload != 0 {
		t.Fatalf("suppressed slot reload = %d, want decremented to zero", slot.Reload)
	}
	if slot.Target != targetBefore || slot.Aim != aimBefore {
		t.Fatalf("terrain gate changed retained target/aim: target=%+v aim=%+v", slot.Target, slot.Aim)
	}
	if sum.Dispatched || sum.Fired != 0 || svc.Count() != 0 {
		t.Fatalf("terrain gate reached aim or fire: %+v projectiles=%d", sum, svc.Count())
	}
	if econ.Players[shooter.Owner].Stock != stockBefore || sim.Draws() != 0 {
		t.Fatalf("terrain gate changed resources or RNG: stock=%v draws=%d", econ.Players[shooter.Owner].Stock, sim.Draws())
	}
}

func TestCommunityTerrainGateWaterLandAndOffMapEdges(t *testing.T) {
	svc := communityWeaponService(true)
	u := &units.Unit{X: 0, Z: 0}
	terrain := &world.Terrain{CellW: 2, CellH: 2, SeaLevel: 10, Plot: make([]world.PlotCell, 4)}
	w := &content.WeaponDef{NoOverWater: true}
	if svc.rules().SlotMayFire(svc, u, w, terrain) {
		t.Fatal("height equal to sea level was not classified as water")
	}
	w.NoOverWater, w.NoOverLand = false, true
	if !svc.rules().SlotMayFire(svc, u, w, terrain) {
		t.Fatal("water terrain was classified as land")
	}
	for i := range terrain.Plot {
		terrain.Plot[i].SetHeight(11)
		terrain.Plot[i].SetMinHeight(11)
		terrain.Plot[i].SetMaxHeight(11)
	}
	if svc.rules().SlotMayFire(svc, u, w, terrain) {
		t.Fatal("height strictly above sea level was not classified as land")
	}
	u.X = numeric.FixedFromInt(-100)
	u.Z = numeric.FixedFromInt(-100)
	w.NoOverWater, w.NoOverLand = true, true
	if !svc.rules().SlotMayFire(svc, u, w, terrain) {
		t.Fatal("negative off-map terrain answer did not fall through")
	}
}

func TestCommunitySurfaceFireKeepsSelfPropelledGuidanceAtSeaLevel(t *testing.T) {
	w := &content.WeaponDef{
		SelfProp: true, WaterWeapon: true, SurfaceFire: true, Guidance: true,
		WeaponVelocity: 65536, WeaponAcceleration: 65536, TurnRate: 32767,
	}
	base := Projectile{
		Pos:        Vec3{Y: numeric.FixedFromInt(10)},
		TargetPos:  Vec3{X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(10)},
		ExpiryTick: 10,
	}
	strictP := base
	strict := &Service{Rules: StrictRules{}, Community: community.Features{WeaponTargetKeys: true}}
	AdvanceSelfProp(&strictP, w, 1, 0, numeric.FixedFromInt(10), GuidanceEnv{Service: strict})
	if strictP.Speed != 0 {
		t.Fatalf("Strict water projectile at sea level accelerated to %v", strictP.Speed)
	}
	communityP := base
	svc := communityWeaponService(true)
	AdvanceSelfProp(&communityP, w, 1, 0, numeric.FixedFromInt(10), GuidanceEnv{Service: svc})
	if communityP.Speed == 0 {
		t.Fatal("surfacefire did not keep self-propelled motion/guidance at sea level")
	}
	offP := base
	off := communityWeaponService(false)
	AdvanceSelfProp(&offP, w, 1, 0, numeric.FixedFromInt(10), GuidanceEnv{Service: off})
	if offP.Speed != 0 {
		t.Fatal("feature-off surfacefire changed Strict guidance")
	}
}

type admitShotTimeOverrideRules struct{ StrictRules }

func (admitShotTimeOverrideRules) ShotTimeAdmitted(ShotTimeAdmission) bool { return true }

func TestCommunitySurfaceFireShortCircuitsShotTimeSuffixAfterRange(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 50}
	shooter := &units.Unit{
		Def: &content.UnitDef{UnitName: "turret", ModelTop: 20},
		Y:   numeric.FixedFromInt(20),
	}
	inside := Vec3{X: numeric.FixedFromInt(50)}
	out := Vec3{X: numeric.FixedFromInt(101)}
	weapon := &content.WeaponDef{Range: 100, SurfaceFire: true}

	for _, tc := range []struct {
		name string
		svc  *Service
	}{
		{"explicit strict ignores enabled table", &Service{Rules: StrictRules{}, Community: community.Features{WeaponTargetKeys: true}}},
		{"community feature off", communityWeaponService(false)},
	} {
		if checkAdmission(tc.svc, shooter, weapon, inside, terrain) {
			t.Fatalf("%s bypassed the submerged-shooter clause", tc.name)
		}
	}

	svc := communityWeaponService(true)
	if !checkAdmission(svc, shooter, weapon, inside, terrain) {
		t.Fatal("surfacefire did not short-circuit the shooter-depth suffix")
	}
	if checkAdmission(svc, shooter, weapon, out, terrain) {
		t.Fatal("surfacefire bypassed the preceding range clause")
	}

	// The router tests surfacefire itself, not waterweapon. The same enabled
	// non-water tag therefore skips a ballistic no-solution result as well.
	shooter.Y = numeric.FixedFromInt(60)
	weapon.Ballistic = true
	if !svc.ShotTimeAdmitsPoint(shooterWithWeapon(shooter, weapon), 0, inside.X, inside.Y, inside.Z, terrain) {
		t.Fatal("surfacefire did not short-circuit the ballistic suffix")
	}
	weapon.SurfaceFire = false
	if svc.ShotTimeAdmitsPoint(shooterWithWeapon(shooter, weapon), 0, inside.X, inside.Y, inside.Z, terrain) {
		t.Fatal("untagged ballistic weapon bypassed its no-solution result")
	}

	// Both callers dispatch through the replaceable request-granular method.
	custom := &Service{Rules: admitShotTimeOverrideRules{}}
	if !checkAdmission(custom, shooter, weapon, inside, terrain) ||
		!custom.ShotTimeAdmitsPoint(shooterWithWeapon(shooter, weapon), 0, inside.X, inside.Y, inside.Z, terrain) {
		t.Fatal("shot-time callers did not use the registered rule method")
	}
}

func shooterWithWeapon(shooter *units.Unit, weapon *content.WeaponDef) *units.Unit {
	shooter.SlotAt(0).Weapon = weapon
	return shooter
}

func TestCommunityNoMapWeaponAlertSkipsDamageBroadcastOnly(t *testing.T) {
	weapon := &content.WeaponDef{
		DamageDefault:    0,
		Damage:           map[string]int32{"combatfixture": 25},
		AreaOfEffect:     32,
		NoMapWeaponAlert: true,
	}
	for _, tc := range []struct {
		name       string
		svc        *Service
		wantDamage bool
	}{
		{"strict", &Service{Rules: StrictRules{}, Community: community.Features{WeaponTargetKeys: true}}, true},
		{"feature-off", communityWeaponService(false), true},
		{"community", communityWeaponService(true), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worldUnits, terrain, _, target := newTestWorldAndUnits(t)
			before := target.Health
			p := &Projectile{Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}, ShooterSide: 10}
			if got := tc.svc.MapWeaponMarker(p, weapon); got != tc.wantDamage {
				// Marker and detonation share one predicate: damage runs exactly
				// when the marker remains visible.
				t.Fatalf("marker visibility disagrees with expected detonation: show=%v", tc.svc.MapWeaponMarker(p, weapon))
			}
			handleProjectileImpact(tc.svc, 0, p, weapon, worldUnits, terrain, nil, nil, nil, 1, Vec3{}, nil, 0)
			damaged := target.Health != before
			if damaged != tc.wantDamage {
				t.Fatalf("damage broadcast ran=%v, want %v (health %d -> %d)", damaged, tc.wantDamage, before, target.Health)
			}
		})
	}

	// Every predicate term is required and evaluated independently.
	svc := communityWeaponService(true)
	p := &Projectile{}
	if svc.MapWeaponMarker(p, &content.WeaponDef{DamageDefault: 1, NoMapWeaponAlert: true}) {
		// nonzero damage must show
	} else {
		t.Fatal("nonzero-damage tagged weapon hid its marker")
	}
	p.Shooter = 7
	if !svc.MapWeaponMarker(p, weapon) {
		t.Fatal("tagged zero-damage weapon with an attacker hid its marker")
	}
	p.Shooter = 0
	untagged := *weapon
	untagged.NoMapWeaponAlert = false
	if !svc.MapWeaponMarker(p, &untagged) {
		t.Fatal("untagged attacker-less zero-damage weapon hid its marker")
	}
}
