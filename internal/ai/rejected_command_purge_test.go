package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// seedPatrol parks one ordinary, unprotected primary record on a unit so the
// replacement purge has something to remove.
func seedPatrol(t *testing.T, m *Manager, u *units.Unit) {
	t.Helper()
	id := orders.Lookup("Patrol")
	if id == 0 {
		t.Fatal("Patrol descriptor missing from the table")
	}
	q := orders.BindQueueBinding(u, m.OrderBinding)
	if q == nil {
		t.Fatal("no queue for the seeded unit")
	}
	q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, 1, u.Handle, true))
	if len(q.Primary()) != 1 {
		t.Fatalf("seed left %d primary records, want 1", len(q.Primary()))
	}
}

// assertSentinelInserted checks that a rejected non-queued issue left exactly
// the reject sentinel standing: descriptor row 0, carrying the caption-pending
// arm only a non-queued insertion makes, and nothing else.
func assertSentinelInserted(t *testing.T, u *units.Unit, what string) {
	t.Helper()
	q := orders.QueueOfUnit(u)
	if q == nil {
		t.Fatalf("%s: no queue at all, want the inserted sentinel", what)
	}
	primary := q.Primary()
	if len(primary) != 1 {
		t.Fatalf("%s: %d primary records after the rejected issue, want exactly the sentinel [04 §3.4]", what, len(primary))
	}
	if primary[0].ID != 0 {
		t.Fatalf("%s: the surviving record is descriptor %d, want the reject sentinel 0", what, primary[0].ID)
	}
	if !primary[0].CaptionPending {
		t.Fatalf("%s: the sentinel was inserted queued; this issue is a replacement [04 R-ORD-01 §13]", what)
	}
}

// assertSentinelCompletes pumps once and checks the sentinel behaved the way
// descriptor row 0 does: freed on the pass that reaches it, with no simulation
// draw and no diagnostic [04 R-ORD-01 §12].
func assertSentinelCompletes(t *testing.T, u *units.Unit, sim *rng.Simulation, tick uint32, what string) {
	t.Helper()
	q := orders.QueueOfUnit(u)
	before := sim.Draws()
	q.Pump(u, tick)
	if n := len(q.Primary()); n != 0 {
		t.Fatalf("%s: %d primary records after the pump, want the sentinel completed and freed [04 R-ORD-01 §12]", what, n)
	}
	if got := sim.Draws() - before; got != 0 {
		t.Fatalf("%s: the sentinel's dispatch drew %d times, want 0", what, got)
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("%s: the sentinel's dispatch recorded %v, want no diagnostic", what, diags)
	}
}

// TestBroadcastPurgesAMemberWhoseCommandIsRejected locks the non-testing issue
// sites of [04 §3.4]. The computer player's group-order broadcast hands the
// resolver's result to the producer insertion without branching on the reject
// sentinel, and four of its six callers — wave gather, wave attack, regroup and
// the explore final leg — pass the non-queued modifier. Descriptor 0 carries no
// purge exception, so a member whose definition fails the issued command's
// capability gate has its unprotected front-segment records freed, receives the
// sentinel, and is idle again as soon as that record is pumped — instead of
// keeping the orders it had.
func TestBroadcastPurgesAMemberWhoseCommandIsRejected(t *testing.T) {
	mover := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "purge-mover"},
		UnitName:         "purge-mover", BMCode: 1, CanMove: true, CanPatrol: true, MaxDamage: 100,
	}
	// No can-move: command code 2's capability gate rejects, and the resolver
	// writes the sentinel [04 §3.4].
	rooted := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "purge-rooted"},
		UnitName:         "purge-rooted", BMCode: 1, CanPatrol: true, MaxDamage: 100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		mover.CanonicalKey:  mover,
		rooted.CanonicalKey: rooted,
	}}

	w := newAIFixtureWorld(4, cat)
	hMover, err := w.Create(mover, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	hRooted, err := w.Create(rooted, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sim := rng.NewSimulation(3)
	m := &Manager{Player: 0, RNG: &sim, OrderBinding: aiFixtureOrderBinding(cat, &sim)}
	for _, h := range []pool.Handle{hMover, hRooted} {
		u := w.Unit(h)
		u.Group = 2
		seedPatrol(t, m, u)
	}

	m.broadcastGroupOrder(w, 2, 2, 0, nil, numeric.FixedFromInt(300), 0, numeric.FixedFromInt(400), 12, 0)

	// The accepted member keeps exactly the record the broadcast issued: its
	// own seed was removed by the same replacement purge every non-queued issue
	// runs, which is the pre-existing behaviour and not what this test is about.
	accepted := orders.QueueOfUnit(w.Unit(hMover))
	if accepted == nil || len(accepted.Primary()) != 1 {
		t.Fatalf("accepted member holds %v, want exactly the issued record", accepted)
	}
	if accepted.Primary()[0].ID == 0 {
		t.Fatal("accepted member received the reject sentinel, not a resolved move")
	}
	// The rejected member is left holding only the sentinel.
	assertSentinelInserted(t, w.Unit(hRooted), "rejected broadcast member")
	// Neither the resolution nor the insertion draws.
	if got := sim.Draws(); got != 0 {
		t.Fatalf("broadcast drew %d times, want 0", got)
	}
	assertSentinelCompletes(t, w.Unit(hRooted), &sim, 13, "rejected broadcast member")
}

// TestQueuedBroadcastLeavesARejectedMemberAlone is the other half of the same
// rule: a queued issue skips the replacement purge, so the member's existing
// records survive and the sentinel is merely appended behind them [04 §3.4].
// Two of the six broadcast callers — the explore task's patrol legs — pass the
// queued modifier.
func TestQueuedBroadcastLeavesARejectedMemberAlone(t *testing.T) {
	rooted := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "queued-rooted"},
		UnitName:         "queued-rooted", BMCode: 1, CanPatrol: true, MaxDamage: 100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{rooted.CanonicalKey: rooted}}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(rooted, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sim := rng.NewSimulation(3)
	m := &Manager{Player: 0, RNG: &sim, OrderBinding: aiFixtureOrderBinding(cat, &sim)}
	u := w.Unit(h)
	u.Group = 8
	seedPatrol(t, m, u)
	seeded := orders.QueueOfUnit(u).Primary()[0]

	m.broadcastGroupOrder(w, 8, 2, 1, nil, numeric.FixedFromInt(300), 0, numeric.FixedFromInt(400), 12, 0)

	primary := orders.QueueOfUnit(u).Primary()
	if len(primary) != 2 {
		t.Fatalf("queued reject left %d primary records, want the seeded one plus the appended sentinel", len(primary))
	}
	if primary[0] != seeded {
		t.Fatal("the queued reject displaced the member's existing record; a queued issue does not purge")
	}
	if primary[1].ID != 0 {
		t.Fatalf("the appended record is descriptor %d, want the reject sentinel 0", primary[1].ID)
	}
	if primary[1].CaptionPending {
		t.Fatal("the appended sentinel armed caption-pending; only a non-queued issue does [04 R-ORD-01 §13]")
	}
}

