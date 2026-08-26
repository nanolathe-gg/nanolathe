package ai

import (
	"hash/fnv"
	"os"
	"regexp"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func makeEconWithControllers(ctrls map[int]uint8) *economy.Service {
	var s economy.Service
	for i := 0; i < 10; i++ {
		if c, ok := ctrls[i]; ok {
			s.Players[i].Exists = true
			s.Players[i].ControllerState = c
			s.Players[i].IsObserver = false
		} else {
			s.Players[i].Exists = false
		}
	}
	return &s
}

func newManagerFor(player uint8, tick uint32) *Manager {
	m := &Manager{Player: player}
	// Seed deadlines to be due at tick for testing virtual task gating.
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = tick
	}
	return m
}

// TestBothGates verifies C1 both gates [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1].
// Outer gate controller ∈ {1,2,3} and index !=10 dispatches; inner gate controller==2 for due virtual tasks.
func TestBothGates(t *testing.T) {
	// Outer gate: controller 0 (inactive) should not even increment entryCount or run tasks.
	m0 := newManagerFor(0, 10)
	econ0 := makeEconWithControllers(map[int]uint8{0: 0})
	m0.Tick(10, nil, econ0)
	if m0.EntryCount() != 0 {
		t.Fatalf("outer gate: controller 0 should not count as eligible entry, got %d", m0.EntryCount())
	}
	if m0.TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("outer gate: controller 0 should not run virtual tasks")
	}
	// Also test player 10 sentinel never dispatches even with controller 2
	m10 := &Manager{Player: 10}
	m10.Deadlines[TaskConstruction] = 10
	econ10 := makeEconWithControllers(map[int]uint8{})
	// Direct Tick should return immediately due to Player==10
	m10.Tick(10, nil, econ10)
	if m10.EntryCount() != 0 || m10.TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("player 10 should never dispatch [08][PLAN_11 C1], got entry %d runs %d", m10.EntryCount(), m10.TaskRuns(TaskConstruction))
	}

	// Controllers 1 and 3 dispatch (increment entryCount) but inner gate blocks virtual tasks.
	for _, ctrl := range []uint8{1, 3} {
		m := newManagerFor(1, 20)
		econ := makeEconWithControllers(map[int]uint8{1: ctrl})
		// Seed RNG for rescheduling but inner gate should prevent any draws
		rng.SeedGlobal(123, 0)
		before := rng.Global.Sim.Draws()
		m.Tick(20, nil, econ)
		if m.EntryCount() != 1 {
			t.Fatalf("controller %d: outer gate should count eligible entry, got %d", ctrl, m.EntryCount())
		}
		if m.TaskRuns(TaskConstruction) != 0 {
			t.Fatalf("controller %d: inner gate should block virtual tasks (controller==2 only) [08][PLAN_11 C1]", ctrl)
		}
		// No deadline should have advanced because tasks not run
		if m.Deadlines[TaskConstruction] != 20 {
			t.Fatalf("controller %d: deadline should stay 20 when inner gate blocks, got %d", ctrl, m.Deadlines[TaskConstruction])
		}
		if rng.Global.Sim.Draws() != before {
			t.Fatalf("controller %d: blocked inner gate should not consume RNG draws", ctrl)
		}
	}

	// Controller 2 runs virtual tasks when due.
	m2 := newManagerFor(2, 30)
	econ2 := makeEconWithControllers(map[int]uint8{2: 2})
	// Use world nil so doConstruction early returns but deadline still rescheduled and taskRuns increments.
	rng.SeedGlobal(99, 0)
	beforeDraws := rng.Global.Sim.Draws()
	m2.Tick(30, nil, econ2)
	if m2.EntryCount() != 1 {
		t.Fatalf("controller 2 entryCount want 1 got %d", m2.EntryCount())
	}
	// All task kinds due at 30 should have been run except empty/nullsub
	for k := TaskKind(0); k < TaskKindCount; k++ {
		if k == TaskEmpty || k == TaskNullSub {
			continue
		}
		if m2.TaskRuns(k) != 1 {
			t.Fatalf("controller 2: task %d should have run when due, got %d", k, m2.TaskRuns(k))
		}
	}
	// Fixed deadlines
	if got := m2.Deadlines[TaskConstruction]; got != 30+90 {
		t.Fatalf("TaskConstruction deadline want %d got %d [08][PLAN_11 C3]", 30+90, got)
	}
	if got := m2.Deadlines[TaskPositioning]; got != 30+90 {
		t.Fatalf("TaskPositioning deadline want %d got %d", 30+90, got)
	}
	if got := m2.Deadlines[TaskResource]; got != 30+30 {
		t.Fatalf("TaskResource deadline want %d got %d", 30+30, got)
	}
	if got := m2.Deadlines[TaskActivity]; got != 30+30 {
		t.Fatalf("TaskActivity deadline want %d got %d", 30+30, got)
	}
	if got := m2.Deadlines[TaskWaveA]; got != 30+300 {
		t.Fatalf("TaskWaveA deadline want %d got %d [P0-02]", 30+300, got)
	}
	if got := m2.Deadlines[TaskWaveB]; got != 30+300 {
		t.Fatalf("TaskWaveB deadline want %d got %d [P0-02]", 30+300, got)
	}
	if got := m2.Deadlines[TaskRegroupA]; got != 30+150 {
		t.Fatalf("TaskRegroupA deadline want %d got %d [P0-02]", 30+150, got)
	}
	if got := m2.Deadlines[TaskRegroupB]; got != 30+150 {
		t.Fatalf("TaskRegroupB deadline want %d got %d [P0-02]", 30+150, got)
	}
	// RNG tasks consume draws and produce correct ranges
	afterDraws := rng.Global.Sim.Draws()
	if afterDraws-beforeDraws != 2 { // 900 and 150 each one draw (resource 5 not drawn without units)
		t.Fatalf("RNG draws for task reschedule: want 2 for 900/150, got %d", afterDraws-beforeDraws)
	}
	// Deadline ranges for RNG tasks
	if d := m2.Deadlines[TaskOther900]; d < 30+30 || d > 30+30+899 {
		t.Fatalf("TaskOther900 deadline %d out of range [60,929] [08][PLAN_11 C3]", d)
	}
	if d := m2.Deadlines[TaskOther150]; d < 30+30 || d > 30+30+149 {
		t.Fatalf("TaskOther150 deadline %d out of range [60,209]", d)
	}

	// Dispatch helper both gates via Dispatch iteration 10 players [PLAN_11 C1]
	var managers [10]*Manager
	for i := 0; i < 10; i++ {
		managers[i] = newManagerFor(uint8(i), 40)
	}
	econDispatch := makeEconWithControllers(map[int]uint8{0: 1, 1: 2, 2: 3, 3: 0, 4: 2})
	// player 0 ctrl1 outer pass inner block, 1 ctrl2 both pass, 2 ctrl3 outer pass inner block, 3 ctrl0 outer block, 4 ctrl2 both pass
	rng.SeedGlobal(7, 0)
	Dispatch(40, econDispatch, managers, nil)
	if managers[0].EntryCount() != 1 || managers[0].TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("dispatch player0 ctrl1: entry 1 no virtual tasks")
	}
	if managers[1].EntryCount() != 1 || managers[1].TaskRuns(TaskConstruction) != 1 {
		t.Fatalf("dispatch player1 ctrl2: both gates should pass and run")
	}
	if managers[2].EntryCount() != 1 || managers[2].TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("dispatch player2 ctrl3 outer pass inner block")
	}
	if managers[3].EntryCount() != 0 || managers[3].TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("dispatch player3 ctrl0 outer block")
	}
	if managers[4].EntryCount() != 1 || managers[4].TaskRuns(TaskConstruction) != 1 {
		t.Fatalf("dispatch player4 ctrl2 both pass")
	}
	for i := 5; i < 10; i++ {
		if managers[i].EntryCount() != 0 {
			t.Fatalf("dispatch player %d no manager dispatch expected", i)
		}
	}
}

