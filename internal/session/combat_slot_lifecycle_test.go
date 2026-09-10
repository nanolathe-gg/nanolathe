package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Unit records are released before the lowest free slot can be reused
// [04 §2.4][P0-16]. A deferred Aim belongs to that record's weapon receiver;
// a later occupant's ungated executor must not inherit it [06 §3.3].
func TestDestroyedAimDoesNotBlockReusedUnitSlot(t *testing.T) {
	s := newLoopTestSession(t, 0)
	def := s.Catalog.Units["armcom"]
	def.Script = &cob.Program{
		Code:    []uint32{0x10021001, 100000, 0x10013000, 0x10021001, 1, 0x10065000},
		Scripts: map[string]int{"AimPrimary": 0}, ScriptsByID: []int{0},
		Pieces: []string{"base"},
	}
	create := func() *units.Unit {
		t.Helper()
		h, err := s.Units.Create(def, 0, numeric.FixedFromInt(32), numeric.FixedFromInt(20), numeric.FixedFromInt(32))
		if err != nil {
			t.Fatal(err)
		}
		u := s.Units.Unit(h)
		vm := cob.NewVM(def.Script)
		binding := &cob.Binding{VM: vm, Callbacks: cob.NewCallbackBridge(vm),
			Model: &model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1}}}, PieceMap: []int{0}}
		u.Script = vm
		u.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
		return u
	}
	weapon := &content.WeaponDef{ID: 1, Turret: true, LineOfSight: true, Range: 1000, Tolerance: 32767}
	install := func(u *units.Unit, w *content.WeaponDef) {
		u.InstallWeapon(0, w)
		u.Slots[0].Flags |= units.SlotFlagEnabled
		u.Slots[0].Target = units.Target{Kind: units.TargetGround, X: numeric.FixedFromInt(48), Z: numeric.FixedFromInt(48)}
	}
	visit := func(u *units.Unit, tick uint32) {
		s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
	}
	old := create()
	install(old, weapon)
	visit(old, 1)
	old.GetScript().Drain(1)
	if !old.Slots[0].Aim.IssueBit || old.Slots[0].Aim.Ready || old.GetScript().Threads[0].Status != cob.ThreadSleeping {
		t.Fatalf("fixture did not park a pending Aim before destruction: aim=%+v thread=%v", old.Slots[0].Aim, old.GetScript().Threads[0].Status)
	}
	s.Units.Destroy(old.Handle, units.DeathKilled)
	s.finalizePhase2Death(old.Handle, 2)
	replacement := create()
	if replacement.Handle != old.Handle {
		t.Fatal("fixture did not reuse the destroyed unit's pool slot")
	}
	ungated := *weapon
	ungated.Turret = false
	install(replacement, &ungated)
	before := s.Combat.Count()
	visit(replacement, 3)
	if s.Combat.Count() <= before {
		t.Fatal("a destroyed unit's pending Aim blocked the replacement's ungated weapon")
	}
}
