package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestSensorTickRequiresTwoPlayers locks C12's outermost gate. The phase's
// only observable output is the status bits it writes plus the per-unit
// snapshot it publishes, so the snapshot is what tells the two apart
// [R-VIS-01 §4] "Gate".
func TestSensorTickRequiresTwoPlayers(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var status uint32
	units := []SensorUnit{{ID: 1, Owner: 0, Status: &status, Alive: true, Active: true, RadarDistance: 100}}

	s.SensorTick(0, 1, nil, units)
	if len(s.SensorInputs()) != 0 || status != 0 {
		t.Fatalf("the sensor phase ran with a single player: inputs %d, status %#x", len(s.SensorInputs()), status)
	}
	s.SensorTick(0, 2, nil, units)
	if len(s.SensorInputs()) != 1 || status&FriendlyMask == 0 {
		t.Fatalf("the sensor phase did not run with two players: inputs %d, status %#x", len(s.SensorInputs()), status)
	}
}

// TestSensorPhaseNeverTouchesTheWordMask locks C11 and, since 2026-08-30, the
// stronger statement that replaced it: the sensor phase rasterizes nothing at
// all. [03 §3.10]'s 2026-08-29 correction establishes that the minimap's
// radar/sonar/jammer circles are presentation drawn by the CONTACTS pass and
// retracts the reading that gave this phase three rasterizing callback tables;
// [R-VIS-01 §5] establishes those callbacks are one-line status-bit writers.
// The phase therefore has no surface of its own to write, and it still must not
// reach the authoritative word mask.
func TestSensorPhaseNeverTouchesTheWordMask(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(1)
	before := append([]uint16(nil), s.wordMask...)

	var status uint32
	units := []SensorUnit{{
		ID: 1, Owner: 1, Status: &status, Alive: true, Active: true,
		X: tileWorld(10), Z: tileWorld(10),
		RadarDistance: 200, SonarDistance: 500, RadarJam: 80, SonarJam: 90,
	}}
	s.SensorTick(0, 2, nil, units)

	for i := range before {
		if s.wordMask[i] != before[i] {
			t.Fatalf("the sensor phase wrote the word mask at %d", i)
		}
	}
	// The snapshot carries status and identity, and nothing shaped like a
	// circle: the authored distances stay on the unit definition, where the
	// contacts pass reads them [03 §3.10].
	got := s.SensorInputs()
	if len(got) != 1 || got[0].ID != 1 || got[0].Status&FriendlyMask == 0 {
		t.Fatalf("sensor snapshot = %+v, want one own-unit record carrying the friendly pair", got)
	}
}

// TestSensorEmissionRequiresTheActivationBit locks the emitter-side activation
// gate of [R-VIS-01 §4] pass 2: an inactive emitter queries no contact
// callback, so it marks nothing seen, and the same emitter after its activation
// edge detects the enemy standing inside its authored radar distance.
func TestSensorEmissionRequiresTheActivationBit(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 128, CellH: 128}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(1)
	var mine, theirs uint32
	units := []SensorUnit{
		{ID: 1, Owner: 1, Status: &mine, Alive: true, Active: false,
			X: tileWorld(2), Z: tileWorld(2), RadarDistance: 900},
		{ID: 2, Owner: 0, Status: &theirs, Alive: true, Hidden: true,
			X: tileWorld(6), Z: tileWorld(6)},
	}
	s.SensorTick(4, 2, nil, units)
	if theirs&SeenBit != 0 {
		t.Fatalf("an INACTIVE emitter detected an enemy: status %#x [R-VIS-01 §4] pass 2", theirs)
	}
	units[0].Active = true
	s.SensorTick(5, 2, nil, units)
	if theirs&SeenBit == 0 {
		t.Fatalf("an ACTIVE emitter missed an enemy inside its authored range: status %#x", theirs)
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
// exemption [03 §3.2] C8 step 3, [03 §3.4] — and locks WHO gets that pair.
//
// Correction (2026-08-30). This test previously fed an allied predicate and
// asserted "allied unit status lacks the friendly mask" against an ally of the
// local player, i.e. that the friendly pass marks allies. [R-VIS-01 §7]
// establishes the opposite: pass 1's allied disjunct gates on an option-word
// bit that no writer anywhere in the image sets, so allies get nothing from
// this pass — an ally's radar contact never reaches the viewer's minimap and an
// ally's submerged units are not exempted on the viewer's behalf. The friendly
// pair goes to own units, and to everything only when the viewer is defeated
// [R-VIS-01 §4] pass 1.
func TestFriendlyMarkingExemptsUnderwater(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128, SeaLevel: 20}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 10, 10, 0, 320)

	var mine, allies, theirs uint32
	allied := func(a, b PlayerID) bool { return a == 0 && b == 1 }
	units := []SensorUnit{
		{Owner: 0, Status: &mine, Alive: true},   // the viewing player's own unit
		{Owner: 1, Status: &allies, Alive: true}, // allied with the local player
		{Owner: 2, Status: &theirs, Alive: true}, // not allied
	}
	s.SensorTick(0, 3, allied, units)

	// 0x300 is 0x100|0x200 and the seen pass also sets 0x100, so the
	// discriminating bit is the upper one — which is exactly the underwater
	// exemption [03 §3.4].
	if mine&FriendlyMask != FriendlyMask {
		t.Fatalf("own unit status %#x lacks the friendly mask", mine)
	}
	if allies&underwaterExempt != 0 {
		t.Fatalf("allied unit status %#x carries the exemption bit [R-VIS-01 §7]", allies)
	}
	if theirs&underwaterExempt != 0 {
		t.Fatalf("enemy unit status %#x carries the exemption bit", theirs)
	}
	// A submerged own unit is exempt from the sea-level rejection.
	sub := Target{
		Owner: 0, Status: mine,
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
// the decloak deadline (tick+90) and the decloak bit into the cloaked unit
// itself when an enemy is within mincloakdistance [03 §3.4][R-VIS-01 §4] pass 4.
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
