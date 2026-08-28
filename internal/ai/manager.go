package ai

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TaskKind identifies one of the manager's ten fixed task slots. The order is
// load-bearing: resource/activity, wave A, regroup A, construction/positioning,
// the inert null task, wave B, regroup B, explore/gather, rally, and the final
// intentionally empty slot [08 "Strategy manager and its task graph"] [R-P0-04 §2].
type TaskKind int

const (
	TaskResource TaskKind = iota
	TaskWaveA
	TaskRegroupA
	TaskConstruction
	TaskNull
	TaskWaveB
	TaskRegroupB
	TaskExplore
	TaskRally
	TaskEmptySlot
	TaskKindCount
)

// Wave thresholds and limits [P0-02 §1.3][P0-02 §3.4].
const (
	waveAThreshold = 20000
	waveBThreshold = 50000
	waveMin        = 3
	waveMax        = 6
)

// TODO(T25): AI transport geometry (attachment, naval, air) remains blocked [PLAN_11 Explicit unknowns][P0-02]. No new T25 beyond this.

// Manager is the per-player AI manager [PLAN_11 Public API] [08 "Established AI-facing data and rooted planner"].
// It is session-owned and dispatched inside kernel phase 5's per-player coordinator
// via economy.TickPlayer's beforeDeadline callback — that AI-as-auxiliary-helper
// identification is supported inference from the shared entry address [08 "Established AI-facing data and rooted planner"] [05 "Authoritative settlement order"] [PLAN_11 C11].
type Manager struct {
	Player    uint8     // 0..9, 10 is sentinel never dispatched [08][PLAN_11 C1]
	Strategic Strategic // fixed-size retail strategic state, named fields per I13 [08][PLAN_11 C2]
	Deadlines [TaskKindCount]uint32
	Profile   *Profile

	// Placement extensions (not part of the retail manager record; Nanolathe
	// wiring state so ai.Place can stay func(m *Manager, ...) per PLAN_11 API).
	OriginX      numeric.Fixed // placement search origin, steps toward strategic center [PLAN_11 C8]
	OriginZ      numeric.Fixed
	SurfaceMetal int32            // mission SurfaceMetal, clamped 0..255 [08]
	Catalog      *content.Catalog // defKey resolution for the extractor gate [PLAN_11 C8]
	Factory      *units.Unit      // builder receiving construction requests [PLAN_11 C12] [P0-07]
	Terrain      *world.Terrain   // placement validation terrain; nil skips yard validation, success resets radius [PLAN_11 C8]
	// MissionGateFlag is the authored mission-mode input to selection's
	// definition gate [R-P0-05 §3].
	MissionGateFlag int32

	// P0-07: typed build request replacing lossy callback [P0-07] ON-06 F-P0-004.
	// Session binds this to construction queue; default nil → error diagnostic.
	QueueBuildTyped func(BuildRequest) error // typed mobile/factory build [P0-07]

	// P0-07: alliance awareness [P0-07] ON-06. Nil means same-owner-only (default) [08].
	IsAlliance func(a, b uint8) bool // alliance test injected at construction; default same-owner-only [P0-07]

	// Manager task vectors for tactical coordination. The retail manager owns
	// nine vector records; the classifier and direct group writer fill some of
	// them from the live unit pool [R-P0-04 §3]. Each slice holds pool handles
	// in deterministic vector order. Resource/construction/null are retained even though their
	// current task handlers do not consume the vectors, so the writer's
	// destination is not silently collapsed into another task.
	GroupResource     []pool.Handle // classifier destination 1
	GroupWaveA        []pool.Handle // classifier never emits 2
	GroupRegroupA     []pool.Handle // classifier destination 3
	GroupConstruction []pool.Handle // classifier destination 4
	GroupNull         []pool.Handle // classifier destination 5
	GroupWaveB        []pool.Handle // classifier never emits 6
	GroupRegroupB     []pool.Handle // classifier destination 7
	GroupExplore      []pool.Handle // classifier destination 8
	GroupRally        []pool.Handle // classifier never emits 9

	// countdown is the authoritative classification cadence. It starts lazily
	// at thirty for zero-value fixture managers, matching construction state.
	countdown uint8
	// unitLossDeadline is written by RecordUnitLoss and gates the corresponding
	// construction retry path when that definition-level gate is present [08].
	unitLossDeadline uint32

	// Rally task state is part of the task's mutable random walk. The score
	// source and formation effects remain unresolved and are intentionally not
	// synthesized here [08 "RNG sites for AI planning"].
	rallyTargetX   numeric.Fixed
	rallyTargetY   numeric.Fixed
	rallyTargetZ   numeric.Fixed
	rallyDriftX    numeric.Fixed
	rallyDriftY    numeric.Fixed
	rallyDriftZ    numeric.Fixed
	rallyScore     uint32 // last accepted score; produced by unresolved score helper
	rallyNextScore uint32 // candidate score; produced by unresolved score helper

	// RS-06: per-session isolated RNG [I4][RS-P0-018]. A nil stream is an
	// unbound setup and must not fall back to process-global randomness.
	RNG *rng.Simulation `json:"-"`
}

