package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestSubmergedEnemySensorClasses locks which sensor class reveals a fully
// submerged enemy to the direct-visibility predicate [03 §3.2] step 3
// [03 R-VIS-01 §4] pass 5 [03 R-VIS-01 §5]:
//
//   - line of sight alone sets the seen bit (the pass-5 probe has no sea-level
//     term), but the predicate still rejects the unit because the sonar bit is
//     clear, so no model, icon or target is exposed;
//   - radar never contacts a unit whose hull top is below the sea plane;
//   - sonar within its strict radius sets the exemption bit and admits it.
//
// The seen bit from line of sight is what the retail minimap blip gate reads
// ([03 §3.9] "Blip gate"), which is why that disclosure is not this predicate.
func TestSubmergedEnemySensorClasses(t *testing.T) {
	const sea = 40
	seaFixed := numeric.Fixed(sea << 16)
	sub := func() (Target, SensorUnit, *uint32) {
		var status uint32
		y := numeric.Fixed(10 << 16) // hull top 10+20 < 40: fully submerged
		u := SensorUnit{ID: 2, Owner: 1, Status: &status, Alive: true,
			X: tileWorld(10), Y: y, Z: tileWorld(10) + numeric.Fixed(5<<16), ModelTopFixed: 20 << 16}
		tg := Target{Owner: 1, X: u.X, Y: y + numeric.Fixed(20<<16), Z: u.Z}
		return tg, u, &status
	}

	t.Run("line of sight without sonar", func(t *testing.T) {
		s := newTestService(&world.Terrain{CellW: 128, CellH: 128, SeaLevel: sea}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.SetLocal(0)
		s.Publish(0, 10, 10, 0, 320)
		tg, enemy, status := sub()
		var mine uint32
		s.SensorTick(0, 2, []SensorUnit{{ID: 1, Owner: 0, Status: &mine, Alive: true}, enemy})
		if *status&SeenBit == 0 || *status&SonarBit != 0 {
			t.Fatalf("status %#x: want seen from line of sight, no sonar", *status)
		}
		tg.Status = *status
		if tg.Y >= seaFixed || s.IsVisible(0, tg) {
			t.Fatal("a submerged enemy without sonar contact passed the direct-visibility predicate")
		}
	})

	t.Run("radar does not reach a submerged hull", func(t *testing.T) {
		s := newTestService(&world.Terrain{CellW: 128, CellH: 128, SeaLevel: sea}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.SetLocal(0)
		_, enemy, status := sub()
		var mine uint32
		radar := SensorUnit{ID: 1, Owner: 0, Status: &mine, Alive: true, Active: true,
			X: enemy.X, Z: enemy.Z + numeric.Fixed(50<<16), RadarDistance: 400}
		s.SensorTick(0, 2, []SensorUnit{radar, enemy})
		if *status&(SeenBit|SonarBit) != 0 {
			t.Fatalf("radar contacted a fully submerged hull: status %#x", *status)
		}
	})

	t.Run("sonar admits it", func(t *testing.T) {
		s := newTestService(&world.Terrain{CellW: 128, CellH: 128, SeaLevel: sea}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.SetLocal(0)
		s.Publish(0, 10, 10, 0, 320)
		tg, enemy, status := sub()
		var mine uint32
		sonar := SensorUnit{ID: 1, Owner: 0, Status: &mine, Alive: true, Active: true,
			X: enemy.X, Z: enemy.Z + numeric.Fixed(50<<16), SonarDistance: 400}
		s.SensorTick(0, 2, []SensorUnit{sonar, enemy})
		if *status&SonarBit == 0 {
			t.Fatalf("sonar did not contact the submerged enemy: status %#x", *status)
		}
		tg.Status = *status
		if !s.IsVisible(0, tg) {
			t.Fatal("a sonar-contacted submerged enemy in line of sight was rejected")
		}
	})
}
