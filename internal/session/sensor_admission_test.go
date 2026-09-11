package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// minimapBlipAdmitted is [03 §3.9]'s blip gate evaluated on a committed
// contact. Retail admits a unit blip when any of four disjuncts holds: a global
// options word bit, the minimap mode word's low two bits being zero, the unit
// carrying a bit of the 0x300 status mask, or the unit's owner being the local
// player. The first two are off in an ordinary battle, so what remains is the
// status mask — which carries the seen marker 0x100, the way a radar or
// line-of-sight contact reaches the minimap — and owner identity.
func minimapBlipAdmitted(c frame.RadarContactView, local uint8) bool {
	return c.Status&visibility.FriendlyMask != 0 || (c.OwnerKnown && c.Owner == local)
}

func radarContactFor(f *frame.Frame, h pool.Handle) (frame.RadarContactView, bool) {
	for _, c := range f.Radar.Contacts {
		if c.Kind == frame.RadarContactUnit && c.Handle == h {
			return c, true
		}
	}
	return frame.RadarContactView{}, false
}

// TestEnemyMinimapAdmissionRequiresSensorCoverage locks the admission
// relationship the minimap contacts pass depends on: an enemy unit reaches the
// local player's minimap only through an actual sensor or line-of-sight
// admission, never by default.
//
// Three states of one enemy unit at one fixed position are compared. Outside
// every sensor and outside line of sight it is not admitted; with the viewer's
// own radar reaching it, it is; with that radar shortened below the separation,
// it is not again. Owning units are admitted throughout.
//
// The regression this locks: the friendly pass used to mark a unit friendly
// when the session's alliance row called it allied, and a default lobby leaves
// every slot at the unassigned sentinel ally group, so every enemy came out
// carrying 0x300 and every enemy blip was admitted. [R-VIS-01 §7] establishes
// that pass 1's allied disjunct cannot fire at all — the option bit it gates on
// has no writer anywhere — so the friendly pair goes to own units only, and to
// everything only when the viewer has been defeated [R-VIS-01 §4] pass 1.
func TestEnemyMinimapAdmissionRequiresSensorCoverage(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1)) // the viewing slot, as composition binds it
	// A default skirmish lobby: two slots, both left at the unassigned
	// sentinel ally group [08 "Skirmish configuration"].
	s.Skirmish.NumPlayers = 2
	s.Skirmish.Players[0].AllyGroup = SkirmishDefaultAllyGroup
	s.Skirmish.Players[1].AllyGroup = SkirmishDefaultAllyGroup

	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	ownH, err := s.Units.Create(def, 1, cell(128), 0, cell(128))
	if err != nil {
		t.Fatalf("create local unit: %v", err)
	}
	// Far enough that the observer's 5x5 sight shape cannot reach it: the
	// separation is 768 by 256 world units, so 655360 squared units apart.
	enemyH, err := s.Units.Create(def, 0, cell(896), 0, cell(384))
	if err != nil {
		t.Fatalf("create enemy: %v", err)
	}
	own := s.Units.Unit(ownH)
	// Every unit is created inactive and the activation bit is raised only
	// through the shared edge setter — at completion for a definition that
	// authors `activatewhenbuilt`, or by an Activate order [04 R-UNIT-06 §2].
	// Raise it the way production does rather than assigning the field, because
	// the emitters of pass 2 and the circle gate both test this bit
	// [R-VIS-01 §4][03 §3.4].
	own.SetActivationEdge(true)
	if !own.Activated {
		t.Fatal("the shared edge setter did not raise the activation bit")
	}
	publishVisibilityForAll(s)

	admitted := func(tick uint32) (enemy, mine bool) {
		t.Helper()
		s.stepAuthoritativePhases(tick)
		s.publishSnapshot(tick)
		f := s.Snapshot.Current()
		ec, ok := radarContactFor(f, enemyH)
		if !ok {
			t.Fatalf("tick %d: enemy publishes no unit contact at all", tick)
		}
		oc, ok := radarContactFor(f, ownH)
		if !ok {
			t.Fatalf("tick %d: own unit publishes no unit contact at all", tick)
		}
		return minimapBlipAdmitted(ec, s.LocalOwner), minimapBlipAdmitted(oc, s.LocalOwner)
	}

	// 1. No sensor, no line of sight: the enemy is not on the minimap; the
	//    viewer's own unit always is.
	if s.Vis.VisiblePoint(visibility.PlayerID(1), cell(896), 0, cell(384)) {
		t.Fatal("precondition: the enemy cell is already covered by line of sight")
	}
	enemy, mine := admitted(1)
	if enemy {
		t.Fatal("an enemy outside every sensor and outside line of sight was admitted to the minimap [03 §3.9][R-VIS-01 §4]")
	}
	if !mine {
		t.Fatal("the viewing player's own unit was not admitted to the minimap [03 §3.9]")
	}

	// 2. The viewer's own radar now reaches the enemy: 900 squared is 810000,
	//    strictly greater than the 655360 separation, and the search radius is
	//    the same authored distance so the candidate is examined [R-VIS-01 §5].
	own.Def.RadarDistance = 900
	if seen, _ := admitted(2); seen {
		t.Fatal("sensor status refreshed before the viewing deadline")
	}
	enemy, mine = admitted(30)
	if !enemy {
		t.Fatal("an enemy inside the viewer's radar range was not admitted to the minimap [R-VIS-01 §5]")
	}
	if !mine {
		t.Fatal("the viewing player's own unit stopped being admitted")
	}

	// 3. Shorten the radar below the separation — 700 squared is 490000, less
	//    than 655360 — and the same enemy at the same place drops off again.
	//    The admission tracks the sensor, not a latch.
	own.Def.RadarDistance = 700
	if seen, _ := admitted(31); !seen {
		t.Fatal("sensor status was cleared between due passes")
	}
	enemy, mine = admitted(60)
	if enemy {
		t.Fatal("an enemy outside the shortened radar range stayed admitted: the seen marker is not being cleared on each due pass [R-VIS-01 §4] pass 1")
	}
	if !mine {
		t.Fatal("the viewing player's own unit stopped being admitted")
	}
}