// GetPlayer satisfies Selector [PLAN_11 WU-11-4] — Manager.Player 0..9.
func (m *Manager) GetPlayer() uint8 { // [PLAN_11 WU-11-2] Selector surface
	if m == nil {
		return 0
	}
	return m.Player
}

// GetProfile satisfies Selector [PLAN_11 WU-11-4].
func (m *Manager) GetProfile() *Profile {
	if m == nil {
		return nil
	}
	return m.Profile
}

// GetStrategic satisfies Selector [PLAN_11 WU-11-4].
func (m *Manager) GetStrategic() *Strategic {
	if m == nil {
		return nil
	}
	return &m.Strategic
}

// GetCatalog satisfies the extended Selector for P0-I16.
func (m *Manager) GetCatalog() *content.Catalog {
	if m == nil {
		return nil
	}
	return m.Catalog
}

// GetMissionGateFlag supplies selection's authored mission-mode gate
// [R-P0-05 §3].
func (m *Manager) GetMissionGateFlag() int32 {
	if m == nil {
		return 0
	}
	return m.MissionGateFlag
}

// GetRNG satisfies Selector RNG extension — returns per-manager RNG when set for session isolation [RS-06][I4] DET-01.
// Production requires injected RNG; nil means no draw (no global fallback).
func (m *Manager) GetRNG() *rng.Simulation {
	if m != nil && m.RNG != nil {
		return m.RNG
	}
	return nil
}

// SetCatalog sets the catalog for both Manager and its Strategic state [P0-I16].
func (m *Manager) SetCatalog(cat *content.Catalog) {
	if m == nil {
		return
	}
	m.Catalog = cat
	m.Strategic.Catalog = cat
}

// RecordUnitLoss writes the unit-loss retry deadline. The draw is consumed
// only with a bound simulation stream; an unbound manager leaves its state
// unchanged, which is the fail-closed setup behavior [08 RNG inventory].
func (m *Manager) RecordUnitLoss(tick uint32) {
	if m == nil {
		return
	}
	r := m.simRNG()
	if r == nil {
		return
	}
	m.unitLossDeadline = tick + 30 + r.Uint32n(300)
}

// NotifyUnitLoss is the lifecycle-facing spelling used by session callers.
func (m *Manager) NotifyUnitLoss(tick uint32) { m.RecordUnitLoss(tick) }

// UnitLossDeadline exposes the current retry deadline for session wiring and
// deterministic tests without making diagnostic counters part of Manager.
func (m *Manager) UnitLossDeadline() uint32 {
	if m == nil {
		return 0
	}
	return m.unitLossDeadline
}

// EnsureStrategicInitialized lazily initializes class maps from catalog for each manager [P0-I12].
// It calls Strategic.Init with all catalog types if vectors are still zero/empty, ensuring vectors not zero.
func (m *Manager) EnsureStrategicInitialized() {
	if m == nil {
		return
	}
	if m.Catalog == nil && m.Strategic.Catalog == nil {
		return
	}
	cat := m.Catalog
	if cat == nil {
		cat = m.Strategic.Catalog
	}
	if cat == nil || cat.Units == nil || len(cat.Units) == 0 {
		return
	}
	types := make([]string, 0, len(cat.Units))
	for k := range cat.Units {
		types = append(types, k)
	}
	sort.Strings(types)
	if strategicMapsComplete(&m.Strategic, types) {
		return
	}
	// Rebuild all per-type maps through the established initializer. Preserve
	// any concrete class-vector values supplied by a caller for keys that are
	// still in the authored catalog; missing and extra keys are never accepted
	// by approximation [08 "Strategic state construction and refresh"].
	provided := make(map[string]ClassVector, len(m.Strategic.ClassVectors))
	for k, v := range m.Strategic.ClassVectors {
		provided[k] = v
	}
	m.Strategic.Counts = nil
	m.Strategic.ClassVectors = nil
	m.Strategic.InitVectors = nil
	m.Strategic.SingleVectors = nil
	m.Strategic.Catalog = cat
	m.Catalog = cat
	m.Strategic.Init(types)
	for k, v := range provided {
		if _, ok := m.Strategic.ClassVectors[k]; ok {
			m.Strategic.ClassVectors[k] = v
		}
	}
}

