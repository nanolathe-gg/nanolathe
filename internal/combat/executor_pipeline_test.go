package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Fixed weapons use the forced Query muzzle for both drift and creation;
// AimFrom is not consulted by this executor [06 R-P0-07].
func TestFixedWeaponDriftUsesForcedMuzzleQuery(t *testing.T) {
	w, terrain, u, _ := newTestWorldAndUnits(t)
	prog := &cob.Program{Code: []uint32{0x10021001, 1, 0x10023002, 0, 0x10065000, 0x10021001, 2, 0x10023002, 0, 0x10065000}, Scripts: map[string]int{"AimFromPrimary": 0, "QueryPrimary": 5}, ScriptsByID: []int{0, 5}, Pieces: []string{"base", "aim", "muzzle"}}
	vm := cob.NewVM(prog)
	mdl := &model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1, Children: []int{1, 2}}, {Parent: 0, Translate: [3]numeric.Fixed{numeric.FixedFromInt(10), 0, 0}}, {Parent: 0}}}
	u.Script = vm
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{VM: vm, Callbacks: cob.NewCallbackBridge(vm), Model: mdl, PieceMap: []int{0, 1, 2}}}
	u.X = numeric.FixedFromInt(80)
	u.Z = numeric.FixedFromInt(80)
	u.Y = 0
	u.Move.Heading = 0
	u.Move.Pitch = 0
	weapon := &content.WeaponDef{ID: 1, LineOfSight: true, WaterWeapon: true, Range: 100, Tolerance: 150, WeaponVelocity: 65536}
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetGround, X: u.X, Z: numeric.FixedFromInt(60)}
	var svc Service
	got := svc.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, nil, nil, nil)
	if got.Fired != 1 {
		t.Fatalf("aligned fixed muzzle fired %d; unwanted AimFrom geometry blocked shot; yaw=%d", got.Fired, slot.DesiredYaw)
	}
}

// A held turret only re-queries its aim origin after the outer fire gates
// and the executor readiness gate admit the shot [06 R-P0-07].
func TestHeldTurretDoesNotQueryAimOriginDuringReload(t *testing.T) {
	w, terrain, u, target := newTestWorldAndUnits(t)
	// The authored query writes a static flag so its synchronous execution is observable.
	prog := &cob.Program{Statics: 1, Code: []uint32{0x10021001, 1, 0x10023004, 0, 0x10021001, 0, 0x10023002, 0, 0x10065000}, Scripts: map[string]int{"AimFromPrimary": 0}, ScriptsByID: []int{0}, Pieces: []string{"base"}}
	vm := cob.NewVM(prog)
	attachTestCOB(u, vm)
	weapon := &content.WeaponDef{ID: 1, Turret: true, LineOfSight: true, WaterWeapon: true, Range: 100, WeaponVelocity: 65536}
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Reload = 2
	slot.Aim.IssueBit = true
	slot.Aim.Ready = true
	slot.Flags |= units.SlotFlagAimLatch
	var svc Service
	svc.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, nil, nil, nil)
	if got := vm.DebugSnapshot().Statics[0]; got != 0 {
		t.Fatalf("held turret on reload executed AimFrom (static=%d); want no aim-time/fire-time query", got)
	}
}

// Meteor alone selects event reconstruction, not a live slot executor [06 §6.2].
func TestUnitFireWithoutLiveExecutorCreatesNothing(t *testing.T) {
	w, terrain, u, target := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{ID: 1, Meteor: true, WaterWeapon: true, Range: 100}
	u.InstallWeapon(0, weapon)
	u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	var svc Service
	got := svc.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, nil, nil, nil)
	if got.Fired != 0 || svc.Count() != 0 {
		t.Fatalf("meteor-only weapon fired %d; no live-fire executor exists [06 §6.2]", got.Fired)
	}
}

// Failed physical admission publishes the order event before checking the
// executor's Aim receiver [06 R-P0-07][06 R-WPN-05 §6].
func TestUnitShotAdmissionPrecedesAimReadiness(t *testing.T) {
	w, terrain, u, target := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{ID: 1, Turret: true, LineOfSight: true, WaterWeapon: true, Range: 1, WeaponVelocity: 65536}
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Aim.IssueBit = true
	slot.Aim.Ready = false
	slot.Flags |= units.SlotFlagAimLatch
	u.Pending = 0
	var svc Service
	svc.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, nil, nil, nil)
	if u.Pending&units.PendingCouldNotFire == 0 {
		t.Fatal("out-of-range loaded slot awaiting Aim did not publish could-not-fire; outer admission must precede readiness")
	}
}

// Authored flag combinations keep the live executor's creator through record
// initialization, while event reconstruction retains its own ladder [06 §6.2].
func TestLiveCreatorInitializationDiffersFromReconstruction(t *testing.T) {
	cases := []struct {
		name   string
		weapon content.WeaponDef
		family CreationFamily
	}{
		{"turret ordinary", content.WeaponDef{Turret: true, LineOfSight: true, Ballistic: true, VLaunch: true, Dropped: true}, CreationOrdinary},
		{"turret ballistic", content.WeaponDef{Turret: true, Ballistic: true, VLaunch: true, Dropped: true}, CreationBallistic},
		{"vertical", content.WeaponDef{VLaunch: true, Ballistic: true, LineOfSight: true, Dropped: true}, CreationVertical},
		{"fixed", content.WeaponDef{LineOfSight: true, Ballistic: true, Dropped: true}, CreationOrdinary},
		{"dropped", content.WeaponDef{Dropped: true}, CreationDropped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			weapon := tc.weapon
			weapon.Meteor = true
			weapon.WeaponVelocity = int32(numeric.FixedFromInt(1))
			weapon.Range = 100
			target := Vec3{X: numeric.FixedFromInt(10), Z: numeric.FixedFromInt(20)}
			slot := &Slot{Weapon: &weapon, DistanceWord: 1}
			var svc Service
			handle, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint, X: target.X, Z: target.Z}, 1, FirePorts{ShooterHealth: 100, ShooterMaxHealth: 100})
			if !ok {
				t.Fatal("live executor did not create projectile")
			}
			p := svc.Records[int(handle)-1]
			switch tc.family {
			case CreationOrdinary:
				if p.TargetPos != target || p.StoredPlanarDistance == 0 || p.Velocity == (Vec3{}) {
					t.Fatalf("ordinary initialization missing: %+v", p)
				}
			case CreationBallistic:
				if p.TargetPos != (Vec3{}) || p.Velocity == (Vec3{}) {
					t.Fatalf("ballistic initialization missing: %+v", p)
				}
			case CreationVertical:
				if p.TargetPos != target || p.Velocity != (Vec3{}) || p.Pitch != 16384 {
					t.Fatalf("vertical initialization missing: %+v", p)
				}
			case CreationDropped:
				if p.TargetPos != (Vec3{}) || p.Velocity != (Vec3{}) || p.Speed != 0 {
					t.Fatalf("dropped initialization missing: %+v", p)
				}
			}
			var reconstructed Projectile
			velocity := Vec3{Y: numeric.FixedFromInt(-2)}
			got := InitProjectile(&reconstructed, &weapon, 1, Vec3{}, target, 0, 0, 0, &velocity, 1, 0, 0, 0)
			if got != CreationMeteor || reconstructed.Velocity != velocity {
				t.Fatalf("event reconstruction changed: family=%v velocity=%v", got, reconstructed.Velocity)
			}
		})
	}
}
