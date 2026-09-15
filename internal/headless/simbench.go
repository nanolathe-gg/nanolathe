// Simulation-cost benchmark: a displayless, bounded run of one dense
// three-army battle whose authoritative ticks are the only thing measured.
// Everything in this file is host-side — a fixture, a wall clock and a report.
// No simulation behavior lives here [I6].
package headless

import (
	"encoding/json"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/platform/benchlock"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// SimBenchDefaultMap is the scene's map. Of the stock maps large enough for
// three 250-unit armies, this is the one whose flattest three-army triangle
// scores a perfect placement heuristic AND carries a full feature table (about
// six thousand plots, two thirds of them flammable), so the armies march on
// traversable ground and the feature lifecycle — obstruction, wreckage, fire —
// is exercised at the same time. A map with a bare feature table measures a
// materially cheaper publication and feature phase.
const SimBenchDefaultMap = "Town & Country"

// Scene defaults. The warm-up covers the AI's first classification sweep
// (every thirty eligible manager entries), the first construction, resource
// and wave deadlines, and the march that brings the three armies into contact
// around tick one thousand; the measured window then runs from battle onset
// through sustained engagement while all three armies are still at full
// strength.
const (
	SimBenchDefaultWarmupTicks  uint32 = 1200
	SimBenchDefaultMeasureTicks uint32 = 3000
	SimBenchDefaultUnitLimit           = 400
	SimBenchDefaultCensusCount         = 7
)

// SimBenchOptions is the whole benchmark request.
type SimBenchOptions struct {
	Gameplay     gameplay.Mode `json:"gameplay"`
	Root         string
	Roots        []string
	OutputDir    string
	Map          string
	Seed         uint32
	Difficulty   int
	UnitLimit    int
	WarmupTicks  uint32
	MeasureTicks uint32
	CensusCount  int
	// PhaseTiming installs the host phase observer. It costs one wall-clock
	// read per phase boundary; the authoritative run is identical either way,
	// which the report proves by carrying the fingerprint.
	PhaseTiming bool
	// Profiles writes cpu.pprof and the allocation pair for the measured
	// window only.
	Profiles bool
	Log      io.Writer
}

func (o *SimBenchOptions) applyDefaults() {
	if o.Map == "" {
		o.Map = SimBenchDefaultMap
	}
	if o.UnitLimit == 0 {
		o.UnitLimit = SimBenchDefaultUnitLimit
	}
	if o.WarmupTicks == 0 {
		o.WarmupTicks = SimBenchDefaultWarmupTicks
	}
	if o.MeasureTicks == 0 {
		o.MeasureTicks = SimBenchDefaultMeasureTicks
	}
	if o.CensusCount <= 0 {
		o.CensusCount = SimBenchDefaultCensusCount
	}
}

// SimBenchTeamCensus is one owner's row of one census sample.
type SimBenchTeamCensus struct {
	Player          int        `json:"player"`
	LiveUnits       int        `json:"live_units"`
	Mobiles         int        `json:"mobiles"`
	Buildings       int        `json:"buildings"`
	Moving          int        `json:"moving"`
	PathRequests    int        `json:"path_requests"`
	Nanoframes      int        `json:"nanoframes"`
	OrderNodes      int        `json:"order_nodes"`
	AttackOrders    int        `json:"attack_orders"`
	MoveOrders      int        `json:"move_orders"`
	BuildOrders     int        `json:"build_orders"`
	FactoryBuilding int        `json:"factory_building"`
	FactoryQueued   int        `json:"factory_queued"`
	Kills           int        `json:"kills"`
	Losses          int        `json:"losses"`
	MetalStock      float32    `json:"metal_stock"`
	EnergyStock     float32    `json:"energy_stock"`
	AIGroups        [10]uint16 `json:"ai_groups"`
	AIDeadlines     []uint32   `json:"ai_deadlines"`
}

// SimBenchCensus proves what the measured window actually contained.
type SimBenchCensus struct {
	Tick            uint32               `json:"tick"`
	Phase           string               `json:"phase"`
	LiveUnits       int                  `json:"live_units"`
	Projectiles     int                  `json:"projectiles"`
	Effects         int                  `json:"effects"`
	Fragments       int                  `json:"fragments"`
	StripObjects    int                  `json:"strip_objects"`
	Features        int                  `json:"features"`
	FeaturesBurning int                  `json:"features_burning"`
	Events          int                  `json:"events"`
	Builds          int                  `json:"builds"`
	DeathsSoFar     int                  `json:"deaths_so_far"`
	Teams           []SimBenchTeamCensus `json:"teams"`
}

// SimBenchPhaseCost is one phase's share of the measured window.
type SimBenchPhaseCost struct {
	Phase         string  `json:"phase"`
	MillisPerTick float64 `json:"ms_per_tick"`
	SharePercent  float64 `json:"share_percent"`
}

// SimBenchRuntime records what the numbers were measured on.
type SimBenchRuntime struct {
	GoVersion  string `json:"go_version"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	NumCPU     int    `json:"num_cpu"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	Revision   string `json:"vcs_revision,omitempty"`
	Modified   bool   `json:"vcs_modified,omitempty"`
}

// SimBenchGC is the garbage-collector and allocation summary for the window.
// Every counter here is a DELTA between two MemStats reads, one taken as the
// window opens and one as it closes, except the two heap gauges (which are
// instantaneous by nature) and the lifetime fraction, whose name says so:
// runtime keeps GCCPUFraction over the whole process, so it cannot be
// differenced and must not be read as the window's GC share. Read the window's
// GC cost from runtime.gcBgMarkWorker in cpu.pprof instead.
type SimBenchGC struct {
	NumGC                 uint32  `json:"num_gc"`
	GCPauseTotalMs        float64 `json:"gc_pause_total_ms"`
	GCCPUFractionLifetime float64 `json:"gc_cpu_fraction_lifetime"`
	HeapAllocStart        uint64  `json:"heap_alloc_start_bytes"`
	HeapAllocEnd          uint64  `json:"heap_alloc_end_bytes"`
	HeapSysEnd            uint64  `json:"heap_sys_end_bytes"`
	BytesPerTick          float64 `json:"alloc_bytes_per_tick"`
	ObjectsPerTick        float64 `json:"alloc_objects_per_tick"`
	TotalAllocBytes       uint64  `json:"alloc_bytes_total"`
	TotalAllocObjs        uint64  `json:"alloc_objects_total"`
}

// SimBenchReport is the whole run: scene, timings, census and provenance.
type SimBenchReport struct {
	Gameplay           gameplay.Mode       `json:"gameplay"`
	SceneVersion       int                 `json:"scene_version"`
	Map                string              `json:"map"`
	SimulationSeed     uint32              `json:"simulation_seed"`
	CRTSeed            uint32              `json:"crt_seed"`
	Difficulty         int                 `json:"difficulty"`
	WarmupTicks        uint32              `json:"warmup_ticks"`
	MeasuredTicks      uint32              `json:"measured_ticks"`
	PhaseTiming        bool                `json:"phase_timing"`
	Scene              *SimBenchScene      `json:"scene"`
	CatalogHash        string              `json:"catalog_hash,omitempty"`
	InitialFingerprint string              `json:"initial_fingerprint"`
	WarmFingerprint    string              `json:"warm_fingerprint"`
	FinalFingerprint   string              `json:"final_fingerprint"`
	SimulationDraws    uint64              `json:"simulation_draws"`
	CRTDraws           uint64              `json:"crt_draws"`
	State              string              `json:"state"`
	TicksPerSecond     float64             `json:"ticks_per_second"`
	WallMillis         float64             `json:"wall_ms"`
	MillisPerTickMean  float64             `json:"ms_per_tick_mean"`
	MillisPerTickP50   float64             `json:"ms_per_tick_p50"`
	MillisPerTickP95   float64             `json:"ms_per_tick_p95"`
	MillisPerTickP99   float64             `json:"ms_per_tick_p99"`
	MillisPerTickMax   float64             `json:"ms_per_tick_max"`
	Phases             []SimBenchPhaseCost `json:"phases,omitempty"`
	GC                 SimBenchGC          `json:"gc"`
	Runtime            SimBenchRuntime     `json:"runtime"`
	Census             []SimBenchCensus    `json:"census"`
	// TickMillis and TickRestamps are parallel per-tick series over the
	// measured window: how long the tick took, and how many end-to-end
	// movement class-layer rebuilds ran inside it. Sampling the counter is a
	// host read of a plain uint64 between ticks; it allocates nothing and the
	// simulation never sees it [I6].
	TickMillis   []float64 `json:"tick_ms"`
	TickRestamps []uint32  `json:"tick_restamps,omitempty"`
	// Restamps summarises those two series so a run states the attribution
	// instead of leaving it to be inferred.
	Restamps SimBenchRestampCorrelation `json:"restamps"`
}

// SimBenchRestampCorrelation relates the window's slow ticks to the class-layer
// full rebuilds. SlowMillis is the threshold the two "slow" counters use.
type SimBenchRestampCorrelation struct {
	Total             uint64  `json:"total"`
	TicksWithRestamp  int     `json:"ticks_with_restamp"`
	SlowMillis        float64 `json:"slow_threshold_ms"`
	SlowTicks         int     `json:"slow_ticks"`
	SlowWithRestamp   int     `json:"slow_ticks_with_restamp"`
	FastWithRestamp   int     `json:"fast_ticks_with_restamp"`
	MeanMillisWith    float64 `json:"mean_ms_with_restamp"`
	MeanMillisWithout float64 `json:"mean_ms_without_restamp"`
}

// RunSimBenchmark composes the scene, warms it up, then measures a fixed
// window of authoritative ticks. The output directory must not already exist,
// the same convention the windowed benchmark uses, so a comparison can never
// silently mix two runs' artifacts.
func RunSimBenchmark(opts SimBenchOptions) (SimBenchReport, error) {
	opts.applyDefaults()
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	if opts.OutputDir != "" {
		lockPath, err := benchlock.Path()
		if err != nil {
			return SimBenchReport{}, fmt.Errorf("nanolathe: sim benchmark lock path: %w", err)
		}
		held, err := benchlock.Acquire(lockPath, func() {
			fmt.Fprintln(log, "nanolathe: waiting for another benchmark run to finish")
		})
		if err != nil {
			return SimBenchReport{}, fmt.Errorf("nanolathe: sim benchmark lock: %w", err)
		}
		defer held.Close()
		if err := os.MkdirAll(filepath.Dir(opts.OutputDir), 0o755); err != nil {
			return SimBenchReport{}, err
		}
		if err := os.Mkdir(opts.OutputDir, 0o755); err != nil {
			return SimBenchReport{}, fmt.Errorf("nanolathe: create fresh benchmark directory: %w", err)
		}
	}

	fs, err := mountContentRoots(opts.Root, opts.Roots)
	if err != nil {
		return SimBenchReport{}, err
	}
	defer fs.Close()

	catalog, err := content.Compile(fs)
	if err != nil {
		return SimBenchReport{}, diagnostic("catalog compile failed: "+err.Error(), opts.Map, providerNames(fs), "a complete compiled catalog")
	}
	return runSimBenchmarkWithContent(opts, fs, catalog, log)
}

func runSimBenchmarkWithContent(opts SimBenchOptions, fs vfs.FSOps, catalog *content.Catalog, log io.Writer) (SimBenchReport, error) {
	composed, scene, err := ComposeSimBenchBattle(opts, fs, catalog)
	if err != nil {
		return SimBenchReport{}, err
	}
	sess := composed.Session
	report := SimBenchReport{
		Gameplay:     opts.Gameplay.Normalize(),
		SceneVersion: SimBenchSceneVersion, Map: opts.Map,
		SimulationSeed: opts.Seed, CRTSeed: opts.Seed, Difficulty: opts.Difficulty,
		WarmupTicks: opts.WarmupTicks, MeasuredTicks: opts.MeasureTicks,
		PhaseTiming: opts.PhaseTiming, Scene: scene,
		InitialFingerprint: composed.InitialFingerprint,
		Runtime:            simBenchRuntime(),
	}
	if catalog != nil {
		report.CatalogHash = catalog.Hash
	}

	deaths := simBenchDeathCounter(sess)
	fmt.Fprintf(log, "nanolathe: sim benchmark scene %d on %q, warm-up %d ticks\n", SimBenchSceneVersion, opts.Map, opts.WarmupTicks)
	for tick := uint32(0); tick < opts.WarmupTicks; tick++ {
		simBenchStep(sess)
	}
	report.WarmFingerprint, _ = sess.PartialStateFingerprint()

	timing := newSimBenchPhaseTimer()
	if opts.PhaseTiming {
		sess.PhaseObserver = timing.observe
	}
	censusAt := simBenchCensusTicks(opts.MeasureTicks, opts.CensusCount)
	report.Census = append(report.Census, simBenchTakeCensus(sess, scene, "window-open", deaths))

	stopCPU := func() {}
	if opts.OutputDir != "" && opts.Profiles {
		if err := writeAllocProfile(filepath.Join(opts.OutputDir, "alloc-base.pprof")); err != nil {
			return report, err
		}
		stopCPU, err = startCPUProfile(filepath.Join(opts.OutputDir, "cpu.pprof"))
		if err != nil {
			return report, err
		}
	}

	var startStats, endStats runtime.MemStats
	runtime.ReadMemStats(&startStats)
	millis := make([]float64, 0, opts.MeasureTicks)
	restamps := make([]uint32, 0, opts.MeasureTicks)
	lastStamps := simBenchRestampCounter(sess)
	next := 0
	windowStart := time.Now()
	for tick := uint32(0); tick < opts.MeasureTicks; tick++ {
		timing.beginTick()
		before := time.Now()
		simBenchStep(sess)
		elapsed := time.Since(before)
		timing.endTick(before, elapsed)
		millis = append(millis, float64(elapsed.Nanoseconds())/1e6)
		// Sampled between ticks, after the wall clock is stopped, so reading
		// the counter is never inside a measurement.
		stamps := simBenchRestampCounter(sess)
		restamps = append(restamps, uint32(stamps-lastStamps))
		lastStamps = stamps
		if next < len(censusAt) && tick+1 == censusAt[next] {
			report.Census = append(report.Census, simBenchTakeCensus(sess, scene, "window", deaths))
			next++
		}
		if sess.State == session.StatePostBattle {
			report.MeasuredTicks = tick + 1
			break
		}
	}
	wall := time.Since(windowStart)
	runtime.ReadMemStats(&endStats)
	stopCPU()
	sess.PhaseObserver = nil
	report.Census = append(report.Census, simBenchTakeCensus(sess, scene, "window-close", deaths))

	if opts.OutputDir != "" && opts.Profiles {
		if err := writeAllocProfile(filepath.Join(opts.OutputDir, "alloc.pprof")); err != nil {
			return report, err
		}
	}

	report.FinalFingerprint, _ = sess.PartialStateFingerprint()
	if simulation := sess.SimRNG(); simulation != nil {
		report.SimulationDraws = simulation.Draws()
	}
	if crt := sess.CrtRNG(); crt != nil {
		report.CRTDraws = crt.Draws()
	}
	report.State = sess.State.String()
	report.TickMillis = millis
	report.TickRestamps = restamps
	report.Restamps = summarizeSimBenchRestamps(millis, restamps)
	report.WallMillis = float64(wall.Nanoseconds()) / 1e6
	if wall > 0 {
		report.TicksPerSecond = float64(len(millis)) / wall.Seconds()
	}
	summarizeSimBenchTicks(&report, millis)
	report.Phases = timing.summary(len(millis))
	report.GC = simBenchGC(startStats, endStats, len(millis))

	if opts.OutputDir != "" {
		if err := writeSimBenchReport(filepath.Join(opts.OutputDir, "sim-bench.json"), report); err != nil {
			return report, err
		}
	}
	fmt.Fprintf(log, "nanolathe: %d measured ticks in %s (%.1f ticks/s, median %.3f ms, p95 %.3f ms)\n",
		len(millis), wall.Round(time.Millisecond), report.TicksPerSecond, report.MillisPerTickP50, report.MillisPerTickP95)
	return report, nil
}

// ComposeSimBenchBattle builds the session and places the scene without
// running a tick. It is exported so a test can compose twice and compare
// fingerprints without measuring anything.
func ComposeSimBenchBattle(opts SimBenchOptions, fs vfs.FSOps, catalog *content.Catalog) (FreshBattle, *SimBenchScene, error) {
	opts.applyDefaults()
	cfg := simBenchConfig(opts.Map, opts.UnitLimit)
	composed, err := ComposeFreshBattle(FreshBattleRequest{
		Gameplay: opts.Gameplay,
		Kind:     ScenarioDirectOTA, Map: opts.Map, LocalOwner: -1,
		Difficulty: opts.Difficulty, Skirmish: cfg,
		SimulationSeed: opts.Seed, CRTSeed: opts.Seed,
		FS: fs, Catalog: catalog,
	})
	if err != nil {
		return FreshBattle{}, nil, err
	}
	scene, err := buildSimBenchScene(composed.Session, opts.Map, opts.UnitLimit)
	if err != nil {
		return FreshBattle{}, nil, err
	}
	return composed, scene, nil
}

// simBenchStep advances exactly one authoritative tick. One Step call per tick
// is what the windowed host does, and it keeps the once-per-pump executor tail
// on the same cadence as the tick it follows [01 §4.4].
//
// The drain destination is retained. Handing the drain a nil slice makes it
// allocate the whole retained queue every tick, and that is the benchmark
// HOST's allocation showing up in the simulation's own allocation profile --
// it was 2.4% of the measured window. The windowed host already keeps a
// buffer; this one now does too.
func simBenchStep(sess *session.Session) {
	sess.Step(sess.Clock.ScaledAnchor + 1)
	if sess.Snapshot != nil {
		simBenchDrained = sess.Snapshot.DrainCommittedEvents(simBenchDrained)
	}
}

// simBenchDrained is the host's event sink. The benchmark discards what it
// drains -- there is no presentation consumer -- and the simulation never
// reads it, so one buffer for the whole run is the whole of its lifetime [I6].
var simBenchDrained []frame.EventView

// simBenchPhaseTimer accumulates the wall time between phase boundaries. The
// callback receives the phases of one tick in registry order [I7], so the slot
// index is a counter rather than a name lookup: no map, no string compare, and
// no allocation on the measured path.
type simBenchPhaseTimer struct {
	names  [13]string
	totals [13]time.Duration
	index  int
	last   time.Time
	active bool
}

func newSimBenchPhaseTimer() *simBenchPhaseTimer { return &simBenchPhaseTimer{} }

func (t *simBenchPhaseTimer) beginTick() {
	t.index = 0
	t.last = time.Now()
	t.active = true
}

func (t *simBenchPhaseTimer) observe(phase string, _ uint32) {
	if !t.active || t.index >= len(t.totals)-1 {
		return
	}
	now := time.Now()
	t.totals[t.index] += now.Sub(t.last)
	t.names[t.index] = phase
	t.last = now
	t.index++
}

// endTick attributes everything after the last phase boundary — the sharing
// tail, result evaluation, publication and the once-per-pump executor tail —
// to a final slot [01 §4.4].
func (t *simBenchPhaseTimer) endTick(start time.Time, elapsed time.Duration) {
	if !t.active {
		return
	}
	slot := len(t.totals) - 1
	t.names[slot] = "tail-sharing-result-publication"
	t.totals[slot] += start.Add(elapsed).Sub(t.last)
	t.active = false
}

func (t *simBenchPhaseTimer) summary(ticks int) []SimBenchPhaseCost {
	if t == nil || ticks == 0 {
		return nil
	}
	var total time.Duration
	for _, d := range t.totals {
		total += d
	}
	if total == 0 {
		return nil
	}
	out := make([]SimBenchPhaseCost, 0, len(t.totals))
	for i, d := range t.totals {
		if t.names[i] == "" {
			continue
		}
		out = append(out, SimBenchPhaseCost{
			Phase:         t.names[i],
			MillisPerTick: float64(d.Nanoseconds()) / 1e6 / float64(ticks),
			SharePercent:  100 * float64(d) / float64(total),
		})
	}
	return out
}

// simBenchDeathCounter chains the unit world's death hook so the census can
// report cumulative deaths. It observes; it does not decide anything.
func simBenchDeathCounter(sess *session.Session) *int {
	count := new(int)
	if sess == nil || sess.Units == nil {
		return count
	}
	previous := sess.Units.OnDeath
	sess.Units.OnDeath = func(handle pool.Handle, cause units.DeathCause, unit *units.Unit) {
		*count++
		if previous != nil {
			previous(handle, cause, unit)
		}
	}
	return count
}

func simBenchCensusTicks(measured uint32, samples int) []uint32 {
	if samples <= 2 || measured == 0 {
		return nil
	}
	out := make([]uint32, 0, samples-2)
	for i := 1; i < samples-1; i++ {
		out = append(out, measured*uint32(i)/uint32(samples-1))
	}
	return out
}

func simBenchTakeCensus(sess *session.Session, scene *SimBenchScene, phase string, deaths *int) SimBenchCensus {
	out := SimBenchCensus{Tick: sess.Clock.GlobalTick, Phase: phase}
	if deaths != nil {
		out.DeathsSoFar = *deaths
	}
	if sess.Snapshot != nil {
		published := sess.Snapshot.Current()
		if published != nil {
			out.Projectiles = len(published.Projectiles)
			out.Effects = len(published.Effects)
			out.Fragments = len(published.Fragments)
			out.StripObjects = len(published.Strips)
			out.Features = len(published.Features)
			out.Events = len(published.Events)
			out.Builds = len(published.Builds)
			for i := range published.Features {
				if published.Features[i].IsBurning {
					out.FeaturesBurning++
				}
			}
		}
	}
	rows := make([]SimBenchTeamCensus, simBenchTeams)
	for t := range rows {
		rows[t].Player = simBenchFirstAISlot + t
	}
	for _, unit := range sess.Units.IterSliced() {
		if unit == nil || !unit.Alive || unit.Def == nil {
			continue
		}
		team := int(unit.Owner) - simBenchFirstAISlot
		if team < 0 || team >= len(rows) {
			continue
		}
		row := &rows[team]
		out.LiveUnits++
		row.LiveUnits++
		if unit.Def.CanMove || unit.Def.CanFly {
			row.Mobiles++
		} else {
			row.Buildings++
		}
		if unit.Move.Speed != 0 || unit.Move.VelX != 0 || unit.Move.VelZ != 0 {
			row.Moving++
		}
		if unit.Remaining > 0 {
			row.Nanoframes++
		}
		if sess.Path != nil && sess.Path.HasRequest(unit.Handle) {
			row.PathRequests++
		}
		countSimBenchOrders(unit, row)
	}
	for _, handle := range scene.simBenchFactoryHandles() {
		unit := sess.Units.Unit(handle)
		if unit == nil || !unit.Alive {
			continue
		}
		team := int(unit.Owner) - simBenchFirstAISlot
		if team < 0 || team >= len(rows) {
			continue
		}
		queue := orders.QueueOfUnit(unit)
		if queue == nil {
			continue
		}
		for _, node := range queue.Primary() {
			if node == nil || node.BuildDefKey == "" {
				continue
			}
			rows[team].FactoryQueued += int(node.Param2)
			if node.Phase != 0 {
				rows[team].FactoryBuilding++
			}
			break
		}
	}
	for t := range rows {
		player := simBenchFirstAISlot + t
		if sess.Econ != nil && player < len(sess.Econ.Players) {
			slot := &sess.Econ.Players[player]
			rows[t].Kills = int(slot.Kills)
			rows[t].Losses = int(slot.Losses)
			rows[t].MetalStock = slot.Stock[0]
			rows[t].EnergyStock = slot.Stock[1]
		}
		if manager := sess.AI[player]; manager != nil {
			rows[t].AIGroups = groupCounts(manager)
			rows[t].AIDeadlines = append([]uint32(nil), manager.Deadlines[:]...)
		}
	}
	out.Teams = rows
	return out
}

func countSimBenchOrders(unit *units.Unit, row *SimBenchTeamCensus) {
	queue := orders.QueueOfUnit(unit)
	if queue == nil {
		return
	}
	for _, nodes := range [2][]*orders.Node{queue.Primary(), queue.Secondary()} {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			row.OrderNodes++
			name := descriptorName(node.ID)
			switch {
			case node.BuildDefKey != "":
				row.BuildOrders++
			case isAttackIntent(name):
				row.AttackOrders++
			case name == "Move_Ground" || name == "Move" || name == "Move_Air":
				row.MoveOrders++
			}
		}
	}
}

