package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestSensorTickRequiresTwoPlayers locks C12's outermost gate. The phase's
// only observable output is the status bits it writes plus the per-unit
// snapshot it publishes, so the snapshot is what tells the two apart
// [R-VIS-01 §4] "Gate".
func TestSensorTickRequiresTwoPlayers(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var status uint32
	units := []SensorUnit{{ID: 1, Owner: 0, Status: &status, Alive: true, Active: true, RadarDistance: 100}}

	s.SensorTick(0, 1, units)
	if len(s.SensorInputs()) != 0 || status != 0 {
		t.Fatalf("the sensor phase ran with a single player: inputs %d, status %#x", len(s.SensorInputs()), status)
	}
	s.SensorTick(0, 2, units)
	if len(s.SensorInputs()) != 1 || status&FriendlyMask == 0 {
		t.Fatalf("the sensor phase did not run with two players: inputs %d, status %#x", len(s.SensorInputs()), status)
	}
	if got, ok := s.SensorStatus(1); !ok || got != status {
		t.Fatalf("sensor status lookup = %#x,%v want %#x,true", got, ok, status)
	}
	s.SensorTick(1, 1, units)
	if _, ok := s.SensorStatus(1); ok {
		t.Fatal("single-player skipped pass retained a stale completed sensor status")
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
	s.SensorTick(0, 2, units)

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
	s.SensorTick(4, 2, units)
	if theirs&SeenBit != 0 {
		t.Fatalf("an INACTIVE emitter detected an enemy: status %#x [R-VIS-01 §4] pass 2", theirs)
	}
	units[0].Active = true
	s.SensorTick(5, 2, units)
	if theirs&SeenBit == 0 {
		t.Fatalf("an ACTIVE emitter missed an enemy inside its authored range: status %#x", theirs)
	}
}

// TestSensorEmissionExcludesDeathLatchedEmitters locks the emitter-side gate
// added by WU-19-215: pass 2 admits an emitter only if it is alive, own,
// active, AND not death-latched [R-VIS-01 §4] pass 2 ("not death-latched").
// SensorUnit.Dying already gated pass 4's proximity source; this is the same
// bit doing the same job for the radar/sonar emitter.
func TestSensorEmissionExcludesDeathLatchedEmitters(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 128, CellH: 128}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(1)
	var mine, theirs uint32
	units := []SensorUnit{
		{ID: 1, Owner: 1, Status: &mine, Alive: true, Active: true, Dying: true,
			X: tileWorld(2), Z: tileWorld(2), RadarDistance: 900},
		{ID: 2, Owner: 0, Status: &theirs, Alive: true, Hidden: true,
			X: tileWorld(6), Z: tileWorld(6)},
	}
	s.SensorTick(4, 2, units)
	if theirs&SeenBit != 0 {
		t.Fatalf("a DEATH-LATCHED emitter detected an enemy: status %#x [R-VIS-01 §4] pass 2", theirs)
	}
	units[0].Dying = false
	s.SensorTick(5, 2, units)
	if theirs&SeenBit == 0 {
		t.Fatalf("a LIVE emitter missed an enemy inside its authored range: status %#x", theirs)
	}
}

// TestSensorRadiusKeepsRawFractionBeforeSquaring locks the shared visitor
// metric and its two different boundary gates. A 1.5-by-1.5 raw separation
// contributes 2+2 after each square's high-word extraction; truncating both
// axes before multiplying would contribute 1+1 and incorrectly pass the
// strict radar callback at radius 2. At an exact radius, the visitor delivers
// the candidate but the radar callback still rejects it, while a jammer's
// inclusive visitor accepts it [R-VIS-01 §5].
func TestSensorRadiusKeepsRawFractionBeforeSquaring(t *testing.T) {
	newService := func() *Service {
		s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.SetLocal(0)
		return s
	}
	unit := func(id uint16, owner PlayerID, x, z numeric.Fixed, status *uint32) SensorUnit {
		return SensorUnit{ID: id, Owner: owner, Status: status, Alive: true, Active: true, X: x, Z: z}
	}

	if got := planarSquared(&SensorUnit{X: numeric.Fixed(3 << 15), Z: numeric.Fixed(3 << 15)}, &SensorUnit{}); got != 4 {
		t.Fatalf("raw 1.5-by-1.5 distance squared = %d, want 4", got)
	}

	var emitterStatus, targetStatus uint32
	s := newService()
	emitter := unit(1, 0, 0, 0, &emitterStatus)
	emitter.RadarDistance = 2
	target := unit(2, 1, numeric.Fixed(3<<15), numeric.Fixed(3<<15), &targetStatus)
	s.SensorTick(1, 2, []SensorUnit{emitter, target})
	if targetStatus&SeenBit != 0 {
		t.Fatalf("fractional target passed strict radius callback: status %#x", targetStatus)
	}

	// The same radius at an exact integral edge reaches a jammer because the
	// visitor's delivery check is inclusive, even though a contact callback is
	// strict at that edge.
	emitterStatus, targetStatus = 0, SeenBit
	s = newService()
	jammer := unit(1, 1, 0, 0, &emitterStatus)
	jammer.RadarJam = 2
	edge := unit(2, 0, numeric.Fixed(2<<16), 0, &targetStatus)
	s.SensorTick(2, 2, []SensorUnit{jammer, edge})
	if targetStatus&SeenBit != 0 || targetStatus&JammedBit == 0 {
		t.Fatalf("inclusive jammer edge status %#x, want seen clear and jammed set", targetStatus)
	}
}