// TestRallySkipsARejectedMemberWithoutPurging locks the one computer-player
// issue site that DOES branch on the resolver's result. A rally member that
// resolves to the sentinel is skipped outright — no insertion at all, and
// therefore no replacement purge and no sentinel record — so it keeps whatever
// it was doing [04 §3.4][08 R-AI-01 §7].
func TestRallySkipsARejectedMemberWithoutPurging(t *testing.T) {
	// can-attack passes the task's own member filter and the resolver's gate,
	// but the unit is not armed and is not a kamikaze, so command code 3 falls
	// to the reject [04 §3.4].
	unarmed := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rally-unarmed"},
		UnitName:         "rally-unarmed", BMCode: 1, CanAttack: true, CanPatrol: true, MaxDamage: 100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{unarmed.CanonicalKey: unarmed}}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(unarmed, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	sim := rng.NewSimulation(11)
	m := &Manager{
		Player:           0,
		RNG:              &sim,
		OrderBinding:     aiFixtureOrderBinding(cat, &sim),
		GroupRally:       []pool.Handle{h},
		rallyInitialized: true,
	}
	seedPatrol(t, m, u)

	m.doRally(5, w, nil)

	primary := orders.QueueOfUnit(u).Primary()
	if len(primary) != 1 || primary[0].ID == 0 {
		t.Fatalf("rally reject changed the member's queue to %v, want its one seeded record untouched [04 §3.4]", primary)
	}
}

// TestMobileBuildIsANonQueuedResolvedIssue locks the shape of the computer
// player's mobile build [04 §3.4][08 R-AI-01 §3]: it goes through command code
// 14 with the non-queued modifier, so the builder's unprotected front-segment
// records are freed before the build record is inserted. The earlier path was a
// queued coalescing tail append, which left them standing.
func TestMobileBuildIsANonQueuedResolvedIssue(t *testing.T) {
	m, w, builder, econ, _, submissions := constructionOrderGateFixture(t)
	// A record that pass one does not itself suppress: static gate bit 3 clear,
	// purge-survivor bit clear.
	seedPatrol(t, m, builder)

	// Pass one alone: the repositioning pass is a separate non-queued issue and
	// would confuse what removed the seeded record.
	m.constructionPlacePass(90, w, econ, m.Strategic.CenterX, m.Strategic.CenterZ, m.Strategic.BuildCapable)

	if *submissions != 1 {
		t.Fatalf("mobile builder submitted %d construction requests, want 1", *submissions)
	}
	// The fixture's typed sink counts rather than inserting, so what is left is
	// exactly what the replacement purge removed.
	if q := orders.QueueOfUnit(builder); q != nil && len(q.Primary()) != 0 {
		t.Fatalf("builder kept %d primary records through a non-queued build issue, want 0 [04 §3.4]", len(q.Primary()))
	}
}

// TestMobileBuildRejectsABuilderWithNoLiveMover locks command code 14's second
// gate term. The arm requires a non-empty compiled build list AND a live mover,
// so a builder with no mover — an immobile one that reached the construction
// group — resolves the reject sentinel. The task does not test for it, so the
// non-queued insertion still purges, inserts the sentinel with pass one's own
// site and trailing pair, and leaves the builder idle with no build order once
// that record is pumped [04 §3.4][04 R-ORD-02 §1][04 R-ORD-01 §12].
func TestMobileBuildRejectsABuilderWithNoLiveMover(t *testing.T) {
	m, w, builder, econ, sim, submissions := constructionOrderGateFixture(t)
	builder.Flags |= units.BuildingClassStatus
	seedPatrol(t, m, builder)

	m.constructionPlacePass(90, w, econ, m.Strategic.CenterX, m.Strategic.CenterZ, m.Strategic.BuildCapable)

	if *submissions != 0 {
		t.Fatalf("builder with no live mover submitted %d construction requests, want 0: code 14 rejects [04 R-ORD-02 §1]", *submissions)
	}
	assertSentinelInserted(t, builder, "rejected mobile build")
	// The insertion's arguments do not change when the resolver rejects: the
	// count beside the argument word is still the literal one [08 R-AI-01 §3].
	if got := orders.QueueOfUnit(builder).Primary()[0].Param2; got != 1 {
		t.Fatalf("sentinel count word = %d, want 1", got)
	}
	assertSentinelCompletes(t, builder, sim, 91, "rejected mobile build")
}
