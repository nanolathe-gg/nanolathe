package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

func runtimeEconomy(player uint8, controller uint8) *economy.Service {
	var e economy.Service
	e.Players[player].Exists = true
	e.Players[player].ControllerState = controller
	return &e
}

func TestTaskSlotsPhysicalOrder(t *testing.T) {
	got := []TaskKind{TaskResource, TaskWaveA, TaskRegroupA, TaskConstruction, TaskNull, TaskWaveB, TaskRegroupB, TaskExplore, TaskRally, TaskEmptySlot}
	if int(TaskKindCount) != len(got) {
		t.Fatalf("task slot count=%d, want %d", TaskKindCount, len(got))
	}
	for i, k := range got {
		if int(k) != i {
			t.Fatalf("slot %d has kind %d", i, k)
		}
	}
}

func TestNullTaskIsVisitedAndEmptySlotIsSkipped(t *testing.T) {
	r := rng.NewSimulation(1)
	m := &Manager{Player: 0, RNG: &r}
	var e economy.Service
	e.Players[0].Exists = true
	e.Players[0].ControllerState = 2
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 100
	}
	m.runDueTasks(100, nil, &e)
	if m.Deadlines[TaskNull] != 100 {
		t.Fatalf("null task changed its deadline")
	}
	if m.Deadlines[TaskEmptySlot] != 100 {
		t.Fatalf("empty slot was treated as a runnable task")
	}
}

func TestNilRNGLeavesRandomDeadlinesUnchanged(t *testing.T) {
	m := &Manager{Player: 0}
	m.Deadlines[TaskExplore] = 100
	m.Deadlines[TaskRally] = 100
	var e economy.Service
	e.Players[0].Exists = true
	e.Players[0].ControllerState = 2
	m.runDueTasks(100, nil, &e)
	if m.Deadlines[TaskExplore] != 100 || m.Deadlines[TaskRally] != 100 {
		t.Fatalf("nil RNG changed explore/rally deadlines: %v", m.Deadlines)
	}
}

func TestManagerGatesAndPerSlotDeadlines(t *testing.T) {
	const tick = uint32(100)
	// The outer gate admits controllers 1..3, but only controller 2 reaches
	// virtual task dispatch [08 "Strategy manager and its task graph"].
	for _, controller := range []uint8{1, 3} {
		m := &Manager{Player: 1}
		m.Deadlines[TaskConstruction] = tick
		m.Tick(tick, nil, runtimeEconomy(1, controller))
		if m.Deadlines[TaskConstruction] != tick {
			t.Fatalf("controller %d ran inner-gated construction", controller)
		}
	}
	m := &Manager{Player: 1}
	m.Deadlines[TaskConstruction] = tick
	m.Tick(tick, nil, runtimeEconomy(1, 0))
	if m.Deadlines[TaskConstruction] != tick {
		t.Fatal("controller 0 passed the outer gate")
	}
	// Player ten is the sentinel and is outside the ten-player economy array.
	sentinel := &Manager{Player: 10}
	sentinel.Deadlines[TaskConstruction] = tick
	sentinel.Tick(tick, nil, runtimeEconomy(0, 2))
	if sentinel.Deadlines[TaskConstruction] != tick {
		t.Fatal("player ten passed the sentinel gate")
	}

	// With both gates open, each due runnable slot receives its established
	// cadence. The null slot has no body/deadline and the empty slot is skipped.
	r := rng.NewSimulation(7)
	m = &Manager{Player: 1, RNG: &r}
	e := runtimeEconomy(1, 2)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = tick
	}
	m.runDueTasks(tick, nil, e)
	want := map[TaskKind]uint32{
		TaskResource:     tick + 30,
		TaskWaveA:        tick + 300,
		TaskRegroupA:     tick + 150,
		TaskConstruction: tick + 90,
		TaskWaveB:        tick + 300,
		TaskRegroupB:     tick + 150,
	}
	for k, deadline := range want {
		if m.Deadlines[k] != deadline {
			t.Fatalf("slot %d deadline=%d, want %d", k, m.Deadlines[k], deadline)
		}
	}
	if m.Deadlines[TaskNull] != tick || m.Deadlines[TaskEmptySlot] != tick {
		t.Fatalf("null/empty slots changed: null=%d empty=%d", m.Deadlines[TaskNull], m.Deadlines[TaskEmptySlot])
	}
	if got := r.Draws(); got != 2 {
		t.Fatalf("empty-world explore/rally cadence draws=%d, want 2", got)
	}
	if m.Deadlines[TaskExplore] < tick+30 || m.Deadlines[TaskExplore] > tick+929 {
		t.Fatalf("explore deadline=%d outside tick+30+RNG(900)", m.Deadlines[TaskExplore])
	}
	if m.Deadlines[TaskRally] < tick+30 || m.Deadlines[TaskRally] > tick+179 {
		t.Fatalf("rally deadline=%d outside tick+30+RNG(150)", m.Deadlines[TaskRally])
	}
}