// TestEnemyInLineOfSightIsAdmittedWithoutRadar locks the other admission route:
// the seen probe of [R-VIS-01 §4] pass 5 sets the marker for any unit standing
// on a tile lit for the viewing player, with no sensor involved.
func TestEnemyInLineOfSightIsAdmittedWithoutRadar(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1))
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	if _, err := s.Units.Create(def, 1, cell(128), 0, cell(128)); err != nil {
		t.Fatalf("create local unit: %v", err)
	}
	// Inside the observer's 5x5 sight shape, which spans two coverage tiles
	// either side of the stamp cell (32 world units per tile).
	enemyH, err := s.Units.Create(def, 0, cell(160), 0, cell(160))
	if err != nil {
		t.Fatalf("create enemy: %v", err)
	}
	publishVisibilityForAll(s)
	s.stepAuthoritativePhases(1)
	s.publishSnapshot(1)

	c, ok := radarContactFor(s.Snapshot.Current(), enemyH)
	if !ok {
		t.Fatal("enemy publishes no unit contact")
	}
	if !c.Seen {
		t.Fatalf("enemy status %#x lacks the seen marker inside line of sight [R-VIS-01 §4] pass 5", c.Status)
	}
	if !minimapBlipAdmitted(c, s.LocalOwner) {
		t.Fatal("an enemy inside line of sight was not admitted to the minimap [03 §3.9]")
	}
	// Line of sight is not alliance: the pair's upper bit stays clear, so the
	// enemy gains no underwater exemption from being seen [R-VIS-01 §4].
	if c.Status&visibility.SonarBit != 0 {
		t.Fatalf("a merely seen enemy carries the sonar/underwater-exempt bit: %#x", c.Status)
	}
}