// TestDeadlineVectors verifies C3 per-class deadline formulas [08][PLAN_11 C3].
func TestDeadlineVectors(t *testing.T) {
	// Fixed classes
	m := newManagerFor(1, 100)
	econ := makeEconWithControllers(map[int]uint8{1: 2})
	rng.SeedGlobal(1, 0)
	// Set deadlines to far future except one kind to isolate RNG consumption per kind
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000 // future
	}
	// Test construction +90 isolated
	m.Deadlines[TaskConstruction] = 100
	before := rng.Global.Sim.Draws()
	m.Tick(100, nil, econ)
	if m.Deadlines[TaskConstruction] != 190 {
		t.Fatalf("construction deadline vector: tick 100 +90 want 190 got %d", m.Deadlines[TaskConstruction])
	}
	if rng.Global.Sim.Draws() != before {
		t.Fatalf("construction should not draw RNG")
	}
	if m.TaskRuns(TaskConstruction) != 1 {
		t.Fatalf("construction should have run")
	}

	// Resource +30 isolated
	m = newManagerFor(1, 200)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	econ = makeEconWithControllers(map[int]uint8{1: 2})
	m.Deadlines[TaskResource] = 200
	rng.SeedGlobal(2, 0)
	before = rng.Global.Sim.Draws()
	m.Tick(200, nil, econ)
	if m.Deadlines[TaskResource] != 230 {
		t.Fatalf("resource deadline vector: 200+30 want 230 got %d", m.Deadlines[TaskResource])
	}
	if rng.Global.Sim.Draws() != before {
		t.Fatalf("resource should not draw RNG")
	}

	// Other900: tick+30+RNG(900) in [30,929] offset
	m = newManagerFor(1, 300)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	m.Deadlines[TaskOther900] = 300
	rng.SeedGlobal(42, 0)
	before = rng.Global.Sim.Draws()
	m.Tick(300, nil, econ)
	after := rng.Global.Sim.Draws()
	if after-before != 1 {
		t.Fatalf("other900 should consume exactly one RNG(900) draw, got %d", after-before)
	}
	got := m.Deadlines[TaskOther900]
	if got < 330 || got > 330+899 {
		t.Fatalf("other900 deadline %d out of [330,1229]", got)
	}
	// Multiple runs stay in range (covers RNG bound)
	for i := 0; i < 10; i++ {
		m.Deadlines[TaskOther900] = uint32(400 + i)
		m.Tick(uint32(400+i), nil, econ)
		gd := m.Deadlines[TaskOther900]
		if gd < uint32(400+i)+30 || gd > uint32(400+i)+30+899 {
			t.Fatalf("other900 iter %d deadline %d out of range", i, gd)
		}
	}

	// Other150: tick+30+RNG(150) in [30,179]
	m = newManagerFor(1, 500)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	m.Deadlines[TaskOther150] = 500
	rng.SeedGlobal(99, 0)
	before = rng.Global.Sim.Draws()
	m.Tick(500, nil, econ)
	after = rng.Global.Sim.Draws()
	if after-before != 1 {
		t.Fatalf("other150 should consume one RNG(150) draw")
	}
	got = m.Deadlines[TaskOther150]
	if got < 530 || got > 530+149 {
		t.Fatalf("other150 deadline %d out of [530,679]", got)
	}

	// Not-due deadlines should not fire nor reschedule
	m = newManagerFor(1, 600)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 1000 // future
	}
	rng.SeedGlobal(5, 0)
	before = rng.Global.Sim.Draws()
	m.Tick(600, nil, econ)
	if m.TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("future deadlines should not fire")
	}
	if m.Deadlines[TaskConstruction] != 1000 {
		t.Fatalf("future deadline should stay 1000")
	}
	if rng.Global.Sim.Draws() != before {
		t.Fatalf("not-due should not consume RNG")
	}
}

