package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestAirToAirLeadInterceptCarriesTheTargetVelocity locks the lead intercept's
// velocity marker [04 R-AIR-01 §8]. Both of its vectors are built from the
// TARGET MOVER'S velocity triple: the position is `targetPos + targetVelocity ·
// 45`, and the commanded velocity is that same triple plus the direction of the
// target's heading at half the target's `MaxVelocity`, componentwise on X and
// Z, with Y the target's own velocity Y rather than zero.
//
// The marker advances its own goal by this velocity every tick, so a velocity
// that omitted the target's own motion drifted the commanded lead point at the
// wrong rate for the whole intercept.
func TestAirToAirLeadInterceptCarriesTheTargetVelocity(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	u.InstallWeapon(0, &content.WeaponDef{Range: 200})

	// Far enough that the range test is on the lead-intercept side of its
	// 0xA0-whole-world-unit threshold, and moving on all three axes with a
	// heading that is not its travel axis, so the two terms are separable.
	target := airTargetFor(t, sys, w, 40, 16)
	target.Def.MaxVelocity = 3 * 65536
	target.Move.Heading = 0x2000
	target.Move.VelX = numeric.Fixed(7 << 10)
	target.Move.VelY = numeric.Fixed(-3 << 10)
	target.Move.VelZ = numeric.Fixed(11 << 10)

	n := pushAirOrder(t, u, "AirToAir", target.X, target.Z)
	n.Target = target.Handle
	n.Phase = 1
	n.Param1 = 0x2D // below 0x5A, so the give-up arm is not the one taken
	n.DynamicGate = 0

	const tick = 700
	if code := sys.legAirToAir(u, n, 0, tick); code != 2 {
		t.Fatalf("the lead intercept returned %d, want 2 (hold) [04 R-AIR-01 §8]", code)
	}

	c := sys.FlightCommandFor(u.Handle, u)
	if c == nil {
		t.Fatal("the lead intercept installed no flight command [04 R-AIR-01 §8]")
	}
	m, ok := c.Payload.(*airVelocityMarker)
	if !ok {
		t.Fatalf("payload is %T, want the velocity marker [04 R-AIR-01 §4]", c.Payload)
	}

	hx, hz := offsetAtBearing(target.Move.Heading, numeric.Fixed(int64(target.Def.MaxVelocity)/2))
	wantVel := Vec3{
		X: target.Move.VelX - hx,
		Y: target.Move.VelY,
		Z: target.Move.VelZ - hz,
	}
	if m.vel != wantVel {
		t.Fatalf("commanded velocity %+v, want %+v — the target's own velocity triple plus the "+
			"heading term at half its MaxVelocity, with Y the target's velocity Y [04 R-AIR-01 §8]", m.vel, wantVel)
	}
	wantPos := Vec3{
		X: target.X + target.Move.VelX*45,
		Y: target.Y,
		Z: target.Z + target.Move.VelZ*45,
	}
	if m.pos != wantPos {
		t.Fatalf("commanded position %+v, want %+v (`targetPos + targetVelocity · 45`) [04 R-AIR-01 §8]", m.pos, wantPos)
	}
	if n.Deadline != int32(tick+45) {
		t.Fatalf("deadline = %d, want %d [04 R-AIR-01 §8]", n.Deadline, tick+45)
	}
}
