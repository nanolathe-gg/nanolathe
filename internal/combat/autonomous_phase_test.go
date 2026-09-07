package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Failed retention can reacquire the same cached fallback immediately. The
// setter preserves Aim state but clears the order-event subset [06 §3.2]
// [06 R-WPN-05 §6]. A miss instead queues TargetCleared without a VM drain.
func TestAutonomousReplacementAndDeferredClear(t *testing.T) {
	for _, miss := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "miss"}[miss], func(t *testing.T) {
			svc, shooter, w, terrain, cat := commandFireProbe(t, false, ControlByteHuman)
			var target *units.Unit
			for _, u := range w.IterSliced() {
				if u.Owner != shooter.Owner {
					target = u
					break
				}
			}
			if target == nil {
				t.Fatal("missing target fixture")
			}
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			var bad content.CategoryMask
			bad.Words[0] = 1 << 3
			target.Def.UnitMask = bad
			shooter.Def.BadTargetCategoryWPRIMask = bad
			program := progWithAim([]uint32{0x10021001, 1, 0x10065000}, "TargetCleared", 0)
			vm := cob.NewVM(program)
			attachTestCOB(shooter, vm)
			slot.Aim.Ready = true
			slot.Aim.IssueBit = true
			slot.Flags |= units.SlotFlagAimLatch
			flags, aim := slot.Flags, slot.Aim
			shooter.Pending = units.PendingSlotSetterClear | 8
			vis := allVisibleService(terrain)
			var econ *economy.Service
			if !miss {
				primeTargetRegistry(svc, w, vis, terrain, econ)
			}
			sim := simRNGPtr(7)
			svc.StepAutonomousForPlayer(shooter.Owner, w, vis, terrain, econ, cat, sim)
			if slot.Flags != flags || slot.Aim != aim {
				t.Fatal("scan changed Aim/control words")
			}
			started := 0
			for _, thread := range vm.Threads {
				if thread.Status != cob.ThreadIdle {
					started++
					if thread.PC != 0 || thread.SP != 1 || thread.Stack[0] != 0 {
						t.Fatal("TargetCleared was drained or carried wrong slot")
					}
				}
			}
			if miss {
				if slot.Target.Kind != units.TargetNone || started != 1 || shooter.Pending != units.PendingSlotSetterClear|8 || sim.Draws() != 0 {
					t.Fatal("miss did not perform only the deferred target clear")
				}
			} else {
				if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != target.Handle || started != 0 || shooter.Pending != 8 || sim.Draws() != 1 {
					t.Fatalf("replacement target=%+v callbacks=%d pending=%x draws=%d", slot.Target, started, shooter.Pending, sim.Draws())
				}
			}
		})
	}
}