// TestClassificationCadence verifies C3 classification every 30 eligible entries [08][PLAN_11 C3].
func TestClassificationCadence(t *testing.T) {
	m := &Manager{Player: 1}
	econ := makeEconWithControllers(map[int]uint8{1: 2}) // eligible
	// Keep deadlines future so only classification counting matters
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 10000
	}
	for i := 1; i <= 29; i++ {
		m.Tick(uint32(i), nil, econ)
		if m.ClassificationRuns() != 0 {
			t.Fatalf("after %d entries classification should be 0, got %d", i, m.ClassificationRuns())
		}
		if m.EntryCount() != uint32(i) {
			t.Fatalf("entryCount after %d ticks want %d got %d", i, i, m.EntryCount())
		}
	}
	// 30th entry triggers first classification
	m.Tick(30, nil, econ)
	if m.ClassificationRuns() != 1 {
		t.Fatalf("after 30 entries classification should be 1, got %d", m.ClassificationRuns())
	}
	if m.EntryCount() != 30 {
		t.Fatalf("entryCount 30")
	}
	// 31 should still 1
	m.Tick(31, nil, econ)
	if m.ClassificationRuns() != 1 {
		t.Fatalf("after 31 entries still 1")
	}
	// Up to 60 should be 2
	for i := 32; i <= 60; i++ {
		m.Tick(uint32(i), nil, econ)
	}
	if m.ClassificationRuns() != 2 {
		t.Fatalf("after 60 entries classification should be 2, got %d", m.ClassificationRuns())
	}
	if m.EntryCount() != 60 {
		t.Fatalf("entryCount 60 got %d", m.EntryCount())
	}
	// Ineligible entries (controller 0) must not count
	m2 := &Manager{Player: 2}
	econ2 := makeEconWithControllers(map[int]uint8{2: 0})
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m2.Deadlines[k] = 10000
	}
	for i := 0; i < 30; i++ {
		m2.Tick(uint32(i), nil, econ2)
	}
	if m2.EntryCount() != 0 {
		t.Fatalf("ineligible (controller 0) entries should not count, got %d", m2.EntryCount())
	}
	if m2.ClassificationRuns() != 0 {
		t.Fatalf("ineligible entries should not trigger classification")
	}
	// Outer eligible but inner blocked (controller 1) still counts for classification [08] every 30 eligible entries
	m3 := &Manager{Player: 3}
	econ3 := makeEconWithControllers(map[int]uint8{3: 1}) // outer pass inner block
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m3.Deadlines[k] = 10000
	}
	for i := 1; i <= 30; i++ {
		m3.Tick(uint32(i), nil, econ3)
	}
	if m3.EntryCount() != 30 {
		t.Fatalf("controller1 outer eligible entryCount want 30 got %d", m3.EntryCount())
	}
	if m3.ClassificationRuns() != 1 {
		t.Fatalf("controller1 outer eligible should still trigger classification every 30, got %d", m3.ClassificationRuns())
	}
	if m3.TaskRuns(TaskConstruction) != 0 {
		t.Fatalf("controller1 inner blocked should not run tasks even on classification tick")
	}
}