// strategicMapsComplete accepts only a complete initialization for the exact
// catalog key set. Partial or extra entries are rebuilt through Strategic.Init;
// a size-based approximation would make task selection depend on fixture or
// load order rather than the authored catalog [08 "Strategic state construction and refresh"].
func strategicMapsComplete(s *Strategic, types []string) bool {
	if s == nil || len(s.Counts) != len(types) || len(s.ClassVectors) != len(types) || len(s.InitVectors) != len(types) || len(s.SingleVectors) != len(types) {
		return false
	}
	for _, k := range types {
		if _, ok := s.Counts[k]; !ok {
			return false
		}
		if _, ok := s.ClassVectors[k]; !ok {
			return false
		}
		if _, ok := s.InitVectors[k]; !ok {
			return false
		}
		if _, ok := s.SingleVectors[k]; !ok {
			return false
		}
	}
	return true
}

// simRNG returns the per-session isolated RNG when set [RS-06][I4] DET-01.
// Production requires injected RNG; nil means no draw.
func (m *Manager) simRNG() *rng.Simulation {
	if m != nil && m.RNG != nil {
		return m.RNG
	}
	return nil
}

// hasBuildOptionsForDef reports whether def has build options via catalog.
func (m *Manager) hasBuildOptionsForDef(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	cat := m.Catalog
	if cat == nil {
		cat = m.Strategic.Catalog
	}
	if cat != nil && cat.BuildMenus != nil {
		ck := canonicalKey(def.UnitName)
		if page, ok := cat.BuildMenus[ck]; ok && page != nil && len(page.Buttons) > 0 {
			return true
		}
		if ck2 := canonicalKey(def.CanonicalKey); ck2 != ck {
			if page, ok := cat.BuildMenus[ck2]; ok && page != nil && len(page.Buttons) > 0 {
				return true
			}
		}
	}
	return false
}

// isOuterEligible reports whether the outer per-tick gate passes [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1].
// Retail iterates ten players 0..9, and for controller ∈ {1,2,3} and index !=10
// calls through the player's manager pointer.
// The Player==10 check is literal retail guard [08]; it never fires for 0..9 but is kept for fidelity.
// TODO(question): semantic names of controller values 1,2,3 unknown; literal set preserved [05 "Authoritative settlement order"].
func isOuterEligible(player uint8, ctrl uint8, hasCtrl bool) bool {
	if player == 10 { // [08] player index !=10 [PLAN_11 C1]
		return false
	}
	if !hasCtrl { // the external controller field is required by the gate
		return false
	}
	return ctrl == 1 || ctrl == 2 || ctrl == 3 // [08] controller {1,2,3} [PLAN_11 C1]
}

// Tick is the per-player AI entry [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1][C3][C11][C12].
// It is designed to be passed as economy.TickPlayer's beforeDeadline callback (kernel phase 5 session-owned coordinator)
// so it runs after eligible per-tick helpers but before the settlement deadline compare; a skipped slot invokes neither AI nor settlement
// and advances nothing [05 "Authoritative settlement order"] [08 "Established AI-facing data and rooted planner"].
// That the AI dispatch is specifically one of that step's auxiliary helpers is supported inference from the shared entry address [08] [PLAN_11 C11].
func (m *Manager) Tick(tick uint32, w *units.World, econ *economy.Service) {
	if m == nil || econ == nil || int(m.Player) >= len(econ.Players) {
		return
	}
	if m.Player == 10 { // [08] index !=10 [PLAN_11 C1]
		return
	}
	ctrl := econ.Players[m.Player].ControllerState // [08]
	if ctrl != 1 && ctrl != 2 && ctrl != 3 {       // outer gate [08][PLAN_11 C1]
		return
	}
	// P0-I16: sync catalog between Manager and Strategic if one is set via direct field assignment
	if m.Catalog != nil && m.Strategic.Catalog == nil {
		m.Strategic.Catalog = m.Catalog
	} else if m.Strategic.Catalog != nil && m.Catalog == nil {
		m.Catalog = m.Strategic.Catalog
	}
	// Eligible entry — count for classification cadence [08] "classifications run every 30 eligible manager entries" [PLAN_11 C3]
	if m.countdown == 0 {
		m.countdown = 30
	}
	m.countdown--
	if m.countdown == 0 { // [08] every 30 eligible entries [PLAN_11 C3]
		m.runClassifications(tick, w, econ)
	}
	// Inner gate: due virtual tasks execute only when manager player controller ==2 [08] [PLAN_11 C1]
	if ctrl == 2 {
		// Class vectors are initialized before the first task can select a
		// candidate, but their periodic refresh is deliberately below task
		// dispatch. Selection therefore observes the prior refresh for this tick
		// [R-P0-05 §1, §6].
		m.EnsureStrategicInitialized()
		m.runDueTasks(tick, w, econ)
	}
	// Strategic refresh follows manager dispatch and precedes economy
	// settlement, which invokes this method as its before-deadline callback
	// [R-P0-05 §6]. Controller values 1 and 3 still participate in the outer
	// eligible cadence, but do not execute virtual tasks.
	if m.Catalog != nil || m.Strategic.Catalog != nil {
		m.Strategic.MaybeRefresh(tick, m.simRNG(), m.Player, w)
	}
	// No diagnostic scan is part of the authoritative manager tick. Semantic
	// state is observed by the owning world/economy services.
}

