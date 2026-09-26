package aikit

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Radar and sonar blips. The engine's sensor phase marks contacts for the
// viewing player only, so the observation evaluates the same rules for its
// owner, with the same arithmetic, rather than reading the engine's bits.
// A blip is what a human sees on the minimap: a position and an owner, no
// type and no identity.
//
// The rules [03 R-VIS-01 §4 pass 2, pass 3][03 R-VIS-01 §5]:
//
//   - A source is an own unit that is alive, not dying and ACTIVE — the
//     instance activation bit, the one read that admits both radar and
//     sonar. A definition that authors a sensor distance its instances never
//     switch on (stock ARMANNI's radar) detects nothing.
//   - The source searches its larger authored distance (inclusive). Within
//     it, a candidate that is not stealthy is a sonar contact when it is at
//     or below sea level and strictly inside the sonar distance, and a radar
//     contact when its model top reaches sea level and it is strictly inside
//     the radar distance plus twice the source's height. A submerged unit is
//     therefore not on radar.
//   - An active jammer of another player clears the radar contact (radar
//     jam) or the sonar contact (sonar jam) of every unit within its jam
//     distance (inclusive). The owner's own jammers never blind it; under
//     Modern an ally's do not either (allied jamming ignored, the Community
//     default Modern resolves, docs/DESIGN_COMMUNITY_PATCH.md §4.4), so only
//     jammers of players the owner is not allied with count.
//
// Distances use the engine's metric: raw 16.16 coordinate differences, the
// high word of each square, summed at 32-bit width.

type sensorSource struct {
	x, z                    numeric.Fixed
	search2, radar2, sonar2 int32
}

type jammerSource struct {
	x, z           numeric.Fixed
	radar2, sonar2 int32 // 0: no such jam
}

// sensorSquare is the high word of a raw 16.16 square.
func sensorSquare(raw int32) int32 { return int32((int64(raw) * int64(raw)) >> 32) }

// sensorRadius2 squares a whole-world-unit radius the same way.
func sensorRadius2(r int32) int32 { return sensorSquare(r << 16) }

func sensorDist2(ax, az, bx, bz numeric.Fixed) int32 {
	return sensorSquare(int32(bx.Sub(ax).Raw())) + sensorSquare(int32(bz.Sub(az).Raw()))
}

// addSensor records an own unit's radar and sonar coverage.
func (b *observer) addSensor(u *units.Unit) {
	d := u.Def
	if d == nil || !u.Alive || u.Dying || !u.Activated || (d.RadarDistance == 0 && d.SonarDistance == 0) {
		return
	}
	search := d.RadarDistance
	if d.SonarDistance > search {
		search = d.SonarDistance
	}
	// The height bonus applies even to a sonar-only source, bounded by its
	// search distance, as in the engine.
	elev := int32(u.Y.Raw() >> 16)
	b.sensors = append(b.sensors, sensorSource{
		x: u.X, z: u.Z,
		search2: sensorRadius2(search),
		radar2:  sensorRadius2(d.RadarDistance + 2*elev),
		sonar2:  sensorRadius2(d.SonarDistance),
	})
}

// addJammer records another player's active jammer.
func (b *observer) addJammer(u *units.Unit) {
	d := u.Def
	if d == nil || !u.Alive || !u.Activated || (d.RadarDistanceJam == 0 && d.SonarDistanceJam == 0) {
		return
	}
	j := jammerSource{x: u.X, z: u.Z}
	if d.RadarDistanceJam != 0 {
		j.radar2 = sensorRadius2(d.RadarDistanceJam)
	}
	if d.SonarDistanceJam != 0 {
		j.sonar2 = sensorRadius2(d.SonarDistanceJam)
	}
	b.jammers = append(b.jammers, j)
}

// blip reports whether a unit out of sight is a radar or sonar contact.
func (b *observer) blip(u *units.Unit, sea numeric.Fixed) bool {
	if len(b.sensors) == 0 || u.Def == nil || u.Def.Stealth {
		return false
	}
	var radar, sonar bool
	top := int32(u.Y.Raw()) + u.Def.ModelTopFixed
	for i := range b.sensors {
		s := &b.sensors[i]
		d2 := sensorDist2(s.x, s.z, u.X, u.Z)
		if d2 > s.search2 {
			continue
		}
		if u.Y <= sea && d2 < s.sonar2 {
			sonar = true
		}
		if int32(sea.Raw()) <= top && d2 < s.radar2 {
			radar = true
		}
	}
	if !radar && !sonar {
		return false
	}
	for i := range b.jammers {
		j := &b.jammers[i]
		d2 := sensorDist2(j.x, j.z, u.X, u.Z)
		if j.radar2 != 0 && d2 <= j.radar2 {
			radar = false
		}
		if j.sonar2 != 0 && d2 <= j.sonar2 {
			sonar = false
		}
	}
	return radar || sonar
}
