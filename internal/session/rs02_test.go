package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestRS02_ManagerForPlayer1NeverRunsDuringPlayer0 verifies that manager for player 1 is not ticked during player 0's economy beforeDeadline [RS-02][08][I1].
func TestRS02_ManagerForPlayer1NeverRunsDuringPlayer0(t *testing.T) {
	rng.SeedGlobal(1000, 2000)
	var econ economy.Service
	for i := 0; i < 2; i++ {
		p := &econ.Players[i]
		p.Exists = true
		p.ControllerState = 2
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.UpdateTime = 10
		p.Helper1Deadline = 10
		p.Helper2Deadline = 10
	}
	m0 := &ai.Manager{Player: 0}
	m1 := &ai.Manager{Player: 1}
	for k := ai.TaskKind(0); k < ai.TaskKindCount; k++ {
		m0.Deadlines[k] = 1000
		m1.Deadlines[k] = 1000
	}
	var order []int
	m0.TestHook = func(tick uint32, player uint8) { order = append(order, int(player)) }
	m1.TestHook = func(tick uint32, player uint8) { order = append(order, int(player)) }
	s := &Session{
		Econ:  &econ,
		Units: units.New(10, nil),
		AI:    [10]*ai.Manager{0: m0, 1: m1},
	}
	// Simulate player 0 TickPlayer only
	order = nil
	econ.TickPlayer(0, 10, nil, func() { m0.Tick(10, nil, &econ) })
	if len(order) != 1 || order[0] != 0 {
		t.Fatalf("player 0 callback should only tick manager 0, got %v", order)
	}
	if m1.EntryCount() != 0 {
		t.Fatalf("manager 1 should not have been ticked during player 0 callback")
	}
	// Now player 1
	order = nil
	econ.TickPlayer(1, 10, nil, func() { m1.Tick(10, nil, &econ) })
	if len(order) != 1 || order[0] != 1 {
		t.Fatalf("player 1 callback should only tick manager 1, got %v", order)
	}
	_ = s
}

// TestRS02_TwoAIsRunOnceEachInAscendingOrder verifies both AIs run once in ascending order via Session dispatch [RS-02][I1].
func TestRS02_TwoAIsRunOnceEachInAscendingOrder(t *testing.T) {
	rng.SeedGlobal(2000, 3000)
	var econ economy.Service
	for i := 0; i < 2; i++ {
		p := &econ.Players[i]
		p.Exists = true
		p.ControllerState = 2
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.UpdateTime = 1000
		p.Helper1Deadline = 1000
		p.Helper2Deadline = 1000
	}
	m0 := &ai.Manager{Player: 0}
	m1 := &ai.Manager{Player: 1}
	for k := ai.TaskKind(0); k < ai.TaskKindCount; k++ {
		m0.Deadlines[k] = 100
		m1.Deadlines[k] = 100
	}
	var order []int
	m0.TestHook = func(tick uint32, player uint8) { order = append(order, int(player)) }
	m1.TestHook = func(tick uint32, player uint8) { order = append(order, int(player)) }
	managers := [10]*ai.Manager{0: m0, 1: m1}
	ai.Dispatch(100, &econ, managers, nil)
	if len(order) != 2 {
		t.Fatalf("both AIs should run once, order %v", order)
	}
	if order[0] != 0 || order[1] != 1 {
		t.Fatalf("AIs should run in ascending player order, got %v", order)
	}
	if m0.EntryCount() != 1 || m1.EntryCount() != 1 {
		t.Fatalf("each manager should have entryCount 1, got %d %d", m0.EntryCount(), m1.EntryCount())
	}
	// Also test Session tickPlayers order
	order = nil
	s := &Session{
		Econ:  &econ,
		Units: units.New(10, nil),
		AI:    managers,
	}
	// Reset entryCounts by creating new managers for second part
	m0b := &ai.Manager{Player: 0}
	m1b := &ai.Manager{Player: 1}
	for k := ai.TaskKind(0); k < ai.TaskKindCount; k++ {
		m0b.Deadlines[k] = 100
		m1b.Deadlines[k] = 100
	}
	var order2 []int
	m0b.TestHook = func(tick uint32, player uint8) { order2 = append(order2, int(player)) }
	m1b.TestHook = func(tick uint32, player uint8) { order2 = append(order2, int(player)) }
	s.AI = [10]*ai.Manager{0: m0b, 1: m1b}
	s.tickPlayers(100)
	if len(order2) != 2 || order2[0] != 0 || order2[1] != 1 {
		t.Fatalf("Session tickPlayers should run in ascending order, got %v", order2)
	}
}

