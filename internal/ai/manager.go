package ai

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TaskKind identifies one of the manager's ten fixed task slots. The order is
// load-bearing: slot 0 is the empty pointer, while slot 5 is a real null-task
// object whose group record holds armed buildings [08 R-P0-04 §2].
type TaskKind int

const (
	TaskEmptySlot TaskKind = iota
	TaskResource
	TaskWaveA
	TaskRegroupA
	TaskConstruction
	TaskNull
	TaskWaveB
	TaskRegroupB
	TaskExplore
	TaskRally
	TaskKindCount
)

// Wave thresholds and limits [P0-02 §1.3][P0-02 §3.4].
const (
	waveAThreshold = 20000
	waveBThreshold = 50000
	waveMin        = 3
	waveMax        = 6
)

// Transport, naval, air, repair, reclaim and scouting policy — closed by
// [08 R-AI-04] (RWU-19-200, 2026-09-04); the former TODO(T25) here is retired.
// Established, and what this file does about it today:
//
//   - Transport: no producer exists. The computer player passes only command
//     codes 2, 3, 9 and 14 and never a target unit with code 2, so no
//     pickup/unload/landing/follow/capture row can be produced, and the class
//     routine's `canload` zeroing makes every non-extracting carrier score at
//     or below zero, skipped without a draw [08 R-AI-04 §2]. Nothing to add.
//   - Naval: exactly three `MinWaterDepth` tests — classifier row to regroup B
//     (wave B is the naval wave, threshold 50,000), the class routine's ×3 for
//     a non-negative depth, and the scatter helper's region select plus the
//     validator's waterline band for shipyards; no map-level water input
//     [08 R-AI-04 §3]. The first two are implemented (classifyGroups,
//     Strategic.recomputeClassVectors); the placement side is
//     internal/ai/placement.go's [08 R-AI-03 §4] contract.
//   - Air: the classifier's `canfly` row (implemented) sends every non-builder
//     aircraft to the explore record and nowhere else; the resolver's air
//     twins are internal/orders'; `+40` for `canfly` in the class routine is
//     implemented [08 R-AI-04 §4]. No air wave, rally or pad logic exists.
//   - Repair/reclaim/guard/capture: never ordered directly. Repair and reclaim
//     happen only inside the RepairPatrol/VTOL_RepairPatrol handlers that the
//     construction repositioning and explore patrol orders resolve to when the
//     builder authors `canreclamate` [08 R-AI-04 §5]. Already carried: this
//     file resolves intent 9 through orders.Resolve (resolveAIIntent), whose
//     code-9 rule picks RepairPatrol/VTOL_RepairPatrol [04 R-ORD-02 §1], and
//     internal/orders implements both handlers.
//
// Unknown: nothing in this lane. Nothing Established by R-AI-04 is missing
// from the code; what an implementation unit can still add is tests that lock
// the medium split (water-class mobiles to regroup B, land to regroup A) and
// the explore record's aircraft-only membership. This unit changed no
// behavior.

