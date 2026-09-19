package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func modernBallisticSlot() Slot {
	return Slot{Weapon: &content.WeaponDef{ID: 7, Turret: true, Ballistic: true, Range: 400, WeaponVelocity: 16 << 16, AreaOfEffect: 8}, DesiredYaw: retailYawFromGo(16384), DesiredPitch: 8192}
}

func TestModernBallisticTrajectory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		floor    uint8
		pitch    uint16
		distance int32
		speed    int32
		cell     int32
		area     uint16
		want     terrainShotResult
	}{
		{name: "clear arc over straight-line obstruction", floor: 32, pitch: 8192, cell: 4, want: terrainShotClear},
		{name: "ridge intersects arc", floor: 64, pitch: 8192, cell: 4, want: terrainShotBlocked},
		{name: "distance word lowers launch", floor: 45, pitch: 8192, distance: 48 << 16, cell: 4, want: terrainShotBlocked},
		{name: "same arc without distance correction clears", floor: 45, pitch: 8192, cell: 4, want: terrainShotClear},
		{name: "ground equality", floor: 16, cell: 2, want: terrainShotClear},
		{name: "thin ridge between discrete samples", floor: 64, speed: 48 << 16, cell: 3, want: terrainShotClear},
		{name: "splash near intended point", floor: 64, pitch: 8192, cell: 4, area: 512, want: terrainShotClear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			launch := modernBallisticSlot()
			launch.DesiredPitch = tc.pitch
			launch.DistanceWord = tc.distance
			if tc.speed != 0 {
				launch.Weapon.WeaponVelocity = tc.speed
			}
			if tc.area != 0 {
				launch.Weapon.AreaOfEffect = int32(tc.area)
			}
			if tc.pitch != 0 {
				terrain.Gravity = numeric.FixedFromInt(1)
			}
			terrain.PlotAt(tc.cell, 1).SetMinHeight(tc.floor)
			muzzle, aim := modernTerrainPoints()
			if got := modernTerrainAdmission(launch, muzzle, aim, 10, terrain, nil, &world.Wind{}); got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
		})
	}
}

func TestModernBallisticWindKnowledge(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cell         int32
		deadline     uint32
		dirX         int32
		constantZero bool
		want         terrainShotResult
	}{
		{name: "overdue launch still knows current vector", cell: 4, dirX: 32 << 16, want: terrainShotBlocked},
		{name: "overdue redraw blocks later prediction", cell: 4, want: terrainShotUnknown},
		{name: "deadline equality permits following sample", cell: 3, deadline: 10, want: terrainShotBlocked},
		{name: "sample after due wind is unknown", cell: 4, deadline: 10, want: terrainShotUnknown},
		{name: "constant zero wind stays known", cell: 4, constantZero: true, want: terrainShotBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			terrain.PlotAt(tc.cell, 1).SetMinHeight(32)
			launch := modernBallisticSlot()
			launch.DesiredPitch = 0
			muzzle, aim := modernTerrainPoints()
			wind := world.Wind{Min: 1, Max: 100, NextChange: tc.deadline, DirX: tc.dirX}
			if tc.constantZero {
				wind.Min = 0
				wind.Max = 1
			}
			before := wind
			if got := modernTerrainAdmission(launch, muzzle, aim, 10, terrain, nil, &wind); got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
			if wind != before {
				t.Fatal("preview mutated wind")
			}
		})
	}
}

func TestModernGuidedLaunchCone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cell  int32
		floor uint8
		turn  uint16
		area  uint16
		burn  bool
		want  terrainShotResult
	}{
		{name: "all first-step turns hit ridge", cell: 2, floor: 32, turn: 512, want: terrainShotBlocked},
		{name: "possible climbing sample clears", cell: 2, floor: 16, turn: 512, want: terrainShotUnknown},
		{name: "ridge only after unknown guidance", cell: 4, floor: 32, turn: 512, want: terrainShotUnknown},
		{name: "splash remains possible", cell: 2, floor: 32, turn: 512, area: 512, want: terrainShotUnknown},
		{name: "zero turn remains target independent", cell: 4, floor: 32, want: terrainShotBlocked},
		{name: "burn blow steering can impact before motion", cell: 2, floor: 32, turn: 512, burn: true, want: terrainShotUnknown},
		{name: "unbounded turn admits", cell: 2, floor: 255, turn: 32768, want: terrainShotUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, terrain := newContactFixture(t)
			terrain.PlotAt(tc.cell, 1).SetMinHeight(tc.floor)
			weapon := modernTerrainWeapon()
			weapon.SelfProp = true
			weapon.Guidance = true
			weapon.TurnRate = int32(tc.turn)
			weapon.BurnBlow = tc.burn
			if tc.area != 0 {
				weapon.AreaOfEffect = int32(tc.area)
			}
			muzzle, aim := modernTerrainPoints()
			if got := modernTerrainAdmission(Slot{Weapon: weapon}, muzzle, aim, 10, terrain, nil, nil); got != tc.want {
				t.Fatalf("admission=%v want %v", got, tc.want)
			}
		})
	}
}