// TestSensorFractionalRadiusCallbacksUseRawSquare exercises all three radius
// callbacks at the boundary that whole-coordinate truncation gets wrong. From
// 31.75 to 64 the raw-square metric is strictly inside radius 33, while a
// pre-square whole-word truncation turns it into the strict equality 33²
// [R-VIS-01 §5]. SensorTick owns no RNG, so each assertion observes only the
// status write from the named callback.
func TestSensorFractionalRadiusCallbacksUseRawSquare(t *testing.T) {
	const (
		sourceX = numeric.Fixed(31<<16 | 3<<14) // 31.75
		targetX = numeric.Fixed(64 << 16)
	)
	newService := func() *Service {
		s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.SetLocal(0)
		return s
	}

	var sourceStatus, targetStatus uint32
	s := newService()
	radar := SensorUnit{ID: 1, Owner: 0, Status: &sourceStatus, Alive: true, Active: true, X: sourceX, RadarDistance: 33}
	target := SensorUnit{ID: 2, Owner: 1, Status: &targetStatus, Alive: true, X: targetX}
	s.SensorTick(1, 2, []SensorUnit{radar, target})
	if targetStatus&SeenBit == 0 {
		t.Fatalf("fractional strict-inside radar target status %#x lacks seen", targetStatus)
	}
	if got, ok := s.SensorStatus(2); !ok || got != targetStatus {
		t.Fatalf("published fractional radar status = %#x,%v want %#x,true", got, ok, targetStatus)
	}

	sourceStatus, targetStatus = 0, 0
	s = newService()
	cloak := SensorUnit{ID: 1, Owner: 1, Status: &sourceStatus, Alive: true, CanCloak: true, OwnerLocallySimulated: true, X: sourceX, MinCloakDistance: 33, DecloakDeadline: new(uint32)}
	listed := SensorUnit{ID: 2, Owner: 0, Status: &targetStatus, Alive: true, X: targetX, PrimaryCandidateOf: 1 << 1}
	s.SensorTick(2, 2, []SensorUnit{cloak, listed})
	if sourceStatus&DecloakBit == 0 || *cloak.DecloakDeadline != 2+DecloakDeadlineAdd {
		t.Fatalf("fractional strict-inside proximity status=%#x deadline=%d", sourceStatus, *cloak.DecloakDeadline)
	}

	sourceStatus, targetStatus = 0, SeenBit
	s = newService()
	jammer := SensorUnit{ID: 1, Owner: 1, Status: &sourceStatus, Alive: true, Active: true, X: sourceX, RadarJam: 33}
	jammed := SensorUnit{ID: 2, Owner: 0, Status: &targetStatus, Alive: true, X: targetX}
	s.SensorTick(3, 2, []SensorUnit{jammer, jammed})
	if targetStatus&SeenBit != 0 || targetStatus&JammedBit == 0 {
		t.Fatalf("fractional strict-inside jammer status %#x, want seen clear and jammed", targetStatus)
	}
}

