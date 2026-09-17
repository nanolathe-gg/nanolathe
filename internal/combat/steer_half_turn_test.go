package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestSteerTowardCarriesTheAbsoluteErrorSigned locks [06 §6.7]: the steering
// block's absolute angular error is a SIGNED 16-bit quantity before both of its
// uses — the burn-blow threshold compare and the compare against the turn rate.
// Errors from 0 to 32,767 behave like a magnitude; an error of exactly a half
// turn carries as −32,768, so it never trips the burn-blow threshold and always
// snaps to the wanted angle whatever the turn rate.
//
// The discriminating row is 0x8000. The other rows pin the two comparison
// strictnesses either side of it so a future rewrite cannot trade one for the
// other.
func TestSteerTowardCarriesTheAbsoluteErrorSigned(t *testing.T) {
	const turn = 1000

	// A pursuit point offset in all three axes, so both wanted angles are
	// well-defined and the two axes can be driven to the same error.
	point := Vec3{
		X: numeric.FixedFromInt(700),
		Y: numeric.FixedFromInt(300),
		Z: numeric.FixedFromInt(900),
	}
	origin := Vec3{}
	wantedYaw := YawFromDelta(point.X, point.Z)
	wantedPitch := PitchFromDelta(point.X, point.Y, point.Z)

	type outcome struct {
		failed bool
		yaw    numeric.Angle
		pitch  numeric.Angle
	}
	// stepped answers the angle one turn rate toward the wanted angle from an
	// error of err, i.e. the non-snap arm.
	stepped := func(cur numeric.Angle, err int16) numeric.Angle {
		if err >= 0 {
			return numeric.Angle(uint16(int32(cur) + turn))
		}
		return numeric.Angle(uint16(int32(cur) - turn))
	}

	for _, tc := range []struct {
		name string
		err  int16
		// snap is true when retail takes the "snap to the wanted angle" arm;
		// burnFail is true when a burn-blow weapon's steer fails on this error.
		snap     bool
		burnFail bool
	}{
		{name: "zero error", err: 0, snap: true},
		{name: "one below the rate", err: 1, snap: true},
		{name: "just below the rate", err: turn - 1, snap: true},
		{name: "exactly the rate steps, the compare is strict", err: turn, snap: false},
		{name: "largest positive error", err: 0x7fff, snap: false, burnFail: true},
		{name: "exactly a half turn", err: -0x8000, snap: true, burnFail: false},
		{name: "one inside a half turn", err: -0x7fff, snap: false, burnFail: true},
	} {
		for _, burn := range []bool{false, true} {
			w := &content.WeaponDef{TurnRate: turn, BurnBlow: burn}
			p := &Projectile{Pos: origin}
			p.Yaw = wantedYaw - numeric.Angle(uint16(tc.err))
			p.Pitch = wantedPitch - numeric.Angle(uint16(tc.err))
			startYaw, startPitch := p.Yaw, p.Pitch

			got := outcome{failed: steerToward(p, w, point), yaw: p.Yaw, pitch: p.Pitch}

			var want outcome
			switch {
			case burn && tc.burnFail:
				// Yaw is processed first, so its failure leaves both angles
				// untouched [06 §6.7].
				want = outcome{failed: true, yaw: startYaw, pitch: startPitch}
			case tc.snap:
				want = outcome{yaw: wantedYaw, pitch: wantedPitch}
			default:
				want = outcome{yaw: stepped(startYaw, tc.err), pitch: stepped(startPitch, tc.err)}
			}
			if got != want {
				t.Errorf("%s (burnblow=%v): got %+v, want %+v [06 §6.7]", tc.name, burn, got, want)
			}
		}
	}
}