// Manager is the per-player planner. The phase-5 session hook dispatches its
// tasks and autonomous maintenance before strategic refresh and settlement
// [08 "Dispatch gates and order sinks"][05 "Authoritative settlement order"].
type Manager struct {
	// WeaponMaintenance is session wiring for the manager's post-task scan.
	// It runs before the separate strategic refresh [06 §3.2][08 "Dispatch gates and order sinks"].
	WeaponMaintenance func(player uint8)

	Player    uint8     // 0..9, 10 is sentinel never dispatched [08][PLAN_11 C1]
	Strategic Strategic // fixed-size retail strategic state, named fields per I13 [08][PLAN_11 C2]
	Deadlines [TaskKindCount]uint32
	Profile   *Profile

	// Placement extensions (not part of the retail manager record; Nanolathe
	// wiring state so ai.Place can stay func(m *Manager, ...) per PLAN_11 API).
	OriginX      numeric.Fixed // placement search origin, steps toward strategic center [PLAN_11 C8]
	OriginZ      numeric.Fixed
	SurfaceMetal int32            // raw mission/session SurfaceMetal word; per-cell seed alone narrows to byte [08 R-AI-03 §4][05 R-PROD-01 §6]
	Catalog      *content.Catalog // defKey resolution for the extractor gate [PLAN_11 C8]
	Factory      *units.Unit      // builder receiving construction requests [PLAN_11 C12] [P0-07]
	Terrain      *world.Terrain   // placement validation terrain; nil skips yard validation, success resets radius [PLAN_11 C8]
	// MissionGateFlag is the authoritative session-kind word copied at manager
	// construction for selection's definition gate. Kind one is campaign; the
	// manager does not infer it from mission data or setup fields [08 R-P0-05
	// §3][08 R-SESS-01 §5].
	MissionGateFlag int32

	// P0-07: typed build request replacing lossy callback [P0-07] ON-06 F-P0-004.
	// Session binds this to construction queue; default nil → error diagnostic.
	QueueBuildTyped func(BuildRequest) error // typed mobile/factory build [P0-07]
	// OrderBinding is the session-owned context for AI order producers that
	// submit ordinary orders directly. It is nil for unbound fixtures, where
	// those producers retain their existing queue behavior [04 §3.3][06 §11.1].
	OrderBinding *orders.QueueBinding

	// P0-07: alliance awareness [P0-07] ON-06. A nil binding fails closed: the
	// manager may not infer hostility from ownership or side identity [08 R-AI-01 §9].
	IsAlliance func(a, b uint8) bool

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
	// strategicTypes is the sorted catalog unit-key list EnsureStrategicInitialized
	// tests the strategic maps against, cached against the catalog it was built
	// from. It is derived state with no simulation meaning of its own.
	strategicTypes    []string
	strategicTypesFor *content.Catalog

	// Each attack-wave task owns its own engagement hysteresis latch. It starts
	// clear, survives ordinary group churn, and is cleared only by the gather
	// arm at three or fewer members [08 R-AI-01 §4].
	waveAEngaged bool
	waveBEngaged bool

	// Rally task state is initialized from the map centre in the exact
	// best/probe/drift order. In particular drift begins equal to best, placing
	// the first probe just outside the far corner [08 R-AI-02 §1].
	rallyInitialized bool
	rallyBestX       numeric.Fixed
	rallyBestY       numeric.Fixed
	rallyBestZ       numeric.Fixed
	rallyProbeX      numeric.Fixed
	rallyProbeY      numeric.Fixed
	rallyProbeZ      numeric.Fixed
	rallyDriftX      numeric.Fixed
	rallyDriftY      numeric.Fixed
	rallyDriftZ      numeric.Fixed
	rallyBestScore   int32
	rallyTargets     []pool.Handle

	// One live-unit walk buffer per call site, players then slots ascending
	// (I1). They are separate because a broadcast submits orders while its own
	// walk is still being read, and the handlers it reaches may drive another
	// of these walks.
	broadcastWalk []*units.Unit
	hostileWalk   []*units.Unit
	rallyWalk     []*units.Unit
	classifyWalk  []*units.Unit

	// RallyVisible supplies the ordinary visibility predicate used only when a
	// 30-tick strategic refresh rebuilds the rally score vector. A nil binding
	// keeps that vector empty rather than granting omniscient target knowledge
	// [08 R-AI-01 §7, §16].
	RallyVisible func(viewer uint8, target *units.Unit) bool `json:"-"`
	// RallyProbeKnown follows the session visibility mode: LineOfSight samples
	// the owner's current-sight byte grid; Permanent LOS samples the local
	// viewing slot's mapping-word bit [08 R-AI-01 §7][03 R-VIS-01 §1].
	RallyProbeKnown func(owner uint8, x, y, z numeric.Fixed) bool `json:"-"`
	// RallyShotTimeAdmits is the gate the rally task applies to a member with
	// no mover. [08 R-AI-01 §19] settles both halves of what used to be an open
	// question here: the member test reads the unit record's mover pointer, and
	// the creator allocates a mover only for `bmcode == 1`, so "no locomotion"
	// is "the member is a building"; and the predicate such a member faces is
	// the slot-1 shot-time PHYSICAL gate of [06 §3.3] against the rally point —
	// no order-admission predicate is involved, which is why this field is no
	// longer called RallyOrderAdmitted. A mobile member is never gated.
	//
	// Session composition binds it to the combat service's own gate so the
	// planner carries no second copy. A nil binding fails closed: an unbound
	// fixture skips buildings rather than rallying them from an invented
	// predicate.
	RallyShotTimeAdmits func(unit *units.Unit, x, y, z numeric.Fixed) bool `json:"-"`

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
// Production requires injected RNG; nil means no draw (no alternate RNG is selected).
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

// SetUnitLimit binds the session's per-player unit limit onto this manager's
// strategic state. It is the only global the class routine's half-capacity
// comparison reads, and it is one word for the whole battle, so a session binds
// it once — before Strategic.Init, whose construction-time class computation
// already consults it [08 R-AI-01 §13]. An unbound manager — a package fixture
// — leaves the comparison false rather than reading the zero value as a cap.
func (m *Manager) SetUnitLimit(limit int32) {
	if m == nil {
		return
	}
	m.Strategic.SetUnitLimit(limit)
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
	// This runs once per AI player per tick and almost always finds the maps
	// already complete, but the sorted key list it tests against was rebuilt
	// from scratch every time: a map walk and a string sort over the whole
	// catalog's unit table for an answer that cannot change while the catalog
	// does not. The list is a pure function of the catalog's key set, so it is
	// cached against the catalog identity and its size; a different catalog,
	// or a fixture that adds or removes a definition, rebuilds it.
	types := m.strategicTypes
	if m.strategicTypesFor != cat || len(types) != len(cat.Units) {
		types = make([]string, 0, len(cat.Units))
		for k := range cat.Units {
			types = append(types, k)
		}
		sort.Strings(types)
		m.strategicTypes, m.strategicTypesFor = types, cat
	}
	if strategicMapsComplete(&m.Strategic, types) {
		return
	}
	// Init and the loop below may retain what they are handed, so the rare
	// rebuild path gets its own copy rather than the cached slice.
	types = append([]string(nil), types...)
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

// Tick runs computer tasks, autonomous weapon maintenance, then the separate
// strategic refresh. The session owns player eligibility and calls it before
// the settlement deadline [08 "Dispatch gates and order sinks"]
// [05 "Authoritative settlement order"].
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
	// Inner gate: the 30-countdown, the classification sweep and the due virtual
	// tasks all run only when this manager's own controller equals the computer
	// policy value [08 "Dispatch gates and order sinks"] [PLAN_11 C1].
	//
	// The countdown and the sweep used to sit above this gate, on the outer
	// eligible cadence, so a human slot (controller 1) classified its own units
	// as well. That stamped an AI task-group number on every unit a human owned,
	// and the composer's unit-label walk draws the digit `'0' + group` under any
	// unit carrying a nonzero group [03 R-FX-01 §6] — which is why a stock
	// skirmish showed a "4" under the commander and a "1" under every unarmed
	// building of the local player. The section is explicit about the order: the
	// outer failure arm "still runs weapon maintenance but no virtual tasks",
	// and it is only "when the gate passes" that "the manager decrements its
	// 30-countdown; when it reaches zero it resets to 30 and runs the
	// classification sweep".
	if ctrl == 2 {
		// Eligible entry — count for classification cadence [08] "classifications run every 30 eligible manager entries" [PLAN_11 C3]
		if m.countdown == 0 {
			m.countdown = 30
		}
		m.countdown--
		if m.countdown == 0 { // [08] every 30 eligible entries [PLAN_11 C3]
			m.runClassifications(tick, w, econ)
		}
		// Class vectors are initialized before the first task can select a
		// candidate, but their periodic refresh is deliberately below task
		// dispatch. Selection therefore observes the prior refresh for this tick
		// [R-P0-05 §1, §6].
		m.EnsureStrategicInitialized()
		m.runDueTasks(tick, w, econ)
	}
	if m.WeaponMaintenance != nil {
		m.WeaponMaintenance(m.Player)
	}
	// Strategic refresh follows manager dispatch and precedes economy
	// settlement, which invokes this method as its before-deadline callback
	// [R-P0-05 §6]. Controller values 1 and 3 still participate in the outer
	// eligible cadence, but do not execute virtual tasks.
	if m.Catalog != nil || m.Strategic.Catalog != nil {
		if m.Strategic.MaybeRefresh(tick, m.simRNG(), m.Player, w) {
			m.refreshRallyTargets(w, econ)
		}
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
	// Records first: a handle whose slot the unit pool has recycled since the
	// member died is not a member of that record under the direct writer's
	// invariant, and the classifier's ungrouped gate would otherwise never see
	// the slot's new occupant as ungrouped [08 R-P0-04 §3].
	m.reconcileGroupRecords(w)
	m.classifyGroups(w)
}

// runDueTasks executes due virtual tasks based on absolute deadlines [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3][P0-02].
// Deadline compare is unsigned deadline <= tick meaning due; while deadline > tick it is in the future and skipped [05 "Authoritative settlement order"] style.
// Tasks are swept in ascending manager offset order (I1) via TaskKind order which mirrors slot order [P0-02 §1.3].
func (m *Manager) runDueTasks(tick uint32, w *units.World, econ *economy.Service) {
	for k := TaskKind(0); k < TaskKindCount; k++ {
		// Slot 0 is an empty task pointer. It is never invoked and its
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
		// Every task writes its next deadline before executing its body. For
		// explore and rally this also makes RNG(900)/RNG(150) the first branch
		// draw, including empty-group invocations [08 R-AI-01 §4, §6, §7].
		next := m.nextDeadline(k, tick)
		if (k == TaskExplore || k == TaskRally) && next == m.Deadlines[k] && m.simRNG() == nil {
			continue
		}
		m.Deadlines[k] = next
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
			m.doRally(tick, w, econ)
		}
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

// doConstruction is the construction-and-positioning task body. It reschedules
// first (the caller has already written tick+90), reads the strategic centre
// once into a local, and then runs the two independent passes over the task's
// group vector in vector order: pass one places buildings, pass two
// repositions builders. A member can be acted on by both passes in the same
// invocation [08 R-AI-01 §3].
//
// Two corrections to the previous body are folded in here, both from the same
// section. It used to score every eligible builder and act only on the
// highest-scoring one, so a computer player with two mobile builders started
// at most one building per ninety ticks; the section places for **each**
// member in vector order. And the manager's damage/loss throttle deadline used
// to return from the whole task, so a single unit loss silenced every builder;
// the section gates only `cancapture` builders on it, next to the
// build-capable-count gate.
func (m *Manager) doConstruction(tick uint32, w *units.World, econ *economy.Service) {
	if w == nil || econ == nil {
		return
	}
	// The centre is read once, before either pass, and both passes use that
	// copy [08 R-AI-01 §3].
	centreX, centreY, centreZ := m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ
	// The build-capable count is the strategic refresh's product, never
	// recomputed here [08 R-AI-01 §3][08 R-P0-05 §5].
	buildCapable := m.Strategic.BuildCapable
	m.constructionPlacePass(tick, w, econ, centreX, centreZ, buildCapable)
	m.constructionRepositionPass(tick, w, centreX, centreY, centreZ, buildCapable)
}

// constructionPlacePass is pass one: choose and place a building for every
// member of the construction vector, in vector order [08 R-AI-01 §3].
func (m *Manager) constructionPlacePass(tick uint32, w *units.World, econ *economy.Service, centreX, centreZ numeric.Fixed, buildCapable int32) {
	// The retail task does not rediscover builders from the world when its
	// vector is empty; the classifier and ordinary group writers are the
	// admissions to this input [R-P0-04].
	for _, h := range m.GroupConstruction {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != m.Player || u.Def == nil {
			continue
		}
		// The membership test is the definition's build-option count, not its
		// authored builder flag [08 R-AI-01 §3].
		if !m.hasBuildOptionsForDef(u.Def) {
			continue
		}
		if u.Def.CanCapture {
			// Signed compare against five build-capable own units, then the
			// manager's damage/loss throttle deadline as an unsigned compare.
			// Both gates are `cancapture`-only [08 R-AI-01 §3].
			if buildCapable >= 5 {
				continue
			}
			if tick < m.unitLossDeadline {
				continue
			}
		}
		// Pass 1 skips a builder whose current primary record carries static
		// gate-mask bit 3. The bit's semantic name remains unknown; test the
		// record copy directly, not its command identity or dynamic wake mask
		// [08 R-AI-01 §3][04 "Order descriptor table"].
		if q := orders.QueueOfUnit(u); q != nil {
			if current := q.Head(); current != nil && current.StaticGate&0x8 != 0 {
				continue
			}
		}
		cand, ok := Select(m, u, econ)
		if !ok {
			continue
		}
		// P0-07: typed build path [P0-07] ON-06 F-P0-004.
		// Mobile builders use placement with MobileSite site coordinates;
		// factories use FactoryQueue without site. Factory vs mobile is
		// determined by both builder immobility and target mobility [P0-07].
		// Buildings (non-mobile targets) always require site placement even if
		// the builder is factory-like, preserving yard validation
		// [04 §6.2][P0-03]. Orders are issued ONLY through the typed queue and
		// the descriptor registry [PLAN_11 C12] [08].
		// The allocator's runtime building-class bit is the authored bmcode
		// discriminator. It is set for buildings (bmcode zero), independently
		// of yard text, movement flags, or velocity [08 "Classifier
		// eligibility, destinations, and order"; 05 "Factory production
		// lifecycle"].
		isFactoryBuilder := u.Def.Builder && u.Flags&classifierBuilding != 0
		isTargetMobile := false
		cat := m.Catalog
		if cat == nil {
			cat = m.Strategic.Catalog
		}
		if cat != nil {
			if def, ok := cat.Unit(cand.DefKey); ok && def != nil {
				isTargetMobile = def.BMCode != 0
			}
		}
		if isFactoryBuilder && isTargetMobile {
			// Factory production: direct typed queue without placement.
			if m.QueueBuildTyped == nil {
				// Production composition binds this ordinary order sink. An
				// unbound fixture has no supported submission path.
				continue
			}
			req := BuildRequest{
				Builder: u.Handle,
				UnitKey: cand.DefKey,
				Count:   1,
				Kind:    BuildKindFactoryQueue,
			}
			_ = m.QueueBuildTyped(req)
			continue
		}
		// Mobile site construction via the placement root. The root searches
		// and validates but does not submit: the task applies the distance cap
		// to the returned site first, and only then submits the typed
		// MobileSite request [08 R-AI-03 §5][08 R-AI-01 §3].
		origFactory := m.Factory
		m.Factory = u
		res := PlaceCandidate(m, cand.DefKey, m.Terrain)
		placed := res.Valid
		if placed && u.Def.CanCapture && !withinConstructionCap(m.Terrain, res.WorldX, res.WorldZ, centreX, centreZ) {
			// The cap vetoes the placement without resetting anything; the
			// radius is already zero by then [08 R-AI-01 §3][08 R-AI-03 §5].
			placed = false
		}
		if placed {
			_ = queueExactResult(m, cand.DefKey, res)
		}
		m.Factory = origFactory
	}
}

// withinConstructionCap applies the `cancapture` distance cap of
// [08 R-AI-01 §3]: the site is vetoed when its planar distance from the
// strategic centre exceeds one third of the sum of the map's playfield
// extents. The playfield extents are the terrain's world-unit width less 32
// and height less 128; the divide truncates toward zero and the result is a
// plain world-unit radius. The vertical term of the distance is a literal
// zero, not an omitted term. Non-`cancapture` builders are not capped, and a
// manager without terrain has no map extent to derive a cap from.
func withinConstructionCap(terrain *world.Terrain, siteX, siteZ, centreX, centreZ numeric.Fixed) bool {
	if terrain == nil {
		return true
	}
	playW := terrain.CellW*16 - 32
	playH := terrain.CellH*16 - 128
	limit := int64((playW+playH)/3) << 16
	dx := int64(fixedWordDelta(siteX, centreX))
	dz := int64(fixedWordDelta(siteZ, centreZ))
	d := int64(placementIntegerSqrt(uint64(dx*dx) + uint64(dz*dz)))
	return d <= limit
}

// constructionRepositionPass is pass two: reposition the vector's builders
// around the strategic centre [08 R-AI-01 §3]. A member whose current order
// does not carry static gate-mask bit 14 is skipped, so a builder already
// carrying a build order keeps it; only an order-free builder is repositioned.
func (m *Manager) constructionRepositionPass(tick uint32, w *units.World, centreX, centreY, centreZ numeric.Fixed, buildCapable int32) {
	for _, h := range m.GroupConstruction {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != m.Player || u.Def == nil {
			continue
		}
		if q := orders.QueueOfUnit(u); q != nil {
			if current := q.Head(); current != nil && current.StaticGate&0x4000 == 0 {
				continue
			}
		}
		if u.Def.CanCapture {
			if buildCapable < 5 {
				continue
			}
			// The working copy of the centre takes the unit's own height; the
			// distance forces its vertical term to zero [08 R-AI-01 §3].
			dx := int64(fixedWordDelta(centreX, u.X))
			dz := int64(fixedWordDelta(centreZ, u.Z))
			d := int64(placementIntegerSqrt(uint64(dx*dx) + uint64(dz*dz)))
			var tx, tz numeric.Fixed
			if d > int64(640)<<16 {
				s := m.simRNG()
				if s == nil {
					// The production session always binds the simulation
					// stream; there is no alternate random source [I4].
					continue
				}
				a := numeric.Angle(uint16(s.Uint32n(65536)))
				tx = numeric.Fixed(int32(centreX) - numeric.MulRound(numeric.Sin(a), 640<<16))
				tz = numeric.Fixed(int32(centreZ) - numeric.MulRound(numeric.Cos(a), 640<<16))
			} else {
				tx = numeric.Fixed(2*int32(centreX) - int32(u.X))
				tz = numeric.Fixed(2*int32(centreZ) - int32(u.Z))
			}
			m.submitResolvedOrder(u, resolveAIIntent(2, u, nil, tx, u.Y, tz), nil, tx, u.Y, tz, tick, 0, 0)
			m.submitResolvedOrder(u, resolveAIIntent(9, u, nil, centreX, u.Y, centreZ), nil, centreX, u.Y, centreZ, tick, 1, 0)
			continue
		}
		dx := int64(fixedWordDelta(centreX, u.X))
		dy := int64(fixedWordDelta(centreY, u.Y))
		dz := int64(fixedWordDelta(centreZ, u.Z))
		d := int64(placementIntegerSqrt(uint64(dx*dx) + uint64(dy*dy) + uint64(dz*dz)))
		tx, ty, tz := centreX, centreY, centreZ
		switch {
		case d >= int64(320)<<16:
			// The target is the centre itself.
		case d < int64(16)<<16:
			s := m.simRNG()
			if s == nil {
				continue
			}
			a := numeric.Angle(uint16(s.Uint32n(65536)))
			tx = numeric.Fixed(int32(u.X) - numeric.MulRound(numeric.Sin(a), 320<<16))
			ty = u.Y
			tz = numeric.Fixed(int32(u.Z) - numeric.MulRound(numeric.Cos(a), 320<<16))
		default:
			// A fixed-point interpolation that lands 320 world units from the
			// unit along the direction to the centre; each axis is scaled
			// independently by the same 64-bit quotient [08 R-AI-01 §3].
			scale := (int64(320) << 32) / d
			tx = numeric.Fixed(int32(u.X) + int32((dx*scale)>>16))
			ty = numeric.Fixed(int32(u.Y) + int32((dy*scale)>>16))
			tz = numeric.Fixed(int32(u.Z) + int32((dz*scale)>>16))
		}
		m.submitResolvedOrder(u, resolveAIIntent(9, u, nil, tx, ty, tz), nil, tx, ty, tz, tick, 0, 0)
	}
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
		// Saved and explicitly assigned groups need not match classifier
		// destinations. The resource body itself requires a building before
		// either activation or factory selection [08 R-AI-01 §2].
		if u.Flags&classifierBuilding == 0 {
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
			// Established: equality disables; nonpositive net energy and a
			// zero draw leave activation unchanged [08 R-AI-01 §2].
			if energyStock <= metalStock+metalStock {
				u.SetActivated(false)
			} else if netEnergy > 0 {
				// Only this surplus branch admits RNG(5) [08 R-AI-01 §2][I4].
				s := m.simRNG()
				if s == nil {
					// The production session always binds the simulation stream;
					// without it there is no supported activation decision.
					continue
				}
				if s.Uint32n(5) != 0 {
					u.SetActivated(true)
				}
			}
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
		// Only a nonempty primary queue blocks selection. A newly completed
		// or restored factory may not have allocated its first queue yet; the
		// ordinary build producer creates it after selection [08 R-AI-01 §2].
		if q := orders.QueueOfUnit(u); q != nil && q.LenPrimary() > 0 {
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
	if w == nil {
		return
	}
	var groupID, peerID uint8
	var engaged *bool
	if threshold == waveAThreshold {
		groupID, peerID = 2, 3
		engaged = &m.waveAEngaged
	} else {
		groupID, peerID = 6, 7
		engaged = &m.waveBEngaged
	}
	// The merge rewrites the wave record, so the slice is read after it and
	// never before [08 "Wave merge" correction].
	m.mergeWaveGroupRecords(groupID, peerID, w, threshold)
	group := m.GroupWaveB
	if threshold == waveAThreshold {
		group = m.GroupWaveA
	}
	n := len(group)
	if n == 0 {
		return
	}
	attack := false
	if min < n {
		attack = *engaged || max <= n
	}
	if !attack {
		// Gather at the first non-empty base record: armed buildings, then
		// resource buildings, then builders. A successful gather clears the
		// engagement latch and broadcasts a fresh move in unit-pool order
		// [08 R-AI-01 §4, §9].
		for _, base := range []struct {
			groupID uint8
			handles []pool.Handle
		}{
			{groupID: 5, handles: m.GroupNull},
			{groupID: 1, handles: m.GroupResource},
			{groupID: 4, handles: m.GroupConstruction},
		} {
			cx, cy, cz, ok := groupCentroid(base.handles, w)
			if !ok {
				continue
			}
			*engaged = false
			m.broadcastGroupOrder(w, groupID, 2, 0, nil, cx, cy, cz, tick, 0xa0)
			return
		}
	}

	// No base centroid is a deliberate fall-through to attack, even for a
	// group below the normal engage threshold [08 R-AI-01 §4].
	*engaged = true
	cx, cy, cz, ok := groupCentroid(group, w)
	if !ok {
		return
	}
	target := m.nearestHostileUnit(w, econ, cx, cy, cz)
	if target == nil {
		return
	}
	m.broadcastGroupOrder(w, groupID, 3, 0, target, target.X, target.Y, target.Z, tick, 0)
}

// mergeWaveGroupRecords applies the recovered wave merge through the direct
// writer. Every transfer therefore performs source swap-delete, destination
// append, and the unit Group update as one operation [R-P0-04 §3, §5].
func (m *Manager) mergeWaveGroupRecords(groupID, peerID uint8, w *units.World, threshold int32) {
	if m == nil || w == nil {
		return
	}
	// Both records are reconciled before the merge reads their counts: the
	// bootstrap, the farthest-member loop and the peer collection all size
	// their comparisons on the record's member count, and the loop's exit
	// depends on each transfer shrinking the own record [08 R-P0-04 §3].
	m.reconcileGroupRecord(groupID, w)
	m.reconcileGroupRecord(peerID, w)
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
	var sumX, sumZ int32
	for _, h := range *current {
		u := w.Unit(h) // reconciled live members [08 R-P0-04 §3]
		sumX += retailCoord(u.X)
		sumZ += retailCoord(u.Z)
	}
	cx, cz := sumX/int32(len(*current)), sumZ/int32(len(*current))
	for len(*current) > 1 {
		limit := threshold * int32(len(*current))
		farthest := -1
		var farthestDistance int32
		for i, h := range *current {
			u := w.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			distance := retailDistanceSquared(u, cx, cz)
			if distance > farthestDistance {
				farthest, farthestDistance = i, distance
			}
		}
		if farthest < 0 || farthestDistance < limit {
			break
		}
		before := len(*current)
		// The writer replaces the removed slot with the last member, including
		// when that slot already is last. Retail subtracts that member's
		// coordinates after the transfer, retaining the resulting biased sums
		// rather than recomputing the survivors' true mean [08 R-P0-04 §3].
		replacement := w.Unit((*current)[before-1])
		if u := w.Unit((*current)[farthest]); u != nil {
			m.transferGroupMember(u, groupID, peerID)
		}
		// The loop's only progress is the transfer removing the farthest member
		// from this record. Reconciliation above guarantees every entry can be
		// transferred, so this never fires; it is here so a future break of the
		// record invariant costs one missed transfer instead of a session that
		// never returns from the wave task [08 R-P0-04 §3].
		if len(*current) >= before {
			break
		}
		sumX -= retailCoord(replacement.X)
		sumZ -= retailCoord(replacement.Z)
		cx, cz = sumX/int32(len(*current)), sumZ/int32(len(*current))
	}
	limit := threshold * int32(len(*current))
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

// doRegroup is the regroup task body. It reschedules at tick+150 (the caller
// has already written it), returns unless both its own record and its peer
// wave's record are non-empty, and otherwise broadcasts intent 2 to the peer's
// centroid with queue modifier 0 and the spacing parameter 0. There is no
// target selection, no draw, and no other state write [08 R-AI-01 §5].
//
// Corrected: the body used to walk its own group VECTOR and push a hand-picked
// `Move_Ground`/`VTOL_Move` node, skipping members still under construction.
// The section routes it through the shared group broadcast, which walks the
// player's whole unit slice in ascending POOL order, keys membership from the
// unit's stored group number, resolves the intent through the ordinary
// resolver, and applies no completion filter [08 R-AI-01 §9].
func (m *Manager) doRegroup(tick uint32, w *units.World, econ *economy.Service, peer TaskKind) {
	_ = econ
	if w == nil {
		return
	}
	var ownID uint8
	var peerGroup []pool.Handle
	if peer == TaskWaveA {
		ownID, peerGroup = 3, m.GroupWaveA
	} else {
		ownID, peerGroup = 7, m.GroupWaveB
	}
	own := m.groupVector(ownID)
	if own == nil || len(*own) == 0 || len(peerGroup) == 0 {
		return
	}
	cx, cy, cz, ok := groupCentroid(peerGroup, w)
	if !ok {
		return
	}
	m.broadcastGroupOrder(w, ownID, 2, 0, nil, cx, cy, cz, tick, 0)
}

// broadcastGroupOrder is the shared manager-task broadcast. It scans the
// owner's whole unit slice in pool order and keys membership from Unit.Group,
// not from the task vector's current order [08 R-AI-01 §9].
func (m *Manager) broadcastGroupOrder(w *units.World, group uint8, intent int, modifier uint8, target *units.Unit, x, y, z numeric.Fixed, tick uint32, spacing int32) {
	if m == nil || w == nil {
		return
	}
	// The helper itself does not read the trailing pair: each member's
	// submission carries them into the order node's ARGUMENT word and its
	// companion, the same slots the construction task fills with the product
	// type index for a MobileBuild [08 R-AI-01 §19]. The wave task's gather
	// broadcast passes 160 there and the regroup and explore broadcasts pass 0;
	// the ground move handler reads that word as its phase-0 arrival radius,
	// `argument + 4` [04 R-ORD-01 §4], so a wave gather is a move with a
	// 164-world-unit arrival radius and a regroup or explore move one with
	// radius 4. That is the whole effect of "spacing": no formation, no
	// per-member offset, so the submitted point stays the supplied
	// centroid/target for every member.
	m.broadcastWalk = w.AppendLiveSliced(m.broadcastWalk[:0]) // players then slots ascending (I1)
	for _, u := range m.broadcastWalk {
		if u == nil || !u.Alive || u.Owner != m.Player || u.Def == nil || u.Group != group {
			continue
		}
		// A fresh unit may have no queue yet. Resolver admission already
		// needs the session diplomacy and sea level [04 R-ORD-02 §1].
		orders.BindQueueBinding(u, m.OrderBinding)
		id := resolveAIIntent(intent, u, target, x, y, z)
		m.submitResolvedOrder(u, id, target, x, y, z, tick, modifier, spacing)
	}
}

// resolveAIIntent uses the ordinary canonical resolver for every task shape.
// Position-only attack is resolved there too; the AI owns no descriptor
// substitute or alternate command policy [04 R-ORD-02 §1][08 R-AI-01 §7].
func resolveAIIntent(intent int, actor, target *units.Unit, x, y, z numeric.Fixed) orders.ID {
	pos := &orders.ResolvePos{X: x, Y: y, Z: z}
	return orders.Resolve(intent, actor, target, pos)
}

// submitResolvedOrder pushes one resolved record. `argument` is the word the
// broadcast helper's trailing pair reaches the node through — the same slot the
// construction task fills with the product type index for a MobileBuild — and
// the ground move handler reads it as `argument + 4`, its phase-0 arrival
// radius [08 R-AI-01 §19][04 R-ORD-01 §4]. Every task that submits directly
// passes 0, which is the radius-4 default.
func (m *Manager) submitResolvedOrder(u *units.Unit, id orders.ID, target *units.Unit, x, y, z numeric.Fixed, tick uint32, modifier uint8, argument int32) {
	if m == nil || u == nil || id == 0 {
		return
	}
	var targetHandle pool.Handle
	if target != nil {
		targetHandle = target.Handle
	}
	queued := modifier != 0
	node := orders.NewNodeForOrder(id, targetHandle, x, y, z, tick, u.Handle, queued)
	node.Param1 = uint32(argument)
	q := orders.BindQueueBinding(u, m.OrderBinding)
	if q == nil {
		return
	}
	if !queued {
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
	}
	q.Push(id, node)
}

func (m *Manager) hostileOwner(owner uint8, econ *economy.Service) bool {
	if m == nil || econ == nil || m.IsAlliance == nil || int(owner) >= len(econ.Players) || int(m.Player) >= len(econ.Players) {
		return false
	}
	if owner == m.Player {
		return false
	}
	p := &econ.Players[owner]
	if !p.Exists || (p.ControllerState != 1 && p.ControllerState != 2 && p.ControllerState != 3) {
		return false
	}
	return !m.IsAlliance(m.Player, owner)
}

// The task records store authoritative positions as signed 32-bit 16.16
// words. Keep the arithmetic operations at that width even though
// numeric.Fixed is wider elsewhere in Nanolathe [08 R-AI-01 §§6,7,9].
func fixedWordFromUnits(v int32) numeric.Fixed {
	return numeric.Fixed(v << 16)
}

func fixedWordAdd(a, b numeric.Fixed) numeric.Fixed {
	return numeric.Fixed(int32(a) + int32(b))
}

func fixedWordDelta(a, b numeric.Fixed) int32 {
	return int32(a) - int32(b)
}

func fixedWordNeg(v int32) numeric.Fixed {
	return numeric.Fixed(-v)
}

// nearestHostileUnit keeps the first minimum in player-slot then pool order.
// Distances are fixed-point products shifted to squared world units before a
// strict comparison against the signed-32 initial maximum [08 R-AI-01 §9].
//
// **Correction (2026-09-02, RWU-19-38).** The third candidate test was
// previously read off the death latch; [08 R-AI-01 §9]'s correction says the
// helper tests status-word bit 15, the mission `Immunity` bit
// (`units.ImmunityStatus`, the same bit the target registry's primary list
// gates [06 §3.1]), not the death latch — a hostile that is dying but not
// immune is still a candidate. The fourth test reads the runtime byte's
// cloaked-INSTANCE bit, which this build carries as `Unit.Hidden`
// (`SetCloakedInstance`'s bit, [03 R-VIS-01 §6]), not bit 2 of the 32-bit
// status word.
func (m *Manager) nearestHostileUnit(w *units.World, econ *economy.Service, x, y, z numeric.Fixed) *units.Unit {
	if m == nil || w == nil || econ == nil {
		return nil
	}
	_ = y // [08 R-AI-01 §9] the vertical coordinate is passed but never read.
	bestDistance := int64(1<<31 - 1)
	var best *units.Unit
	m.hostileWalk = w.AppendLiveSliced(m.hostileWalk[:0]) // players then slots ascending (I1)
	for _, u := range m.hostileWalk {
		if u == nil || !u.Alive || u.Def == nil || !m.hostileOwner(u.Owner, econ) {
			continue
		}
		if u.Flags&0x3 == 2 || u.Flags&units.ImmunityStatus != 0 || u.Hidden {
			continue
		}
		dx := int64(fixedWordDelta(u.X, x))
		dz := int64(fixedWordDelta(u.Z, z))
		distance := ((dx * dx) >> 32) + ((dz * dz) >> 32)
		if distance < bestDistance {
			bestDistance = distance
			best = u
		}
	}
	return best
}

// RallyBattleBindings are the session-owned predicates needed by the rally
// task. Keeping them explicit prevents an unbound manager from gaining
// omniscient visibility or inventing a locomotion/admission approximation.
type RallyBattleBindings struct {
	Visible    func(viewer uint8, target *units.Unit) bool
	ProbeKnown func(owner uint8, x, y, z numeric.Fixed) bool
	// ShotTimeAdmits is the slot-1 shot-time physical gate of [06 §3.3] asked
	// against a point; the rally task applies it to a member with no mover
	// [08 R-AI-01 §19].
	ShotTimeAdmits func(unit *units.Unit, x, y, z numeric.Fixed) bool
}

// InitializeBattleState binds the terrain-dependent manager state at battle
// entry and initializes rally best, probe, drift, and score in retail order.
// It is intentionally explicit and one-shot: rally execution never lazily
// derives constructor state from whichever terrain happens to be present
// [08 R-AI-02 §1]. SP-REV-03 owns the production call site.
func (m *Manager) InitializeBattleState(terrain *world.Terrain, bindings RallyBattleBindings) bool {
	if m == nil || terrain == nil || m.rallyInitialized {
		return false
	}
	m.Terrain = terrain
	m.RallyVisible = bindings.Visible
	m.RallyProbeKnown = bindings.ProbeKnown
	m.RallyShotTimeAdmits = bindings.ShotTimeAdmits
	halfX := (terrain.CellW * 16) / 2
	halfZ := (terrain.CellH * 16) / 2
	x := fixedWordFromUnits(halfX)
	z := fixedWordFromUnits(halfZ)
	m.rallyBestX, m.rallyBestY, m.rallyBestZ = x, 0, z
	m.rallyProbeX, m.rallyProbeY, m.rallyProbeZ = x, 0, z
	m.rallyDriftX, m.rallyDriftY, m.rallyDriftZ = x, 0, z
	m.rallyBestScore = 0
	m.rallyInitialized = true
	return true
}

// refreshRallyTargets rebuilds the strategic state's first vector only on its
// 30-tick refresh. The visibility predicate is supplied by session composition;
// nil remains empty instead of becoming omniscient [08 R-AI-01 §16].
func (m *Manager) refreshRallyTargets(w *units.World, econ *economy.Service) {
	if m == nil {
		return
	}
	m.rallyTargets = m.rallyTargets[:0]
	if w == nil || econ == nil || m.RallyVisible == nil {
		return
	}
	m.rallyWalk = w.AppendLiveSliced(m.rallyWalk[:0]) // players then slots ascending (I1)
	for _, u := range m.rallyWalk {
		if u == nil || !u.Alive || u.Dying || !m.hostileOwner(u.Owner, econ) || !m.RallyVisible(m.Player, u) {
			continue
		}
		m.rallyTargets = append(m.rallyTargets, u.Handle)
	}
}

func (m *Manager) rallyProbeScore(w *units.World, x, z numeric.Fixed) int32 {
	if m == nil || w == nil {
		return 0
	}
	var score int32
	for _, h := range m.rallyTargets {
		u := w.Unit(h)
		if u == nil || u.Def == nil {
			continue
		}
		dx := int64(fixedWordDelta(u.X, x))
		dz := int64(fixedWordDelta(u.Z, z))
		if ((dx*dx)>>32)+((dz*dz)>>32) > 160*160 {
			continue
		}
		key := canonicalKey(u.Def.CanonicalKey)
		if key == "" {
			key = canonicalKey(u.Def.UnitName)
		}
		score += int32(m.Strategic.SingleVectors[key])
	}
	return score
}

// drawBelowSigned preserves the simulation helper's signed bound gate: values
// below two, including negative scores, return zero without advancing
// [01 §7.1][08 R-AI-01 §7].
func drawBelowSigned(r *rng.Simulation, bound int32) uint32 {
	if r == nil || bound < 2 {
		return 0
	}
	return r.Uint32n(uint32(bound))
}

// doExplore implements the explore/gather task at tick plus 30 plus RNG(900)
// [08 "Strategy manager and its task graph"].
// The explore vector is populated by the recovered classifier (category 8),
// load, control-group, or wave-transfer writers. An empty vector still consumes
// the centre-branch draws before broadcasting to no members [08 R-AI-01 §6].
func (m *Manager) doExplore(tick uint32, w *units.World, econ *economy.Service) {
	_ = econ
	if w == nil {
		return
	}
	r := m.simRNG()
	if r == nil {
		return
	}
	group := m.GroupExplore
	if m.Terrain == nil {
		return
	}
	mapWidth := m.Terrain.CellW * 16
	mapHeight := m.Terrain.CellH * 16
	if len(group) < 5 {
		centreX, centreY, centreZ := m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ
		centreY = numeric.Fixed(int32(centreY))
		if int16(int32(centreX)>>16) != 0 || int16(int32(centreZ)>>16) != 0 {
			legs := r.Uint32n(2) + 2
			gw, gh := mapWidth>>3, mapHeight>>3
			for i := uint32(0); i < legs; i++ {
				tx := fixedWordAdd(centreX, fixedWordFromUnits(int32(r.Uint32n(uint32(gw)))-gw/2))
				tz := fixedWordAdd(centreZ, fixedWordFromUnits(int32(r.Uint32n(uint32(gh)))-gh/2))
				intent, modifier := 9, uint8(1)
				if i == 0 {
					intent, modifier = 2, 0
				}
				m.broadcastGroupOrder(w, 8, intent, modifier, nil, tx, centreY, tz, tick, 0)
			}
			return
		}

		cx, cy, cz, ok := groupCentroid(group, w)
		if !ok {
			return
		}
		target := m.nearestHostileUnit(w, econ, cx, cy, cz)
		if target == nil {
			// Retail dereferences the missing target here. Bounding it to a
			// deterministic no-op is the research-sanctioned divergence
			// [08 R-AI-01 §6].
			return
		}
		m.broadcastGroupOrder(w, 8, 9, 1, nil, target.X, target.Y, target.Z, tick, 0)
		return
	}

	// Five or more members patrol one random map edge. Exactly three body
	// draws occur: orientation, one coordinate, and near/far edge selection
	// [08 R-AI-01 §6].
	var x, z int32
	if r.Uint32n(2) != 0 {
		x = int32(r.Uint32n(uint32(mapWidth)))
		if r.Uint32n(2) == 0 {
			z = mapHeight - 1
		}
	} else {
		if r.Uint32n(2) == 0 {
			x = mapWidth - 1
		}
		z = int32(r.Uint32n(uint32(mapHeight)))
	}
	m.broadcastGroupOrder(w, 8, 9, 0, nil, fixedWordFromUnits(x), 0, fixedWordFromUnits(z), tick, 0)
}

// doRally implements the random-walk rally task at tick plus 30 plus RNG(150)
// [08 "Strategy manager and its task graph"].
// An empty rally vector remains a normal no-op. Category 9 is not emitted by
// the classifier and must arrive through a separate established writer
// [R-P0-04].
func (m *Manager) doRally(tick uint32, w *units.World, econ *economy.Service) {
	_ = econ
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
	if !m.rallyInitialized {
		// Battle entry binds the three rally vectors through
		// InitializeBattleState [08 R-AI-02 §1]; session composition calls it
		// for every manager it builds. An unbound manager is a fixture that
		// skipped composition, and it must stay inert rather than rally from
		// zeroed vectors.
		return
	}
	if r.Uint32n(10) == 0 {
		m.rallyProbeX, m.rallyProbeY, m.rallyProbeZ = m.rallyBestX, m.rallyBestY, m.rallyBestZ
		a := numeric.Angle(r.Uint32n(65536))
		m.rallyDriftX = fixedWordNeg(numeric.MulRound(numeric.Sin(a), int32(numeric.FixedFromInt(320))))
		m.rallyDriftY = 0
		m.rallyDriftZ = fixedWordNeg(numeric.MulRound(numeric.Cos(a), int32(numeric.FixedFromInt(320))))
	}
	m.rallyProbeX = fixedWordAdd(m.rallyProbeX, m.rallyDriftX)
	m.rallyProbeY = fixedWordAdd(m.rallyProbeY, m.rallyDriftY)
	m.rallyProbeZ = fixedWordAdd(m.rallyProbeZ, m.rallyDriftZ)
	if m.RallyProbeKnown != nil && m.RallyProbeKnown(m.Player, m.rallyProbeX, m.rallyProbeY, m.rallyProbeZ) {
		score := m.rallyProbeScore(w, m.rallyProbeX, m.rallyProbeZ)
		if drawBelowSigned(r, m.rallyBestScore) < drawBelowSigned(r, score) {
			m.rallyBestScore = score
			m.rallyBestX, m.rallyBestY, m.rallyBestZ = m.rallyProbeX, m.rallyProbeY, m.rallyProbeZ
		}
	}
	// Rally is the only task whose submissions follow group-vector order.
	for _, h := range group {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Def == nil || !u.Def.CanAttack {
			continue
		}
		// Only a member with no mover is gated, and the gate is the slot-1
		// shot-time physical check against `best` [08 R-AI-01 §19]. The creator
		// allocates a mover for `bmcode == 1` alone; only byte one bypasses
		// this gate [08 R-AI-03 §7.4]. A building
		// whose first slot cannot reach `best` is skipped without resolving
		// anything, and one with no weapon in slot 1 reads the sentinel
		// record's zero range and is likewise skipped.
		if u.Def.BMCode != 1 {
			if m.RallyShotTimeAdmits == nil || !m.RallyShotTimeAdmits(u, m.rallyBestX, m.rallyBestY, m.rallyBestZ) {
				continue
			}
		}
		id := resolveAIIntent(3, u, nil, m.rallyBestX, m.rallyBestY, m.rallyBestZ)
		m.submitResolvedOrder(u, id, nil, m.rallyBestX, m.rallyBestY, m.rallyBestZ, tick, 0, 0)
	}
}
