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

// TaskKind identifies the manager virtual tasks [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3][P0-02].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This enum preserves pre-P0 names for test compatibility and adds wave/regroup tasks per P0-02.
// Ordering is by creation for compatibility; virtual sweep is still ascending TaskKind (I1) which is stable.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// construction/positioning into separate kinds for clarity, preserving deadlines +30/+90 identical
// per slot. The combined slots share the same deadline and handler (TaskConstruction/TaskPositioning
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// while keeping distinct observability. See docs/SPEC_CONFLICTS.md (no retail conflict) and [P0-02].
// TODO(T25): AI transport geometry (attachment, naval, air) remains blocked [PLAN_11 Explicit unknowns][P0-02].
type TaskKind int

const (
	TaskConstruction TaskKind = iota // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskPositioning                  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskResource                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskActivity                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskOther900                     // explore/gather at +30+RNG(900) [P0-02]
	TaskOther150                     // rally at +30+RNG(150) [P0-02]
	TaskWaveA                        // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskWaveB                        // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskRegroupA                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskRegroupB                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskEmpty                        // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskNullSub                      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskKindCount
)

// Aliases for P0-02 naming (explore/rally) to keep new code readable.
const (
	TaskExplore = TaskOther900
	TaskRally   = TaskOther150
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
	Strategic Strategic // 0x10D-byte retail identity, named fields per I13 [08][PLAN_11 C2]
	Deadlines [TaskKindCount]uint32
	Profile   *Profile

	// Placement extensions (not part of the retail manager record; Nanolathe
	// wiring state so ai.Place can stay func(m *Manager, ...) per PLAN_11 API).
	OriginX      numeric.Fixed // placement search origin, steps toward strategic center [PLAN_11 C8]
	OriginZ      numeric.Fixed
	SurfaceMetal int32            // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Catalog      *content.Catalog // defKey resolution for the extractor gate [PLAN_11 C8]
	Factory      *units.Unit      // builder receiving construction requests [PLAN_11 C12] [P0-07]
	Terrain      *world.Terrain   // placement validation terrain; nil skips yard validation, success resets radius [PLAN_11 C8]

	// P0-I16: authoritative hooks moved from package globals onto the owning
	// service. These affect future behavior and therefore are session-owned
	// (serializable via service fields, not package var). Immutable tables remain
	// package-level.
	CandidateSource func(builder *units.Unit) []string                        // was package var CandidateSource [P0-I16]
	MissionGateFlag int32                                                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	GateCandidates  map[string]struct{}                                       // was package var Gate241Candidates [P0-I16]
	QueueBuild      func(factory *units.Unit, defKey string, count int) error // was package var queueBuild [P0-I16] DEPRECATED: use QueueBuildTyped [P0-07]

	// P0-07: typed build request replacing lossy callback [P0-07] ON-06 F-P0-004.
	// Session binds this to construction queue; default nil → error diagnostic.
	QueueBuildTyped func(BuildRequest) error // typed mobile/factory build [P0-07]

	// missedQueueCallbacks counts typed build requests dropped because the
	// session never bound QueueBuildTyped [RX-01][F-P0-004]. Diagnostic only;
	// read via MissedQueueCallbacks. Production sessions bind at manager
	// creation and ValidateComposition rejects unbound managers, so nonzero
	// here means a fixture constructed an AI without the production binder.
	missedQueueCallbacks uint32

	// P0-07: alliance awareness [P0-07] ON-06. Nil means same-owner-only (default) [08].
	IsAlliance func(a, b uint8) bool // alliance test injected at construction; default same-owner-only [P0-07]

	// Groups for AI tactical coordination — populated from unit creation/death/completion via updateGroups [P0-I12].
	// Each slice holds pool handles in deterministic order; cleaned each tick before dispatch.
	GroupWaveA    []pool.Handle
	GroupWaveB    []pool.Handle
	GroupExplore  []pool.Handle
	GroupRally    []pool.Handle
	GroupRegroupA []pool.Handle
	GroupRegroupB []pool.Handle

	entryCount         uint32 // eligible manager entries for classification cadence [08][PLAN_11 C3]
	classificationRuns int
	taskRuns           [TaskKindCount]int

	// P0-07 milestones and last tick [P0-07] ON-06.
	milestones map[string]uint32 // stage→tick, set only from observed production state [P0-07]
	lastTick   uint32            // last Tick tick, used by Place to record PlacementSelected with tick [P0-07]

	// RS-02 test hook: if non-nil, called at Tick entry for order verification (not persisted).
	TestHook func(tick uint32, player uint8)

	// RS-06: per-session isolated RNG [I4][RS-P0-018]. Nil => rng.Global.Sim (single global stream per I4, but per-session isolated when set).
	RNG *rng.Simulation `json:"-"`

	// RS-06: hook for ObserveHostileDamage order verification [RS-P0-014].
	ObserveHook func(tick uint32, target pool.Handle) `json:"-"`
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

// GetCandidateSource satisfies the extended Selector for P0-I16 [P0-I16].
func (m *Manager) GetCandidateSource() func(builder *units.Unit) []string {
	if m == nil {
		return nil
	}
	return m.CandidateSource
}

// GetCatalog satisfies the extended Selector for P0-I16.
func (m *Manager) GetCatalog() *content.Catalog {
	if m == nil {
		return nil
	}
	return m.Catalog
}

// GetMissionGateFlag satisfies the extended Selector for P0-I16.
func (m *Manager) GetMissionGateFlag() int32 {
	if m == nil {
		return 0
	}
	return m.MissionGateFlag
}

// GetGateCandidates satisfies the extended Selector for P0-I16.
func (m *Manager) GetGateCandidates() map[string]struct{} {
	if m == nil {
		return nil
	}
	return m.GateCandidates
}

// GetRNG satisfies Selector RNG extension — returns per-manager RNG when set for session isolation [RS-06][I4], else Global.Sim per RS-02.
func (m *Manager) GetRNG() *rng.Simulation {
	if m != nil && m.RNG != nil {
		return m.RNG
	}
	return rng.Global.Sim
}

// GetQueueBuild returns the ordinary build path for P0-I16.
func (m *Manager) GetQueueBuild() func(factory *units.Unit, defKey string, count int) error {
	if m == nil {
		return nil
	}
	if m.QueueBuild != nil {
		return m.QueueBuild
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

// EntryCount returns the number of eligible manager entries seen [08][PLAN_11 C3].
func (m *Manager) EntryCount() uint32 {
	if m == nil {
		return 0
	}
	return m.entryCount
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) ClassificationRuns() int {
	if m == nil {
		return 0
	}
	return m.classificationRuns
}

// TaskRuns returns how many times the given TaskKind has been executed (due dispatch count).
func (m *Manager) TaskRuns(k TaskKind) int {
	if m == nil || k < 0 || k >= TaskKindCount {
		return 0
	}
	return m.taskRuns[k]
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
	// If already initialized with varied vectors, skip.
	if len(m.Strategic.ClassVectors) > 0 {
		// Check if any vector is non-zero; if all zero, reinit (fallback case)
		hasVaried := false
		for _, v := range m.Strategic.ClassVectors {
			if v.C0 != 0 || v.C1 != 0 || v.C2 != 0 {
				hasVaried = true
				break
			}
		}
		if hasVaried && len(m.Strategic.ClassVectors) == len(cat.Units) {
			return
		}
		if hasVaried && len(m.Strategic.ClassVectors) >= len(cat.Units)/2 {
			// Assume sufficiently initialized
			return
		}
	}
	types := make([]string, 0, len(cat.Units))
	for k := range cat.Units {
		types = append(types, k)
	}
	sort.Strings(types)
	m.Strategic.Catalog = cat
	m.Catalog = cat
	m.Strategic.Init(types)
}

// simRNG returns the per-session isolated RNG when set [RS-06][I4], else the one global simulation RNG per RS-02.
func (m *Manager) simRNG() *rng.Simulation {
	if m != nil && m.RNG != nil {
		return m.RNG
	}
	return rng.Global.Sim
}

// findBuilder returns a valid builder for the manager's player [P0-I12].
// It scans in deterministic sliced order (player asc, slot asc) [I1] and returns the
// first completed builder that has a non-empty build menu via the manager's catalog.
// This replaces the previous first-owned-unit logic which could return a non-builder.
func (m *Manager) findBuilder(w *units.World) *units.Unit {
	if m == nil || w == nil {
		return nil
	}
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.Owner != m.Player {
			continue
		}
		if u.Remaining != 0 {
			continue
		}
		if u.Def == nil {
			continue
		}
		if !u.Def.Builder {
			continue
		}
		// Must have build options via catalog's BuildMenus or at least Builder flag with candidate source.
		// Prefer check via manager's catalog; fallback to Builder true if no catalog.
		if m.Catalog != nil && m.Catalog.BuildMenus != nil {
			ck := canonicalKey(u.Def.UnitName)
			if ck == "" {
				ck = canonicalKey(u.Def.CanonicalKey)
			}
			if page, ok := m.Catalog.BuildMenus[ck]; ok && page != nil && len(page.Buttons) > 0 {
				return u
			}
			if ck2 := canonicalKey(u.Def.CanonicalKey); ck2 != ck {
				if page, ok := m.Catalog.BuildMenus[ck2]; ok && page != nil && len(page.Buttons) > 0 {
					return u
				}
			}
			// Also check candidate source if set
			if m.CandidateSource != nil {
				cands := m.CandidateSource(u)
				if len(cands) > 0 {
					return u
				}
			}
			continue
		}
		if m.Strategic.Catalog != nil && m.Strategic.Catalog.BuildMenus != nil {
			ck := canonicalKey(u.Def.UnitName)
			if page, ok := m.Strategic.Catalog.BuildMenus[ck]; ok && page != nil && len(page.Buttons) > 0 {
				return u
			}
			continue
		}
		// No catalog: any builder qualifies
		return u
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
	if m.CandidateSource != nil {
		// We can't test without a unit instance; assume builder flag indicates options
	}
	return def.Builder
}

// isOuterEligible reports whether the outer per-tick gate passes [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// The Player==10 check is literal retail guard [08]; it never fires for 0..9 but is kept for fidelity.
// TODO(question): semantic names of controller values 1,2,3 unknown; literal set preserved [05 "Authoritative settlement order"].
func isOuterEligible(player uint8, ctrl uint8, hasCtrl bool) bool {
	if player == 10 { // [08] player index !=10 [PLAN_11 C1]
		return false
	}
	if !hasCtrl { // no economy context — treat as eligible for isolated tests
		return true
	}
	return ctrl == 1 || ctrl == 2 || ctrl == 3 // [08] controller {1,2,3} [PLAN_11 C1]
}

// Tick is the per-player AI entry [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1][C3][C11][C12].
// It is designed to be passed as economy.TickPlayer's beforeDeadline callback (kernel phase 5 session-owned coordinator)
// so it runs after eligible per-tick helpers but before the settlement deadline compare; a skipped slot invokes neither AI nor settlement
// and advances nothing [05 "Authoritative settlement order"] [08 "Established AI-facing data and rooted planner"].
// That the AI dispatch is specifically one of that step's auxiliary helpers is supported inference from the shared entry address [08] [PLAN_11 C11].
func (m *Manager) Tick(tick uint32, w *units.World, econ *economy.Service) {
	if m == nil {
		return
	}
	if m.TestHook != nil {
		m.TestHook(tick, m.Player)
	}
	m.lastTick = tick
	// P0-I16: sync catalog between Manager and Strategic if one is set via direct field assignment
	if m.Catalog != nil && m.Strategic.Catalog == nil {
		m.Strategic.Catalog = m.Catalog
	} else if m.Strategic.Catalog != nil && m.Catalog == nil {
		m.Catalog = m.Strategic.Catalog
	}
	// P0-I12: lazily initialize class maps from catalog if not yet done, ensuring vectors not zero.
	m.EnsureStrategicInitialized()
	// P0-I12: refresh strategic center/counts every 30 ticks via MaybeRefresh [08][P0-01] using Simulation RNG bound 30 [I4][RS-06 per-session isolated].
	if m.Catalog != nil || m.Strategic.Catalog != nil {
		m.Strategic.MaybeRefresh(tick, m.simRNG(), m.Player, w)
	}
	// P0-I12: populate and maintain AI groups from unit creation/death/completion.
	m.updateGroups(w)
	// P0-07: observe milestones from production state [P0-07] ON-06.
	m.observeMilestones(tick, w)
	if m.Player == 10 { // [08] index !=10 [PLAN_11 C1]
		return
	}
	var ctrl uint8
	var hasCtrl bool
	if econ != nil && int(m.Player) < len(econ.Players) {
		ctrl = econ.Players[m.Player].ControllerState // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		hasCtrl = true
		if ctrl != 1 && ctrl != 2 && ctrl != 3 { // outer gate [08][PLAN_11 C1]
			return
		}
	}
	// Eligible entry — count for classification cadence [08] "classifications run every 30 eligible manager entries" [PLAN_11 C3]
	m.entryCount++
	if m.entryCount%30 == 0 { // [08] every 30 eligible entries [PLAN_11 C3]
		m.runClassifications(tick, w, econ)
	}
	// Inner gate: due virtual tasks execute only when manager player controller ==2 [08] [PLAN_11 C1]
	if hasCtrl && ctrl != 2 { // [08] controller==2 [PLAN_11 C1]
		return
	}
	m.runDueTasks(tick, w, econ)
}

func (m *Manager) runClassifications(tick uint32, w *units.World, econ *economy.Service) {
	m.classificationRuns++
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Placeholder: no state change beyond counting; real work would read completed counts / profile.
	// For P0-I12, we ensure strategic center is fresh (already via MaybeRefresh above) and that
	// classifications produce varied scores via existing ClassVectors (established via Init).
	_, _, _ = tick, w, econ
}

// runDueTasks executes due virtual tasks based on absolute deadlines [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3][P0-02].
// Deadline compare is unsigned deadline <= tick meaning due; while deadline > tick it is in the future and skipped [05 "Authoritative settlement order"] style.
// Tasks are swept in ascending manager offset order (I1) via TaskKind order which mirrors slot order [P0-02 §1.3].
func (m *Manager) runDueTasks(tick uint32, w *units.World, econ *economy.Service) {
	for k := TaskKind(0); k < TaskKindCount; k++ {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// We still model it as TaskEmpty with vtable nullsub RET that does nothing; it is due when deadline <=tick but we skip work.
		// For determinism we still count TaskRuns for empty? No, empty should not increment. So skip if k==TaskEmpty and deadline==0?
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if k == TaskEmpty {
			continue
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// We model it as no-op but increment taskRuns every tick for observability; deadline remains 0.
		if k == TaskNullSub {
			if m.Deadlines[k] > tick {
				continue
			}
			m.taskRuns[k]++ // fires every tick when inner gate passes (deadline 0) [P0-02] [PLAN_11 C3]
			// deadline stays 0 per P0-02 table
			continue
		}
		deadline := m.Deadlines[k]
		if deadline > tick { // [08] separate deadlines [PLAN_11 C3]; 0 <= any tick so initial zero is due
			continue
		}
		m.taskRuns[k]++
		switch k {
		case TaskConstruction, TaskPositioning:
			m.doConstruction(tick, w, econ)
		case TaskResource, TaskActivity:
			m.doResource(tick, w, econ)
		case TaskWaveA:
			m.doWave(tick, w, econ, waveAThreshold, waveMin, waveMax)
		case TaskWaveB:
			m.doWave(tick, w, econ, waveBThreshold, waveMin, waveMax)
		case TaskRegroupA:
			m.doRegroup(tick, w, econ, TaskWaveA)
		case TaskRegroupB:
			m.doRegroup(tick, w, econ, TaskWaveB)
		case TaskOther900:
			m.doExplore(tick, w, econ)
		case TaskOther150:
			m.doRally(tick, w, econ)
		}
		m.Deadlines[k] = m.nextDeadline(k, tick)
	}
}

func (m *Manager) nextDeadline(k TaskKind, tick uint32) uint32 {
	switch k {
	case TaskConstruction, TaskPositioning:
		return tick + 90 // [08] construction/positioning at currentTick+90 [P0-02]
	case TaskResource, TaskActivity:
		return tick + 30 // [08] resource/queue at +30 [P0-02]
	case TaskWaveA, TaskWaveB:
		return tick + 300 // [P0-02] attack waves at +300
	case TaskRegroupA, TaskRegroupB:
		return tick + 150 // [P0-02] regroup at +150
	case TaskOther900:
		var r uint32
		if s := m.simRNG(); s != nil {
			r = s.Uint32n(900) // [08] bound 900 (I4) [P0-02][PLAN_11 C9] per-session isolated [RS-06]
		}
		return tick + 30 + r // [08] tick+30+RNG(900) [P0-02]
	case TaskOther150:
		var r uint32
		if s := m.simRNG(); s != nil {
			r = s.Uint32n(150) // [08] bound 150 (I4) [P0-02] per-session isolated [RS-06]
		}
		return tick + 30 + r // [08] tick+30+RNG(150) [P0-02]
	case TaskEmpty, TaskNullSub:
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
	// Find best builder+candidate across all owned completed builders [P0-I12].
	// Iterate builders in deterministic sliced order [I1] and pick highest scoring candidate.
	var bestBuilder *units.Unit
	var bestCand Candidate
	var bestScore int32 = -1
	found := false
	for _, u := range w.IterSliced() {
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
		// Fallback to old findBuilder for compatibility
		bestBuilder = m.findBuilder(w)
		if bestBuilder == nil {
			return
		}
		cand, ok := Select(m, bestBuilder, econ)
		if !ok {
			return
		}
		bestCand = cand
	}
	builder := bestBuilder
	cand := bestCand
	// P0-07: typed build path [P0-07] ON-06 F-P0-004.
	// Mobile builders use Place with MobileSite site coordinates; factories use FactoryQueue without site.
	// Factory vs mobile is determined by both builder immobility and target mobility: factories (immobile builders) producing mobile units use FactoryQueue [P0-07].
	// Buildings (non-mobile targets) always require site placement even if builder is factory-like, preserving yard validation [04 §6.2][P0-03].
	// Issues orders ONLY through typed queue and descriptor registry [PLAN_11 C12] [08].
	// No privileged mutation. Chain Select → Place → QueueBuildTyped [PLAN_11 C8+C12] [P0-07].
	isFactoryBuilder := builder.Def != nil && builder.Def.Builder && !builder.Def.CanMove
	isTargetMobile := false
	if m.Catalog != nil {
		if def, ok := m.Catalog.Unit(cand.DefKey); ok && def != nil {
			isTargetMobile = def.CanMove || def.MaxVelocity > 0
		}
	} else if m.Strategic.Catalog != nil {
		if def, ok := m.Strategic.Catalog.Unit(cand.DefKey); ok && def != nil {
			isTargetMobile = def.CanMove || def.MaxVelocity > 0
		}
	}
	if isFactoryBuilder && isTargetMobile {
		// Factory production: direct typed queue without placement [P0-07] FactoryQueue.
		if m.QueueBuildTyped == nil {
			// Diagnostic: session has not bound typed queue [P0-07] F-P0-004.
			// Counted, not silent: observable via MissedQueueCallbacks.
			m.missedQueueCallbacks++
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
		m.recordMilestone(MilestoneBuildRequestAccepted, tick)
		// Factory product queued will also be observed via world scan; also record now for testability.
		m.recordMilestone(MilestoneFactoryProductQueued, tick)
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
	// Place already issued typed MobileSite request and recorded PlacementSelected/BuildRequestAccepted internally.
	// Ensure milestones are observed via world scan as well.
	_ = x
	_ = z
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doResource(tick uint32, w *units.World, econ *economy.Service) {
	_ = tick
	if w == nil || econ == nil {
		return
	}
	if int(m.Player) >= len(econ.Players) {
		return
	}
	metalStock := econ.Players[m.Player].Stock[economy.Metal]
	energyStock := econ.Players[m.Player].Stock[economy.Energy]
	netEnergy := econ.Players[m.Player].PassProduced[economy.Energy] - econ.Players[m.Player].PassConsumed[economy.Energy]
	// Iterate completed units in sliced order (I1) stable ordering, no map iteration.
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Owner != m.Player {
			continue
		}
		if u.Def == nil {
			continue
		}
		if u.Remaining != 0 {
			continue
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if u.Def.OnOffable {
			// Branch 2*metal > energy ?
			enable := false
			if 2*metalStock > energyStock && netEnergy >= 1 {
				// Draw RNG(5) only when branch taken [P0-02 §5] per-session isolated [RS-06][I4].
				var draw uint32
				if s := m.simRNG(); s != nil {
					draw = s.Uint32n(5)
				}
				if draw != 0 {
					enable = true
				} else {
					// When RNG unavailable, default to non-zero (enable) to keep determinism; production always seeded.
					enable = m.simRNG() == nil
				}
			} else {
				enable = false
			}
			// Ordinary toggle via unit Flags bit for metal maker [P0-I12].
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// We mutate Flags bit 12 as active marker; economy can observe via Flags if extended.
			// This is ordinary mutation via unit instance, not privileged economy write.
			const activeBit uint32 = 1 << 12 // INFERENCE for OnOffable active state
			if enable {
				u.Flags |= activeBit
			} else {
				u.Flags &^= activeBit
			}
			// Also try repair of damaged units via builder orders when resource allows.
			// This provides stock-reachable reclaim/repair behavior via ordinary orders [P0-I12].
			if enable {
				m.tryRepair(tick, w)
			}
		}
	}
}

// tryRepair issues a repair order from a builder to the most damaged owned unit.
// Uses ordinary order emission via orders.QueueForUnit [P0-I12][08].
func (m *Manager) tryRepair(tick uint32, w *units.World) {
	if w == nil {
		return
	}
	builder := m.findBuilder(w)
	if builder == nil {
		return
	}
	// Don't repair if builder already has queued work
	qb := orders.QueueForUnit(builder)
	if qb != nil && qb.LenPrimary() > 0 {
		return
	}
	var damaged *units.Unit
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Owner != m.Player || u.Remaining != 0 {
			continue
		}
		if u.Health >= u.MaxHealth {
			continue
		}
		if u.Health <= 0 {
			continue
		}
		if u.Def != nil && u.Def.Builder {
			continue
		}
		if damaged == nil || u.Health < damaged.Health || (u.Health == damaged.Health && u.Handle < damaged.Handle) {
			damaged = u
		}
	}
	if damaged == nil {
		return
	}
	id := orders.Lookup("RepairUnit")
	if id == 0 {
		id = orders.Lookup("RepairUnitNoMove")
	}
	if id == 0 {
		return
	}
	node := orders.NewNodeForOrder(id, damaged.Handle, damaged.X, damaged.Y, damaged.Z, tick, builder.Handle, false)
	q := orders.QueueForUnit(builder)
	if q == nil {
		return
	}
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	q.Push(id, node)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doWave(tick uint32, w *units.World, econ *economy.Service, threshold int32, min, max int) {
	_, _ = econ, threshold
	if w == nil {
		return
	}
	m.updateGroups(w)
	var group []pool.Handle
	if threshold == waveAThreshold {
		group = m.GroupWaveA
	} else {
		group = m.GroupWaveB
	}
	group = cleanGroup(group, w, m.Player)
	// Update stored group after clean
	if threshold == waveAThreshold {
		m.GroupWaveA = group
	} else {
		m.GroupWaveB = group
	}
	if len(group) < min {
		return
	}
	if len(group) > max {
		group = group[:max]
	}
	// Find enemy target deterministically; if none, use map centroid.
	target := m.findEnemyTarget(w)
	var tx, tz numeric.Fixed
	var tid pool.Handle
	if target != nil {
		tx, tz = target.X, target.Z
		tid = target.Handle
	} else {
		tx, tz = m.enemyCentroid(w)
	}
	// Issue ordinary attack/move orders to each unit in group via orders queue [08][P0-02].
	issued := 0
	for _, h := range group {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Remaining != 0 {
			continue
		}
		var id orders.ID
		if tid != 0 {
			id = orders.Lookup("Attack_Chase")
			if id == 0 {
				id = orders.Lookup("Attack_NoMove")
			}
		}
		if id == 0 {
			if u.Def != nil && u.Def.CanFly {
				id = orders.Lookup("VTOL_Move")
			} else {
				id = orders.Lookup("Move_Ground")
			}
		}
		if id == 0 {
			continue
		}
		node := orders.NewNodeForOrder(id, tid, tx, 0, tz, tick, u.Handle, false)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		// Ordinary path: purge unprotected and push (queued false) [04 §3.3][P0-I03]
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		q.Push(id, node)
		issued++
	}
	if issued > 0 {
		m.recordMilestone(MilestoneAttackMoveIssued, tick)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It moves own group toward peer centroid via ordinary move orders [P0-02][P0-I12].
func (m *Manager) doRegroup(tick uint32, w *units.World, econ *economy.Service, peer TaskKind) {
	_, _, _ = tick, econ, peer
	if w == nil {
		return
	}
	m.updateGroups(w)
	var ownGroup []pool.Handle
	var peerGroup []pool.Handle
	if peer == TaskWaveA {
		ownGroup = m.GroupWaveA
		peerGroup = m.GroupWaveB
		// Alternative: use Regroup slices if they have members, else wave
		if len(m.GroupRegroupA) > 0 {
			ownGroup = m.GroupRegroupA
		}
	} else {
		ownGroup = m.GroupWaveB
		peerGroup = m.GroupWaveA
		if len(m.GroupRegroupB) > 0 {
			ownGroup = m.GroupRegroupB
		}
	}
	ownGroup = cleanGroup(ownGroup, w, m.Player)
	peerGroup = cleanGroup(peerGroup, w, m.Player)
	if len(ownGroup) == 0 || len(peerGroup) == 0 {
		return
	}
	// Peer centroid
	cx, cz, ok := groupCentroid(peerGroup, w)
	if !ok {
		cx, cz = m.enemyCentroid(w)
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
	if issued > 0 {
		m.recordMilestone(MilestoneAttackMoveIssued, tick)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): population of manager task group vectors is bounded negative [P0-02]; this task is inert as static image until a runtime writer is found. It issues ordinary move orders only when the explore group is non-empty via proven producer; otherwise it returns without tick-derived coordinates or extra RNG.
func (m *Manager) doExplore(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	if w == nil {
		return
	}
	m.updateGroups(w)
	group := cleanGroup(m.GroupExplore, w, m.Player)
	m.GroupExplore = group
	if len(group) == 0 {
		return
	}
	tx, tz := m.enemyCentroid(w)
	issued := 0
	for _, h := range group {
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
		node := orders.NewNodeForOrder(id, 0, tx, 0, tz, tick, u.Handle, false)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		q.Push(id, node)
		issued++
	}
	if issued > 0 {
		m.recordMilestone(MilestoneAttackMoveIssued, tick)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): see doExplore — inert until group population writer found; no tick-derived coordinates or extra RNG beyond deadline's 150 bound.
func (m *Manager) doRally(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	if w == nil {
		return
	}
	m.updateGroups(w)
	group := cleanGroup(m.GroupRally, w, m.Player)
	m.GroupRally = group
	if len(group) == 0 {
		return
	}
	tx, tz := m.enemyCentroid(w)
	issued := 0
	for _, h := range group {
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
		node := orders.NewNodeForOrder(id, 0, tx, 0, tz, tick, u.Handle, false)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		q.Push(id, node)
		issued++
	}
	if issued > 0 {
		m.recordMilestone(MilestoneAttackMoveIssued, tick)
	}
}

// Dispatch iterates the ten players per-tick entry [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1][P0-02].
// Outer gate controller ∈ {1,2,3} and index !=10 dispatches through manager pointer; inner gate controller==2 is enforced inside Manager.Tick [08][PLAN_11 C1].
// Both gates required. Stable ordering player 0..9 ascending (I1), no map iteration.
func Dispatch(tick uint32, econ *economy.Service, managers [10]*Manager, w *units.World) {
	for i := 0; i < 10; i++ { // [08] iterates ten players [PLAN_11 C1] (I1)
		if i == 10 { // [08] player index !=10 [PLAN_11 C1]
			continue
		}
		m := managers[i]
		if m == nil {
			continue
		}
		if econ != nil {
			ctrl := econ.Players[i].ControllerState  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			if ctrl != 1 && ctrl != 2 && ctrl != 3 { // outer gate [08][PLAN_11 C1]
				continue
			}
		}
		m.Tick(tick, w, econ)
	}
}

// DispatchSlice is a helper for variable-length slices used by session coordinator wiring [PLAN_11 C11][PLAN_03 Phase 5].
// It iterates in ascending player order (I1) and preserves both gates [08][PLAN_11 C1].
func DispatchSlice(tick uint32, econ *economy.Service, managers []*Manager, w *units.World) {
	for i, m := range managers {
		if i == 10 { // [08] !=10 [PLAN_11 C1]
			continue
		}
		if m == nil {
			continue
		}
		if econ != nil && i < len(econ.Players) {
			ctrl := econ.Players[i].ControllerState
			if ctrl != 1 && ctrl != 2 && ctrl != 3 {
				continue
			}
		}
		m.Tick(tick, w, econ)
	}
}
