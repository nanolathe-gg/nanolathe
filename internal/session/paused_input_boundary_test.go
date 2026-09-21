package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// pausedInputCatalog authors two finished constructors and one product. The
// definitions are fixture data, not a second content source.
func pausedInputCatalog() *content.Catalog {
	builder := &content.UnitDef{UnitName: "fixbuilder", ObjectName: "fixbuilder", Builder: true, BMCode: 1,
		CanMove: true, MaxDamage: 100, BuildPageCount: 3, FootprintX: 2, FootprintZ: 2}
	builder.CanonicalKey = content.CanonicalKey(builder.UnitName)
	builder.DefinitionHeader.CanonicalKey = builder.CanonicalKey
	product := &content.UnitDef{UnitName: "fixsolar", ObjectName: "fixsolar", MaxDamage: 100,
		FootprintX: 2, FootprintZ: 2, YardMap: "oooo"}
	product.CanonicalKey = content.CanonicalKey(product.UnitName)
	product.DefinitionHeader.CanonicalKey = product.CanonicalKey
	return &content.Catalog{
		Units: map[string]*content.UnitDef{builder.CanonicalKey: builder, product.CanonicalKey: product},
		BuildMenus: map[string]*content.BuildMenuPage{builder.CanonicalKey: {
			Buttons: []string{"fixsolar", "fixsolar", "fixsolar", "fixsolar", "fixsolar", "fixsolar", "fixsolar"},
		}},
	}
}

// pausedInputFixture is a battle a host can step: two finished constructors
// owned by the local player, a clock at nominal speed, and a committed-frame
// buffer. Nothing here is paused yet.
func pausedInputFixture(t *testing.T) (s *Session, a, b *units.Unit) {
	t.Helper()
	cat := pausedInputCatalog()
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	uw := newSessionFixtureWorld(8, cat)
	def, ok := cat.Unit("fixbuilder")
	if !ok {
		t.Fatal("fixture catalog lost its builder")
	}
	ha, err := uw.Create(def, 0, numeric16(10), 0, numeric16(10))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := uw.Create(def, 0, numeric16(20), 0, numeric16(20))
	if err != nil {
		t.Fatal(err)
	}
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = 1
	econ.Players[0].Stock = [2]float32{5000, 5000}
	econ.Players[0].Capacity = [2]float32{5000, 5000}
	s = &Session{
		State:    StateBattle,
		Catalog:  cat,
		World:    terrain,
		Units:    uw,
		Econ:     econ,
		Snapshot: frame.NewBuffer(),
		Clock:    &clock.State{Active: 10, Requested: 10},
	}
	s.SeedSessionRNG(12345, 67890)
	a, b = uw.Unit(ha), uw.Unit(hb)
	for _, u := range []*units.Unit{a, b} {
		u.Health, u.MaxHealth, u.Remaining = 100, 100, 0
	}
	_ = orders.Lookup("Move_Ground")
	return s, a, b
}

// numeric16 is one map cell, sixteen world units, in 16.16.
func numeric16(cells int32) numeric.Fixed { return numeric.Fixed(cells) * 16 << 16 }

func pausedFrame(t *testing.T, s *Session) *frame.Frame {
	t.Helper()
	f := s.Snapshot.Current()
	if f == nil {
		t.Fatal("no committed frame")
	}
	return f
}

func selectionOf(f *frame.Frame) []pool.Handle { return f.Selection.Handles }

