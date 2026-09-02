package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestBallisticT0DividesTheSlotDistanceWordUnsigned locks the ballistic
// creator's `T0` [06 §6.4] (RWU-19-39): it is the firing slot's stored distance
// word over the weapon velocity, an UNSIGNED divide of the two raw words.
//
// The distance word is the per-unit constant the slot initializer wrote at
// construction [06 R-WPN-05 §3], not a flight distance to this shot's target,
// so `T0` does not vary with range. The negative case is reproduced as the
// section states — the unsigned divide turns it into a very large tick count —
// and whether stock units ever store a negative word is Unknown.
func TestBallisticT0DividesTheSlotDistanceWordUnsigned(t *testing.T) {
	const vel = 65536 // one world unit per tick, 16.16
	if got := BallisticFlightTicks(5*vel, vel); got != 5 {
		t.Fatalf("T0 = %d, want 5 = 5.0 / 1.0 [06 §6.4]", got)
	}
	// Truncating, not rounding: 5.75 / 1.0 is five whole ticks.
	if got := BallisticFlightTicks(5*vel+49152, vel); got != 5 {
		t.Fatalf("T0 = %d, want 5: the divide yields whole ticks [06 §6.4]", got)
	}
	// The unsigned divide of a negative word. -1.0 read unsigned is 2^32-65536,
	// which over a velocity of 65536 is 65535 ticks, not -1.
	if got := BallisticFlightTicks(-vel, vel); got != 65535 {
		t.Fatalf("T0 = %d, want 65535: the divide is unsigned, so a negative word yields a very large T0 [06 §6.4]", got)
	}
}

// TestBallisticLaunchPreDecrementsVerticalVelocity locks the second half of the
// creator's velocity build [06 §6.4]:
//
//	velocityY = sin(pitch, weaponvelocity) - T0 * gravity
//
// The horizontal components are untouched by the pre-decrement, and a zero
// distance word leaves the whole vector at the bare angle build — which is why
// the word's one writer matters: a unit whose script answers neither piece
// query fires with no pre-decrement at all.
func TestBallisticLaunchPreDecrementsVerticalVelocity(t *testing.T) {
	const vel = 65536
	w := &content.WeaponDef{ID: 1, Ballistic: true, WeaponVelocity: vel, WeaponTimer: 90}
	gravity := numeric.Fixed(1000)
	pitch := numeric.Angle(4096) // an eighth turn up
	yaw := numeric.Angle(1234)

	var bare Projectile
	InitBallistic(&bare, w, 0, Vec3{}, Vec3{}, 0, pitch, yaw, 0, gravity)

	var dropped Projectile
	InitBallistic(&dropped, w, 0, Vec3{}, Vec3{}, 0, pitch, yaw, 5*vel, gravity)

	wantY := bare.Velocity.Y.Raw() - 5*gravity.Raw()
	if dropped.Velocity.Y.Raw() != wantY {
		t.Fatalf("velocityY = %d, want %d = sin(pitch, v) - T0 × gravity with T0 = 5 [06 §6.4]", dropped.Velocity.Y.Raw(), wantY)
	}
	if dropped.Velocity.X != bare.Velocity.X || dropped.Velocity.Z != bare.Velocity.Z {
		t.Fatalf("the pre-decrement moved the horizontal components (%v vs %v); it is vertical only [06 §6.4]", dropped.Velocity, bare.Velocity)
	}
	// A zero word is the whole of the "script answered neither query" case.
	var zero Projectile
	InitBallistic(&zero, w, 0, Vec3{}, Vec3{}, 0, pitch, yaw, 0, gravity)
	if zero.Velocity != bare.Velocity {
		t.Fatalf("a zero distance word changed the launch velocity: %v vs %v [06 R-WPN-05 §3]", zero.Velocity, bare.Velocity)
	}
}
