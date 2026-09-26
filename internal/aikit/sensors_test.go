package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The observation's blips are the engine's radar and sonar contacts: a scene
// of random sources, jammers and candidates (inactive, stealthy, submerged,
// on hills, allied jammers) is run through the engine's sensor phase for the
// owner as viewing player, under Modern's rules, and every hostile unit's
// contact bits must agree with the observer's answer [03 R-VIS-01 §4][03
// R-VIS-01 §5]. Nothing is in line of sight, so a contact is a blip.
func TestBlipsMatchTheEngineSensorPhase(t *testing.T) {
	const me, enemy, ally = 0, 1, 2
	terrain := &world.Terrain{CellW: 512, CellH: 512, SeaLevel: 40}
	vis := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	vis.SetLocal(me)
	vis.Rules = visibility.ModernRules{}
	vis.Community.AlliedJammingIgnored = true
	vis.Community.Allied = func(a, b visibility.PlayerID) bool { return a != b && a != enemy && b != enemy }

	seed := uint32(12345)
	next := func(n int32) int32 {
		seed = seed*1664525 + 1013904223
		return int32((seed >> 8) % uint32(n))
	}
	var us []*units.Unit
	add := func(u *units.Unit) {
		us = append(us, u)
	}
	// Edges: a radar on the sea-level plane (no height bonus) does not see an
	// enemy exactly at its distance and sees one a unit nearer; a raised
	// radar's search still ends at its authored distance, inclusive; a jammer
	// exactly at its distance still jams.
	for _, c := range []struct {
		owner            uint8
		x, y, radar, jam int32
	}{
		{owner: me, x: 100, radar: 300}, {owner: enemy, x: 400, y: 45}, {owner: enemy, x: 399, y: 45},
		{owner: me, x: 3100, radar: 500}, {owner: enemy, x: 3400, y: 45, jam: 150}, {owner: enemy, x: 3240, y: 45}, {owner: enemy, x: 3550, y: 45},
		{owner: me, x: 5100, y: 45, radar: 300}, {owner: enemy, x: 5400, y: 45}, {owner: enemy, x: 5401, y: 45},
	} {
		add(&units.Unit{Def: &content.UnitDef{RadarDistance: c.radar, RadarDistanceJam: c.jam, ModelTopFixed: 10 << 16},
			Owner: c.owner, Alive: true, Activated: true, X: numeric.Fixed(c.x << 16), Z: 7000 << 16, Y: numeric.Fixed(c.y << 16)})
	}
	for i := 0; i < 400; i++ {
		d := &content.UnitDef{ModelTopFixed: next(40) << 16}
		switch next(6) {
		case 0:
			d.RadarDistance = 200 + next(1200)
		case 1:
			d.SonarDistance = 200 + next(800)
		case 2:
			d.RadarDistance, d.SonarDistance = 100+next(900), 100+next(900)
		case 3:
			d.RadarDistanceJam = 100 + next(500)
		case 4:
			d.SonarDistanceJam = 100 + next(500)
		}
		d.Stealth = next(8) == 0
		add(&units.Unit{
			Def: d, Owner: uint8(next(3)), Alive: true, Activated: next(4) != 0,
			X: numeric.Fixed(next(8000) << 16), Z: numeric.Fixed(next(6000) << 16), Y: numeric.Fixed(next(90)<<16 | next(65536)),
		})
	}
	su := make([]visibility.SensorUnit, len(us))
	status := make([]uint32, len(us))
	for i, u := range us {
		d := u.Def
		su[i] = visibility.SensorUnit{
			ID: uint16(i + 1), Owner: visibility.PlayerID(u.Owner), Status: &status[i], X: u.X, Y: u.Y, Z: u.Z,
			Alive: true, Stealth: d.Stealth, Active: u.Activated, ModelTopFixed: d.ModelTopFixed,
			RadarDistance: d.RadarDistance, SonarDistance: d.SonarDistance, RadarJam: d.RadarDistanceJam, SonarJam: d.SonarDistanceJam,
		}
	}
	vis.SensorTick(1, 3, su)

	var ob observer
	for _, u := range us {
		if u.Owner == me {
			ob.addSensor(u)
		} else if u.Owner == enemy {
			ob.addJammer(u)
		}
	}
	sea := terrain.SeaLevelWorld()
	contacts := 0
	for i, u := range us {
		if u.Owner != enemy {
			continue
		}
		want := status[i]&(visibility.SeenBit|visibility.SonarBit) != 0
		if got := ob.blip(u, sea); got != want {
			t.Errorf("unit %d at (%d, %d, %d): blip %v, engine status %#x", i, u.X>>16, u.Y>>16, u.Z>>16, got, status[i])
		}
		if want {
			contacts++
		}
	}
	if contacts == 0 {
		t.Fatal("the scene produced no contacts; it tests nothing")
	}
}
