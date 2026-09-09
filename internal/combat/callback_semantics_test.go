package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestFireTimeMuzzleIsTheForcedQueryPieceNotTheAimOrigin locks the two
// synchronous piece queries to their two consumers [06 §3.4][06 §4.1]
// [R-P0-07]: the aim-time visit solves its angles FROM the `AimFrom*` piece
// (seeded −1, falling back to `Query*` only on the sentinel), and the
// fire-time executor spawns the projectile AT the forced `Query*` piece (seeded
// 0, `AimFrom*` never consulted). They are different pieces on most stock
// models, so the slot's retained muzzle identity must be the Query answer and
// the record must start at that piece's world point.
//
// The fixture's AimFromPrimary answers piece 1 (the "turret") and QueryPrimary
// answers piece 2 (the "flare"). A build that spawns from the aim origin —
// which this one used to do — puts the record at piece 1.
func TestFireTimeMuzzleIsTheForcedQueryPieceNotTheAimOrigin(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	prog := &cob.Program{
		Code: []uint32{
			// AimFromPrimary: push 1; pop local 0; return [04 §4.3]
			0x10021001, 1, 0x10023002, 0, 0x10065000,
			// QueryPrimary: push 2; pop local 0; return [04 §4.3]
			0x10021001, 2, 0x10023002, 0, 0x10065000,
		},
		Scripts:     map[string]int{"AimFromPrimary": 0, "QueryPrimary": 5},
		ScriptsByID: []int{0, 5},
		Pieces:      []string{"base", "turret", "flare"},
	}
	vm := cob.NewVM(prog)
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Children: []int{1, 2}},
			{Name: "turret", Parent: 0, Translate: [3]numeric.Fixed{0, numeric.FixedFromInt(10), 0}},
			{Name: "flare", Parent: 0, Translate: [3]numeric.Fixed{numeric.FixedFromInt(5), numeric.FixedFromInt(20), 0}},
		},
	}
	shooter.Script = vm
	shooter.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{
		VM: vm, Callbacks: cob.NewCallbackBridge(vm), Model: mdl, PieceMap: []int{0, 1, 2},
	}}
	shooter.Move.Heading, shooter.Move.Pitch, shooter.Move.Bank = 0, 0, 0
	weapon := weaponNonTurret(30) // line-of-sight: no Aim handshake, fires when reload is zero [06 §3.3]
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.MuzzlePiece = -1
	slot.Reload = 2 // visit 1 decrements to 1 and aims only; visit 2 reaches zero and fires [06 §4.1]
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	catalog.RebuildWeaponIndex()

	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
	if sum.Fired != 0 {
		t.Fatalf("visit with reload 2 fired %d shots; only a countdown that is zero after the decrement fires [06 §4.1]", sum.Fired)
	}
	if slot.MuzzlePiece != -1 {
		t.Fatalf("aim-time visit wrote the slot's muzzle identity (%d); the aim origin is a local of the solve, and only the fire-time Query* writes the word [06 §4.1]", slot.MuzzlePiece)
	}
	sum = svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, catalog, nil, nil)
	if sum.Fired != 1 || svc.Count() != 1 {
		t.Fatalf("second visit fired %d (pool %d), want exactly one root", sum.Fired, svc.Count())
	}
	if slot.MuzzlePiece != 2 {
		t.Fatalf("retained muzzle piece = %d, want 2: the forced QueryPrimary answer, not AimFromPrimary's 1 [06 §4.1][R-P0-07]", slot.MuzzlePiece)
	}
	rec := svc.Records[0]
	flare := Vec3{X: shooter.X.Add(numeric.FixedFromInt(5)), Y: shooter.Y.Add(numeric.FixedFromInt(20)), Z: shooter.Z}
	turret := Vec3{X: shooter.X, Y: shooter.Y.Add(numeric.FixedFromInt(10)), Z: shooter.Z}
	switch rec.Pos {
	case flare:
	case turret:
		t.Fatalf("record spawned at the AimFrom piece %v; retail spawns at the Query piece %v [06 §4.1]", rec.Pos, flare)
	case Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}:
		t.Fatalf("record spawned at the unit's own position; the Query piece resolved to %v [06 §4.1]", flare)
	default:
		t.Fatalf("record spawned at %v, want the Query piece's world point %v [06 §4.1][03 R-RAST-01 §8]", rec.Pos, flare)
	}
	if rec.MuzzlePiece != 2 {
		t.Fatalf("record muzzle identity = %d, want 2 so a burst clone re-derives the same flare [06 §4.3]", rec.MuzzlePiece)
	}
}

// TestWeaponAimPoolExhaustionDeliversImplicitZero verifies that a full COB
// thread pool reaches the concrete combat receiver as an implicit zero, so
// the missing callback cannot authorize a shot [04 §4.6][06 §3.3].
func TestWeaponAimPoolExhaustionDeliversImplicitZero(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	prog := progWithAim([]uint32{0x10065000}, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	for i := range vm.Threads {
		vm.Threads[i].Status = cob.ThreadSleeping
		vm.Threads[i].Sleep = 100
	}
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(31)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	catalog.RebuildWeaponIndex()

	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
	if !sum.ReturnSeen || sum.ReturnValue != 0 {
		t.Fatalf("full callback pool did not deliver implicit zero: %+v", sum)
	}
	if sum.Fired != 0 || slot.Aim.Ready {
		t.Fatalf("implicit zero authorized a shot: summary=%+v aim=%+v", sum, slot.Aim)
	}
}
