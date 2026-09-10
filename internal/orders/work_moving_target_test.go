package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// The cached movement tier is the target state pair both restart arms read;
// capture restarts after 30 ticks and air repair after 15
// [04 R-ORD-01 §5][04 R-ORD-01 §7][04 R-ORD-01 §12].
func TestWorkRestartsWhenTargetStartsMoving(t *testing.T) {
	t.Run("capture", func(t *testing.T) {
		_, builder, target := workFixture()
		target.Def.BMCode = 1
		target.MoveTier = 1
		n := &Node{ID: Lookup("Capture"), Owner: builder.Handle, Target: target.Handle, Phase: 4, Param2: 100, Deadline: -1, Flags: FlagStopBuildingPending}
		if code := captureHandler(builder, n, 0, 100); code != 0 || n.Deadline != 130 || n.DynamicGate&gateDeadline == 0 || n.Param1 != 0 || n.Flags&FlagStopBuildingPending != 0 {
			t.Fatalf("moving capture: code=%d deadline=%d gate=%#x progress=%d flags=%#x", code, n.Deadline, n.DynamicGate, n.Param1, n.Flags)
		}
	})
	t.Run("air repair", func(t *testing.T) {
		q, builder, target := vtolWorkFixture()
		target.MoveTier = 1
		var repairs int
		q.binding.Work.Repair = func(*units.Unit, *units.Unit, *Node, uint32) bool { repairs++; return true }
		n := &Node{ID: Lookup("VTOL_RepairUnit"), Owner: builder.Handle, Target: target.Handle, Phase: 2, Deadline: -1}
		if code := vtolRepairUnitHandler(builder, n, 0, 100); code != 0 || n.Deadline != 115 || n.DynamicGate&gateDeadline == 0 || repairs != 0 {
			t.Fatalf("moving repair: code=%d deadline=%d gate=%#x repairs=%d", code, n.Deadline, n.DynamicGate, repairs)
		}
	})
}