func (m *Manager) runClassifications(tick uint32, w *units.World, econ *economy.Service) {
	// The classifier is a live-unit sweep, not a strategic score refresh. It runs
	// before the ten task callbacks and calls the direct group writer for units whose stored
	// group value is zero. Keep the cadence counter and call shape here so the
	// writer remains on the established manager entry path [R-P0-04].
	_, _ = tick, econ
	m.classifyGroups(w)
}

// runDueTasks executes due virtual tasks based on absolute deadlines [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3][P0-02].
// Deadline compare is unsigned deadline <= tick meaning due; while deadline > tick it is in the future and skipped [05 "Authoritative settlement order"] style.
// Tasks are swept in ascending manager offset order (I1) via TaskKind order which mirrors slot order [P0-02 §1.3].
func (m *Manager) runDueTasks(tick uint32, w *units.World, econ *economy.Service) {
	for k := TaskKind(0); k < TaskKindCount; k++ {
		// The tenth slot is an empty task pointer. It is never invoked and its
		// deadline remains zero [R-P0-04 §2].
		if k == TaskEmptySlot {
			continue
		}
		// The null task is a real slot with no body and no rescheduling.
		if k == TaskNull {
			if m.Deadlines[k] > tick {
				continue
			}
			// deadline stays 0 per P0-02 table
			continue
		}
		deadline := m.Deadlines[k]
		if deadline > tick { // [08] separate deadlines [PLAN_11 C3]; 0 <= any tick so initial zero is due
			continue
		}
		switch k {
		case TaskConstruction:
			m.doConstruction(tick, w, econ)
		case TaskResource:
			m.doResource(tick, w, econ)
		case TaskWaveA:
			m.doWave(tick, w, econ, waveAThreshold, waveMin, waveMax)
		case TaskWaveB:
			m.doWave(tick, w, econ, waveBThreshold, waveMin, waveMax)
		case TaskRegroupA:
			m.doRegroup(tick, w, econ, TaskWaveA)
		case TaskRegroupB:
			m.doRegroup(tick, w, econ, TaskWaveB)
		case TaskExplore:
			if m.simRNG() == nil {
				continue
			}
			m.doExplore(tick, w, econ)
		case TaskRally:
			if m.simRNG() == nil {
				continue
			}
			m.doRally(tick, w, econ)
		}
		m.Deadlines[k] = m.nextDeadline(k, tick)
	}
}

func (m *Manager) nextDeadline(k TaskKind, tick uint32) uint32 {
	switch k {
	case TaskConstruction:
		return tick + 90 // [08] construction/positioning at currentTick+90 [P0-02]
	case TaskResource:
		return tick + 30 // [08] resource/queue at +30 [P0-02]
	case TaskWaveA, TaskWaveB:
		return tick + 300 // [P0-02] attack waves at +300
	case TaskRegroupA, TaskRegroupB:
		return tick + 150 // [P0-02] regroup at +150
	case TaskExplore:
		s := m.simRNG()
		if s == nil {
			return m.Deadlines[k]
		}
		r := s.Uint32n(900)  // [08] bound 900 (I4) [P0-02] per-session isolated [RS-06]
		return tick + 30 + r // [08] tick+30+RNG(900) [P0-02]
	case TaskRally:
		s := m.simRNG()
		if s == nil {
			return m.Deadlines[k]
		}
		r := s.Uint32n(150)  // [08] bound 150 (I4) [P0-02] per-session isolated [RS-06]
		return tick + 30 + r // [08] tick+30+RNG(150) [P0-02]
	case TaskEmptySlot, TaskNull:
		return 0 // stays 0 [P0-02]
	default:
		return tick + 30
	}
}