// A paused battle must still accept selection, page and command-panel input:
// the host frame keeps running with the options window closed
// [01 R-PLAT-01 §1 steps 2, 4, 5], so the queued command has to reach
// authoritative state and the committed frame without a tick
// (DESIGN_INTERFACE_HUD_INPUT §3.12).
func TestPausedInputSelectsAndPagesWithoutAdvancingTheTick(t *testing.T) {
	s, a, b := pausedInputFixture(t)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(1)
	if got := pausedFrame(t, s).CommandPage.Builder; got != a.Handle {
		t.Fatalf("first tick published builder %d, want A=%d", got, a.Handle)
	}

	s.SetPaused(true)
	tickBefore := s.Clock.GlobalTick
	simBefore, crtBefore := *s.SimRNG(), *s.CrtRNG()
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{b.Handle}}}); err != nil {
		t.Fatal(err)
	}
	for now := int32(2); now <= 6; now++ {
		s.Step(now)
	}
	if s.Clock.GlobalTick != tickBefore {
		t.Fatalf("paused steps advanced the global tick to %d, want %d", s.Clock.GlobalTick, tickBefore)
	}
	if *s.SimRNG() != simBefore || *s.CrtRNG() != crtBefore {
		t.Fatal("the paused input boundary moved an authoritative random stream")
	}
	f := pausedFrame(t, s)
	if f.Tick != tickBefore {
		t.Fatalf("paused publication carries tick %d, want the committed %d", f.Tick, tickBefore)
	}
	handles := selectionOf(f)
	if len(handles) != 1 || handles[0] != b.Handle {
		t.Fatalf("paused selection published %v, want only B=%d", handles, b.Handle)
	}
	if f.CommandPage.Builder != b.Handle {
		t.Fatalf("paused command page names builder %d, want B=%d", f.CommandPage.Builder, b.Handle)
	}
	if !f.Paused {
		t.Fatal("paused publication does not report the pause bit")
	}
	if len(s.PendingHumanCommands()) != 0 {
		t.Fatal("a drained command stayed queued")
	}

	// A build-page change is visible while paused, on the newly selected unit.
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage,
		BuildPage: HumanBuildPageCommand{Builder: b.Handle, Page: 2}}); err != nil {
		t.Fatal(err)
	}
	s.Step(7)
	f = pausedFrame(t, s)
	if f.CommandPage.Page != 2 || f.CommandPage.Builder != b.Handle {
		t.Fatalf("paused page published %d for builder %d, want page 2 for B", f.CommandPage.Page, f.CommandPage.Builder)
	}
	if s.Clock.GlobalTick != tickBefore {
		t.Fatal("the page change advanced the global tick")
	}

	// Exactly once: resuming must not apply either command a second time.
	s.SetPaused(false)
	s.Step(8)
	if s.Clock.GlobalTick == tickBefore {
		t.Fatal("resume did not run a tick")
	}
	f = pausedFrame(t, s)
	if handles := selectionOf(f); len(handles) != 1 || handles[0] != b.Handle {
		t.Fatalf("resumed selection %v, want only B", handles)
	}
	if f.CommandPage.Page != 2 {
		t.Fatalf("resumed page %d, want 2", f.CommandPage.Page)
	}
}

// Idle-paused pumps must not republish: a host frame with nothing queued is
// not an input boundary (DESIGN_INTERFACE_HUD_INPUT §3.12).
func TestPausedInputDoesNothingWithAnEmptyQueue(t *testing.T) {
	s, a, _ := pausedInputFixture(t)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(1)
	s.SetPaused(true)
	before := s.Snapshot.Current()
	for now := int32(2); now <= 8; now++ {
		s.Step(now)
	}
	if s.Snapshot.Current() != before {
		t.Fatal("an idle paused pump republished the committed frame")
	}
}

// The paused boundary republishes the tick that already ran, so the Enhanced
// blend must not be handed two copies of one tick as a previous/current pair
// (DESIGN_GPU_RENDERER §13.5).
func TestPausedRepublicationLeavesNoPreviousPair(t *testing.T) {
	s, a, b := pausedInputFixture(t)
	s.Step(1)
	s.Step(2)
	if s.Snapshot.Previous() == nil {
		t.Fatal("two ticks did not produce a previous frame")
	}
	s.SetPaused(true)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle, b.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(3)
	if prev := s.Snapshot.Previous(); prev != nil {
		t.Fatalf("paused republication offered a previous frame at tick %d", prev.Tick)
	}
	s.SetPaused(false)
	s.Step(4)
	prev := s.Snapshot.Previous()
	cur := s.Snapshot.Current()
	if prev == nil || cur == nil || prev.Tick != cur.Tick-1 {
		t.Fatal("the tick after a resume did not restore an ordinary interpolation pair")
	}
}