func TestSensorSeenBitClearsAtFrameStart(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var status uint32 = SeenBit
	units := []SensorUnit{{ID: 1, Owner: 1, Status: &status, Alive: true, X: tileWorld(10), Z: tileWorld(10)}}
	s.SensorTick(1, 2, units)
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
	units := []SensorUnit{
		{Owner: 0, Status: &mine, Alive: true},   // the viewing player's own unit
		{Owner: 1, Status: &allies, Alive: true}, // allied with the local player
		{Owner: 2, Status: &theirs, Alive: true}, // not allied
	}
	// No alliance row is fed at all: the phase takes none (WU-19-210), because
	// the only pass that consulted one now reads the per-side primary candidate
	// lists instead [06 §3.1].
	s.SensorTick(0, 3, units)

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

// TestProximityDecloak locks C10/C12: a cloak-capable unit's proximity search
// writes the decloak deadline (tick+90) and the decloak bit into that unit
// itself when a PRIMARY CANDIDATE of its own side is within mincloakdistance
// [03 §3.4][R-VIS-01 §4] pass 4.
//
// Correction (WU-19-210). The source used to be gated on the instance cloak bit
// and the candidate set used to be every live hostile. The section gates the
// source on the derived can-cloak flag and an active controller of type 1 or 2,
// does not test current cloak at all, and searches only the source owner's
// primary candidate list of [06 §3.1].
func TestProximityDecloak(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	var cloakedStatus, nearStatus, farStatus uint32
	var cloakedDeadline uint32
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }

	units := []SensorUnit{
		{Owner: 1, Status: &cloakedStatus, Alive: true, Hidden: true,
			CanCloak: true, OwnerLocallySimulated: true,
			X: px(100), Z: px(100), MinCloakDistance: 50, DecloakDeadline: &cloakedDeadline},
		{Owner: 0, Status: &nearStatus, Alive: true, PrimaryCandidateOf: 1 << 1, X: px(120), Z: px(100)},
		{Owner: 0, Status: &farStatus, Alive: true, PrimaryCandidateOf: 1 << 1, X: px(300), Z: px(100)},
	}
	s.SensorTick(1000, 2, units)

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
	s.SensorTick(1000+DecloakDeadlineAdd+1, 2, units)
	if cloakedStatus&DecloakBit != 0 {
		t.Fatal("decloak bit should clear after GT>=deadline and no longer within range [03 §3.4] P0-11")
	}
}

// TestProximityIgnoresEnemiesOffThePrimaryList is the R14 regression: the
// candidate set of pass 4 is the SOURCE OWNER's primary candidate list of
// [06 §3.1], so a hostile that never entered that list cannot move the
// suppression deadline however close it stands [R-VIS-01 §4] pass 4.
//
// The review's probe: with empty visibility grids an invisible cloaked enemy
// ten world units from a cloaked source still moved the source's deadline. Such
// an enemy fails the rebuild's cloak clause every time, so it can never be a
// candidate, and repeating the scan every tick let it suppress cloak forever.
func TestProximityIgnoresEnemiesOffThePrimaryList(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }
	var srcStatus, enemyStatus, listedStatus uint32
	var deadline uint32

	units := []SensorUnit{
		{Owner: 1, Status: &srcStatus, Alive: true, Hidden: true, CanCloak: true,
			OwnerLocallySimulated: true, X: px(100), Z: px(100), MinCloakDistance: 50,
			DecloakDeadline: &deadline},
		// A live hostile ten units away that is on nobody's list.
		{Owner: 0, Status: &enemyStatus, Alive: true, X: px(110), Z: px(100)},
		// A live hostile on ANOTHER side's list — slot 0's, not the source's.
		{Owner: 0, Status: &listedStatus, Alive: true, PrimaryCandidateOf: 1 << 0,
			X: px(112), Z: px(100)},
	}
	s.SensorTick(7, 2, units)
	if deadline != 0 || srcStatus&DecloakBit != 0 {
		t.Fatalf("an enemy off the source owner's primary list breached: deadline %d status %#x [R-VIS-01 §4] pass 4", deadline, srcStatus)
	}

	// The same enemy, now filed on the source owner's list, does breach.
	units[1].PrimaryCandidateOf = 1 << 1
	s.SensorTick(7, 2, units)
	if deadline != 7+DecloakDeadlineAdd || srcStatus&DecloakBit == 0 {
		t.Fatalf("a listed enemy inside mincloakdistance must breach: deadline %d status %#x", deadline, srcStatus)
	}
}

// TestProximityCandidateLivenessIsRetestedAtUse locks the half of the staleness
// contract that is not the list's: the list is up to thirty ticks old, so the
// pass re-tests the candidate's alive bit and death latch at use, exactly as the
// per-attempt acquisition filter does [06 §3.1][R-VIS-01 §4] pass 4.
func TestProximityCandidateLivenessIsRetestedAtUse(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }
	var srcStatus, candStatus uint32
	var deadline uint32
	units := []SensorUnit{
		{Owner: 1, Status: &srcStatus, Alive: true, Hidden: true, CanCloak: true,
			OwnerLocallySimulated: true, X: px(100), Z: px(100), MinCloakDistance: 50,
			DecloakDeadline: &deadline},
		{Owner: 0, Status: &candStatus, Alive: true, PrimaryCandidateOf: 1 << 1,
			X: px(110), Z: px(100)},
	}

	units[1].Dying = true // still listed, but death-latched
	s.SensorTick(10, 2, units)
	if deadline != 0 {
		t.Fatalf("a death-latched candidate breached: deadline %d [06 §3.1]", deadline)
	}
	units[1].Dying, units[1].Alive = false, false
	s.SensorTick(11, 2, units)
	if deadline != 0 {
		t.Fatalf("a dead candidate breached: deadline %d [06 §3.1]", deadline)
	}
	units[1].Alive = true
	s.SensorTick(12, 2, units)
	if deadline != 12+DecloakDeadlineAdd {
		t.Fatalf("a live listed candidate must breach: deadline %d", deadline)
	}
}

