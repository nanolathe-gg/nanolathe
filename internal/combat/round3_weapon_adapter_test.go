package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// rockRecorderProgram keeps RockUnit parked at its entry point. Its two
// argument cells are therefore directly inspectable in the real COB thread
// before the session-owned drain runs it.
func rockRecorderProgram() *cob.Program {
	return &cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"RockUnit": 0},
		ScriptsByID: []int{0},
		Pieces:      []string{"base"},
	}
}

func aimRecorderProgram() *cob.Program {
	return &cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"AimPrimary": 0},
		ScriptsByID: []int{0},
		Pieces:      []string{"base"},
	}
}

// TestProductionFireAdapterRockUnitUsesTheMutatedSlotPair exercises the unit
// adapter rather than FirePorts' test seam. Both trajectory families retain
// the spread in the live slot for recoil, while a full pool consumes the same
// draws and suppresses both deferred callbacks [06 R-WPN-03 §4][06 R-WPN-05 §5].
func TestProductionFireAdapterRockUnitUsesTheMutatedSlotPair(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ballistic bool
		full      bool
	}{
		{name: "ordinary"},
		{name: "ballistic", ballistic: true},
		{name: "ordinary full pool", full: true},
		{name: "ballistic full pool", ballistic: true, full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, _ := newTestWorldAndUnits(t)
			vm := cob.NewVM(rockRecorderProgram())
			attachTestCOB(shooter, vm)
			weapon := &content.WeaponDef{
				ID: 701, Turret: true, LineOfSight: !tc.ballistic, Ballistic: tc.ballistic,
				WeaponVelocity: 4 << 16, Accuracy: 256, Tolerance: wideDriftTolerance,
			}
			shooter.Health, shooter.MaxHealth = 50, 100
			shooter.Move.Heading = 0x2345
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetGround, X: numeric.FixedFromInt(70), Z: numeric.FixedFromInt(30)}
			slot.DesiredYaw, slot.DesiredPitch = 0x1200, 0x0800
			beforePair := [2]uint16{slot.DesiredYaw, slot.DesiredPitch}
			bound := AccuracySpreadBound(weapon.Accuracy, shooter.Health, shooter.MaxHealth, shooter.Kills)
			expectedRNG := rng.NewSimulation(17)
			yawDraw := recentred(expectedRNG.Uint32n(uint32(bound)), int32(bound))
			pitchDraw := recentred(expectedRNG.Uint32n(uint32(bound)), int32(bound))
			wantYaw := uint16(int32(beforePair[0]) + int32(shooter.Move.Heading) + yawDraw)
			wantPitch := uint16(int32(beforePair[1]) + pitchDraw)

			var svc Service
			if tc.full {
				for i := 0; i < ProjectileCapacity; i++ {
					if _, ok := svc.Reserve(); !ok {
						t.Fatalf("reserve %d", i)
					}
				}
			}
			sim := rng.NewSimulation(17)
			if ok := tryFireForSlot(shooter, slot, 0, 9, terrain, &sim, &svc, w, nil,
				Vec3{X: slot.Target.X, Y: PointTargetHeight(terrain, slot.Target.X, slot.Target.Z), Z: slot.Target.Z}); ok == tc.full {
				t.Fatalf("fire ok=%v, want %v", ok, !tc.full)
			}
			if got := sim.Draws(); got != 2 {
				t.Fatalf("spread draws=%d, want 2", got)
			}
			if slot.DesiredYaw != wantYaw || slot.DesiredPitch != wantPitch {
				t.Fatalf("retained spread pair=(%#04x,%#04x), want (%#04x,%#04x) from seeded draws (%d,%d)", slot.DesiredYaw, slot.DesiredPitch, wantYaw, wantPitch, yawDraw, pitchDraw)
			}
			if tc.full {
				if vm.ActiveThreadCount() != 0 {
					t.Fatalf("pool-full fire scheduled %d callbacks", vm.ActiveThreadCount())
				}
				return
			}
			if vm.ActiveThreadCount() != 1 {
				t.Fatalf("expected only RockUnit callback, got %d active threads", vm.ActiveThreadCount())
			}
			wantX, wantZ := cob.RockUnitArgs(int16(slot.DesiredYaw - shooter.Move.Heading))
			got := vm.Threads[0].Stack
			if got[0] != wantX || got[1] != wantZ {
				t.Fatalf("RockUnit args=(%d,%d), want (%d,%d) from the mutated slot yaw", got[0], got[1], wantX, wantZ)
			}
		})
	}
}