func TestModernTwoPhaseLaunchStopsBeforeTransition(t *testing.T) {
	_, terrain := newContactFixture(t)
	weapon := &content.WeaponDef{VLaunch: true, SelfProp: true, Guidance: true, TwoPhase: true, Range: 400, WeaponVelocity: 1 << 16, NoAutoRange: true, WeaponTimer: 3, FlightTime: 10, TurnRate: 1000, AreaOfEffect: 8}
	muzzle, aim := modernTerrainPoints()
	terrain.PlotAt(1, 1).SetMinHeight(32)
	if got := modernTerrainAdmission(Slot{Weapon: weapon}, muzzle, aim, 10, terrain, nil, nil); got != terrainShotBlocked {
		t.Fatalf("target-independent launch admission=%v", got)
	}
	terrain.PlotAt(1, 1).SetMinHeight(16)
	terrain.Gravity = numeric.FixedFromInt(100)
	// At expiry the gravity/transition visit would fall into terrain. That
	// visit and all subsequent guiding phases are outside the launch proof.
	if got := modernTerrainAdmission(Slot{Weapon: weapon}, muzzle, aim, 10, terrain, nil, nil); got != terrainShotUnknown {
		t.Fatalf("phase uncertainty admission=%v", got)
	}
}

func TestModernTerrainSpreadTransaction(t *testing.T) {
	for _, tc := range []struct {
		name                string
		modern, admit, full bool
	}{
		{name: "rejected", modern: true}, {name: "admitted", modern: true, admit: true},
		{name: "admitted pool full", modern: true, admit: true, full: true},
		{name: "rejected pool full", modern: true, full: true}, {name: "strict bypass"}, {name: "strict pool full", full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launch := modernBallisticSlot()
			launch.Weapon.Accuracy = 1024
			muzzle, aim := modernTerrainPoints()
			random := rng.NewSimulation(77)
			before := random
			expected := random
			bound := AccuracySpreadBound(launch.Weapon.Accuracy, 100, 100, 0)
			want := launch
			want.DesiredYaw = uint16(int32(want.DesiredYaw) + recentred(expected.Uint32n(uint32(bound)), int32(bound)))
			want.DesiredPitch = uint16(int32(want.DesiredPitch) + recentred(expected.Uint32n(uint32(bound)), int32(bound)))
			svc := &Service{}
			if tc.full {
				for i := 0; i < ProjectileCapacity; i++ {
					svc.Reserve()
				}
			}
			calls := 0
			ports := FirePorts{Origin: muzzle, RNG: &random, ShooterHealth: 100, ShooterMaxHealth: 100}
			var shot ShotQuery
			if tc.modern {
				ports.Shot = &shot
				svc.Rules = &terrainSpyRules{admit: func(q *ShotQuery) bool {
					calls++
					if q.Muzzle != muzzle || q.Aim != aim || q.Launch.DesiredYaw != want.DesiredYaw || q.Launch.DesiredPitch != want.DesiredPitch {
						t.Fatalf("preview did not receive exact spread: got=%+v want=%+v", q.Launch, want)
					}
					if random != before {
						t.Fatal("preview consumed shared RNG")
					}
					return tc.admit
				}}
			}
			old := launch
			_, fired := TryFire(svc, &launch, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, ports)
			if tc.modern && !tc.admit {
				if fired || random != before || launch != old {
					t.Fatalf("rejection changed RNG/angles or fired: fired=%v random=%+v launch=%+v", fired, random, launch)
				}
			} else {
				if random != expected || launch.DesiredYaw != want.DesiredYaw || launch.DesiredPitch != want.DesiredPitch || fired == tc.full {
					t.Fatalf("committed spread mismatch: fired=%v random=%+v launch=%+v", fired, random, launch)
				}
				if fired {
					p := &svc.Records[0]
					var pWant Projectile
					InitBallistic(&pWant, launch.Weapon, 10, muzzle, aim, 0, numeric.Angle(want.DesiredPitch), numeric.Angle(retailYawFromGo(want.DesiredYaw)), want.DistanceWord, 0)
					if p.Velocity != pWant.Velocity {
						t.Fatalf("actual projectile differs from admitted launch: got=%+v want=%+v", p.Velocity, pWant.Velocity)
					}
				}
			}
			if (calls == 1) != tc.modern {
				t.Fatalf("callback calls=%d", calls)
			}
		})
	}
}

type terrainAdmissionRandomScript struct {
	random     *rng.Simulation
	fire, rock uint32
}

func (s *terrainAdmissionRandomScript) FireWeapon(int) { s.fire = s.random.Uint32n(65536) }
func (s *terrainAdmissionRandomScript) RockUnit(int)   { s.rock = s.random.Uint32n(65536) }