func (m *Manager) doConstruction(tick uint32, w *units.World, econ *economy.Service) {
	_, _ = tick, econ
	if w == nil || econ == nil {
		return
	}
	// Unit loss arms a manager throttle deadline. Construction retries are
	// deferred until that absolute unsigned deadline is due; zero means the
	// manager has not been throttled [08 RNG inventory].
	if m.unitLossDeadline != 0 && m.unitLossDeadline > tick {
		return
	}
	// Find best builder+candidate in this task's assigned construction vector
	// [P0-02]. The retail task does not rediscover builders from the world when
	// its vector is empty; the classifier and ordinary group writers are the
	// admissions to this input.
	var bestBuilder *units.Unit
	var bestCand Candidate
	var bestScore int32 = -1
	found := false
	for _, h := range m.GroupConstruction {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != m.Player || u.Remaining != 0 {
			continue
		}
		if u.Def == nil || !u.Def.Builder {
			continue
		}
		if !m.hasBuildOptionsForDef(u.Def) {
			continue
		}
		cand, ok := Select(m, u, econ)
		if !ok {
			continue
		}
		if !found || cand.Score > bestScore {
			bestBuilder = u
			bestCand = cand
			bestScore = cand.Score
			found = true
		}
	}
	if !found || bestBuilder == nil {
		// Debug: log why no builder found (for TestDebugG5LabQueue)
		// fmt.Printf("doConstruction tick %d found %v bestBuilder %v GroupConstruction %v\n", tick, found, bestBuilder, m.GroupConstruction)
		return
	}
	builder := bestBuilder
	cand := bestCand
	// Debug log for G5
	// fmt.Printf("doConstruction tick %d builder %d cand %s score %d isFactory %v isTargetMobile %v\n", tick, builder.Handle, cand.DefKey, cand.Score, isFactoryBuilder, isTargetMobile)
	// P0-07: typed build path [P0-07] ON-06 F-P0-004.
	// Mobile builders use Place with MobileSite site coordinates; factories use FactoryQueue without site.
	// Factory vs mobile is determined by both builder immobility and target mobility: factories (immobile builders) producing mobile units use FactoryQueue [P0-07].
	// Buildings (non-mobile targets) always require site placement even if builder is factory-like, preserving yard validation [04 §6.2][P0-03].
	// Issues orders ONLY through typed queue and descriptor registry [PLAN_11 C12] [08].
	// No privileged mutation. Chain Select → Place → QueueBuildTyped [PLAN_11 C8+C12] [P0-07].
	// The allocator's runtime building-class bit is the authored bmcode
	// discriminator. It is set for buildings (bmcode zero), independently of
	// yard text, movement flags, or velocity [08 "Classifier eligibility,
	// destinations, and order"; 05 "Factory production lifecycle"].
	isFactoryBuilder := builder.Def != nil && builder.Def.Builder && builder.Flags&classifierBuilding != 0
	isTargetMobile := false
	cat := m.Catalog
	if cat == nil {
		cat = m.Strategic.Catalog
	}
	if cat != nil {
		if def, ok := cat.Unit(cand.DefKey); ok && def != nil {
			isTargetMobile = def.BMCode
		}
	}
	if isFactoryBuilder && isTargetMobile {
		// Factory production: direct typed queue without placement [P0-07] FactoryQueue.
		if m.QueueBuildTyped == nil {
			// Production composition binds this ordinary order sink. An unbound
			// fixture has no supported submission path, so this task is a no-op.
			return
		}
		req := BuildRequest{
			Builder: builder.Handle,
			UnitKey: cand.DefKey,
			Count:   1,
			Kind:    BuildKindFactoryQueue,
		}
		if err := m.QueueBuildTyped(req); err != nil {
			return
		}
		return
	}
	// Mobile site construction via placement [P0-07] MobileSite.
	origFactory := m.Factory
	m.Factory = builder
	x, z, ok := Place(m, cand.DefKey, m.Terrain) // [PLAN_11 C8][C12] [P0-07] preserves X/Z via typed request
	m.Factory = origFactory
	if !ok {
		return
	}
	// Placement has already issued the typed MobileSite request. The world and
	// construction services own the resulting state transition.
	_ = x
	_ = z
}