// Applying a command at the paused boundary must leave exactly the state the
// unpaused run would have produced on the next tick: the same order queues,
// the same creation stamps, the same resources and the same position in both
// random streams (DESIGN_INTERFACE_HUD_INPUT §3.12).
func TestPausedInputEquivalentToApplyingTheCommandsOnTheNextTick(t *testing.T) {
	script := func(s *Session, a, b *units.Unit) []HumanCommand {
		return []HumanCommand{
			{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{b.Handle}}},
			{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: b.Handle, Page: 1}},
			{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
				Builder: b.Handle, Product: "fixsolar", WX: numeric16(30), WZ: numeric16(30)}},
			{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 4}},
			{Kind: HumanStop},
		}
	}

	paused, pa, pb := pausedInputFixture(t)
	paused.Step(1)
	paused.SetPaused(true)
	for _, c := range script(paused, pa, pb) {
		if err := paused.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	paused.Step(2) // drains at the paused boundary
	if len(paused.PendingHumanCommands()) != 0 {
		t.Fatal("the paused boundary left commands queued")
	}
	paused.SetPaused(false)
	paused.Step(2) // the tick the commands were due for

	running, ra, rb := pausedInputFixture(t)
	running.Step(1)
	for _, c := range script(running, ra, rb) {
		if err := running.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	running.Step(2)

	got, err := paused.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	want, err := running.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("paused-then-resumed state %s differs from the unpaused run %s", got, want)
	}
	// The fingerprint is a bounded diagnostic, so name the facts outright too.
	if paused.Clock.GlobalTick != running.Clock.GlobalTick {
		t.Fatal("the two runs ended on different ticks")
	}
	if *paused.SimRNG() != *running.SimRNG() || *paused.CrtRNG() != *running.CrtRNG() {
		t.Fatal("the two runs ended at different random-stream positions")
	}
	if paused.Econ.Players[0].Stock != running.Econ.Players[0].Stock {
		t.Fatal("the two runs ended with different resources")
	}
	pq, rq := orders.QueueOfUnit(pb), orders.QueueOfUnit(rb)
	if (pq == nil) != (rq == nil) {
		t.Fatal("only one run built an order queue")
	}
	if pq != nil {
		if pq.LenPrimary() != rq.LenPrimary() {
			t.Fatalf("queue lengths differ: paused %d, unpaused %d", pq.LenPrimary(), rq.LenPrimary())
		}
		for i, node := range pq.Primary() {
			other := rq.Primary()[i]
			if node.ID != other.ID || node.CreationTick != other.CreationTick ||
				node.GoalX != other.GoalX || node.GoalZ != other.GoalZ || node.BuildDefKey != other.BuildDefKey {
				t.Fatalf("order %d differs: paused %+v unpaused %+v", i, node, other)
			}
		}
	}
}

// A command that would be simulation work rather than input bookkeeping stays
// queued, and it holds everything enqueued behind it, so enqueue order is the
// same order the next tick applies (DESIGN_INTERFACE_HUD_INPUT §3.12).
func TestPausedInputDefersSimulationWorkAndKeepsEnqueueOrder(t *testing.T) {
	s, a, b := pausedInputFixture(t)
	s.Step(1)
	s.SetPaused(true)
	crtBefore := *s.CrtRNG()
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	// The argument-free meteor command arms a storm and spends CRT draws.
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanMeteor}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{b.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(2)
	if *s.CrtRNG() != crtBefore {
		t.Fatal("a deferred command spent the CRT stream at the paused boundary")
	}
	pending := s.PendingHumanCommands()
	if len(pending) != 2 || pending[0].Kind != HumanMeteor || pending[1].Kind != HumanSelectionReplace {
		t.Fatalf("the drain did not stop at the deferred command: %v", pending)
	}
	if handles := selectionOf(pausedFrame(t, s)); len(handles) != 1 || handles[0] != a.Handle {
		t.Fatalf("paused selection %v, want the prefix's A=%d", handles, a.Handle)
	}
}