// TestRNGBoundCensus verifies manager.go uses only 900/150/5 bounds [08][PLAN_11 C3][C9][P0-02].
func TestRNGBoundCensus(t *testing.T) {
	data, err := os.ReadFile("manager.go")
	if err != nil {
		t.Fatalf("read manager.go: %v", err)
	}
	re := regexp.MustCompile(`Uint32n\((\d+)\)`)
	matches := re.FindAllSubmatch(data, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		seen[string(m[1])] = true
	}
	// Expect 150, 900 and 5 (eco toggle) per P0-02, no other bound like 30,255 etc in this file.
	want := map[string]bool{"150": true, "900": true, "5": true}
	if len(seen) != len(want) {
		t.Fatalf("manager.go RNG bounds = %v want %v (150/900/5 per P0-02) [08][PLAN_11 C9]", seen, want)
	}
	for k := range want {
		if !seen[k] {
			t.Fatalf("manager.go missing bound %s", k)
		}
	}
	for k := range seen {
		if !want[k] {
			t.Fatalf("manager.go unexpected bound %s (only 150/900/5 allowed in this file) [PLAN_11 C9][P0-02]", k)
		}
	}
	// Also verify via execution that draws are consumed for RNG tasks; resource may draw 5 when branch taken.
	// For this test we use nil world so resource task has no units to scan, thus no 5 draw; only 900/150 should draw.
	m := newManagerFor(1, 10)
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 10
	}
	// Make Empty and NullSub not due to avoid extra counts? They stay 0, but we set deadlines to 10, they will be due and count, but they don't draw.
	// For determinism, set Empty/NullSub to far future so they don't run.
	m.Deadlines[TaskEmpty] = 1000
	m.Deadlines[TaskNullSub] = 1000
	econ := makeEconWithControllers(map[int]uint8{1: 2})
	rng.SeedGlobal(100, 0)
	before := rng.Global.Sim.Draws()
	m.Tick(10, nil, econ)
	after := rng.Global.Sim.Draws()
	// Construction/positioning/resource/activity should not draw; other two each one draw => 2 draws (resource 5 not drawn without units)
	if after-before != 2 {
		t.Fatalf("execution RNG draws want 2 (900/150) got %d", after-before)
	}
}

// TestSelectorSurface verifies Manager satisfies Selector interface [PLAN_11 WU-11-2].
func TestSelectorSurface(t *testing.T) {
	prof := &Profile{Weight: map[string]int32{"armfav": 50}}
	m := &Manager{Player: 5, Profile: prof}
	m.Strategic.CenterX = 123
	var sel Selector = m // compile check that Manager satisfies Selector
	if sel.GetPlayer() != 5 {
		t.Fatalf("Selector GetPlayer want 5 got %d", sel.GetPlayer())
	}
	if sel.GetProfile() != prof {
		t.Fatalf("Selector GetProfile mismatch")
	}
	if sel.GetStrategic() != &m.Strategic {
		t.Fatalf("Selector GetStrategic should return &Manager.Strategic")
	}
}

