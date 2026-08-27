package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
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

	// Populate every per-handle map the system owns.
	s.Routes[h] = &Route{}
	s.Steers[h] = &SteerState{}
	s.Collisions[h] = &CollisionState{}
	s.Flights[h] = &FlightState{}
	s.profiles[h] = Profile{}
	s.prevMoveTier[h] = 3
	s.prevSFXBand[h] = 2
	s.avoidNext[h] = 11
	s.pathFailures[h] = PathFailure{}
	s.activeOrders[h] = &activeMove{}
	s.arrivalHandles[h] = &arrivalHandle{}
	if s.tickCarried == nil {
		s.tickCarried = make(map[pool.Handle]struct{})
	}
	s.tickCarried[h] = struct{}{}

	s.ForgetUnit(h)

	for name, present := range map[string]bool{
		"Routes":         mapHas(s.Routes, h),
		"Steers":         mapHas(s.Steers, h),
		"Collisions":     mapHas(s.Collisions, h),
		"Flights":        mapHas(s.Flights, h),
		"profiles":       mapHas(s.profiles, h),
		"prevMoveTier":   mapHas(s.prevMoveTier, h),
		"prevSFXBand":    mapHas(s.prevSFXBand, h),
		"avoidNext":      mapHas(s.avoidNext, h),
		"pathFailures":   mapHas(s.pathFailures, h),
		"activeOrders":   mapHas(s.activeOrders, h),
		"arrivalHandles": mapHas(s.arrivalHandles, h),
		"tickCarried":    mapHas(s.tickCarried, h),
	} {
		if present {
			t.Errorf("ForgetUnit left %s state for handle %d", name, h)
		}
	}
}

func mapHas[V any](m map[pool.Handle]V, h pool.Handle) bool {
	_, ok := m[h]
	return ok
}
