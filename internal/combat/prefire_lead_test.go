package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The lead fixture is chosen so every step of [06 §3.3] closes on an exact
// integer and the expected point can be written as a literal rather than as a
// second copy of the formula:
//
//	shooter at the origin, resolved point at (300, 0, 400) whole world units,
//	so the THREE-dimensional distance is exactly 500 world units — the 3-4-5
//	triangle — and D = 500 * 65,536 = 32,768,000 raw.
//	weaponvelocity = 65,536, one world unit per tick, so
//	T  = (32,768,000 << 16) / 65,536 = 32,768,000  (500 ticks in 16.16)
//	T2 = (32,768,000 * 0xcccc) >> 16 = 26,214,000  (0.79998779… of it)
//	a velocity of one world unit per tick therefore displaces the point by
//	T2 itself, and two world units per tick by twice T2.
const (
	leadPointRawX = 300 * 65536 // 19,660,800
	leadPointRawZ = 400 * 65536 // 26,214,400
	leadT2        = 26214000
)

// leadFixture builds a shooter with the given kill count, a moving target, an
// armed slot and a weapon, all satisfying the five gates of [06 §3.3] unless
// the caller breaks one.
func leadFixture(kills int32) (*units.Unit, *units.Unit, *units.Slot, *content.WeaponDef, Vec3) {
	shooter := &units.Unit{Alive: true, Kills: kills, Def: &content.UnitDef{UnitName: "shooter", BMCode: true}}
	target := &units.Unit{Alive: true, Def: &content.UnitDef{UnitName: "target", BMCode: true}}
	// One world unit per tick east, two per tick north; no vertical motion.
	target.Move.VelX = numeric.Fixed(65536)
	target.Move.VelY = 0
	target.Move.VelZ = numeric.Fixed(2 * 65536)
	slot := &units.Slot{Flags: units.SlotFlagEnabled}
	weapon := &content.WeaponDef{WeaponVelocity: 65536}
	point := Vec3{X: numeric.Fixed(leadPointRawX), Y: 0, Z: numeric.Fixed(leadPointRawZ)}
	return shooter, target, slot, weapon, point
}

// TestPreFireLeadArithmetic locks the pre-fire lead of [06 §3.3] against a
// hand-computed point. The whole chain matters: the distance is
// THREE-dimensional (unlike the planar range test in the same section), the
// flight time is a 16.16 tick count, the 0xcccc scale is applied by an
// arithmetic shift, and each axis is displaced by that scaled time times the
// TARGET mover's velocity on that axis.
func TestPreFireLeadArithmetic(t *testing.T) {
	shooter, target, slot, weapon, point := leadFixture(6)
	got := PreFireLeadPoint(shooter, target, slot, weapon, point)
	want := Vec3{
		X: numeric.Fixed(leadPointRawX + leadT2),   // 1 wu/tick  -> +T2
		Y: 0,                                       // no vertical velocity, no vertical lead
		Z: numeric.Fixed(leadPointRawZ + 2*leadT2), // 2 wu/tick  -> +2*T2
	}
	if got != want {
		t.Fatalf("led point = %v, want %v [06 §3.3]", got, want)
	}
	// The scale is 52,428/65,536 — a hair UNDER four fifths, and applied by an
	// arithmetic shift, so the scaled time must sit just below 0.8 of the
	// 500-tick flight time (32,768,000 * 4/5 = 26,214,400) and not at or above
	// it. A rounded-up 0.8, or a shift the wrong way, breaks this band.
	const fourFifths = 32768000 * 4 / 5
	if leadT2 >= fourFifths || leadT2 < fourFifths-1000 {
		t.Fatalf("T2 = %d, want just below four fifths of the flight time (%d) [06 §3.3]", leadT2, fourFifths)
	}
}