// TestBallisticCreatorCopiesStoredPairAndFullPoolRetryKeepsIt distinguishes
// the ballistic creator (which copies its slot pair) from ordinary creation
// (which re-solves), and locks the retry consequence at both half-turn
// headings [06 §6.4][06 R-WPN-05 §5].
func TestBallisticCreatorCopiesStoredPairAndFullPoolRetryKeepsIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		heading  uint16
		accuracy int32
		retry    bool
		wantFire bool
	}{
		{name: "zero spread", accuracy: 0, wantFire: true},
		{name: "nonzero spread", accuracy: 256, wantFire: true},
		{name: "retry heading zero", heading: 0, accuracy: 8, retry: true, wantFire: true},
		{name: "retry heading half turn", heading: 0x8000, accuracy: 8, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			terrain.Gravity = numeric.Fixed(8155)
			weapon := &content.WeaponDef{ID: 702, Range: 1000, Turret: true, Ballistic: true, WeaponVelocity: 500000, Accuracy: tc.accuracy, Tolerance: 4096}
			shooter.Move.Heading = tc.heading
			shooter.Health, shooter.MaxHealth = 100, 100
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			slot.Aim.IssueBit, slot.Aim.Ready = true, true
			pitch, ok := BallisticSolve(target.X.Sub(shooter.X), target.Y.Sub(shooter.Y), target.Z.Sub(shooter.Z), numeric.Fixed(weapon.WeaponVelocity), terrain.Gravity, weapon.MinBarrelAngle)
			if !ok {
				t.Fatal("fixture ballistic solve failed")
			}
			slot.DesiredYaw = aimYawForScript(uint16(YawFromDelta(target.X.Sub(shooter.X), target.Z.Sub(shooter.Z))), shooter.Move.Heading)
			slot.DesiredPitch = pitch
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"ballistic": weapon}}
			cat.RebuildWeaponIndex()
			sim := rng.NewSimulation(33)
			var svc Service

			if tc.retry {
				for i := 0; i < ProjectileCapacity; i++ {
					svc.Reserve()
				}
				svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &sim, nil)
				if !slot.Aim.IssueBit || !slot.Aim.Ready {
					t.Fatal("pool-full creator cleared the Aim state")
				}
				for svc.Count() != 0 {
					svc.MarkDead(1)
					svc.Compact(nil)
				}
			}

			fired := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &sim, nil).Fired == 1
			if fired != tc.wantFire {
				t.Fatalf("retry fired=%v, want %v; aim=%+v", fired, tc.wantFire, slot.Aim)
			}
			if !fired {
				if slot.Aim.IssueBit {
					t.Fatal("half-turn retry must fail the relative drift gate and clear IssueBit")
				}
				return
			}
			p := &svc.Records[svc.Count()-1]
			if p.Yaw != numeric.Angle(retailYawFromGo(slot.DesiredYaw)) || p.Pitch != numeric.Angle(slot.DesiredPitch) {
				t.Fatalf("ballistic record pair=(%#04x,%#04x), want stored slot pair (%#04x,%#04x)", p.Yaw, p.Pitch, retailYawFromGo(slot.DesiredYaw), slot.DesiredPitch)
			}
			if want := VelocityFromAngles(p.Yaw, p.Pitch, p.Speed); p.Velocity.X != want.X || p.Velocity.Z != want.Z {
				t.Fatalf("ballistic planar velocity=%v, want rebuild from stored pair %v", p.Velocity, want)
			}
		})
	}
}