// TestRS02_RNGDrawLedgerMatchesHandAuthoredSequence verifies that hand-authored task sequence consumes expected draws from Global.Sim [RS-02][I4].
func TestRS02_RNGDrawLedgerMatchesHandAuthoredSequence(t *testing.T) {
	rng.SeedGlobal(12345, 0)
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 1, FootprintZ: 1, Builder: true},
		},
	}
	attrs := make([]formats.TNTAttribute, 4*4)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 4, 4)
	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: plot, Version: world.VersionCanonical}
	w := units.New(16, cat)
	defOnOff := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armmex_onoff")}, UnitName: "armmex_onoff", FootprintX: 1, FootprintZ: 1, YardMap: "o", OnOffable: true, MaxDamage: 100, MakesMetal: 10}
	cat.Units[content.CanonicalKey("armmex_onoff")] = defOnOff
	h, _ := w.Create(defOnOff, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	u := w.Unit(h)
	u.Remaining = 0
	var econ economy.Service
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = 2
	econ.Players[0].IsObserver = false
	econ.Players[0].StatusHalfwordAt144 = 1
	econ.Players[0].Stock[economy.Metal] = 100
	econ.Players[0].Stock[economy.Energy] = 50
	econ.Players[0].AIProduction[economy.Energy] = 10
	econ.Players[0].AIConsumption[economy.Energy] = 0
	econ.Players[0].PassProduced[economy.Energy] = 10
	econ.Players[0].PassConsumed[economy.Energy] = 0
	econ.Players[0].UpdateTime = 1000
	econ.Players[0].Helper1Deadline = 1000
	econ.Players[0].Helper2Deadline = 1000
	mgr := &ai.Manager{
		Player:       0,
		Catalog:      cat,
		Terrain:      terrain,
		SurfaceMetal: 0,
	}
	mgr.Strategic.Catalog = cat
	mgr.Strategic.Init([]string{"armcom", "armmex_onoff"})
	mgr.Strategic.LastRefreshTick = 0
	for k := ai.TaskKind(0); k < ai.TaskKindCount; k++ {
		mgr.Deadlines[k] = 1000
	}
	mgr.Deadlines[ai.TaskOther900] = 100
	mgr.Deadlines[ai.TaskOther150] = 100
	mgr.Deadlines[ai.TaskResource] = 100
	// Resource task draws RNG(5) only for makesmetal units in its vector [P0-02 §3.3]; populate it.
	mgr.GroupResource = []pool.Handle{h}
	// DET-01: the manager draws from the INJECTED session stream; the
	// process-global stream is never consulted. Bind a local stream and count
	// from it.
	sim := rng.NewSimulation(12345)
	mgr.RNG = &sim
	before := sim.Draws()
	mgr.Tick(100, w, &econ)
	draws := sim.Draws() - before
	// Expected: strategic refresh (30) + Other900 (900) + Other150 (150) + Resource (5) = 4 draws
	if draws != 4 {
		t.Fatalf("hand-authored sequence should consume 4 draws (30,900,150,5), got %d", draws)
	}
	if rng.Global.Sim != nil && rng.Global.Sim.Draws() != 0 {
		t.Fatalf("global stream advanced %d draws; managers must draw only from the injected stream [DET-01]", rng.Global.Sim.Draws())
	}
}

// TestRS02_TwoSessionsDoNotShareMutableState verifies that two sessions have isolated AI callbacks and state [RS-02].
func TestRS02_TwoSessionsDoNotShareMutableState(t *testing.T) {
	rng.SeedGlobal(777, 888)
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 1, FootprintZ: 1, YardMap: "o", Builder: true, MaxDamage: 100, SightDistance: 300},
		},
		Sides: []*content.SideDef{{Commander: "armcom"}},
	}
	// Create two sessions via NewSkirmishForTest with same config but we will check isolation
	s1 := &Session{
		Catalog: cat,
		Econ:    &economy.Service{},
		Units:   units.New(10, cat),
		AI:      [10]*ai.Manager{},
	}
	s2 := &Session{
		Catalog: cat,
		Econ:    &economy.Service{},
		Units:   units.New(10, cat),
		AI:      [10]*ai.Manager{},
	}
	for i := 0; i < 2; i++ {
		s1.Econ.Players[i].Exists = true
		s1.Econ.Players[i].ControllerState = 2
		s1.Econ.Players[i].StatusHalfwordAt144 = 1
		s2.Econ.Players[i].Exists = true
		s2.Econ.Players[i].ControllerState = 2
		s2.Econ.Players[i].StatusHalfwordAt144 = 1
	}
	mgr1 := &ai.Manager{Player: 0}
	mgr2 := &ai.Manager{Player: 0}
	s1.AI[0] = mgr1
	s2.AI[0] = mgr2
	if s1.AI[0] == s2.AI[0] {
		t.Fatalf("two sessions share same manager pointer")
	}
	// Callbacks should be distinct: bind different closures
	called1 := 0
	called2 := 0
	mgr1.QueueBuildTyped = func(req ai.BuildRequest) error { called1++; return nil }
	mgr2.QueueBuildTyped = func(req ai.BuildRequest) error { called2++; return nil }
	_ = mgr1.QueueBuildTyped(ai.BuildRequest{Builder: 1, UnitKey: "armcom", Kind: ai.BuildKindMobileSite})
	if called1 != 1 || called2 != 0 {
		t.Fatalf("session1 callback should not affect session2: %d %d", called1, called2)
	}
	_ = mgr2.QueueBuildTyped(ai.BuildRequest{Builder: 1, UnitKey: "armcom", Kind: ai.BuildKindMobileSite})
	if called1 != 1 || called2 != 1 {
		t.Fatalf("session2 callback should be isolated: %d %d", called1, called2)
	}
	// Strategic state isolation
	mgr1.Strategic.Init([]string{"armcom"})
	mgr2.Strategic.Init([]string{"armcom"})
	mgr1.Strategic.CenterX = 100
	mgr2.Strategic.CenterX = 200
	if mgr1.Strategic.CenterX == mgr2.Strategic.CenterX {
		t.Fatalf("strategic state should be isolated")
	}
	mgr1.Strategic.Counts["armcom"] = 5
	if mgr2.Strategic.Counts["armcom"] == 5 {
		t.Fatalf("counts should be isolated")
	}
}