func TestResourceProbeKeepsIdleFactoryQueueNil(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "idle-factory"},
		UnitName:         "idle-factory",
		MaxDamage:        100,
		Builder:          true,
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{def.CanonicalKey: def},
		BuildMenus: map[string]*content.BuildMenuPage{
			def.CanonicalKey: {Buttons: []string{"idle-factory"}},
		},
	}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(def, 1, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatalf("create AI-owned idle factory: %v", err)
	}
	u := w.Unit(h)
	if u == nil || u.Orders != nil {
		t.Fatal("fixture factory unexpectedly has an order queue")
	}
	econ := runtimeEconomy(1, 2)
	m := &Manager{Player: 1, Catalog: cat, GroupResource: []pool.Handle{h}}
	for tick := uint32(1); tick <= 5; tick++ {
		m.doResource(tick, w, econ)
		if u.Orders != nil {
			t.Fatalf("AI read/probe materialized an idle queue on tick %d", tick)
		}
	}
}

func TestRegroupMoveBindsQueueBeforeSubmission(t *testing.T) {
	def := &content.UnitDef{UnitName: "ai-mover", MaxDamage: 100, CanMove: true}
	w := newAIFixtureWorld(4, nil)
	hOwn, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatalf("create regroup unit: %v", err)
	}
	hPeer, err := w.Create(def, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	if err != nil {
		t.Fatalf("create regroup peer: %v", err)
	}
	sim := rng.NewSimulation(77)
	binding := &orders.QueueBinding{SimRNG: &sim}
	m := &Manager{Player: 0, OrderBinding: binding, GroupRegroupA: []pool.Handle{hOwn}, GroupWaveA: []pool.Handle{hPeer}}
	m.doRegroup(1, w, nil, TaskWaveA)
	q := orders.QueueOfUnit(w.Unit(hOwn))
	if q == nil || q.LenPrimary() != 1 {
		t.Fatal("AI regroup did not submit its move order")
	}
	if q.Binding() != binding || q.Binding().SimRNG != &sim {
		t.Fatal("AI regroup queue lost its owning session binding")
	}
}

func TestClassificationCadencePublishesGroups(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true}
	w := newAIFixtureWorld(2, &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}})
	h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Flags = classifierEligibleBit
	m := &Manager{Player: 0}
	e := runtimeEconomy(0, 2)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	for tick := uint32(1); tick <= 29; tick++ {
		m.Tick(tick, w, e)
		if u.Group != 0 {
			t.Fatalf("unit grouped before the thirtieth eligible entry at tick %d", tick)
		}
	}
	m.Tick(30, w, e)
	if u.Group != 4 || len(m.GroupConstruction) != 1 || m.GroupConstruction[0] != h {
		t.Fatalf("classification did not publish builder group: group=%d construction=%v", u.Group, m.GroupConstruction)
	}
	if u.Flags&classifierOutputSet == 0 || u.Flags&classifierOutputB == 0 {
		t.Fatalf("classification status masks not published: %#x", u.Flags)
	}
}

func TestDispatchPrecedesStrategicRefresh(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout"}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	w := newAIFixtureWorld(1, cat)
	terrain := &world.Terrain{CellW: 32, CellH: 24}
	r := rng.NewSimulation(41)
	m := &Manager{Player: 0, Catalog: cat, RNG: &r, Terrain: terrain, GroupExplore: []pool.Handle{1}}
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	m.Deadlines[TaskExplore] = 30
	m.Strategic.LastRefreshTick = 0
	e := runtimeEconomy(0, 2)
	m.Tick(30, w, e)
	probe := rng.NewSimulation(41)
	trials := probe.Uint32n(2)
	for i := uint32(0); i <= trials+1; i++ {
		probe.Uint32n(uint32(terrain.CellW / 8))
		probe.Uint32n(uint32(terrain.CellH / 8))
	}
	probe.Uint32n(900)
	probe.Uint32n(30)
	if r.State != probe.State || r.Draws() != probe.Draws() {
		t.Fatalf("dispatch/refresh RNG order changed: got draws=%d state=%d, want draws=%d state=%d", r.Draws(), r.State, probe.Draws(), probe.State)
	}
	if m.Strategic.LastRefreshTick != 30 {
		t.Fatalf("strategic refresh did not run after dispatch")
	}
}