// TestProximitySourceGates locks pass 4's three source gates: the derived
// can-cloak flag (`cloakcost > 0`), an owning player record that is active with
// controller type 1 or 2, and liveness — and locks what is NOT a gate: "the pass
// does not test whether the unit is currently cloaked" [R-VIS-01 §4] pass 4.
//
// The uncloaked case is the one the old implementation could not express: it
// required the instance cloak bit, so a cloak-capable unit that had not yet paid
// its cloak debit could never accumulate a suppression window.
func TestProximitySourceGates(t *testing.T) {
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }
	run := func(t *testing.T, src SensorUnit) uint32 {
		t.Helper()
		s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
		var srcStatus, candStatus uint32
		var deadline uint32
		src.Status = &srcStatus
		src.DecloakDeadline = &deadline
		src.Owner = 1
		src.X, src.Z = px(100), px(100)
		src.MinCloakDistance = 50
		units := []SensorUnit{src, {Owner: 0, Status: &candStatus, Alive: true,
			PrimaryCandidateOf: 1 << 1, X: px(110), Z: px(100)}}
		s.SensorTick(50, 2, units)
		return deadline
	}

	if got := run(t, SensorUnit{Alive: true, Hidden: false, CanCloak: true, OwnerLocallySimulated: true}); got != 50+DecloakDeadlineAdd {
		t.Fatalf("an eligible but currently UNCLOAKED source must receive the suppression deadline, got %d [R-VIS-01 §4] pass 4", got)
	}
	if got := run(t, SensorUnit{Alive: true, Hidden: true, CanCloak: false, OwnerLocallySimulated: true}); got != 0 {
		t.Fatalf("a source whose definition has no cloakcost is not a proximity source, got %d", got)
	}
	if got := run(t, SensorUnit{Alive: true, Hidden: true, CanCloak: true, OwnerLocallySimulated: false}); got != 0 {
		t.Fatalf("a source whose owner is not an active controller of type 1 or 2 is skipped, got %d [06 R-WPN-02 §2]", got)
	}
	if got := run(t, SensorUnit{Alive: false, Hidden: true, CanCloak: true, OwnerLocallySimulated: true}); got != 0 {
		t.Fatalf("a dead source is skipped, got %d", got)
	}
}

// TestProximityDecloakUsesTheDerivedRadius is the consequence half of the
// `mincloakdistance` derived default: a cloak-capable definition that omits the
// key compiles to 80, not 0, so pass 4 breaches at 80 world units and only
// there. With the old compiled 0 the `d² <= 0` test never fired and such a unit
// never decloaked [03 R-VIS-01 §4] pass 4 [02 "Unit record"].
func TestProximityDecloakUsesTheDerivedRadius(t *testing.T) {
	px := func(p int64) numeric.Fixed { return numeric.Fixed(p * 65536) }
	for _, tc := range []struct {
		name       string
		radius     int32
		enemyX     int64
		wantBreach bool
	}{
		{"derived radius breaches at 80", 80, 180, true},
		{"derived radius does not breach at 81", 80, 181, false},
		{"a compiled zero does not breach at one unit", 0, 101, false},
	} {
		s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
		var cloakedStatus, enemyStatus uint32
		var cloakedDeadline uint32
		units := []SensorUnit{
			{Owner: 1, Status: &cloakedStatus, Alive: true,
				CanCloak: true, OwnerLocallySimulated: true,
				X: px(100), Z: px(100), MinCloakDistance: tc.radius, DecloakDeadline: &cloakedDeadline},
			{Owner: 0, Status: &enemyStatus, Alive: true, PrimaryCandidateOf: 1 << 1, X: px(tc.enemyX), Z: px(100)},
		}
		s.SensorTick(1000, 2, units)
		if got := cloakedStatus&DecloakBit != 0; got != tc.wantBreach {
			t.Fatalf("%s: decloak bit %v, want %v", tc.name, got, tc.wantBreach)
		}
	}
}