// TestRadarEmissionRequiresTheActivationBit locks the interaction between the
// activation edge and the sensor phase's emitters. "Active" in the sensor phase
// means the unit instance's activation/on-state bit is set: a live unit whose
// radar or sonar distance is nonzero queries its contact callback only after
// that test [03 §3.4][R-VIS-01 §4] pass 2.
//
// Every unit is created inactive and the bit is raised only through the shared
// edge setter [04 R-UNIT-06 §2] — at completion for a definition that authors
// `activatewhenbuilt`, or by an Activate order for an `onoffable` one. A radar
// tower that has not completed therefore detects nothing, and the same tower
// after its completion edge does.
//
// The minimap circle is a separate question and is asserted separately below:
// [03 §3.10]'s 2026-08-29 correction establishes the circles as presentation
// drawn by the contacts pass, so detection and the circle no longer share a
// producer. This test previously read the circle list as a proxy for the
// activation gate; it now reads the status bit the gate actually writes.
func TestRadarEmissionRequiresTheActivationBit(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1))
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.RadarDistance = 900

	towerH, err := s.Units.Create(def, 1, cell(128), 0, cell(128))
	if err != nil {
		t.Fatalf("create radar tower: %v", err)
	}
	enemyH, err := s.Units.Create(def, 0, cell(896), 0, cell(384))
	if err != nil {
		t.Fatalf("create enemy: %v", err)
	}
	tower := s.Units.Unit(towerH)
	if tower.Activated {
		t.Fatal("precondition: a freshly created unit is expected to be inactive")
	}
	publishVisibilityForAll(s)

	// Inactive: no contact query, so no detection.
	s.stepAuthoritativePhases(1)
	if got := s.Units.Unit(enemyH).Flags; got&visibility.SeenBit != 0 {
		t.Fatalf("an INACTIVE radar emitter detected an enemy: status %#x [03 §3.4]", got)
	}

	// The completion edge raises the bit through the one writer retail has.
	tower.SetActivationEdge(true)
	s.stepAuthoritativePhases(30)
	if got := s.Units.Unit(enemyH).Flags; got&visibility.SeenBit == 0 {
		t.Fatalf("an ACTIVE radar emitter did not detect an enemy inside its range: status %#x [R-VIS-01 §5]", got)
	}

	// Lowering the edge again withdraws detection.
	tower.SetActivationEdge(false)
	s.stepAuthoritativePhases(60)
	if got := s.Units.Unit(enemyH).Flags; got&visibility.SeenBit != 0 {
		t.Fatalf("a DEACTIVATED radar emitter kept detecting an enemy: status %#x", got)
	}
}

// TestMinimapCirclesOnlyForTheViewersSelectedUnits locks report 4's three
// observable outcomes at the frame boundary, where the sole circle producer now
// lives [03 §3.10] correction of 2026-08-29:
//
//  1. an enemy radar tower the viewer CAN see publishes no circle distances;
//  2. the viewer's own UNSELECTED tower publishes none;
//  3. the viewer's own SELECTED tower publishes exactly its authored ones.
//
// The gate is [03 §3.9] "Selected-unit circle gate correction" (Established):
// the selected/range-status bit must be set. Only the local player's own units
// can carry that bit, so outcome 1 holds however well the enemy is detected —
// which is what the older sensor-phase producer got wrong.
func TestMinimapCirclesOnlyForTheViewersSelectedUnits(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1))
	s.LocalOwner = 1
	s.ViewingOwner = 1
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.RadarDistance = 900

	mineH, err := s.Units.Create(def, 1, cell(128), 0, cell(128))
	if err != nil {
		t.Fatalf("create own tower: %v", err)
	}
	enemyH, err := s.Units.Create(def, 0, cell(256), 0, cell(256))
	if err != nil {
		t.Fatalf("create enemy tower: %v", err)
	}
	s.Units.Unit(mineH).SetActivationEdge(true)
	s.Units.Unit(enemyH).SetActivationEdge(true)
	publishVisibilityForAll(s)

	s.stepAuthoritativePhases(1)
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()

	enemy, ok := radarContactFor(cur, enemyH)
	if !ok {
		t.Fatal("the enemy tower published no contact")
	}
	if !enemy.Seen {
		t.Fatalf("precondition: this case needs a DETECTED enemy, status %#x", enemy.Status)
	}
	if enemy.RangeStatus || enemy.RadarDistance != 0 {
		t.Fatalf("a detected enemy published circle distances: %+v [03 §3.9]", enemy)
	}

	mine, ok := radarContactFor(cur, mineH)
	if !ok {
		t.Fatal("the viewer's own tower published no contact")
	}
	if mine.RangeStatus || mine.RadarDistance != 0 {
		t.Fatalf("the viewer's own UNSELECTED tower published circle distances: %+v [03 §3.9]", mine)
	}

	// Selecting it is the whole difference.
	s.Units.Unit(mineH).Flags |= 0x10 // the authoritative selected bit [07 §9]
	s.stepAuthoritativePhases(30)
	s.publishSnapshot(2)
	mine, ok = radarContactFor(s.Snapshot.Current(), mineH)
	if !ok {
		t.Fatal("the viewer's own tower published no contact after selection")
	}
	if !mine.RangeStatus || mine.RadarDistance != 900 {
		t.Fatalf("the viewer's own SELECTED tower published no circle: %+v [03 §3.9]", mine)
	}
	// Selection does not leak across owners.
	if enemy, _ := radarContactFor(s.Snapshot.Current(), enemyH); enemy.RangeStatus {
		t.Fatalf("the enemy tower gained the circle gate: %+v", enemy)
	}
}
