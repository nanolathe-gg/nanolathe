package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestStealthSuppressesRadarButNotLineOfSight locks the one thing the
// definition's `stealth` flag does and the one thing it must not do.
//
// [03 R-VIS-01 §5]: `stealth` is the contact callback's THIRD reject, taken
// before any distance or elevation term, so it suppresses radar and sonar
// detection outright — a stealth unit sitting well inside an enemy radar is
// never given the seen bit by that dish. It is not a range reduction and it is
// not a cloak: pass 5's seen probe and the direct-visibility predicate of
// [03 §3.2] read the instance cloaked bit alone [03 R-VIS-01 §6], so a stealth
// unit standing on a lit tile is seen exactly like any other unit.
//
// The regression this locks: `stealth` was ORed into the `hidden` input of the
// session's own visibility predicate and of the published contact, which made
// every stealth unit invisible to the eye as well as to the dish — a stealth
// scout could stand in front of a commander unseen and unshootable.
func TestStealthSuppressesRadarButNotLineOfSight(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1))

	base := s.Catalog.Units[content.CanonicalKey("armcom")]
	// A private copy so raising `stealth` on the candidate cannot reach the
	// viewer's own unit through a shared definition record.
	stealthDef := *base
	stealthDef.Stealth = true

	ownH, err := s.Units.Create(base, 1, cell(128), 0, cell(128))
	if err != nil {
		t.Fatalf("create local unit: %v", err)
	}
	own := s.Units.Unit(ownH)
	// The sensor phase's emitters run only for an ACTIVE unit
	// [03 R-VIS-01 §4] pass 2, and the bit is raised only through the shared
	// edge setter [04 R-UNIT-06 §2].
	own.SetActivationEdge(true)
	// 900 squared is 810000, strictly greater than the 655360 separation below,
	// so the radar candidate is examined [03 R-VIS-01 §5].
	own.Def.RadarDistance = 900
	defer func() { own.Def.RadarDistance = 0 }()

	// Outside the observer's 5x5 sight shape, inside the radar.
	radarH, err := s.Units.Create(&stealthDef, 0, cell(896), 0, cell(384))
	if err != nil {
		t.Fatalf("create radar candidate: %v", err)
	}
	// Inside the sight shape, which spans two coverage tiles either side of the
	// stamp cell at 32 world units per tile.
	losH, err := s.Units.Create(&stealthDef, 0, cell(160), 0, cell(160))
	if err != nil {
		t.Fatalf("create line-of-sight candidate: %v", err)
	}
	publishVisibilityForAll(s)
	s.stepAuthoritativePhases(1)
	s.publishSnapshot(1)

	if s.Vis.VisiblePoint(visibility.PlayerID(1), cell(896), 0, cell(384)) {
		t.Fatal("precondition: the radar candidate's cell is also lit by line of sight")
	}

	f := s.Snapshot.Current()
	rc, ok := radarContactFor(f, radarH)
	if !ok {
		t.Fatal("radar candidate publishes no unit contact")
	}
	if rc.Status&visibility.SeenBit != 0 {
		t.Fatalf("a stealth unit inside enemy radar gained the seen bit (status %#x): stealth is the contact callback's third reject [03 R-VIS-01 §5]", rc.Status)
	}

	lc, ok := radarContactFor(f, losH)
	if !ok {
		t.Fatal("line-of-sight candidate publishes no unit contact")
	}
	if lc.Status&visibility.SeenBit == 0 {
		t.Fatalf("a stealth unit standing on a lit tile lacks the seen bit (status %#x): stealth never touches line of sight [03 R-VIS-01 §5][03 R-VIS-01 §4] pass 5", lc.Status)
	}
	if lc.Hidden {
		t.Fatal("the published contact's cloak input carries definition stealth: the input is the INSTANCE cloaked bit alone [03 R-VIS-01 §6]")
	}
	if !lc.Stealth {
		t.Fatal("the published contact dropped the stealth flag, which the minimap blink gate reads on its own [03 §3.9]")
	}

	// The session's own visibility predicate agrees: stealth is not a cloak.
	if !s.IsUnitVisible(1, s.Units.Unit(losH)) {
		t.Fatal("a stealth unit inside line of sight failed the direct-visibility predicate [03 §3.2][03 R-VIS-01 §5]")
	}
	// And the instance cloaked bit — the predicate's only cloak input — still
	// hides it [03 R-VIS-01 §6].
	s.Units.Unit(losH).Hidden = true
	s.visStatus[int(losH)] = 0
	if s.IsUnitVisible(1, s.Units.Unit(losH)) {
		t.Fatal("the instance cloaked bit no longer hides a unit from the direct-visibility predicate [03 §3.2] step 2")
	}
}
