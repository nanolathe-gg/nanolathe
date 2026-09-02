package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func runtimeEconomy(player uint8, controller uint8) *economy.Service {
	var e economy.Service
	e.Players[player].Exists = true
	e.Players[player].ControllerState = controller
	return &e
}

func TestTaskSlotsPhysicalOrder(t *testing.T) {
	got := []TaskKind{TaskEmptySlot, TaskResource, TaskWaveA, TaskRegroupA, TaskConstruction, TaskNull, TaskWaveB, TaskRegroupB, TaskExplore, TaskRally}
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
	// The broadcast helper keys membership from the unit's STORED group
	// number, not from the task vector, so the fixture must stamp both
	// [08 R-AI-01 §9].
	w.Unit(hOwn).Group = 3
	w.Unit(hPeer).Group = 2
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
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", CanMove: true, CanPatrol: true}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	w := newAIFixtureWorld(1, cat)
	h, err := w.Create(def, 0, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(100))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Group = 8
	terrain := &world.Terrain{CellW: 32, CellH: 24}
	r := rng.NewSimulation(41)
	m := &Manager{Player: 0, Catalog: cat, RNG: &r, Terrain: terrain, GroupExplore: []pool.Handle{h}}
	m.Strategic.CenterX = numeric.FixedFromInt(100)
	m.Strategic.CenterZ = numeric.FixedFromInt(100)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	m.Deadlines[TaskExplore] = 30
	m.Strategic.LastRefreshTick = 0
	e := runtimeEconomy(0, 2)
	m.Tick(30, w, e)
	probe := rng.NewSimulation(41)
	probe.Uint32n(900)
	trials := probe.Uint32n(2)
	for i := uint32(0); i <= trials+1; i++ {
		probe.Uint32n(uint32((terrain.CellW * 16) / 8))
		probe.Uint32n(uint32((terrain.CellH * 16) / 8))
	}
	probe.Uint32n(30)
	if r.State != probe.State || r.Draws() != probe.Draws() {
		t.Fatalf("dispatch/refresh RNG order changed: got draws=%d state=%d, want draws=%d state=%d", r.Draws(), r.State, probe.Draws(), probe.State)
	}
	if m.Strategic.LastRefreshTick != 30 {
		t.Fatalf("strategic refresh did not run after dispatch")
	}
}

