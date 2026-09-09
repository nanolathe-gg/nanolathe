package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// cruiseTerrainSeaLevel is the fixture map's sea-level byte. The fixture has
// no plot, so the bilinear query answers zero everywhere and the sea level is
// what `max(terrainHeight, seaLevel)` returns — which makes the below-threshold
// branch's altitude a single readable number.
const cruiseTerrainSeaLevel = 37

func cruiseTerrain() *world.Terrain {
	return &world.Terrain{CellW: 256, CellH: 256, SeaLevel: cruiseTerrainSeaLevel}
}

// cruiseRecord is a self-propelled guided record whose stored target point sits
// at a fixed place, with the projectile placed `awayRaw` raw 16.16 units west
// of it so the helper's three-dimensional distance is exactly that value.
func cruiseRecord(awayRaw int64) *Projectile {
	stored := Vec3{X: numeric.Fixed(2000 * 65536), Y: numeric.Fixed(11 * 65536), Z: numeric.Fixed(3000 * 65536)}
	return &Projectile{
		Pos:       Vec3{X: stored.X.Sub(numeric.Fixed(awayRaw)), Y: stored.Y, Z: stored.Z},
		TargetPos: stored,
	}
}

// TestCruiseWaypointThreshold locks the cruise waypoint helper of [06 §6.8].
// The steer point is always the STORED target's X and Z — cruise ignores the
// projectile link and the retained unit target — and only its altitude moves:
//
//	(int16)(d >> 16) > 1024  ->  Y = 700 whole world units, ABSOLUTE
//	otherwise                ->  Y = max(terrainHeight, seaLevel) << 16
//
// The compare is a signed short and it is STRICT, so exactly 1,024 world units
// takes the floor branch. That boundary is the whole point of the test: a
// `>=` here would put a missile on its terminal descent one world unit early.
func TestCruiseWaypointThreshold(t *testing.T) {
	terrain := cruiseTerrain()
	stored := cruiseRecord(0).TargetPos
	floorY := numeric.Fixed(int64(cruiseTerrainSeaLevel) << 16)
	ceilingY := numeric.Fixed(CruiseCeiling << 16)

	cases := []struct {
		name    string
		awayRaw int64
		wantY   numeric.Fixed
	}{
		{"far above the threshold", 4096 * 65536, ceilingY},
		{"one world unit above the threshold", 1025 * 65536, ceilingY},
		{"exactly the threshold takes the floor branch", 1024 * 65536, floorY},
		{"a fraction above the threshold still floors, the shift truncates", 1024*65536 + 32768, floorY},
		{"one world unit below the threshold", 1023 * 65536, floorY},
		{"on top of the target", 0, floorY},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CruiseTargetPoint(cruiseRecord(tc.awayRaw), terrain)
			want := Vec3{X: stored.X, Y: tc.wantY, Z: stored.Z}
			if got != want {
				t.Fatalf("steer point = %v, want %v [06 §6.8]", got, want)
			}
		})
	}
	// The far branch's altitude is ABSOLUTE world Y, not a height above the
	// terrain: raising the map's floor must not move it.
	high := &world.Terrain{CellW: 256, CellH: 256, SeaLevel: 200}
	if got := CruiseTargetPoint(cruiseRecord(4096*65536), high); got.Y != ceilingY {
		t.Fatalf("far branch altitude = %d over a higher sea level, want the absolute %d [06 §6.8]", got.Y, ceilingY)
	}
}

// TestCruiseWaypointIgnoresRetainedReferences locks the first half of
// [06 §6.8]: `cruise` — not `commandfire` — selects the waypoint helper, and it
// "ignores the projectile-link and retained-unit sources entirely and works
// from the stored target point". A cruise record with both references set must
// therefore answer the stored point's X and Z, never the linked record's
// position and never the live unit's.
func TestCruiseWaypointIgnoresRetainedReferences(t *testing.T) {
	terrain := cruiseTerrain()
	p := cruiseRecord(4096 * 65536)
	p.TargetProjectile = 1
	p.TargetUnit = 1
	link := &Projectile{Pos: Vec3{X: fi(10), Y: fi(10), Z: fi(10)}}
	live := &units.Unit{X: fi(20), Y: fi(20), Z: fi(20), Alive: true}

	env := guidanceEnvFor(link, live)
	env.Terrain = terrain
	cruise := &content.WeaponDef{SelfProp: true, Guidance: true, Cruise: true}
	got := GuidanceTargetPoint(p, cruise, env)
	want := Vec3{X: p.TargetPos.X, Y: numeric.Fixed(CruiseCeiling << 16), Z: p.TargetPos.Z}
	if got != want {
		t.Fatalf("cruise steer point = %v, want the stored point at the cruise ceiling %v [06 §6.8]", got, want)
	}
	// The same record on a non-cruise weapon takes the ordinary three-source
	// order and follows the LINK, which proves the branch above was the cruise
	// arm and not a coincidence of the fixture.
	ordinary := &content.WeaponDef{SelfProp: true, Guidance: true}
	if got := GuidanceTargetPoint(p, ordinary, env); got != link.Pos {
		t.Fatalf("non-cruise steer point = %v, want the linked record's point %v [06 §6.7]", got, link.Pos)
	}
	// The stored point itself is never rewritten: it is the lost-target
	// fallback [06 §6.8].
	if p.TargetPos.Y != numeric.Fixed(11*65536) {
		t.Fatalf("the stored target point's Y was overwritten to %d [06 §6.8]", p.TargetPos.Y)
	}
}