// doResource implements the eco/queue task at tick plus thirty. It consumes
// only the resource/activity group populated by established writer paths
// [08 "Eco toggle and group-vector population"].
func (m *Manager) doResource(tick uint32, w *units.World, econ *economy.Service) {
	if w == nil || econ == nil {
		return
	}
	if int(m.Player) >= len(econ.Players) {
		return
	}
	metalStock := econ.Players[m.Player].Stock[economy.Metal]
	energyStock := econ.Players[m.Player].Stock[economy.Energy]
	// Manager dispatch reads the settled economy aggregates, not the
	// per-pass reporting counters [R-P0-05].
	netEnergy := econ.Players[m.Player].AIProduction[economy.Energy] - econ.Players[m.Player].AIConsumption[economy.Energy]
	// The eco task scans its assigned resource/activity vector, not the whole
	// owner slice [08][R-P0-04].
	for _, h := range m.GroupResource {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != m.Player {
			continue
		}
		if u.Def == nil {
			continue
		}
		if u.Remaining != 0 {
			continue
		}
		// The authored makes-metal byte selects the activation branch
		// [08 "Eco toggle and group-vector population"].
		if u.Def.MakesMetal != 0 {
			// Branch 2*metal > energy ?
			enable := false
			if 2*metalStock > energyStock && netEnergy >= 1 {
				// Draw RNG(5) only when branch taken [P0-02 §5] per-session isolated [RS-06][I4].
				s := m.simRNG()
				if s == nil {
					// The production session always binds the simulation stream;
					// without it there is no supported activation decision.
					continue
				}
				enable = s.Uint32n(5) != 0
			} else {
				enable = false
			}
			// Ordinary toggle via the unit activation state. The retail
			// activation path changes the operational bit; the
			// units adapter owns that mutable state and economy reads it through
			// EconomyActive [05 "Unit instance economy state"] [P0-02].
			u.SetActivated(enable)
			continue
		}
		// Second branch: a building that does not make metal but does carry
		// build options is a factory, and this task is what queues its
		// products [08 "Eco toggle and group-vector population"]. The
		// construction task never sees a factory, because the classifier sends
		// every building to this record and only mobile builders to the
		// construction record.
		//
		// This branch was missing entirely: nanolathe queued factory products
		// from the construction task instead, which only worked while the
		// classifier misfiled buildings into the construction group.
		if !m.hasBuildOptionsForDef(u.Def) {
			continue
		}
		// A factory with anything already on its primary queue is skipped, so
		// products are queued one at a time as the queue drains.
		if q := orders.QueueForUnit(u); q == nil || len(q.Primary()) > 0 {
			continue
		}
		cand, ok := Select(m, u, econ)
		if !ok {
			continue
		}
		if m.QueueBuildTyped == nil {
			// No supported order sink is available in an unbound fixture.
			continue
		}
		req := BuildRequest{
			Builder: u.Handle,
			UnitKey: cand.DefKey,
			Count:   1,
			Kind:    BuildKindFactoryQueue,
		}
		if err := m.QueueBuildTyped(req); err != nil {
			continue
		}
	}
}

// doWave implements the attack wave A/B task at +300 [08 "Wave merge"][P0-02].
// Thresholds 20000/50000, min 3 max 6. The merge seeds an empty wave from its
// peer's first member, sheds the wave's outliers at inclusive dist² >=
// threshold*n, and collects peer members at strict dist² < threshold*n
// [08 "Wave merge"].
//
// The peer is the task's PAIRED REGROUP record — wave A with regroup A, wave B
// with regroup B — carried as an authored peer slot index on the task
// instance. It is not the other wave. This was previously wired wave A ↔ wave
// B with no bootstrap, which left both wave records permanently empty: the
// classifier never assigns a wave, so nothing could ever seed one and the
// computer player issued no attack order [08 "Wave merge" correction].
//
// Tasks then issue ordinary move/attack orders [P0-02][08].
func (m *Manager) doWave(tick uint32, w *units.World, econ *economy.Service, threshold int32, min, max int) {
	_, _ = econ, threshold
	if w == nil {
		return
	}
	var group []pool.Handle
	var groupID, peerID uint8
	if threshold == waveAThreshold {
		group, groupID = m.GroupWaveA, 2
		peerID = 3
	} else {
		group, groupID = m.GroupWaveB, 6
		peerID = 7
	}
	m.mergeWaveGroupRecords(groupID, peerID, w, threshold)
	if threshold == waveAThreshold {
		group = m.GroupWaveA
	} else {
		group = m.GroupWaveB
	}
	if len(group) < min {
		return
	}
	if len(group) > max {
		group = group[:max]
	}
	// The target-selection and formation sink for a populated wave are not
	// established by the recovered manager contract. Keep the merge lifecycle
	// authoritative and leave the unresolved sink explicit rather than choosing
	// a guessed enemy or map fallback [R-P0-04 §5].
	// TODO(question): recover the target-selection/formation call and its order
	// descriptor before issuing attack orders from a wave.
}