// TestBallisticCreatorDoesNotResolveAgainAfterAcceptedAim distinguishes an
// accepted stored pair from a fresh aim solve. Both offsets stay inside the
// drift gate, but cross the velocity table's quantization boundary; with zero
// new spread, a creator that re-solves would make the two launches identical
// [06 §6.4][06 R-WPN-05 §5].
func TestBallisticCreatorDoesNotResolveAgainAfterAcceptedAim(t *testing.T) {
	launch := func(t *testing.T, yawOffset, pitchOffset uint16) Projectile {
		t.Helper()
		w, terrain, shooter, target := newTestWorldAndUnits(t)
		terrain.Gravity = numeric.Fixed(8155)
		weapon := &content.WeaponDef{ID: 706, Range: 1000, Turret: true, Ballistic: true, WeaponVelocity: 500000, Tolerance: 4096}
		shooter.Move.Heading = 0
		shooter.InstallWeapon(0, weapon)
		slot := shooter.SlotAt(0)
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
		slot.Aim.IssueBit, slot.Aim.Ready = true, true
		slot.DistanceWord = 2 * weapon.WeaponVelocity

		freshPitch, ok := BallisticSolve(target.X.Sub(shooter.X), target.Y.Sub(shooter.Y), target.Z.Sub(shooter.Z), numeric.Fixed(weapon.WeaponVelocity), terrain.Gravity, weapon.MinBarrelAngle)
		if !ok {
			t.Fatal("fixture ballistic solve failed")
		}
		freshYaw := aimYawForScript(uint16(YawFromDelta(target.X.Sub(shooter.X), target.Z.Sub(shooter.Z))), shooter.Move.Heading)
		slot.DesiredYaw = freshYaw + yawOffset
		slot.DesiredPitch = freshPitch + pitchOffset
		wantYaw, wantPitch := slot.DesiredYaw, slot.DesiredPitch

		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"ballistic": weapon}}
		cat.RebuildWeaponIndex()
		var svc Service
		if got := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, nil, nil).Fired; got != 1 {
			t.Fatalf("fired=%d, want one accepted stored-pair launch", got)
		}
		p := svc.Records[0]
		if p.Yaw != numeric.Angle(retailYawFromGo(wantYaw)) || p.Pitch != numeric.Angle(wantPitch) {
			t.Fatalf("record pair=(%#04x,%#04x), want stored pair (%#04x,%#04x), retained=(%#04x,%#04x)", p.Yaw, p.Pitch, retailYawFromGo(wantYaw), wantPitch, slot.DesiredYaw, slot.DesiredPitch)
		}
		var expected Projectile
		InitBallistic(&expected, weapon, 2, Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}, Vec3{X: target.X, Y: target.Y, Z: target.Z}, target.Handle, p.Pitch, p.Yaw, slot.DistanceWord, terrain.Gravity)
		if p.Velocity != expected.Velocity {
			t.Fatalf("velocity=%v, want stored-pair launch %v including the distance-word gravity term", p.Velocity, expected.Velocity)
		}
		return p
	}

	fresh := launch(t, 0, 0)
	offset := launch(t, 1024, 1024)
	if fresh.Yaw == offset.Yaw || fresh.Pitch == offset.Pitch {
		t.Fatalf("zero-spread stored pairs collapsed: fresh=(%#04x,%#04x) offset=(%#04x,%#04x)", fresh.Yaw, fresh.Pitch, offset.Yaw, offset.Pitch)
	}
	if fresh.Velocity == offset.Velocity {
		t.Fatalf("zero-spread stored-pair velocities collapsed: %v", fresh.Velocity)
	}
}

// TestVerticalAimUsesZeroArgsAndStockpileGateWithTurretPrecedence records the
// actual deferred Aim cells: vertical launch sends (0,0), stockpile requires
// ammunition, and turret wins the overlapping flag pair [06 R-WPN-03 §6].
func TestVerticalAimUsesZeroArgsAndStockpileGateWithTurretPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		weapon       content.WeaponDef
		ammo         int32
		wantDispatch bool
		wantZeroArgs bool
	}{
		{name: "vertical", weapon: content.WeaponDef{ID: 703, VLaunch: true}, wantDispatch: true, wantZeroArgs: true},
		{name: "empty stockpile", weapon: content.WeaponDef{ID: 704, VLaunch: true, Stockpile: true}, wantDispatch: false},
		{name: "loaded stockpile", weapon: content.WeaponDef{ID: 706, VLaunch: true, Stockpile: true}, ammo: 1, wantDispatch: true, wantZeroArgs: true},
		{name: "turret precedence", weapon: content.WeaponDef{ID: 705, Turret: true, VLaunch: true, LineOfSight: true, WeaponVelocity: 4 << 16}, wantDispatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			vm := cob.NewVM(aimRecorderProgram())
			attachTestCOB(shooter, vm)
			shooter.InstallWeapon(0, &tc.weapon)
			slot := shooter.SlotAt(0)
			slot.Ammo = tc.ammo
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			sum := (&Service{}).StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, nil, nil, nil)
			if sum.Dispatched != tc.wantDispatch {
				t.Fatalf("dispatched=%v, want %v", sum.Dispatched, tc.wantDispatch)
			}
			if !tc.wantDispatch {
				if slot.Aim.IssueBit || vm.ActiveThreadCount() != 0 {
					t.Fatalf("empty stockpile queued Aim: aim=%+v threads=%d", slot.Aim, vm.ActiveThreadCount())
				}
				return
			}
			got := vm.Threads[0].Stack
			if tc.wantZeroArgs && (got[0] != 0 || got[1] != 0) {
				t.Fatalf("vertical Aim args=(%d,%d), want (0,0)", got[0], got[1])
			}
			if !tc.wantZeroArgs && got[0] == 0 && got[1] == 0 {
				t.Fatal("turret+vlaunch took the vertical fixed-forward Aim form")
			}
		})
	}
}