func TestWaveGatherEngageHysteresisAndNearestStableTie(t *testing.T) {
	// A mobile fixture authors BMcode 1, as every stock mobile unit does: the
	// order resolver's live-mover test reads the building-class status bit
	// creation derives from that byte, and without it the manager's move
	// broadcasts resolve to the immobile-builder rally marker
	// [04 R-ORD-02 §1][04 R-COLL-01 §2].
	// A resolved weapon slot is what gives the unit the armed state bit, and
	// code 3's whole armed branch is gated on it [R-ORD-02 §1]: without one the
	// resolver falls through to the kamikaze test and rejects, so a wave of
	// `canattack` units with no weapon would queue nothing at all.
	attackerWeapon := &content.WeaponDef{ID: 1, Name: "attackergun", Range: 180}
	attackerDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "attacker"}, UnitName: "attacker", BMCode: true, CanMove: true, CanAttack: true, MaxDamage: 100, Weapon1: "attackergun", Weapon1Def: attackerWeapon}
	baseDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "base"}, UnitName: "base", MaxDamage: 100}
	enemyDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "enemy"}, UnitName: "enemy", BMCode: true, CanMove: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"attacker": attackerDef, "base": baseDef, "enemy": enemyDef}}
	w := newAIFixtureWorld(12, cat)
	var wave []pool.Handle
	for i := 0; i < 6; i++ {
		h, err := w.Create(attackerDef, 0, numeric.FixedFromInt(64), 0, numeric.FixedFromInt(64))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Group = 2
		wave = append(wave, h)
	}
	base, err := w.Create(baseDef, 0, numeric.FixedFromInt(96), 0, numeric.FixedFromInt(80))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(base).Group = 5
	// Equal-distance targets prove the strict first-minimum rule. IterSliced
	// visits player one before player two regardless of allocation history.
	first, err := w.Create(enemyDef, 1, numeric.FixedFromInt(32), 0, numeric.FixedFromInt(64))
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Create(enemyDef, 2, numeric.FixedFromInt(96), 0, numeric.FixedFromInt(64))
	if err != nil {
		t.Fatal(err)
	}
	e := runtimeEconomy(0, 2)
	e.Players[1].Exists, e.Players[1].ControllerState = true, 1
	e.Players[2].Exists, e.Players[2].ControllerState = true, 1
	m := &Manager{
		Player: 0, GroupWaveA: append([]pool.Handle(nil), wave...), GroupNull: []pool.Handle{base},
		IsAlliance: func(uint8, uint8) bool { return false },
	}

	if got := m.nearestHostileUnit(w, e, numeric.FixedFromInt(64), 0, numeric.FixedFromInt(64)); got == nil || got.Handle != first {
		t.Fatalf("nearest hostile tie chose %v, want first player/pool target %d (other %d)", got, first, second)
	}
	m.IsAlliance = nil
	if got := m.nearestHostileUnit(w, e, numeric.FixedFromInt(64), 0, numeric.FixedFromInt(64)); got != nil {
		t.Fatalf("nil alliance binding exposed hostile target %v", got)
	}
	m.IsAlliance = func(uint8, uint8) bool { return false }
	m.doWave(10, w, e, waveAThreshold, waveMin, waveMax)
	if !m.waveAEngaged {
		t.Fatal("six-member wave did not enter engaged state")
	}
	for _, h := range wave {
		nodes := orders.QueueOfUnit(w.Unit(h)).Primary()
		if len(nodes) != 1 || nodes[0].ID != orders.Lookup("Attack_Chase") || nodes[0].Target != first {
			if len(nodes) == 1 {
				t.Fatalf("wave member %d attack id=%d target=%d, want Attack_Chase id=%d target=%d", h, nodes[0].ID, nodes[0].Target, orders.Lookup("Attack_Chase"), first)
			}
			t.Fatalf("wave member %d attack=%v, want one Attack_Chase target %d", h, nodes, first)
		}
	}

	// Four and five members keep attacking only while the latch is set.
	m.GroupWaveA = append([]pool.Handle(nil), wave[:4]...)
	m.doWave(11, w, e, waveAThreshold, waveMin, waveMax)
	if !m.waveAEngaged {
		t.Fatal("four-member engaged wave lost its latch")
	}
	for _, h := range wave[:4] {
		nodes := orders.QueueOfUnit(w.Unit(h)).Primary()
		if len(nodes) != 1 || nodes[0].ID != orders.Lookup("Attack_Chase") {
			t.Fatalf("engaged four-member wave member %d did not keep attacking: %v", h, nodes)
		}
	}

	// Three members retreat to the first non-empty base record and clear the
	// latch. The stored group field, rather than vector order, selects all
	// broadcast recipients; remove the other members from group two explicitly.
	for _, h := range wave[3:] {
		w.Unit(h).Group = 3
	}
	m.GroupWaveA = append([]pool.Handle(nil), wave[:3]...)
	m.doWave(12, w, e, waveAThreshold, waveMin, waveMax)
	if m.waveAEngaged {
		t.Fatal("three-member retreat did not clear engaged latch")
	}
	for _, h := range wave[:3] {
		nodes := orders.QueueOfUnit(w.Unit(h)).Primary()
		if len(nodes) != 1 || nodes[0].ID != orders.Lookup("Move_Ground") || nodes[0].GoalX != numeric.FixedFromInt(96) || nodes[0].GoalZ != numeric.FixedFromInt(80) {
			t.Fatalf("retreat order for %d=%v, want base centroid", h, nodes)
		}
	}
}

