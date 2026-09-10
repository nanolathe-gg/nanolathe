package orders

import "testing"

// Air feature reclaim installs one marker, advances, and consumes its no-route
// outcome in phase 2; it has no distance polling retry [04 R-ORD-01 §7].
func TestVTOLReclaimUnreachableMarkerEndsTheOrder(t *testing.T) {
	q, builder, _ := vtolWorkFixture()
	builder.X, builder.Z = 10000<<16, 10000<<16
	builder.Def.BuildDistance = 10
	q.Push(Lookup("VTOL_Reclaim"), Node{Owner: builder.Handle, GoalX: 70 << 16, GoalZ: 90 << 16})
	n := q.Primary()[0]
	n.Phase = 1
	var installs int
	q.binding.Movement.InstallAir = func(AirGoalRequest) bool { installs++; return true }
	q.Pump(builder, 100)
	if n.Phase != 2 || n.DynamicGate != gateMoveOutcomes || n.Deadline != -1 || installs != 1 {
		t.Fatalf("marker wait: phase=%d gate=%#x deadline=%d installs=%d", n.Phase, n.DynamicGate, n.Deadline, installs)
	}
	n.Satisfied |= gateNoRoute
	q.Pump(builder, 101)
	if q.LenPrimary() != 0 || installs != 1 {
		t.Fatalf("unreachable reclaim kept retrying: queue=%d installs=%d", q.LenPrimary(), installs)
	}
}
