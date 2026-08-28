package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// recordingSurfaces captures what the sensor phase rasterizes.
type recordingSurfaces struct {
	wipes    int
	sensor   [][3]int32
	radarJam [][3]int32
	sonarJam [][3]int32
}

func (r *recordingSurfaces) Wipe()                  { r.wipes++ }
func (r *recordingSurfaces) Sensor(u, v, rad int32) { r.sensor = append(r.sensor, [3]int32{u, v, rad}) }
func (r *recordingSurfaces) RadarJam(u, v, rad int32) {
	r.radarJam = append(r.radarJam, [3]int32{u, v, rad})
}
func (r *recordingSurfaces) SonarJam(u, v, rad int32) {
	r.sonarJam = append(r.sonarJam, [3]int32{u, v, rad})
}

// TestSensorTickRequiresTwoPlayers locks C12's outermost gate.
func TestSensorTickRequiresTwoPlayers(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	surf := &recordingSurfaces{}
	s.SetSurfaces(surf)
	var status uint32
	units := []SensorUnit{{Owner: 0, Status: &status, Alive: true, Active: true, RadarDistance: 100}}

	s.SensorTick(0, 1, nil, units)
	if surf.wipes != 0 {
		t.Fatal("the sensor phase ran with a single player")
	}
	s.SensorTick(0, 2, nil, units)
	if surf.wipes != 1 {
		t.Fatal("the sensor phase did not run with two players")
	}
}

// TestSensorCirclesNeverTouchTheWordMask locks C11.
func TestSensorCirclesNeverTouchTheWordMask(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	surf := &recordingSurfaces{}
	s.SetSurfaces(surf)
	before := append([]uint16(nil), s.wordMask...)

	var status uint32
	units := []SensorUnit{{
		Owner: 1, Status: &status, Alive: true, Active: true,
		X: tileWorld(10), Z: tileWorld(10),
		RadarDistance: 200, SonarDistance: 500, RadarJam: 80, SonarJam: 90,
	}}
	s.SensorTick(0, 2, nil, units)

	for i := range before {
		if s.wordMask[i] != before[i] {
			t.Fatalf("the sensor phase wrote the word mask at %d", i)
		}
	}
	// ONE sensor circle, at the larger of radar and sonar [03 §3.4].
	if len(surf.sensor) != 1 {
		t.Fatalf("emitted %d sensor circles, want 1", len(surf.sensor))
	}
	if surf.sensor[0][2] != 500 {
		t.Fatalf("sensor radius %d, want 500 (the larger of 200 and 500)", surf.sensor[0][2])
	}
	if len(surf.radarJam) != 1 || len(surf.sonarJam) != 1 {
		t.Fatalf("jam circles: radar %d sonar %d, want 1 each", len(surf.radarJam), len(surf.sonarJam))
	}
}

func TestSensorActiveGateAndCircleSnapshot(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	surf := &recordingSurfaces{}
	s.SetSurfaces(surf)
	var inactiveStatus, activeStatus uint32
	units := []SensorUnit{
		{ID: 1, Owner: 1, Status: &inactiveStatus, Alive: true, Active: false, Hidden: true, RadarDistance: 200, RadarJam: 10},
		{ID: 2, Owner: 1, Status: &activeStatus, Alive: true, Active: true, Hidden: true, RadarDistance: 300, SonarJam: 20},
	}
	s.SensorTick(4, 2, nil, units)
	if len(surf.sensor) != 1 || surf.sensor[0][2] != 300 || len(surf.radarJam) != 0 || len(surf.sonarJam) != 1 {
		t.Fatalf("active callback gate: outer=%v radarJam=%v sonarJam=%v", surf.sensor, surf.radarJam, surf.sonarJam)
	}
	circles := s.SensorCircles()
	if len(circles) != 2 || circles[0].Radius != 300 || circles[1].Kind != 2 {
		t.Fatalf("circle snapshot = %+v, want outer then sonar jammer", circles)
	}
	circles[0].Radius = 1
	if got := s.SensorCircles()[0].Radius; got != 300 {
		t.Fatalf("circle snapshot was not copied: got %d", got)
	}
}