// mergeWaveGroupRecords applies the recovered wave merge through the direct
// writer. Every transfer therefore performs source swap-delete, destination
// append, and the unit Group update as one operation [R-P0-04 §3, §5].
func (m *Manager) mergeWaveGroupRecords(groupID, peerID uint8, w *units.World, threshold int32) {
	if m == nil || w == nil {
		return
	}
	current := m.groupVector(groupID)
	peer := m.groupVector(peerID)
	if current == nil || peer == nil {
		return
	}
	if len(*current) == 0 {
		if len(*peer) == 0 {
			return
		}
		if u := w.Unit((*peer)[0]); u != nil {
			m.transferGroupMember(u, peerID, groupID)
		}
	}
	cx, cz, ok := retailGroupCentroid(*current, w)
	if !ok {
		return
	}
	for len(*current) > 1 {
		limit := int64(threshold) * int64(len(*current))
		farthest := -1
		var farthestDistance int64
		for i, h := range *current {
			u := w.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			distance := retailDistanceSquared(u, cx, cz)
			if farthest < 0 || distance > farthestDistance {
				farthest, farthestDistance = i, distance
			}
		}
		if farthest < 0 || farthestDistance < limit {
			break
		}
		if u := w.Unit((*current)[farthest]); u != nil {
			m.transferGroupMember(u, groupID, peerID)
		}
		cx, cz, ok = retailGroupCentroid(*current, w)
		if !ok {
			return
		}
	}
	if len(*current) == 0 {
		return
	}
	cx, cz, ok = retailGroupCentroid(*current, w)
	if !ok {
		return
	}
	limit := int64(threshold) * int64(len(*current))
	collected := make([]pool.Handle, 0, len(*peer))
	for _, h := range *peer {
		u := w.Unit(h)
		if u != nil && u.Alive && retailDistanceSquared(u, cx, cz) < limit {
			collected = append(collected, h)
		}
	}
	for _, h := range collected {
		if u := w.Unit(h); u != nil {
			m.transferGroupMember(u, peerID, groupID)
		}
	}
}

// transferGroupMember performs one group transfer through the direct writer.
// Its source vector and Unit.Group are established together by the admitting
// writer; the transfer then swap-deletes, appends, and updates Unit.Group as a
// single operation [R-P0-04 §3].
func (m *Manager) transferGroupMember(u *units.Unit, from, to uint8) {
	if m == nil || u == nil {
		return
	}
	if u.Group != from {
		return
	}
	m.writeGroup(u, int8(to))
}

// doRegroup implements the regroup task at tick plus 150, paired with its wave
// record [08 "Strategy manager and its task graph"].
// It moves own group toward peer centroid via ordinary move orders [P0-02][P0-I12].
func (m *Manager) doRegroup(tick uint32, w *units.World, econ *economy.Service, peer TaskKind) {
	_, _, _ = tick, econ, peer
	if w == nil {
		return
	}
	var ownGroup []pool.Handle
	var peerGroup []pool.Handle
	if peer == TaskWaveA {
		ownGroup = m.GroupRegroupA
		peerGroup = m.GroupWaveA
	} else {
		ownGroup = m.GroupRegroupB
		peerGroup = m.GroupWaveB
	}
	if len(ownGroup) == 0 || len(peerGroup) == 0 {
		return
	}
	// Peer centroid
	cx, cz, ok := groupCentroid(peerGroup, w)
	if !ok {
		return
	}
	issued := 0
	for _, h := range ownGroup {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Remaining != 0 {
			continue
		}
		var id orders.ID
		if u.Def != nil && u.Def.CanFly {
			id = orders.Lookup("VTOL_Move")
		} else {
			id = orders.Lookup("Move_Ground")
		}
		if id == 0 {
			continue
		}
		node := orders.NewNodeForOrder(id, 0, cx, 0, cz, tick, u.Handle, false)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		q.Push(id, node)
		issued++
	}
	_ = issued
}