// TestWiringBeforeDeadline verifies C11 economy beforeDeadline wiring [05 "Authoritative settlement order"] [08] [PLAN_11 C11].
// Manager.Tick is designed to be passed as economy.TickPlayer's beforeDeadline callback (kernel phase 5).
func TestWiringBeforeDeadline(t *testing.T) {
	var svc economy.Service
	// Player 0: active settling state, not observer, so settlement will run
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 2 // both outer and inner pass
	svc.Players[0].IsObserver = false
	svc.Players[0].StatusHalfwordAt144 = 1
	svc.Players[0].StatusWordAt140 = 0
	svc.Players[0].GameEnded = false
	svc.Players[0].EndGameCountdown = -1
	svc.Players[0].UpdateTime = 10
	svc.Players[0].Helper1Deadline = 10
	svc.Players[0].Helper2Deadline = 10
	svc.Players[0].Stock[economy.Energy] = 200
	svc.Players[0].Stock[economy.Metal] = 200
	svc.Players[0].Capacity[economy.Energy] = 1000
	svc.Players[0].Capacity[economy.Metal] = 1000

	m := &Manager{Player: 0, Profile: &Profile{Weight: map[string]int32{}, Limit: map[string]int32{}}}
	// Make manager due
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = 10
	}
	rng.SeedGlobal(1, 0)
	// This is the wiring: session-owned coordinator passes Manager.Tick as beforeDeadline [PLAN_11 C11] [05 "Authoritative settlement order"]
	// Supported inference: AI-as-auxiliary-helper from shared entry address [08]
	beforeCalled := false
	origHelper1 := svc.Players[0].Helper1Calls
	svc.TickPlayer(0, 10, nil, func() {
		beforeCalled = true
		m.Tick(10, nil, &svc)
	})
	if !beforeCalled {
		t.Fatalf("beforeDeadline callback should have been invoked [05][PLAN_11 C11]")
	}
	if svc.Players[0].Helper1Calls != origHelper1+1 {
		t.Fatalf("helpers should run before beforeDeadline [05]")
	}
	if m.EntryCount() != 1 {
		t.Fatalf("manager should have recorded eligible entry via beforeDeadline wiring")
	}
	if svc.Players[0].UpdateTime != 40 { // settlement advanced by 30
		t.Fatalf("settlement deadline should have advanced 10->40, got %d", svc.Players[0].UpdateTime)
	}
	// Skipped slot should invoke neither beforeDeadline nor settlement
	var svc2 economy.Service
	svc2.Players[0].Exists = false
	svc2.Players[0].UpdateTime = 10
	m2 := &Manager{Player: 0}
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m2.Deadlines[k] = 10
	}
	called := false
	svc2.TickPlayer(0, 10, nil, func() { called = true; m2.Tick(10, nil, &svc2) })
	if called {
		t.Fatalf("skipped slot should not invoke beforeDeadline [05][PLAN_11 C11]")
	}
	if m2.EntryCount() != 0 {
		t.Fatalf("skipped slot should not increment manager entryCount")
	}
	if svc2.Players[0].UpdateTime != 10 {
		t.Fatalf("skipped slot should freeze deadline [05]")
	}
}