func TestSensorSeenBitClearsAtFrameStart(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var status uint32 = SeenBit
	units := []SensorUnit{{ID: 1, Owner: 1, Status: &status, Alive: true, X: tileWorld(10), Z: tileWorld(10)}}
	s.SensorTick(1, 2, nil, units)
	if status&SeenBit != 0 {
		t.Fatal("unseen unit retained stale per-frame SeenBit")
	}
}

// TestFriendlyMarkingExemptsUnderwater locks the coupling between the sensor
// phase and the predicate: the friendly mask's upper bit IS the underwater
// exemption [03 §3.2] C8 step 3, [03 §3.4].
func TestFriendlyMarkingExemptsUnderwater(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128, SeaLevel: 20}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 10, 10, 0, 320)

	var mine, theirs uint32
	allied := func(a, b PlayerID) bool { return a == 0 && b == 1 }
	units := []SensorUnit{
		{Owner: 1, Status: &mine, Alive: true},   // allied with the local player
		{Owner: 2, Status: &theirs, Alive: true}, // not allied
	}
	s.SensorTick(0, 3, allied, units)

	// 0x300 is 0x100|0x200 and the seen pass also sets 0x100, so the
	// discriminating bit is the upper one — which is exactly the underwater
	// exemption [03 §3.4].
	if mine&FriendlyMask != FriendlyMask {
		t.Fatalf("allied unit status %#x lacks the friendly mask", mine)
	}
	if theirs&underwaterExempt != 0 {
		t.Fatalf("enemy unit status %#x carries the exemption bit", theirs)
	}
	// A submerged allied unit is now exempt from the sea-level rejection.
	sub := Target{
		Owner: 1, Status: mine,
		X: tileWorld(10), Y: numeric.Fixed(10 * 65536),
		Z: tileWorld(10) + numeric.Fixed(5*65536),
	}
	if !s.IsVisible(0, sub) {
		t.Fatal("a friendly-marked submerged unit should be exempt from the sea-level test")
	}
	sub.Status = theirs
	sub.Owner = 2
	if s.IsVisible(0, sub) {
		t.Fatal("an unmarked submerged enemy should be rejected")
	}
}

// TestProximityDecloak locks C10/C12: a cloaked unit's proximity search writes
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestProximityDecloak(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var cloakedStatus, nearStatus, farStatus uint32
	var cloakedDeadline uint32
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }

	units := []SensorUnit{
		{Owner: 1, Status: &cloakedStatus, Alive: true, Hidden: true,
			X: px(100), Z: px(100), MinCloakDistance: 50, DecloakDeadline: &cloakedDeadline},
		{Owner: 0, Status: &nearStatus, Alive: true, X: px(120), Z: px(100)},
		{Owner: 0, Status: &farStatus, Alive: true, X: px(300), Z: px(100)},
	}
	s.SensorTick(1000, 2, nil, units)

	if cloakedStatus&DecloakBit == 0 {
		t.Fatal("a cloaked unit with enemy inside mincloakdistance was not marked [03 §3.4] P0-11")
	}
	if cloakedDeadline != 1000+DecloakDeadlineAdd {
		t.Fatalf("decloak deadline %d, want %d [03 §3.4] P0-11", cloakedDeadline, 1000+DecloakDeadlineAdd)
	}
	if nearStatus&DecloakBit != 0 {
		t.Fatal("an enemy unit outside cloaked search was incorrectly marked")
	}
	if farStatus&DecloakBit != 0 {
		t.Fatal("a far unit outside mincloakdistance was marked")
	}
	// Test timeout: move cloaked far away before deadline expires, then advance beyond deadline.
	units[0].X = px(1000)
	units[0].Z = px(1000)
	s.SensorTick(1000+DecloakDeadlineAdd+1, 2, nil, units)
	if cloakedStatus&DecloakBit != 0 {
		t.Fatal("decloak bit should clear after GT>=deadline and no longer within range [03 §3.4] P0-11")
	}
}