func TestNearestHostileAndRallyScoreUseSignedPositionWordDeltas(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "enemy"}, UnitName: "enemy", MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"enemy": def}}
	w := newAIFixtureWorld(4, cat)
	// The first target is distant only after subtraction wraps as an int32.
	// Widening before subtraction instead overflows its later int64 square and
	// can make it appear spuriously near [08 R-AI-01 §§7,9].
	first, err := w.Create(def, 1, numeric.Fixed(-1879048192), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	queryX := numeric.Fixed(1879048192)
	second, err := w.Create(def, 1, queryX-numeric.FixedOne, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := runtimeEconomy(0, 2)
	e.Players[1].Exists, e.Players[1].ControllerState = true, 1
	m := &Manager{
		Player:       0,
		IsAlliance:   func(uint8, uint8) bool { return false },
		rallyTargets: []pool.Handle{first},
	}
	m.Strategic.SingleVectors = map[string]int8{"enemy": 7}

	if got := m.nearestHostileUnit(w, e, queryX, 0, 0); got == nil || got.Handle != second {
		t.Fatalf("wrapped-distance nearest=%v, want second target %d", got, second)
	}
	if got := m.rallyProbeScore(w, queryX, 0); got != 0 {
		t.Fatalf("wrapped-distance rally score=%d, want distant target excluded", got)
	}
	if got := fixedWordDelta(numeric.Fixed(-1<<31), numeric.Fixed(1<<31-1)); got != 1 {
		t.Fatalf("boundary delta=%d, want signed-word wrap to 1", got)
	}
}

func TestExploreMovePatrolSequenceAndEdgeDraws(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", BMCode: true, CanMove: true, CanPatrol: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	terrain := &world.Terrain{CellW: 31, CellH: 25}
	w := newAIFixtureWorld(8, cat)
	var group []pool.Handle
	for i := 0; i < 5; i++ {
		h, err := w.Create(def, 0, numeric.FixedFromInt(int64(40+i)), 0, numeric.FixedFromInt(40))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Group = 8
		group = append(group, h)
	}

	seed := uint32(73)
	probe := rng.NewSimulation(seed)
	deadlineDraw := probe.Uint32n(900)
	legs := probe.Uint32n(2) + 2
	wg, hg := uint32((terrain.CellW*16)>>3), uint32((terrain.CellH*16)>>3)
	wantX := make([]numeric.Fixed, legs)
	wantZ := make([]numeric.Fixed, legs)
	for i := range wantX {
		wantX[i] = numeric.FixedFromInt(120 + int64(int32(probe.Uint32n(wg))-int32(wg)/2))
		wantZ[i] = numeric.FixedFromInt(90 + int64(int32(probe.Uint32n(hg))-int32(hg)/2))
	}
	r := rng.NewSimulation(seed)
	m := &Manager{Player: 0, RNG: &r, Terrain: terrain, GroupExplore: []pool.Handle{group[1], group[0]}}
	m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ = numeric.FixedFromInt(120), numeric.FixedFromInt(17), numeric.FixedFromInt(90)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	m.Deadlines[TaskExplore] = 20
	m.runDueTasks(20, w, nil)
	if m.Deadlines[TaskExplore] != 50+deadlineDraw || r.State != probe.State || r.Draws() != probe.Draws() {
		t.Fatalf("explore RNG ledger deadline=%d draws=%d state=%d, want deadline=%d draws=%d state=%d", m.Deadlines[TaskExplore], r.Draws(), r.State, 50+deadlineDraw, probe.Draws(), probe.State)
	}
	for _, h := range group { // broadcast follows stored group membership, not vector membership/order
		nodes := orders.QueueOfUnit(w.Unit(h)).Primary()
		if len(nodes) != int(legs) {
			t.Fatalf("scout %d route length=%d, want %d", h, len(nodes), legs)
		}
		for i, node := range nodes {
			// The manager submits intent 9 and the ordinary resolver picks the
			// descriptor: a mobile scout with `canpatrol` and no `canreclamate`
			// mirror bit resolves `Patrol` [04 R-ORD-02 §1]. These assertions
			// used to expect `QPatrol`, which the resolver only produces for an
			// actor with no live mover.
			wantID := orders.Lookup("Patrol")
			if i == 0 {
				wantID = orders.Lookup("Move_Ground")
			}
			if node.ID != wantID || node.GoalX != wantX[i] || node.GoalY != numeric.FixedFromInt(17) || node.GoalZ != wantZ[i] {
				t.Fatalf("scout %d leg %d=%+v, want id=%d goal=(%d,%d)", h, i, node, wantID, wantX[i], wantZ[i])
			}
		}
	}

	// Five members take the exact three-draw edge arm and replace the route.
	for _, h := range group {
		orders.QueueOfUnit(w.Unit(h)).SetPrimary(nil)
	}
	r = rng.NewSimulation(seed)
	probe = rng.NewSimulation(seed)
	orientation := probe.Uint32n(2)
	var edgeX, edgeZ int32
	if orientation != 0 {
		edgeX = int32(probe.Uint32n(uint32(terrain.CellW * 16)))
		if probe.Uint32n(2) == 0 {
			edgeZ = terrain.CellH*16 - 1
		}
	} else {
		if probe.Uint32n(2) == 0 {
			edgeX = terrain.CellW*16 - 1
		}
		edgeZ = int32(probe.Uint32n(uint32(terrain.CellH * 16)))
	}
	m.RNG, m.GroupExplore = &r, group
	m.doExplore(21, w, nil)
	if r.Draws() != 3 || r.State != probe.State {
		t.Fatalf("edge explore draws/state=%d/%d, want 3/%d", r.Draws(), r.State, probe.State)
	}
	nodes := orders.QueueOfUnit(w.Unit(group[0])).Primary()
	if len(nodes) != 1 || nodes[0].ID != orders.Lookup("Patrol") || nodes[0].GoalX != numeric.FixedFromInt(int64(edgeX)) || nodes[0].GoalZ != numeric.FixedFromInt(int64(edgeZ)) {
		t.Fatalf("edge patrol=%v, want (%d,%d)", nodes, edgeX, edgeZ)
	}
}

func TestExploreTargetsRemainSignedPositionWords(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", CanMove: true, CanPatrol: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	w := newAIFixtureWorld(8, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Group = 8

	// Choose a near-centre scatter whose positive X offset crosses MaxInt32.
	terrain := &world.Terrain{CellW: 8, CellH: 8}
	seed := uint32(1)
	var dx int32
	for {
		probe := rng.NewSimulation(seed)
		probe.Uint32n(2)
		dx = int32(probe.Uint32n(uint32((terrain.CellW*16)>>3))) - (terrain.CellW*16>>3)/2
		if dx > 0 {
			break
		}
		seed++
	}
	centreX := numeric.Fixed(1<<31 - 33)
	r := rng.NewSimulation(seed)
	m := &Manager{Player: 0, RNG: &r, Terrain: terrain, GroupExplore: []pool.Handle{h}}
	m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ = centreX, 17, numeric.FixedOne
	m.doExplore(1, w, nil)
	nodes := orders.QueueOfUnit(w.Unit(h)).Primary()
	wantX := numeric.Fixed(int32(centreX) + (dx << 16))
	if len(nodes) == 0 || nodes[0].GoalX != wantX {
		t.Fatalf("near explore X=%v, want signed-word wrapped %d", nodes, wantX)
	}

	// At the largest researched extent, the far edge is 0xffff0000 as a
	// signed position word, not a widened positive integer [08 R-AI-01 §6].
	for len(m.GroupExplore) < 5 {
		next, createErr := w.Create(def, 0, 0, 0, 0)
		if createErr != nil {
			t.Fatal(createErr)
		}
		w.Unit(next).Group = 8
		m.GroupExplore = append(m.GroupExplore, next)
	}
	orders.QueueOfUnit(w.Unit(h)).SetPrimary(nil)
	terrain = &world.Terrain{CellW: 4096, CellH: 4096}
	seed = 1
	for {
		probe := rng.NewSimulation(seed)
		if probe.Uint32n(2) != 0 {
			probe.Uint32n(65536)
			if probe.Uint32n(2) == 0 {
				break
			}
		}
		seed++
	}
	r = rng.NewSimulation(seed)
	m.RNG, m.Terrain = &r, terrain
	m.doExplore(2, w, nil)
	nodes = orders.QueueOfUnit(w.Unit(h)).Primary()
	if len(nodes) != 1 || nodes[0].GoalZ != numeric.Fixed(-65536) {
		t.Fatalf("far-edge explore=%v, want signed word Z=-65536", nodes)
	}
}

func TestRallyConstructorOffMapProbeAndPerMemberAdmission(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "attacker"}, UnitName: "attacker", CanAttack: true, CanMove: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"attacker": def}}
	w := newAIFixtureWorld(4, cat)
	mobile, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	immobile, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(mobile).Group = 9
	w.Unit(immobile).Group = 9
	w.Unit(mobile).Flags |= units.ArmedStatus
	w.Unit(immobile).Flags |= units.ArmedStatus
	terrain := &world.Terrain{CellW: 32, CellH: 24}
	seed := uint32(1)
	for {
		probe := rng.NewSimulation(seed)
		if probe.Uint32n(10) != 0 {
			break
		}
		seed++
	}
	r := rng.NewSimulation(seed)
	var seenX, seenZ numeric.Fixed
	m := &Manager{Player: 0, RNG: &r, GroupRally: []pool.Handle{immobile, mobile}}
	if !m.InitializeBattleState(terrain, RallyBattleBindings{
		ProbeKnown: func(_ uint8, x, _ numeric.Fixed, z numeric.Fixed) bool {
			seenX, seenZ = x, z
			return false
		},
		OrderAdmitted: func(u *units.Unit, _, _, _ numeric.Fixed) bool { return u.Handle == mobile },
	}) {
		t.Fatal("explicit rally battle initialization failed")
	}
	if m.InitializeBattleState(terrain, RallyBattleBindings{}) {
		t.Fatal("rally battle constructor state initialized twice")
	}
	m.doRally(90, w, nil)
	centreX := numeric.FixedFromInt(int64(terrain.CellW * 8))
	centreZ := numeric.FixedFromInt(int64(terrain.CellH * 8))
	if m.rallyBestX != centreX || m.rallyBestZ != centreZ || seenX != centreX*2 || seenZ != centreZ*2 {
		t.Fatalf("rally constructor best=(%d,%d) probe=(%d,%d), want centre=(%d,%d) initial probe=(%d,%d)", m.rallyBestX, m.rallyBestZ, seenX, seenZ, centreX, centreZ, centreX*2, centreZ*2)
	}
	if r.Draws() != 1 {
		t.Fatalf("unknown-ground rally drew %d body values, want only RNG(10)", r.Draws())
	}
	if q := orders.QueueOfUnit(w.Unit(immobile)); q != nil && len(q.Primary()) != 0 {
		t.Fatalf("no-locomotion member bypassed ordinary admission: %v", q.Primary())
	}
	nodes := orders.QueueOfUnit(w.Unit(mobile)).Primary()
	if len(nodes) != 1 || nodes[0].ID != orders.Lookup("Suppress") || nodes[0].GoalX != centreX || nodes[0].GoalZ != centreZ {
		t.Fatalf("mobile rally order=%v, want Suppress at incumbent centre", nodes)
	}
}

