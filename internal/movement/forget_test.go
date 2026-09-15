package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// TestForgetUnitLeavesNoPerHandleState locks P0-011. Pool slots are reused by
// handle, so any per-handle movement state a dead unit leaves behind is
// inherited by the next unit in that slot. Death cleanup used to touch only
// routes, steers, flights and collisions, leaving the resolved profile and
// every cached band in place. A forgotten handle must be indistinguishable
// from one that was never used.
func TestForgetUnitLeavesNoPerHandleState(t *testing.T) {
	s := NewSystem(nil, Profile{}, nil)
	const h pool.Handle = 7
	s.growHandleTables(int(h))

	// Populate every per-handle row the system owns.
	setHandleRow(&s.Routes, h, &Route{})
	setHandleRow(&s.Steers, h, &SteerState{})
	setHandleRow(&s.Collisions, h, &CollisionState{})
	setHandleRow(&s.Flights, h, &FlightState{})
	setHandleRow(&s.profiles, h, &Profile{})
	setHandleRow(&s.prevMoveTier, h, 3)
	setHandleRow(&s.prevSFXBand, h, 2)
	setHandleRow(&s.pathFailures, h, &PathFailure{})
	setHandleRow(&s.activeOrders, h, &activeMove{})
	setHandleRow(&s.clearanceRoutes, h, &modernClearanceRoute{})
	setHandleRow(&s.arrivalHandles, h, &arrivalHandle{})
	s.ForgetUnit(h)

	for name, present := range map[string]bool{
		"Routes":          rowHas(s.Routes, h),
		"Steers":          rowHas(s.Steers, h),
		"Collisions":      rowHas(s.Collisions, h),
		"Flights":         rowHas(s.Flights, h),
		"profiles":        rowHas(s.profiles, h),
		"prevMoveTier":    handleRow(s.prevMoveTier, h) != 0,
		"prevSFXBand":     handleRow(s.prevSFXBand, h) != 0,
		"pathFailures":    rowHas(s.pathFailures, h),
		"activeOrders":    rowHas(s.activeOrders, h),
		"clearanceRoutes": rowHas(s.clearanceRoutes, h),
		"arrivalHandles":  rowHas(s.arrivalHandles, h),
	} {
		if present {
			t.Errorf("ForgetUnit left %s state for handle %d", name, h)
		}
	}
}

// rowHas reports whether a dense per-handle row still holds something for h —
// the slice twin of the map lookup this test used to make.
func rowHas[V any](row []*V, h pool.Handle) bool {
	return int(h) < len(row) && row[h] != nil
}