// TestPreFireLeadKillThresholdIsStrictAndUnsigned locks the gate row of
// [06 R-DMG-01 §8]: `kills > 5`, strict, on the unsigned 16-bit count. This is
// the only `>`-form consumer of the kill count in the engine — every other one
// divides — so an off-by-one here is invisible everywhere else.
func TestPreFireLeadKillThresholdIsStrictAndUnsigned(t *testing.T) {
	// Five kills: the panel still prints a number, and the shot does not lead.
	shooter, target, slot, weapon, point := leadFixture(5)
	if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
		t.Fatalf("five kills: point = %v, want the unled point %v — the test is STRICT [06 §3.3]", got, point)
	}
	// Six kills: the first led shot.
	shooter, target, slot, weapon, point = leadFixture(6)
	if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got == point {
		t.Fatal("six kills: the point was not led; the threshold is kills > 5 [06 §3.3]")
	}
	// The count is a 16-bit unsigned field that wraps at 65,536
	// [06 R-DMG-01 §8], so a count whose low sixteen bits are 5 is five again.
	shooter, target, slot, weapon, point = leadFixture(0x10005)
	if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
		t.Fatalf("wrapped count: point = %v, want the unled point %v — the compare is on the low sixteen bits [06 R-DMG-01 §8]", got, point)
	}
}

// TestPreFireLeadGates locks the remaining four gates of [06 §3.3]. Each is
// broken on its own from an otherwise-leading fixture, so a gate that stops
// being read fails exactly one subtest.
func TestPreFireLeadGates(t *testing.T) {
	t.Run("armed bit clear", func(t *testing.T) {
		shooter, target, slot, weapon, point := leadFixture(6)
		slot.Flags &^= units.SlotFlagEnabled
		if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
			t.Fatalf("point = %v, want the unled point with the armed bit clear [06 §3.3]", got)
		}
	})
	t.Run("cruise suppresses the lead", func(t *testing.T) {
		shooter, target, slot, weapon, point := leadFixture(6)
		weapon.Cruise = true
		if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
			t.Fatalf("point = %v, want the unled point for a `cruise` weapon [06 §3.3][06 §6.7]", got)
		}
	})
	t.Run("target has no movement record", func(t *testing.T) {
		shooter, target, slot, weapon, point := leadFixture(6)
		target.Def.BMCode = false // a building has no mover [04 R-COLL-01 §1]
		if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
			t.Fatalf("point = %v, want the unled point for a target with no mover [06 §3.3]", got)
		}
	})
	t.Run("zero weaponvelocity", func(t *testing.T) {
		shooter, target, slot, weapon, point := leadFixture(6)
		weapon.WeaponVelocity = 0
		if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
			t.Fatalf("point = %v, want the unled point with a zero weaponvelocity [06 §3.3]", got)
		}
	})
	t.Run("a stationary target is led by nothing", func(t *testing.T) {
		shooter, target, slot, weapon, point := leadFixture(6)
		target.Move.VelX, target.Move.VelY, target.Move.VelZ = 0, 0, 0
		if got := PreFireLeadPoint(shooter, target, slot, weapon, point); got != point {
			t.Fatalf("point = %v, want the unled point for a target at rest [06 §3.3]", got)
		}
	})
}

// TestPreFireLeadDistanceIsThreeDimensional locks the sentence [06 §3.3] adds
// after the arithmetic: "The distance here is three-dimensional, unlike the
// range test." A solver that reused the planar range distance would shorten
// the flight time — and so the lead — for every target above or below the
// shooter.
func TestPreFireLeadDistanceIsThreeDimensional(t *testing.T) {
	shooter, target, slot, weapon, point := leadFixture(6)
	flat := PreFireLeadPoint(shooter, target, slot, weapon, point)
	// Raise the point 1,200 world units: the planar distance is unchanged, so
	// a planar solver would produce an identical lead.
	point.Y = numeric.Fixed(1200 * 65536)
	raised := PreFireLeadPoint(shooter, target, slot, weapon, point)
	if raised.X.Sub(point.X) == flat.X.Sub(numeric.Fixed(leadPointRawX)) {
		t.Fatal("the lead ignored the vertical delta; the distance is three-dimensional [06 §3.3]")
	}
	// 300-0-400 planar with 1,200 of height is 1,300 world units, so the lead
	// grows by exactly the ratio the longer flight time gives.
	if raised.X.Sub(point.X) <= flat.X.Sub(numeric.Fixed(leadPointRawX)) {
		t.Fatalf("a farther target led by %d, no more than the nearer target's %d",
			raised.X.Sub(point.X), flat.X.Sub(numeric.Fixed(leadPointRawX)))
	}
}