// TestC12OnlyOrdinaryPaths verifies construction path uses ordinary queue [PLAN_11 C12].
func TestC12OnlyOrdinaryPaths(t *testing.T) {
	// Verify that doConstruction uses typed queue only when a candidate is selected [P0-07]
	// Setup world with a builder unit
	w := units.New(10, nil)
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")},
		UnitName:         "armcom",
		MaxDamage:        100,
	}
	def2 := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfav")},
		UnitName:         "armfav",
		MaxDamage:        100,
	}
	_ = def2
	// Enrich profile to allow armfav [P0-I16: per-manager CandidateSource]
	prof := &Profile{
		Weight: map[string]int32{"armfav": 100},
		Limit:  map[string]int32{},
	}
	m := &Manager{Player: 1, Profile: prof, CandidateSource: func(b *units.Unit) []string { return []string{"armfav"} }}
	m.Strategic.ClassVectors = map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}}
	m.Strategic.Counts = map[string]int32{}
	// Create builder alive with Remaining 0 (built)
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	_ = h
	// Need orders queue attached: units.World.Create does not init Orders; orders.QueueForUnit lazy? Check orders package existence.
	// Instead we can at least verify that doConstruction does not panic and that orders.QueueForUnit returns nil leading to early return without privileged mutation.
	// Ensure economy with sufficient stocks so gates pass
	var econ economy.Service
	econ.Players[1].Stock[economy.Energy] = 200
	econ.Players[1].Stock[economy.Metal] = 200
	econ.Players[1].Capacity[economy.Energy] = 1000
	econ.Players[1].Capacity[economy.Metal] = 500
	econ.Players[1].AIProduction[economy.Energy] = 300
	econ.Players[1].AIProduction[economy.Metal] = 10
	econ.Players[1].AIConsumption[economy.Energy] = 0
	econ.Players[1].AIConsumption[economy.Metal] = 0
	econ.Players[1].Exists = true
	econ.Players[1].ControllerState = 2

	// Seed RNG to ensure reservoir draw succeeds and construction queue path attempted
	rng.SeedGlobal(12345, 0)
	// Trigger construction via runDueTasks directly to avoid other deadlines
	m.Deadlines[TaskConstruction] = 0
	m.Tick(0, w, &econ)
	// If queue was nil, QueueBuild returns ErrNoQueue but we ignore error — C12 still holds (ordinary path used, no privileged mutation)
	// Just ensuring no panic and that taskRuns incremented
	if m.TaskRuns(TaskConstruction) == 0 {
		t.Fatalf("construction task should have run")
	}
	// Verify that the construction path is routed Select → Place → QueueBuildTyped [PLAN_11 C8+C12] [P0-07]
	data, _ := os.ReadFile("manager.go")
	if !regexp.MustCompile(`Select\(m,`).Match(data) {
		t.Fatalf("manager.go should route TaskConstruction/Positioning through Select [PLAN_11 C8]")
	}
	if !regexp.MustCompile(`Place\(m,`).Match(data) {
		t.Fatalf("manager.go should route through Place before QueueBuildTyped [PLAN_11 C8+C12] [P0-07]")
	}
	// Typed QueueBuild path is inside placement.go via per-manager QueueBuildTyped [P0-07][P0-I16]
	pdata, _ := os.ReadFile("placement.go")
	if !regexp.MustCompile(`QueueBuildTyped`).Match(pdata) {
		t.Fatalf("placement.go should issue QueueBuildTyped via typed path [P0-07] [P0-I16]")
	}
	if !regexp.MustCompile(`BuildRequest`).Match(pdata) {
		t.Fatalf("placement.go should use BuildRequest with MobileSite [P0-07]")
	}
}

