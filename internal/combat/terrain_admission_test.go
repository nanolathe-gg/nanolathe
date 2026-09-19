package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// directTerrainAdmission retains the direct-only entry for its contract tests.
func directTerrainAdmission(weapon *content.WeaponDef, muzzle, aim Vec3, tick uint32, terrain *world.Terrain, target *units.Unit) terrainShotResult {
	if weapon == nil || liveCreationFamilyForWeapon(weapon) != CreationOrdinary || MotionFamilyForWeapon(weapon) != MotionDirect {
		return terrainShotUnknown
	}
	return modernTerrainAdmission(Slot{Weapon: weapon}, muzzle, aim, tick, terrain, target, nil)
}

func modernTerrainWeapon() *content.WeaponDef {
	return &content.WeaponDef{ID: 7, LineOfSight: true, BeamWeapon: true, Range: 400,
		WeaponVelocity: 16 << 16, Tolerance: wideDriftTolerance, AreaOfEffect: 8}
}

func modernTerrainPoints() (Vec3, Vec3) {
	return Vec3{X: cellCentre(1), Y: numeric.FixedFromInt(16), Z: cellCentre(1)},
		Vec3{X: cellCentre(12), Y: numeric.FixedFromInt(16), Z: cellCentre(1)}
}

// These are Modern admission policies applied to the established launch,
// discrete contact and blast kernels, not claims about retail admission.
func TestModernTerrainDiscreteSamples(t *testing.T) {
	for _, tc := range []struct {
		name     string
		floor    uint8
		velocity int32
		muzzleY  int64
		cell     int32
		want     terrainShotResult
	}{
		{name: "ridge", floor: 32, velocity: 16, muzzleY: 16, cell: 4, want: terrainShotBlocked},
		{name: "strict equality clears", floor: 16, velocity: 16, muzzleY: 16, cell: 4, want: terrainShotClear},
		{name: "raised muzzle clears", floor: 32, velocity: 16, muzzleY: 64, cell: 4, want: terrainShotClear},
		{name: "ridge between samples", floor: 32, velocity: 48, muzzleY: 16, cell: 3, want: terrainShotClear},
		{name: "ground beyond aim", floor: 32, velocity: 16, muzzleY: 16, cell: 14, want: terrainShotClear},
		{name: "ground at aim", floor: 32, velocity: 16, muzzleY: 16, cell: 12, want: terrainShotClear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			terrain.PlotAt(tc.cell, 1).SetMinHeight(tc.floor)
			weapon := modernTerrainWeapon()
			weapon.WeaponVelocity = tc.velocity << 16
			muzzle, aim := modernTerrainPoints()
			muzzle.Y = numeric.FixedFromInt(tc.muzzleY)
			if got := directTerrainAdmission(weapon, muzzle, aim, 10, terrain, nil); got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
		})
	}
}

func TestModernTerrainPreservesUnitContactAndSplash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		air    bool
		y      int64
		splash bool
		want   terrainShotResult
	}{
		{name: "ground footprint before terrain", y: 16, want: terrainShotClear},
		{name: "air lower inclusive", air: true, y: 16, want: terrainShotClear},
		{name: "air upper inclusive", air: true, y: 0, want: terrainShotClear},
		{name: "air below band", air: true, y: 17, want: terrainShotBlocked},
		{name: "ground upper strict", y: 0, want: terrainShotBlocked},
		{name: "nearby blast reaches target box", y: 16, splash: true, want: terrainShotClear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			weapon := modernTerrainWeapon()
			muzzle, aim := modernTerrainPoints()
			target := &units.Unit{Handle: 2, Def: &content.UnitDef{ModelTopFixed: 16 << 16, FootprintX: 2, FootprintZ: 2}, X: aim.X, Y: numeric.FixedFromInt(tc.y), Z: aim.Z}
			cell := terrain.PlotAt(4, 1)
			cell.SetMinHeight(32)
			if tc.splash {
				target.X = cellCentre(5)
				weapon.AreaOfEffect = 64
			} else if tc.air {
				cell.SetOccupantB(2)
			} else {
				cell.SetOccupantA(2)
			}
			if got := directTerrainAdmission(weapon, muzzle, aim, 10, terrain, target); got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
		})
	}
}

func TestModernTerrainUnknownTrajectoriesRemainAdmitted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*content.WeaponDef)
	}{
		{"burst", func(w *content.WeaponDef) { w.Burst = 2 }},
		{"ballistic arc", func(w *content.WeaponDef) { w.LineOfSight = false; w.Turret = true; w.Ballistic = true }},
		{"guided missile", func(w *content.WeaponDef) { w.SelfProp = true; w.Guidance = true }},
		{"vertical launch", func(w *content.WeaponDef) { w.VLaunch = true }},
		{"dropped", func(w *content.WeaponDef) { w.LineOfSight = false; w.Dropped = true }},
		{"units only", func(w *content.WeaponDef) { w.UnitsOnly = true }},
		{"bounce", func(w *content.WeaponDef) { w.GroundBounce = true }},
		{"penetrating", func(w *content.WeaponDef) { w.NoExplode = true }},
		{"stationary projectile", func(w *content.WeaponDef) { w.WeaponAcceleration = 1 }},
		{"budget exhaustion", func(w *content.WeaponDef) { w.WeaponVelocity = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			terrain.PlotAt(4, 1).SetMinHeight(32)
			weapon := modernTerrainWeapon()
			tc.change(weapon)
			muzzle, aim := modernTerrainPoints()
			if got := directTerrainAdmission(weapon, muzzle, aim, 10, terrain, nil); got != terrainShotUnknown {
				t.Fatalf("admission=%v want unknown", got)
			}
		})
	}
}