// doExplore implements the explore/gather task at tick plus 30 plus RNG(900)
// [08 "Strategy manager and its task graph"].
// The explore vector is populated by the recovered classifier (category 8),
// load, control-group, or wave-transfer writers. An empty vector remains a
// normal no-op; no world scan is permitted here [P0-02][R-P0-04].
func (m *Manager) doExplore(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	if w == nil {
		return
	}
	r := m.simRNG()
	if r == nil {
		return
	}
	group := m.GroupExplore
	if len(group) == 0 {
		return
	}
	// The body consumes its branch draws even though the target/formation sink
	// is not yet recovered. Keeping this ledger here prevents an unresolved
	// order effect from shifting later authoritative consumers [08 RNG inventory].
	if len(group) < 5 {
		trials := r.Uint32n(2)
		// The branch always submits one baseline attempt and adds the bounded
		// binary choice, for two or three iterations total [08 RNG inventory].
		for i := uint32(0); i <= trials+1; i++ {
			if m.Terrain != nil {
				if width := uint32(m.Terrain.CellW / 8); width >= 2 {
					r.Uint32n(width)
				}
				if height := uint32(m.Terrain.CellH / 8); height >= 2 {
					r.Uint32n(height)
				}
			}
		}
		return
	}
	// Large groups use the edge-target branch: two binary choices, map-width
	// and map-height samples, then the final binary choice [08 RNG inventory].
	r.Uint32n(2)
	r.Uint32n(2)
	if m.Terrain != nil {
		if width := uint32(m.Terrain.CellW); width >= 2 {
			r.Uint32n(width)
		}
		if height := uint32(m.Terrain.CellH); height >= 2 {
			r.Uint32n(height)
		}
	}
	r.Uint32n(2)
	// TODO(question): recover the target coordinate conversion and ordinary
	// formation order before mutating a unit queue for this task.
}

// doRally implements the random-walk rally task at tick plus 30 plus RNG(150)
// [08 "Strategy manager and its task graph"].
// An empty rally vector remains a normal no-op. Category 9 is not emitted by
// the classifier and must arrive through a separate established writer
// [R-P0-04].
func (m *Manager) doRally(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	if w == nil {
		return
	}
	r := m.simRNG()
	if r == nil {
		return
	}
	group := m.GroupRally
	if len(group) == 0 {
		return
	}
	if r.Uint32n(10) == 0 {
		// The drift seed has two independent 16-bit random components. Their
		// conversion into the task's fixed-point target remains unresolved, but
		// both draws are authoritative even while that effect is deferred.
		r.Uint32n(65536)
		r.Uint32n(65536)
	}
	// The score helper's visibility inputs and target sink are unresolved, but
	// its two score-valued random comparisons are established. Keep their
	// order and bounds in the task state; zero bounds consume no draw under the
	// simulation helper's contract [08 RNG inventory].
	current := r.Uint32n(m.rallyScore)
	next := r.Uint32n(m.rallyNextScore)
	_ = current
	_ = next
	// TODO(question): recover the visibility guard, score helper, and ordinary
	// formation/order sink that populate the candidate score and target.
}

// Dispatch iterates the ten players per-tick entry [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1][P0-02].
// Outer gate controller ∈ {1,2,3} and index !=10 dispatches through manager pointer; inner gate controller==2 is enforced inside Manager.Tick [08][PLAN_11 C1].
// Both gates required. Stable ordering player 0..9 ascending (I1), no map iteration.
func Dispatch(tick uint32, econ *economy.Service, managers [10]*Manager, w *units.World) {
	if econ == nil {
		return
	}
	for i := 0; i < 10; i++ { // [08] iterates ten players [PLAN_11 C1] (I1)
		if i == 10 { // [08] player index !=10 [PLAN_11 C1]
			continue
		}
		m := managers[i]
		if m == nil {
			continue
		}
		if i >= len(econ.Players) {
			continue
		}
		ctrl := econ.Players[i].ControllerState  // [08]
		if ctrl != 1 && ctrl != 2 && ctrl != 3 { // outer gate [08][PLAN_11 C1]
			continue
		}
		m.Tick(tick, w, econ)
	}
}

// DispatchSlice is a helper for variable-length slices used by session coordinator wiring [PLAN_11 C11][PLAN_03 Phase 5].
// It iterates in ascending player order (I1) and preserves both gates [08][PLAN_11 C1].
func DispatchSlice(tick uint32, econ *economy.Service, managers []*Manager, w *units.World) {
	if econ == nil {
		return
	}
	for i, m := range managers {
		if i == 10 { // [08] !=10 [PLAN_11 C1]
			continue
		}
		if m == nil {
			continue
		}
		if i >= len(econ.Players) {
			continue
		}
		ctrl := econ.Players[i].ControllerState
		if ctrl != 1 && ctrl != 2 && ctrl != 3 {
			continue
		}
		m.Tick(tick, w, econ)
	}
}