func summarizeSimBenchTicks(report *SimBenchReport, millis []float64) {
	if len(millis) == 0 {
		return
	}
	sorted := append([]float64(nil), millis...)
	sort.Float64s(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	report.MillisPerTickMean = sum / float64(len(sorted))
	report.MillisPerTickP50 = percentileOf(sorted, 0.50)
	report.MillisPerTickP95 = percentileOf(sorted, 0.95)
	report.MillisPerTickP99 = percentileOf(sorted, 0.99)
	report.MillisPerTickMax = sorted[len(sorted)-1]
}

// simBenchRestampCounter reads the movement system's running total of
// end-to-end class-layer rebuilds. Host diagnostic: it allocates nothing,
// takes no lock and cannot reach the simulation [I6].
func simBenchRestampCounter(sess *session.Session) uint64 {
	if sess == nil || sess.Movement == nil {
		return 0
	}
	return sess.Movement.ClassLayerFullStamps()
}

// simBenchSlowTickMillis is the threshold the restamp correlation calls
// "slow". It is the elbow between the two modes the baseline distribution
// shows, not a budget: at 30 Hz a tick has 33.3 ms.
const simBenchSlowTickMillis = 15.0

// summarizeSimBenchRestamps answers the question the per-tick series exists to
// answer: do the slow ticks carry a full class-layer rebuild, and do rebuilds
// ever land in a fast tick?
func summarizeSimBenchRestamps(millis []float64, restamps []uint32) SimBenchRestampCorrelation {
	out := SimBenchRestampCorrelation{SlowMillis: simBenchSlowTickMillis}
	var withSum, withoutSum float64
	var withCount, withoutCount int
	for i := range millis {
		var stamps uint32
		if i < len(restamps) {
			stamps = restamps[i]
		}
		out.Total += uint64(stamps)
		slow := millis[i] > simBenchSlowTickMillis
		if slow {
			out.SlowTicks++
		}
		if stamps > 0 {
			out.TicksWithRestamp++
			withSum += millis[i]
			withCount++
			if slow {
				out.SlowWithRestamp++
			} else {
				out.FastWithRestamp++
			}
			continue
		}
		withoutSum += millis[i]
		withoutCount++
	}
	if withCount > 0 {
		out.MeanMillisWith = withSum / float64(withCount)
	}
	if withoutCount > 0 {
		out.MeanMillisWithout = withoutSum / float64(withoutCount)
	}
	return out
}

func percentileOf(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(q * float64(len(sorted)-1))
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func simBenchGC(start, end runtime.MemStats, ticks int) SimBenchGC {
	out := SimBenchGC{
		NumGC:                 end.NumGC - start.NumGC,
		GCPauseTotalMs:        float64(end.PauseTotalNs-start.PauseTotalNs) / 1e6,
		GCCPUFractionLifetime: end.GCCPUFraction,
		HeapAllocStart:        start.HeapAlloc,
		HeapAllocEnd:          end.HeapAlloc,
		HeapSysEnd:            end.HeapSys,
		TotalAllocBytes:       end.TotalAlloc - start.TotalAlloc,
		TotalAllocObjs:        end.Mallocs - start.Mallocs,
	}
	if ticks > 0 {
		out.BytesPerTick = float64(out.TotalAllocBytes) / float64(ticks)
		out.ObjectsPerTick = float64(out.TotalAllocObjs) / float64(ticks)
	}
	return out
}

func simBenchRuntime() SimBenchRuntime {
	out := SimBenchRuntime{
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				out.Revision = setting.Value
			case "vcs.modified":
				out.Modified = setting.Value == "true"
			}
		}
	}
	// A binary built inside a git worktree carries no VCS stamp, so the
	// wrapper passes the revision instead. Provenance only: nothing reads it
	// back, and it cannot reach the session.
	if out.Revision == "" {
		out.Revision = os.Getenv("NANOLATHE_BENCH_REVISION")
	}
	return out
}

func startCPUProfile(path string) (func(), error) {
	file, err := os.Create(path)
	if err != nil {
		return func() {}, fmt.Errorf("nanolathe: create CPU profile %q: %w", path, err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return func() {}, fmt.Errorf("nanolathe: start CPU profile %q: %w", path, err)
	}
	return func() {
		pprof.StopCPUProfile()
		file.Close()
	}, nil
}

func writeAllocProfile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("nanolathe: create allocation profile %q: %w", path, err)
	}
	if err := pprof.Lookup("allocs").WriteTo(file, 0); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write allocation profile %q: %w", path, err)
	}
	return file.Close()
}

func writeSimBenchReport(path string, report SimBenchReport) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("nanolathe: create sim benchmark report %q: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write sim benchmark report %q: %w", path, err)
	}
	return file.Close()
}