func TestModernTerrainRefusesBeforeShotSideEffects(t *testing.T) {
	_, terrain := newContactFixture(t)
	terrain.PlotAt(4, 1).SetMinHeight(32)
	muzzle, aim := modernTerrainPoints()
	weapon := modernTerrainWeapon()
	weapon.Turret = true
	weapon.Accuracy = 128
	weapon.SoundStart = "laser"
	weapon.StartSmoke = true
	queries := 0
	r := rng.NewSimulation(1)
	script, events := &scriptRecorder{}, &eventRecorder{}
	var shot ShotQuery
	ports := FirePorts{Origin: Vec3{}, MuzzlePiece: func(int) int32 { queries++; return 3 }, MuzzleWorld: func(int32) (Vec3, bool) { return muzzle, true }, RNG: &r, Script: script, Events: events, ShooterHealth: 100, ShooterMaxHealth: 100, Shot: &shot}
	svc := Service{Rules: &terrainSpyRules{admit: func(q *ShotQuery) bool {
		if q.Muzzle != muzzle || q.Aim != aim {
			t.Fatal("preflight did not receive resolved muzzle and aim")
		}
		return directTerrainAdmission(weapon, q.Muzzle, q.Aim, 10, terrain, nil) != terrainShotBlocked
	}}}
	if _, ok := TryFire(&svc, &Slot{Weapon: weapon}, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, ports); ok {
		t.Fatal("blocked terrain shot fired")
	}
	if queries != 1 || r.Draws() != 0 || svc.Count() != 0 || len(script.calls) != 0 || len(events.sounds) != 0 || len(events.smoke) != 0 {
		t.Fatalf("blocked attempt mutated shot effects: queries=%d draws=%d count=%d script=%v events=%+v", queries, r.Draws(), svc.Count(), script.calls, events)
	}
}

func TestModernTerrainServicePreservesCostsAndRetryAim(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "modern"}[modern], func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			muzzle, aim := modernTerrainPoints()
			shooter.X, shooter.Y, shooter.Z = muzzle.X, muzzle.Y, muzzle.Z
			target.X, target.Y, target.Z = aim.X, aim.Y, aim.Z
			terrain.PlotAt(4, 1).SetMinHeight(32)
			weapon := modernTerrainWeapon()
			weapon.EnergyPerShot = 100
			weapon.MetalPerShot = 5
			weapon.ReloadTime = 30
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			slot.Ammo = 3
			priorPending := shooter.Pending
			var econ economy.Service
			econ.Players[shooter.Owner].Stock[economy.Energy] = 500
			econ.Players[shooter.Owner].Stock[economy.Metal] = 50
			svc := &Service{Rules: rulesForModern(modern)}
			var sum UnitStepSummary
			svc.firePreparedSlot(shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: aim}, 10, terrain, &econ, nil, w, nil, &sum)
			if modern {
				if shooter.Pending != priorPending {
					t.Fatal("terrain refusal changed order feedback")
				}
				if sum.Fired != 0 || slot.Reload != 0 || slot.Ammo != 3 || econ.Players[shooter.Owner].Stock[economy.Energy] != 500 || econ.Players[shooter.Owner].Stock[economy.Metal] != 50 {
					t.Fatalf("modern rejected shot changed resource/reload/ammunition: fired=%d slot=%+v stock=%v", sum.Fired, slot, econ.Players[shooter.Owner].Stock)
				}
				weapon.Stockpile = true
				svc.firePreparedSlot(shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: aim}, 11, terrain, &econ, nil, w, nil, &sum)
				if slot.Ammo != 3 || sum.Fired != 0 {
					t.Fatal("blocked stockpile launch spent ammunition")
				}
				weapon.Stockpile = false
				// Exercise the turret query's conversion separately from the Aim
				// handshake, keeping the same blocked geometry on every attempt.
				weapon.Turret = true
				shooter.Move.Heading = 1234
				slot.DesiredYaw = 2345
				slot.DesiredPitch = 3456
				for retry := 0; retry < 3; retry++ {
					if tryFireForSlot(shooter, slot, 0, 11+uint32(retry), terrain, nil, svc, w, nil, aim) {
						t.Fatal("blocked retry fired")
					}
					if slot.DesiredYaw != 2345 || slot.DesiredPitch != 3456 {
						t.Fatalf("blocked retry corrupted relative Aim: yaw=%d pitch=%d", slot.DesiredYaw, slot.DesiredPitch)
					}
				}
			} else if sum.Fired != 1 || slot.Reload == 0 || econ.Players[shooter.Owner].Stock[economy.Energy] != 400 || econ.Players[shooter.Owner].Stock[economy.Metal] != 45 {
				t.Fatalf("strict shot behavior changed: fired=%d reload=%d stock=%v", sum.Fired, slot.Reload, econ.Players[shooter.Owner].Stock)
			}
		})
	}
}

