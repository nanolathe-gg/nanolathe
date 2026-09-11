package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A point-marker arrival publishes arrival and release together. The pump
// delivers both through the landing gate; release is not a failure when arrival
// is present. A queued successor takes precedence over touchdown on that same
// visit [04 R-AIR-01 §1][04 R-AIR-01 §6].
func TestTerrainLandingArrivalAndQueuedSuccessor(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "touchdown"
		if queued {
			name = "queued_successor"
		}
		t.Run(name, func(t *testing.T) {
			sys, w, u := airFixture(t)
			sys.SetMoverMode(u, 2)
			u.Y += numeric.Fixed(20 << 16)
			landing := pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
			q := orders.QueueForUnit(u)
			var tick uint32
			for tick = 1; tick < 500; tick++ {
				runLandingTick(sys, tick, w)
				if landing.Phase == 2 && landing.Satisfied&0xE0 != 0 {
					break
				}
			}
			if tick == 500 || landing.Satisfied&0xE0 != 0xA0 {
				t.Fatalf("landing movement outcomes = %#x, want natural arrival plus release", landing.Satisfied&0xE0)
			}
			if sys.AirGoalPayload(u.Handle) != nil || landing.DynamicGate != 0xE0 {
				t.Fatal("arrival must detach the point marker while leaving the landing gate armed")
			}
			if queued {
				q.Push(orders.Lookup("Wait"), orders.Node{Owner: u.Handle, Param1: 30})
			}
			q.Pump(u, tick+1)
			wantMode := uint8(1)
			if queued {
				wantMode = 2
			}
			if u.Move.Mode != wantMode {
				t.Fatalf("mode = %d, want %d after landing's delivered arrival [04 R-AIR-01 §6]", u.Move.Mode, wantMode)
			}
			for _, n := range q.Primary() {
				if n == landing {
					t.Fatal("landing record did not complete on its delivered arrival")
				}
			}
		})
	}
}