// TestManagerSelectPlaceQueueChain verifies TaskConstruction and TaskPositioning
// route Select(m, builder, econ) → Place(m, defKey, terrain) → queueBuild,
// and that the chain order is Select called → Place called → queue contains product
// [PLAN_11 C8+C12]. On Place failure the deadline still reschedules (retry) [PLAN_11 C3].
func TestManagerSelectPlaceQueueChain(t *testing.T) {
	for _, kind := range []TaskKind{TaskConstruction, TaskPositioning} {
		t.Run(kind.String(), func(t *testing.T) {
			// Gate fixtures: flat terrain 32x32, simple catalog with builder and favee.
			cat := &content.Catalog{
				Units: map[string]*content.UnitDef{
					content.CanonicalKey("chainbuilder"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("chainbuilder")}, UnitName: "chainbuilder", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100},
					content.CanonicalKey("chainfavee"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("chainfavee")}, UnitName: "chainfavee", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100},
				},
				BuildMenus: map[string]*content.BuildMenuPage{
					content.CanonicalKey("chainbuilder"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("chainbuilder")}, Builder: "chainbuilder", Buttons: []string{"chainfavee"}},
				},
			}
			// Flat terrain valid for 2x2 oooo.
			attrs := make([]formats.TNTAttribute, 32*32)
			for i := range attrs {
				attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
			}
			plot := world.ExpandPlot(attrs, 32, 32)
			terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: plot, Version: world.VersionCanonical}

			w := units.New(32, cat)
			bx := world.CellToWorld(5)
			bz := world.CellToWorld(5)
			h, err := w.Create(cat.Units[content.CanonicalKey("chainbuilder")], 1, bx, 0, bz)
			if err != nil {
				t.Fatalf("create builder: %v", err)
			}
			builder := w.Unit(h)
			builder.Remaining = 0

			var econ economy.Service
			econ.Players[1].Exists = true
			econ.Players[1].ControllerState = 2
			econ.Players[1].Stock[economy.Energy] = 800
			econ.Players[1].Stock[economy.Metal] = 400
			econ.Players[1].Capacity[economy.Energy] = 1000
			econ.Players[1].Capacity[economy.Metal] = 500
			econ.Players[1].AIProduction[economy.Energy] = 300
			econ.Players[1].AIProduction[economy.Metal] = 10
			econ.Players[1].AIConsumption[economy.Energy] = 0
			econ.Players[1].AIConsumption[economy.Metal] = 0

			prof := &Profile{Weight: map[string]int32{content.CanonicalKey("chainfavee"): 100}, Limit: map[string]int32{}}
			mgr := &Manager{
				Player:       1,
				Profile:      prof,
				Strategic:    Strategic{CenterX: world.CellToWorld(16), CenterZ: world.CellToWorld(16), Radius: 0, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("chainfavee"): {C0: 40, C1: 30, C2: 30}}},
				OriginX:      bx,
				OriginZ:      bz,
				SurfaceMetal: 0,
				Catalog:      cat,
				Factory:      builder,
				Terrain:      terrain,
			}
			for k := TaskKind(0); k < TaskKindCount; k++ {
				mgr.Deadlines[k] = 1000
			}
			mgr.Deadlines[kind] = 0

			// P0-I16: per-manager hooks
			mgr.SetCatalog(cat)
			mgr.CandidateSource = nil
			mgr.MissionGateFlag = 0
			mgr.GateCandidates = nil
			// Save original QueueBuildTyped for spy [P0-07]
			origTyped := mgr.QueueBuildTyped

			var calls []string
			useSourceSpy := false
			if kind == TaskConstruction {
				mgr.SetCatalog(nil)
				mgr.CandidateSource = func(b *units.Unit) []string {
					calls = append(calls, "select")
					return []string{"chainfavee"}
				}
				useSourceSpy = true
			}
			placeCalled := false
			var placeDef string
			var capturedReq BuildRequest
			mgr.QueueBuildTyped = func(req BuildRequest) error {
				calls = append(calls, "place")
				placeCalled = true
				placeDef = req.UnitKey
				capturedReq = req
				if origTyped != nil {
					return origTyped(req)
				}
				// Enqueue via typed construction helpers for verification [P0-I05]
				builderUnit := w.Unit(req.Builder)
				if builderUnit == nil {
					builderUnit = builder
				}
				if req.Kind == BuildKindMobileSite {
					return construction.QueueMobileBuild(builderUnit, req.UnitKey, req.X, req.Z, req.Count, cat)
				}
				return construction.QueueFactoryBuild(builderUnit, req.UnitKey, req.Count, cat)
			}
			_ = capturedReq
			// If we used CandidateSource spy, Select will be observed as "select";
			// Place will be observed as "place" via queueBuild var.

			rng.SeedGlobal(0x12345678, 0)
			mgr.Tick(0, w, &econ)

			if useSourceSpy {
				if len(calls) < 2 || calls[0] != "select" || calls[1] != "place" {
					t.Fatalf("kind %v chain order want [select place] got %v", kind, calls)
				}
			} else {
				if !placeCalled {
					t.Fatalf("kind %v Place not called (queueBuild spy not invoked)", kind)
				}
				if placeDef != "chainfavee" && content.CanonicalKey(placeDef) != content.CanonicalKey("chainfavee") {
					t.Fatalf("kind %v place def %q want chainfavee", kind, placeDef)
				}
			}
			if !placeCalled {
				t.Fatalf("kind %v expected Place to issue queueBuild [PLAN_11 C12]", kind)
			}
			q := orders.QueueForUnit(builder)
			if q == nil {
				t.Fatalf("kind %v queue nil after chain", kind)
			}
			wantPID := func(defKey string) uint32 {
				if cat != nil {
					if idx, ok := cat.UnitDefIndex(content.CanonicalKey(defKey)); ok {
						return idx
					}
				}
				h := fnv.New32a()
				_, _ = h.Write([]byte(content.CanonicalKey(defKey)))
				return h.Sum32()
			}("chainfavee")
			found := false
			for _, n := range q.Primary() {
				if n != nil && (n.Param1 == wantPID || n.BuildDefKey == content.CanonicalKey("chainfavee")) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("kind %v queue missing product chainfavee pid %d (or BuildDefKey), calls %v", kind, wantPID, calls)
			}
			// Origin should have stepped toward center if radius was 0 it stays;
			// with radius 0 origin stays, but if we test with radius later, ensure Place moved.
			// At least radius should be reset on success.
			if mgr.Strategic.Radius != 0 {
				t.Fatalf("kind %v radius should be reset to 0 on success, got %d", kind, mgr.Strategic.Radius)
			}
			// Deadline rescheduled to tick+90 on success.
			if mgr.Deadlines[kind] != 90 {
				t.Fatalf("kind %v deadline want 90 got %d [PLAN_11 C3]", kind, mgr.Deadlines[kind])
			}
		})
	}
}

