package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestHitDirectionByteIsVictimRelativeRetailBearing locks byte 7 of the damage
// packet [06 §9.1]: the high byte of
// `atan2q(record - victim) - victim.heading`. Under retail's convention a yaw
// `a` names the direction (-sin a, -cos a) and the bearing helper's operand
// order is record minus victim, so a record dead ahead of the victim reads
// 0x80 at every heading, one on its right (facing -Z, +X is right) reads 0x40,
// and one behind reads 0x00. The byte is not the projectile's yaw: this build
// used to hand `p.Yaw >> 8` over, which carried neither the heading term nor
// retail's numbering [06 R-WPN-05 §11].
func TestHitDirectionByteIsVictimRelativeRetailBearing(t *testing.T) {
	const reach = 64 << 16
	cases := []struct {
		name    string
		heading uint16
		dx, dz  int64 // record minus victim, world
		want    uint8
	}{
		{"ahead, heading 0 faces -Z", 0, 0, -reach, 0x80},
		{"behind, heading 0", 0, 0, reach, 0x00},
		{"right of heading 0 (+X)", 0, reach, 0, 0x40},
		{"left of heading 0 (-X)", 0, -reach, 0, 0xC0},
		{"ahead, heading 0x4000 faces -X", 0x4000, -reach, 0, 0x80},
		{"ahead, heading 0x8000 faces +Z", 0x8000, 0, reach, 0x80},
		{"ahead, heading 0xC000 faces +X", 0xC000, reach, 0, 0x80},
		{"behind, heading 0xC000", 0xC000, -reach, 0, 0x00},
	}
	for _, c := range cases {
		victim := &units.Unit{X: 500 << 16, Y: 0, Z: 500 << 16}
		victim.Move.Heading = c.heading
		p := &Projectile{Pos: Vec3{X: victim.X + numeric.Fixed(c.dx), Y: 0, Z: victim.Z + numeric.Fixed(c.dz)}}
		// The stored yaw is deliberately garbage: the byte must not read it.
		p.Yaw = numeric.Angle(0x1234)
		if got := hitDirectionByte(p, victim); got != c.want {
			t.Fatalf("%s: direction byte %#02x, want %#02x", c.name, got, c.want)
		}
	}
}

// TestRetailYawIsTheHeadingOfTheSameDirection pins the published yaw: the
// frame carries retail's word, which for a projectile flying the way a unit at
// heading h travels is h itself — what the renderer's projectile angle block
// [03 §5.2] and every research sentence are written over [06 R-WPN-05 §11].
func TestRetailYawIsTheHeadingOfTheSameDirection(t *testing.T) {
	const reach = 100 << 16
	for _, h := range []uint16{0, 0x2000, 0x4000, 0x8000, 0xA000, 0xC000, 0xE000} {
		// The heading's direction is (-sin h, -cos h) [04 R-MOV-01 §4].
		dx := numeric.Fixed(-(int64(numeric.Sin(numeric.Angle(h))) * reach) >> 13)
		dz := numeric.Fixed(-(int64(numeric.Cos(numeric.Angle(h))) * reach) >> 13)
		goYaw := YawFromDelta(dx, dz)
		if got := RetailYaw(goYaw); got != h {
			t.Fatalf("heading %#04x: retail yaw %#04x (stored %#04x)", h, got, uint16(goYaw))
		}
	}
}