func TestRallyConstructorAndProbeAdditionWrapPositionWords(t *testing.T) {
	def := &content.UnitDef{UnitName: "attacker", CanAttack: true, MaxDamage: 100}
	w := newAIFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Group = 9
	terrain := &world.Terrain{CellW: 4096, CellH: 4096}
	seed := uint32(1)
	for {
		probe := rng.NewSimulation(seed)
		if probe.Uint32n(10) != 0 && probe.Uint32n(10) != 0 {
			break
		}
		seed++
	}
	r := rng.NewSimulation(seed)
	seen := make([]numeric.Fixed, 0, 2)
	m := &Manager{Player: 0, RNG: &r, GroupRally: []pool.Handle{h}}
	if !m.InitializeBattleState(terrain, RallyBattleBindings{
		ProbeKnown: func(_ uint8, x, _, _ numeric.Fixed) bool {
			seen = append(seen, x)
			return false
		},
		OrderAdmitted: func(*units.Unit, numeric.Fixed, numeric.Fixed, numeric.Fixed) bool { return false },
	}) {
		t.Fatal("explicit rally battle initialization failed")
	}
	minWord := numeric.Fixed(-1 << 31)
	if m.rallyBestX != minWord || m.rallyDriftX != minWord {
		t.Fatalf("constructor best/drift=%d/%d, want MinInt32 position words", m.rallyBestX, m.rallyDriftX)
	}
	m.doRally(1, w, nil)
	m.doRally(2, w, nil)
	if len(seen) != 2 || seen[0] != 0 || seen[1] != minWord {
		t.Fatalf("successive off-map probes=%v, want [0 %d] after word additions", seen, minWord)
	}
	if got := fixedWordNeg(-1 << 31); got != minWord {
		t.Fatalf("MinInt32 drift negation=%d, want wrapped %d", got, minWord)
	}
}