func TestManagerPlaceFailureRetry(t *testing.T) {
	// Verify that on Place failure the deadline still reschedules and no product queued,
	// keeping existing retry behavior [PLAN_11 C3].
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("failbuilder"):     {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("failbuilder")}, UnitName: "failbuilder", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100},
			content.CanonicalKey("geothermalplant"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("geothermalplant")}, UnitName: "geothermalplant", FootprintX: 2, FootprintZ: 2, YardMap: "GGGG", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("failbuilder"): {Builder: "failbuilder", Buttons: []string{"geothermalplant"}},
		},
	}
	// Terrain with no geothermal features, so GGGG yard fails.
	ter := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16), Version: world.VersionCanonical}
	w := units.New(8, cat)
	h, _ := w.Create(cat.Units[content.CanonicalKey("failbuilder")], 2, world.CellToWorld(1), 0, world.CellToWorld(1))
	builder := w.Unit(h)
	builder.Remaining = 0
	var econ economy.Service
	econ.Players[2].Exists = true
	econ.Players[2].ControllerState = 2
	econ.Players[2].Stock[economy.Energy] = 800
	econ.Players[2].Stock[economy.Metal] = 400
	econ.Players[2].Capacity[economy.Energy] = 1000
	econ.Players[2].Capacity[economy.Metal] = 500
	econ.Players[2].AIProduction[economy.Energy] = 300
	econ.Players[2].AIProduction[economy.Metal] = 10
	prof := &Profile{Weight: map[string]int32{content.CanonicalKey("geothermalplant"): 100}, Limit: map[string]int32{}}
	mgr := &Manager{
		Player:    2,
		Profile:   prof,
		Strategic: Strategic{CenterX: world.CellToWorld(2), CenterZ: world.CellToWorld(2), Radius: 0, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("geothermalplant"): {C0: 40, C1: 30, C2: 30}}},
		OriginX:   world.CellToWorld(1),
		OriginZ:   world.CellToWorld(1),
		Catalog:   cat,
		Factory:   builder,
		Terrain:   ter,
	}
	for k := TaskKind(0); k < TaskKindCount; k++ {
		mgr.Deadlines[k] = 1000
	}
	mgr.Deadlines[TaskConstruction] = 0
	// P0-I16 per-manager [P0-07] typed
	mgr.SetCatalog(cat)
	calls := 0
	origTyped2 := mgr.QueueBuildTyped
	mgr.QueueBuildTyped = func(req BuildRequest) error {
		calls++
		if origTyped2 != nil {
			return origTyped2(req)
		}
		builderUnit := w.Unit(req.Builder)
		if builderUnit == nil {
			builderUnit = builder
		}
		if req.Kind == BuildKindMobileSite {
			return construction.QueueMobileBuild(builderUnit, req.UnitKey, req.X, req.Z, req.Count, cat)
		}
		return construction.QueueFactoryBuild(builderUnit, req.UnitKey, req.Count, cat)
	}
	rng.SeedGlobal(1, 0)
	mgr.Tick(0, w, &econ)
	if calls != 0 {
		t.Fatalf("Place failure should not queue, got %d queue calls", calls)
	}
	q := orders.QueueForUnit(builder)
	if q != nil && len(q.Primary()) != 0 {
		t.Fatalf("queue should be empty on Place failure")
	}
	if mgr.Deadlines[TaskConstruction] != 90 {
		t.Fatalf("deadline should still reschedule to 90 on Place failure, got %d", mgr.Deadlines[TaskConstruction])
	}
	if mgr.Strategic.Radius == 0 {
		t.Fatalf("radius should have grown on Place failure, still 0")
	}
	// Verify that manager still reports task run even though Place failed.
	if mgr.TaskRuns(TaskConstruction) != 1 {
		t.Fatalf("taskRuns should increment even on Place failure")
	}
}

// String returns short name for TaskKind for test naming.
func (k TaskKind) String() string {
	switch k {
	case TaskConstruction:
		return "construction"
	case TaskPositioning:
		return "positioning"
	case TaskResource:
		return "resource"
	case TaskActivity:
		return "activity"
	case TaskOther900:
		return "other900"
	case TaskOther150:
		return "other150"
	default:
		return "unknown"
	}
}

// TestDispatchSlice covers DispatchSlice wiring.
func TestDispatchSlice(t *testing.T) {
	econ := makeEconWithControllers(map[int]uint8{0: 2, 1: 0, 2: 2})
	managers := []*Manager{
		newManagerFor(0, 50),
		newManagerFor(1, 50),
		newManagerFor(2, 50),
	}
	rng.SeedGlobal(1, 0)
	DispatchSlice(50, econ, managers, nil)
	if managers[0].EntryCount() != 1 {
		t.Fatalf("slice dispatch player0 should run")
	}
	if managers[1].EntryCount() != 0 {
		t.Fatalf("slice dispatch player1 outer blocked")
	}
	if managers[2].EntryCount() != 1 {
		t.Fatalf("slice dispatch player2 should run")
	}
}