// A republication must not deliver the previous tick's one-shots a second
// time: the staged presentation events and the per-tick big-brother notices
// are both reset by their own producer before the boundary runs
// (DESIGN_INTERFACE_HUD_INPUT §3.12) [03 R-AUD-01 §7][07 R-CAM-01 §12].
func TestPausedRepublicationDoesNotRepeatOneShots(t *testing.T) {
	s, a, _ := pausedInputFixture(t)
	s.Step(1)
	pub := s.ensurePublicationState()
	pub.events.EmitAnnounce(frame.Event{Tick: s.Clock.GlobalTick, StatusText: "one shot", StatusClass: 4, AnnounceSlot: 10})
	s.bigBrother.cycle = true
	s.bigBrother.resetVisited = true
	s.bigBrother.cancelFollow = true
	s.publishSnapshot(s.Clock.GlobalTick + 1)
	s.Clock.GlobalTick++
	first := pausedFrame(t, s)
	if len(first.Events) != 1 || !first.BigBrotherCycle {
		t.Fatalf("the fixture did not publish its one-shots: events=%d cycle=%t", len(first.Events), first.BigBrotherCycle)
	}
	drained := s.Snapshot.DrainCommittedEvents(nil)
	if len(drained) != 1 {
		t.Fatalf("presentation drained %d retained events, want 1", len(drained))
	}

	s.SetPaused(true)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(9)
	again := pausedFrame(t, s)
	if len(again.Events) != 0 {
		t.Fatalf("the republication repeated %d presentation events", len(again.Events))
	}
	if again.BigBrotherCycle || again.BigBrotherResetVisited || again.BigBrotherCancelFollow {
		t.Fatal("the republication repeated a per-tick big-brother notice")
	}
	if s.Snapshot.PendingCommittedEvents() != 0 {
		t.Fatalf("the republication retained %d events for presentation", s.Snapshot.PendingCommittedEvents())
	}
}

// A factory selected from an empty selection while paused publishes its
// construction panel facts, which is what makes the side panel usable
// (DESIGN_INTERFACE_HUD_INPUT §3.12) [07 §9].
func TestPausedSelectionOfAFactoryPublishesItsPanelFacts(t *testing.T) {
	s, a, _ := pausedInputFixture(t)
	s.Step(1)
	if got := pausedFrame(t, s).CommandPage.Builder; got != 0 {
		t.Fatalf("the fixture started with builder %d selected, want none", got)
	}
	s.SetPaused(true)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(2)
	f := pausedFrame(t, s)
	if f.CommandPage.Builder != a.Handle {
		t.Fatalf("paused selection published builder %d, want %d", f.CommandPage.Builder, a.Handle)
	}
	if f.CommandPage.PageCount != 3 {
		t.Fatalf("paused selection published page count %d, want the authored 3", f.CommandPage.PageCount)
	}
	if len(f.CommandPage.AllowedProducts) == 0 {
		t.Fatal("paused selection published no build membership")
	}
}

// A session that is not in battle has no host frame to run at all: the pump's
// single-player step reaches the sub-ticks only in the battle state
// [01 R-PLAT-01 §1 step 2].
func TestPausedInputRequiresTheBattleState(t *testing.T) {
	s, a, _ := pausedInputFixture(t)
	s.Step(1)
	s.SetPaused(true)
	s.State = StatePostBattle
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace,
		Selection: HumanSelectionCommand{Handles: []pool.Handle{a.Handle}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(2)
	if len(s.PendingHumanCommands()) != 1 {
		t.Fatal("a non-battle state drained the input queue")
	}
}