func TestRallyRequiresExplicitBattleBindings(t *testing.T) {
	def := &content.UnitDef{UnitName: "attacker", CanAttack: true, MaxDamage: 100}
	w := newAIFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Group = 9
	w.Unit(h).Flags |= units.ArmedStatus
	terrain := &world.Terrain{CellW: 16, CellH: 12}
	r := rng.NewSimulation(27)
	m := &Manager{Player: 0, RNG: &r, Terrain: terrain, GroupRally: []pool.Handle{h}}
	m.doRally(1, w, nil)
	if m.rallyInitialized || r.Draws() != 0 {
		t.Fatalf("rally lazily initialized=%v or drew %d values before battle binding", m.rallyInitialized, r.Draws())
	}
	if !m.InitializeBattleState(terrain, RallyBattleBindings{}) {
		t.Fatal("explicit rally initialization failed")
	}
	m.doRally(2, w, nil)
	if r.Draws() != 1 {
		t.Fatalf("initialized rally drew %d values, want RNG(10) only", r.Draws())
	}
	if q := orders.QueueOfUnit(w.Unit(h)); q != nil && len(q.Primary()) != 0 {
		t.Fatalf("nil rally admission binding submitted %v", q.Primary())
	}
}

func TestWaveAndExploreNoTargetPathsAreDeterministicNoOps(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", CanMove: true, CanPatrol: true, CanAttack: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	w := newAIFixtureWorld(8, cat)
	var group []pool.Handle
	for i := 0; i < 6; i++ {
		h, err := w.Create(def, 0, numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Group = 2
		group = append(group, h)
	}
	r := rng.NewSimulation(19)
	m := &Manager{
		Player: 0, RNG: &r, Terrain: &world.Terrain{CellW: 16, CellH: 16}, GroupWaveA: group,
		IsAlliance: func(uint8, uint8) bool { return false },
	}
	e := runtimeEconomy(0, 2)
	m.doWave(1, w, e, waveAThreshold, waveMin, waveMax)
	if !m.waveAEngaged {
		t.Fatal("no-target attack arm did not preserve its engaged latch write")
	}
	for _, h := range group {
		if q := orders.QueueOfUnit(w.Unit(h)); q != nil && len(q.Primary()) != 0 {
			t.Fatalf("no-target wave submitted an order for %d: %v", h, q.Primary())
		}
		w.Unit(h).Group = 8
	}
	m.GroupExplore = group[:1]
	m.Strategic.CenterX, m.Strategic.CenterZ = 0, 0
	m.doExplore(2, w, e)
	if r.Draws() != 0 {
		t.Fatalf("no-target wave/explore body drew %d values, want zero", r.Draws())
	}
	if q := orders.QueueOfUnit(w.Unit(group[0])); q != nil && len(q.Primary()) != 0 {
		t.Fatalf("no-target explore submitted an order: %v", q.Primary())
	}
}