func TestModernTerrainCommitsSpreadBeforeScriptRandomness(t *testing.T) {
	launch := modernBallisticSlot()
	launch.Weapon.Accuracy = 1024
	random := rng.NewSimulation(77)
	expected := random
	bound := AccuracySpreadBound(launch.Weapon.Accuracy, 100, 100, 0)
	expected.Uint32n(uint32(bound))
	expected.Uint32n(uint32(bound))
	wantFire, wantRock := expected.Uint32n(65536), expected.Uint32n(65536)
	script := &terrainAdmissionRandomScript{random: &random}
	muzzle, aim := modernTerrainPoints()
	svc := &Service{Rules: &terrainSpyRules{admit: func(*ShotQuery) bool { return true }}}
	var shot ShotQuery
	if _, ok := TryFire(svc, &launch, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, FirePorts{Origin: muzzle, RNG: &random, Script: script, ShooterHealth: 100, ShooterMaxHealth: 100, Shot: &shot}); !ok {
		t.Fatal("admitted shot failed")
	}
	if script.fire != wantFire || script.rock != wantRock || random != expected {
		t.Fatalf("script draws did not follow committed accuracy: fire=%d rock=%d RNG=%+v", script.fire, script.rock, random)
	}
}

func TestModernGuidedConeIncludesQuantizationBoundary(t *testing.T) {
	_, terrain := newContactFixture(t)
	terrain.PlotAt(1, 2).SetMinHeight(17)
	muzzle := Vec3{X: cellCentre(1), Y: numeric.Fixed((16 << 16) + 60000), Z: cellCentre(1)}
	aim := Vec3{X: muzzle.X, Y: muzzle.Y, Z: cellCentre(12)}
	weapon := modernTerrainWeapon()
	weapon.SelfProp = true
	weapon.Guidance = true
	weapon.TurnRate = 1
	var preview Projectile
	InitOrdinary(&preview, weapon, 10, muzzle, aim, 0)
	preview.Pitch = 94
	sample := terrainAdmissionSample{weapon: weapon, terrain: terrain, muzzle: muzzle, aim: aim, box: UnitForArea{Min: aim, Max: aim}}
	if got := guidedLaunchTerrainAdmission(preview, weapon, 10, terrain, sample); got != terrainShotBlocked {
		t.Fatalf("cone within low trig cell=%v", got)
	}
	// Pitch 96 enters the next shared-trig cell, and that sample clears.
	// Inclusive turn endpoints must visit it even though nominal pitch is 95.
	preview.Pitch = 95
	if got := guidedLaunchTerrainAdmission(preview, weapon, 10, terrain, sample); got != terrainShotUnknown {
		t.Fatalf("cone crossing trig boundary=%v", got)
	}
}

func TestModernTrajectoryUnknownArithmeticAndBudgets(t *testing.T) {
	_, terrain := newContactFixture(t)
	terrain.PlotAt(1, 1).SetMinHeight(255)
	muzzle, aim := modernTerrainPoints()
	for _, tc := range []struct {
		name   string
		change func(*Slot)
		wind   *world.Wind
	}{
		{name: "missing ballistic wind", change: func(*Slot) {}},
		{name: "zero ballistic divide", wind: &world.Wind{}, change: func(s *Slot) { s.Weapon.WeaponVelocity = 0 }},
		{name: "negative distance word", wind: &world.Wind{}, change: func(s *Slot) { s.DistanceWord = -1 }},
		{name: "guided proof budget", change: func(s *Slot) {
			s.Weapon = &content.WeaponDef{SelfProp: true, Guidance: true, Range: 400, TurnRate: 8192, NoAutoRange: true, WeaponTimer: 10}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launch := modernBallisticSlot()
			tc.change(&launch)
			if got := modernTerrainAdmission(launch, muzzle, aim, 10, terrain, nil, tc.wind); got != terrainShotUnknown {
				t.Fatalf("uncertain trajectory=%v", got)
			}
		})
	}
}

func TestModernBallisticAdmissionUsesSpreadAdjustedArc(t *testing.T) {
	_, terrain := newContactFixture(t)
	terrain.Gravity = numeric.FixedFromInt(1)
	for z := int32(0); z < terrain.CellH; z++ {
		terrain.PlotAt(4, z).SetMinHeight(58)
	}
	launch := modernBallisticSlot()
	launch.Weapon.Accuracy = 4096
	muzzle, aim := modernTerrainPoints()
	if got := modernTerrainAdmission(launch, muzzle, aim, 10, terrain, nil, &world.Wind{}); got != terrainShotBlocked {
		t.Fatalf("nominal arc=%v want blocked", got)
	}
	random := rng.NewSimulation(77)
	var shot ShotQuery
	ports := FirePorts{Origin: muzzle, Gravity: terrain.Gravity, RNG: &random, ShooterHealth: 100, ShooterMaxHealth: 100, Shot: &shot}
	svc := &Service{Rules: &terrainSpyRules{admit: func(q *ShotQuery) bool {
		got := modernTerrainAdmission(q.Launch, q.Muzzle, q.Aim, 10, terrain, nil, &world.Wind{})
		if got == terrainShotBlocked {
			t.Fatal("spread-adjusted arc should clear nominal obstruction")
		}
		return got != terrainShotBlocked
	}}}
	if _, ok := TryFire(svc, &launch, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, ports); !ok {
		t.Fatal("spread-adjusted clear shot rejected")
	}
}