func TestModernAnnihilatorTerrainAdmissionRetail(t *testing.T) {
	catalog, _ := retailcat.Shared(t)
	unit := catalog.Units["armanni"]
	if unit == nil || unit.Weapon1Def == nil {
		t.Fatal("installed Annihilator primary weapon missing")
	}
	_, terrain := newContactFixture(t)
	for cx := int32(4); cx <= 7; cx++ {
		terrain.PlotAt(cx, 1).SetMinHeight(64)
	}
	muzzle, aim := modernTerrainPoints()
	if got := directTerrainAdmission(unit.Weapon1Def, muzzle, aim, 10, terrain, nil); got != terrainShotBlocked {
		t.Fatalf("installed Annihilator admission=%v want blocked", got)
	}
}

// guidedTerrainWeapon is a stock-shaped guided self-propelled missile: the
// Swatter's family (selfprop + guidance + tracks, a turret, no two-phase).
func guidedTerrainWeapon() *content.WeaponDef {
	return &content.WeaponDef{ID: 9, SelfProp: true, Guidance: true, Tracks: true, Turret: true,
		Range: 400, WeaponVelocity: 16 << 16, TurnRate: 1666, WeaponTimer: 150,
		Tolerance: wideDriftTolerance, AreaOfEffect: 8}
}

func immobileTerrainTarget(aim Vec3) *units.Unit {
	return &units.Unit{Handle: 2, Alive: true,
		Def: &content.UnitDef{ModelTopFixed: 16 << 16, FootprintX: 2, FootprintZ: 2, MaxVelocity: 0},
		X:   aim.X, Y: aim.Y, Z: aim.Z}
}

// TestModernGuidedPursuitReachesBeyondTheFirstTick locks the Modern extension
// of DESIGN_WEAPONS_PROJECTILES §2.3.1 "Guided pursuit against an immobile
// target": a ridge several ticks downrange is provable when the target cannot
// move, and is still admitted for every other target, which is the one-step
// proof this extension does not replace.
func TestModernGuidedPursuitReachesBeyondTheFirstTick(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ridgeCell   int32
		ridgeFloor  uint8
		maxVelocity int32
		burnBlow    bool
		noTarget    bool
		want        terrainShotResult
	}{
		{name: "distant ridge blocks for an immobile target", ridgeCell: 6, ridgeFloor: 32, want: terrainShotBlocked},
		{name: "clear path admits", ridgeCell: 6, ridgeFloor: 8, want: terrainShotClear},
		{name: "mobile target keeps the one-step proof", ridgeCell: 6, ridgeFloor: 32, maxVelocity: 2, want: terrainShotUnknown},
		{name: "no unit target keeps the one-step proof", ridgeCell: 6, ridgeFloor: 32, noTarget: true, want: terrainShotUnknown},
		{name: "burn-blow is excluded", ridgeCell: 6, ridgeFloor: 32, burnBlow: true, want: terrainShotUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			terrain.PlotAt(tc.ridgeCell, 1).SetMinHeight(tc.ridgeFloor)
			weapon := guidedTerrainWeapon()
			weapon.BurnBlow = tc.burnBlow
			muzzle, aim := modernTerrainPoints()
			var target *units.Unit
			if !tc.noTarget {
				target = immobileTerrainTarget(aim)
				target.Def.MaxVelocity = tc.maxVelocity
			}
			got := modernTerrainAdmission(Slot{Weapon: weapon}, muzzle, aim, 10, terrain, target, nil)
			if got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
		})
	}
}

// TestModernGuidedPursuitIsModernOnly proves the extension stays behind the
// central gameplay mode: Strict 3.1 never reaches the preview at all.
func TestModernGuidedPursuitIsModernOnly(t *testing.T) {
	svc := &Service{}
	if previewsShot(svc.rules()) || !svc.rules().AdmitShot(&ShotQuery{}) {
		t.Fatal("unbound rules must answer Strict 3.1; retail previews no terrain and admits [I11]")
	}
}

// terrainSpyRules answers AdmitShot from the presented query, so a test can
// both inspect what the spawner hands the seam and choose the verdict. It
// previews, which is what makes the spawner run the spread on value copies.
type terrainSpyRules struct {
	StrictRules
	admit func(q *ShotQuery) bool
}

func (r *terrainSpyRules) AdmitShot(q *ShotQuery) bool {
	q.Blocked = !r.admit(q)
	return !q.Blocked
}

func (r *terrainSpyRules) HoldsFire(*units.Unit) bool { return false }

func (r *terrainSpyRules) previewsShot() bool { return true }
