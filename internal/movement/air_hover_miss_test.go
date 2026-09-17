package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestAirToGroundHoverMissArmInstallsNothing locks the asymmetry in
// `AirToGroundHover` phase 3 [04 R-AIR-01 §8]. When the miss counter exceeds 1
// the arm resets it, draws a full-circle bearing and builds a point marker at
// `targetPos − offset(bearing, Range)` with arrival radius 0x80 — and then
// never installs it. There is no payload release and no install call before it
// returns *hold*, unlike the alternation arm, so the aircraft keeps the payload
// it already had and is commanded nowhere new. The draw is spent either way,
// which is what keeps the stream in step (I4).
func TestAirToGroundHoverMissArmInstallsNothing(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	u.Def.HoverAttack = true
	u.InstallWeapon(0, &content.WeaponDef{Range: 240})
	target := airTargetFor(t, sys, w, 40, 16)

	n := pushAirOrder(t, u, "AirToGroundHover", target.X, target.Z)
	n.Target = target.Handle
	n.Phase = 3
	n.Param2 = 2 // the miss counter is already above 1, whatever the query says

	// A payload the arm must leave standing: anything installed here is what
	// the aircraft should still be flying when the arm returns.
	standing := sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
	sys.installAirGoal(u, n, standing)

	sim := sys.simRNG(u)
	if sim == nil {
		t.Fatal("fixture: the order queue has no simulation stream [I4]")
	}
	before := sim.Draws()

	code := sys.legAirToGroundHover(u, n, 5)

	if code != 2 {
		t.Fatalf("the miss arm returned %d, want 2 (*hold*) [04 R-AIR-01 §8]", code)
	}
	if n.Param2 != 0 {
		t.Fatalf("the miss counter is %d, want 0: the arm resets it [04 R-AIR-01 §14.3]", n.Param2)
	}
	if d := sim.Draws() - before; d != 1 {
		t.Fatalf("the miss arm drew %d values, want exactly 1 — the full-circle bearing is spent "+
			"even though its marker is dropped [04 R-AIR-01 §8][I4]", d)
	}
	if n.DynamicGate&airLegGateHoverMiss != airLegGateHoverMiss {
		t.Fatalf("gate = %#x, want %#x OR-ed in [04 R-AIR-01 §8]", n.DynamicGate, airLegGateHoverMiss)
	}
	c := sys.FlightCommandFor(u.Handle, u)
	if c == nil || c.Payload != GoalPayload(standing) {
		t.Fatalf("the miss arm replaced the payload (%T), want the standing one left alone: retail "+
			"builds its marker and never installs it [04 R-AIR-01 §8]", c.Payload)
	}
}
