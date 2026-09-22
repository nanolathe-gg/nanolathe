package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// repeatClickFixture builds a session with one ground builder and two products.
func repeatClickFixture(t *testing.T) (*Session, pool.Handle) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	for _, name := range []string{"solar", "mex"} {
		d := &content.UnitDef{UnitName: name, MaxDamage: 10}
		d.CanonicalKey = name
		cat.Units[name] = d
	}
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, MaxDamage: 10}
	bdef.CanonicalKey = "builder"
	cat.Units[bdef.CanonicalKey] = bdef
	w := newSessionFixtureWorld(8, cat)
	hb, _ := w.Create(bdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	return s, hb
}

func queueBuild(s *Session, hb pool.Handle, product string, wx, wz numeric.Fixed, queued bool, tick uint32) {
	s.applyHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
		Builder: hb, Product: product, WX: wx, WZ: wz, WY: 7 << 16, Queued: queued,
	}}, tick)
}

const cell = numeric.Fixed(16 << 16)

// A queued click on a point that already carries a queued build of the same
// kind removes that build and issues nothing [07 R-P0-11 §6]. Before this
// contract landed the repeat click coalesced into the tail node and queued a
// second building (defect PT3-12).
func TestQueuedRepeatClickRemovesTheQueuedBuild(t *testing.T) {
	s, hb := repeatClickFixture(t)
	site := numeric.Fixed(4) * cell
	queueBuild(s, hb, "solar", site, site, true, 1)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	if q == nil || q.LenPrimary() != 1 {
		t.Fatalf("first queued click produced %v nodes, want 1", q)
	}
	queueBuild(s, hb, "solar", site, site, true, 2)
	if q.LenPrimary() != 0 {
		t.Fatalf("the repeat click left %d nodes, want the queued build removed", q.LenPrimary())
	}
	// A third click at the same point queues again: the toggle has no memory.
	queueBuild(s, hb, "solar", site, site, true, 3)
	if q.LenPrimary() != 1 {
		t.Fatalf("the third click left %d nodes, want 1", q.LenPrimary())
	}
}

// The product identity is deliberately not part of the match: retail passes it
// in an argument the duplicate test never reads [07 R-P0-11 §6].
func TestQueuedRepeatClickIgnoresTheProduct(t *testing.T) {
	s, hb := repeatClickFixture(t)
	site := numeric.Fixed(4) * cell
	queueBuild(s, hb, "solar", site, site, true, 1)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	queueBuild(s, hb, "mex", site, site, true, 2) // different product, same point
	if q.LenPrimary() != 0 {
		t.Fatalf("a repeat click carrying a different product left %d nodes; the product is not part of the match", q.LenPrimary())
	}
}

// The tolerance is one whole map cell, inclusive, on each axis independently —
// a square, not a radius, and not an exact site match. Y never participates.
func TestQueuedRepeatClickTolerance(t *testing.T) {
	site := numeric.Fixed(4) * cell
	cases := []struct {
		name       string
		dx, dz     numeric.Fixed
		wantRemove bool
	}{
		{"exact", 0, 0, true},
		{"one cell on X", cell, 0, true},
		{"one cell on Z", 0, -cell, true},
		{"one cell on both axes", cell, cell, true}, // a square: the diagonal is in
		{"just past one cell on X", cell + 1, 0, false},
		{"just past one cell on Z", 0, cell + 1, false},
		{"one cell on X, past it on Z", cell, cell + 1, false}, // axes are independent
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, hb := repeatClickFixture(t)
			queueBuild(s, hb, "solar", site, site, true, 1)
			q := orders.QueueForUnit(s.Units.Unit(hb))
			queueBuild(s, hb, "solar", site+tc.dx, site+tc.dz, true, 2)
			want := 2 // out of tolerance: the second click queues its own build
			if tc.wantRemove {
				want = 0 // in tolerance: the queued build goes and nothing is issued
			}
			if got := q.LenPrimary(); got != want {
				t.Fatalf("offset (%d,%d) left %d nodes, want %d", tc.dx, tc.dz, got, want)
			}
		})
	}
}

// Y is not compared: the same X/Z with a different site height still matches.
func TestQueuedRepeatClickIgnoresSiteHeight(t *testing.T) {
	s, hb := repeatClickFixture(t)
	site := numeric.Fixed(4) * cell
	queueBuild(s, hb, "solar", site, site, true, 1)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	s.applyHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
		Builder: hb, Product: "solar", WX: site, WZ: site, WY: 900 << 16, Queued: true,
	}}, 2)
	if q.LenPrimary() != 0 {
		t.Fatalf("a repeat click at a different site height left %d nodes; Y is not part of the match", q.LenPrimary())
	}
}

// The duplicate test runs only in queued mode. A plain click purges and issues
// as it always did, so clicking the same site twice without Shift leaves one
// build queued rather than none.
func TestUnqueuedRepeatClickStillIssues(t *testing.T) {
	s, hb := repeatClickFixture(t)
	site := numeric.Fixed(4) * cell
	queueBuild(s, hb, "solar", site, site, false, 1)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	if q == nil || q.LenPrimary() != 1 {
		t.Fatalf("first plain click produced %d nodes, want 1", q.LenPrimary())
	}
	queueBuild(s, hb, "solar", site, site, false, 2)
	if q.LenPrimary() != 1 {
		t.Fatalf("a plain repeat click left %d nodes, want 1", q.LenPrimary())
	}
}

// The removal takes the front-most match, so the oldest queued build at a
// point goes first and later ones at other points are untouched.
func TestQueuedRepeatClickTakesTheFrontMost(t *testing.T) {
	s, hb := repeatClickFixture(t)
	a := numeric.Fixed(4) * cell
	b := numeric.Fixed(20) * cell
	queueBuild(s, hb, "solar", a, a, true, 1)
	queueBuild(s, hb, "solar", b, b, true, 2)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	if q.LenPrimary() != 2 {
		t.Fatalf("two distinct sites produced %d nodes, want 2", q.LenPrimary())
	}
	queueBuild(s, hb, "solar", a, a, true, 3)
	if q.LenPrimary() != 1 {
		t.Fatalf("the repeat click left %d nodes, want 1", q.LenPrimary())
	}
	if got := q.Head().GoalX; got != b {
		t.Fatalf("the repeat click removed the wrong node: head goal %d, want %d", got, b)
	}
}

// The modern shortcut requests append-only command intent; an accidental
// repeated site must not invoke the ordinary Shift-click removal gesture.
func TestAppendOnlyMobileBuildPreservesRepeatedSite(t *testing.T) {
	s, hb := repeatClickFixture(t)
	site := numeric.Fixed(4) * cell
	queueBuild(s, hb, "solar", site, site, true, 1)
	s.applyHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
		Builder: hb, Product: "solar", WX: site, WZ: site, Queued: true, AppendOnly: true,
	}}, 2)
	q := orders.QueueForUnit(s.Units.Unit(hb))
	if q.LenPrimary() != 1 || q.Head().Param2 != 0 {
		t.Fatalf("append-only click canceled existing work: %+v", q.Primary())
	}
}
