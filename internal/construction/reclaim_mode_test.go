package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Reclaim consults the committed mode both at admission and before each pulse
// [05 R-WORK-01 §4]. A blocked touchdown retains an airborne mirror despite
// its grounded request [04 R-AIR-01 §6][04 R-COLL-01 §1]; the movement test
// TestModeCommitRefusedTouchdownPreservesAirborneMirror exercises that producer.
func TestUnitReclaimPendingModeUsesCommittedState(t *testing.T) {
	for _, row := range []struct {
		name      string
		workPhase uint8
	}{
		{"ReclaimUnit", 5},
		{"VTOL_ReclaimUnit", 2},
	} {
		for _, transition := range []struct {
			name                 string
			requested, committed uint8
			admitted             bool
		}{
			{"blocked touchdown", 1, 2, false},
			{"pending takeoff", 2, 1, true},
		} {
			t.Run(row.name+"/"+transition.name, func(t *testing.T) {
				s, builder, target, node := reclaimFixture(t, 100, 10)
				node.ID = orders.Lookup(row.name)
				if row.name == "VTOL_ReclaimUnit" {
					builder.Def.CanFly = true
					builder.Move.Mode, builder.Move.ModeMirror = 2, 2
				}
				target.Def.BMCode, target.Def.CanFly = 1, true
				target.Move.Mode, target.Move.ModeMirror = transition.requested, transition.committed
				node.Phase = 0
				wantCode := orders.Code(8)
				if transition.admitted {
					wantCode = 1
				}
				if code := s.unitReclaimVisit(builder, node, 0, 0); code != wantCode {
					t.Errorf("admission code=%d, want %d", code, wantCode)
				}

				// An existing order must recheck the mode before a due pulse,
				// including when the target changes modes after admission.
				node.Phase, node.Param1, node.Param2 = row.workPhase, 10, 16
				node.DynamicGate, node.Deadline = 0, -1
				s.StepUnit(TickContext{Tick: 0}, builder.Handle)
				wantHealth := int32(100)
				if transition.admitted {
					wantHealth = 90
				}
				if target.Health != wantHealth {
					t.Errorf("due pulse health=%d, want %d", target.Health, wantHealth)
				}
			})
		}
	}
}
