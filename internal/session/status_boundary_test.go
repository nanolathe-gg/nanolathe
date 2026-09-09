package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The health port is read-only. Observe a supported read during the normal
// drain to distinguish its ordering from status maintenance [04 R-MOV-03 §1].
func TestStatusRefreshFollowsExactlyOneNormalDrain(t *testing.T) {
	for _, aim := range []bool{false, true} {
		t.Run(map[bool]string{false: "weaponless", true: "aim-handshake"}[aim], func(t *testing.T) {
			s := newLoopTestSession(t, 2)
			u, target := s.Units.IterSliced()[0], s.Units.IterSliced()[1]
			prog := &cob.Program{Code: []uint32{
				0x10021001, 4, 0x10042000, // read HEALTH
				0x10065000, // return its value
			}, Scripts: map[string]int{"Observe": 0, "AimPrimary": 0}, ScriptsByID: []int{0}}
			vm := cob.NewVM(prog)
			u.SetScript(vm)
			u.ScriptState.Binding = &cob.Binding{VM: vm, Callbacks: u.ScriptBridge(), Model: &model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1}}}, PieceMap: []int{0}}
			u.Health, u.MaxHealth = 60, 100
			u.PriorSample, u.CurrentSample, u.BlinkSuppress = 11, 83, -16
			reads := 0
			vm.BindPortBinding(4, cob.PortBinding{Read: func([4]int32) int32 {
				reads++
				if u.PriorSample != 11 || u.CurrentSample != 83 || uint8(u.BlinkSuppress) != 240 {
					t.Errorf("status refreshed before drain: prior/current/blink = %d/%d/%d", u.PriorSample, u.CurrentSample, u.BlinkSuppress)
				}
				return cob.HealthPercent(u.Health, u.MaxHealth)
			}})
			if aim {
				weapon := &content.WeaponDef{ID: 1, Range: 1000 * 65536, Turret: true, WeaponVelocity: 100 * 65536 / 30, LineOfSight: true}
				s.Catalog.Weapons = map[string]*content.WeaponDef{"status-aim": weapon}
				s.Catalog.RebuildWeaponIndex()
				u.InstallWeapon(0, weapon)
				slot := u.SlotAt(0)
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
				slot.Flags |= 0x02
			} else if !vm.StartByName("Observe", nil) {
				t.Fatal("start observer")
			}
			before := vm.DrainCalls
			s.stepUnitPhase(30)
			if reads != 1 || vm.DrainCalls-before != 1 {
				t.Fatalf("health reads/drains = %d/%d, want 1/1", reads, vm.DrainCalls-before)
			}
			if u.PriorSample != 83 || u.CurrentSample != 60 || uint8(u.BlinkSuppress) != 239 {
				t.Fatalf("post-drain status = %d/%d/%d", u.PriorSample, u.CurrentSample, u.BlinkSuppress)
			}
		})
	}
}

func TestDeathAcrossHealthSampleBoundaryUsesRolledPrior(t *testing.T) {
	s := newLoopTestSession(t, 2)
	u := s.Units.IterSliced()[0]
	s.stepUnitPhase(29)
	u.Health, u.MaxHealth = -10, 100
	u.PriorSample, u.CurrentSample = 12, 80
	// Killed writes its input to a supported engine port. The test observes
	// the supplied value before preserving that port's boolean semantics.
	vm := cob.NewVM(&cob.Program{Code: []uint32{
		0x10021001, 5, 0x10021002, 0, 0x10082000, // set INBUILDSTANCE to severity
		0x10021001, 0, 0x10065000,
	}, Scripts: map[string]int{"Killed": 0}, ScriptsByID: []int{0}})
	u.SetScript(vm)
	severity := int32(-1)
	vm.BindPortBinding(5, cob.PortBinding{Write: func(v int32) { severity = v; u.InBuildStance = v != 0 }})
	original := s.Units.OnDeath
	calls := 0
	s.Units.OnDeath = func(h pool.Handle, c units.DeathCause, dead *units.Unit) {
		calls++
		if dead.PriorSample != 80 {
			t.Errorf("death prior sample = %d, want rolled 80", dead.PriorSample)
		}
		original(h, c, dead)
	}
	s.Units.Destroy(u.Handle, units.DeathKilled)
	s.stepOneSubTick(30)
	if calls != 1 || severity != 45 {
		t.Fatalf("death calls/severity = %d/%d, want 1/45 [04 §5.1]", calls, severity)
	}
	if s.Units.Unit(u.Handle) != nil {
		t.Fatal("death not finalized")
	}
}
