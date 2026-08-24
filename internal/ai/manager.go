package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TaskKind identifies the manager virtual tasks [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3].
// Retail manager 0x3D bytes holds several per-task absolute deadline slots; this enum groups them by their rescheduling formula.
type TaskKind int

const (
	TaskConstruction TaskKind = iota // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskPositioning                  // positioning at tick+90 [08] [PLAN_11 C3]
	TaskResource                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TaskActivity                     // activity at +30 [08] [PLAN_11 C3]
	TaskOther900                     // others at +30+RNG(900) [08] [PLAN_11 C3]
	TaskOther150                     // others at +30+RNG(150) [08] [PLAN_11 C3]
	TaskKindCount                    // count of kinds
)

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
	RNG          *rng.Simulation  // nil => rng.Global.Sim [01 §7.1] I4
	SurfaceMetal int32            // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Catalog      *content.Catalog // defKey resolution for the extractor gate [PLAN_11 C8]
	Factory      *units.Unit      // builder receiving construction.QueueBuild [PLAN_11 C12]
	Terrain      *world.Terrain   // placement validation terrain; nil skips yard validation, success resets radius [PLAN_11 C8]

	entryCount         uint32 // eligible manager entries for classification cadence [08][PLAN_11 C3]
	classificationRuns int
	taskRuns           [TaskKindCount]int
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
	_, _, _ = tick, w, econ
}

// runDueTasks executes due virtual tasks based on absolute deadlines [08 "Established AI-facing data and rooted planner"] [PLAN_11 C3].
// Deadline compare is unsigned deadline <= tick meaning due; while deadline > tick it is in the future and skipped [05 "Authoritative settlement order"] style.
func (m *Manager) runDueTasks(tick uint32, w *units.World, econ *economy.Service) {
	for k := TaskKind(0); k < TaskKindCount; k++ {
		deadline := m.Deadlines[k]
		if deadline > tick { // [08] separate deadlines [PLAN_11 C3]; 0 <= any tick so initial zero is due
			continue
		}
		m.taskRuns[k]++
		switch k {
		case TaskConstruction:
			m.doConstruction(tick, w, econ)
		case TaskPositioning:
			m.doPositioning(tick, w, econ)
		case TaskResource:
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		case TaskActivity:
			// TODO(T25): blocked [08 "What remains not established"]
		case TaskOther900:
			// TODO(T25): remaining classes blocked; rescheduling still follows tick+30+RNG(900) [08][PLAN_11 C3]
		case TaskOther150:
			// TODO(T25): blocked
		}
		m.Deadlines[k] = m.nextDeadline(k, tick)
	}
}

func (m *Manager) nextDeadline(k TaskKind, tick uint32) uint32 {
	switch k {
	case TaskConstruction, TaskPositioning:
		return tick + 90 // [08] construction/positioning at currentTick+90 [PLAN_11 C3]
	case TaskResource, TaskActivity:
		return tick + 30 // [08] resource/activity at +30 [PLAN_11 C3]
	case TaskOther900:
		var r uint32
		if rng.Global.Sim != nil {
			r = rng.Global.Sim.Uint32n(900) // [08] bound 900 (I4) [PLAN_11 C3][C9]
		}
		return tick + 30 + r // [08] tick+30+RNG(900) [PLAN_11 C3]
	case TaskOther150:
		var r uint32
		if rng.Global.Sim != nil {
			r = rng.Global.Sim.Uint32n(150) // [08] bound 150 (I4) [PLAN_11 C3][C9]
		}
		return tick + 30 + r // [08] tick+30+RNG(150) [PLAN_11 C3]
	default:
		return tick + 30
	}
}

func findBuilder(w *units.World, player uint8) *units.Unit {
	if w == nil {
		return nil
	}
	for _, u := range w.Iter() { // [I1] pool asc [05 "Authoritative settlement order"]
		if u == nil || !u.Alive {
			continue
		}
		if u.Owner != player {
			continue
		}
		if u.Def == nil {
			continue
		}
		return u
	}
	return nil
}

func (m *Manager) doConstruction(tick uint32, w *units.World, econ *economy.Service) {
	_, _ = tick, econ
	if w == nil || econ == nil {
		return
	}
	builder := findBuilder(w, m.Player)
	if builder == nil {
		return
	}
	// Issues orders ONLY through ordinary paths — construction.QueueBuild and descriptor registry [PLAN_11 C12] [08 "Established AI-facing data and rooted planner"].
	// No privileged mutation. Chain Select → Place → QueueBuild [PLAN_11 C8+C12].
	cand, ok := Select(m, builder, econ) // [PLAN_11 C5-C7]
	if !ok {
		return
	}
	// Route through Place before QueueBuild [PLAN_11 C8+C12]: Place moves search
	// origin toward strategic center using stored radius, handles the extractor
	// RNG(255) branch, validates against terrain yard map, resets radius on
	// success, and issues the build command via the ordinary queueBuild path.
	// Temporarily bind Factory to the builder that Select evaluated so the
	// placement queues to the correct unit even if Manager.Factory was stale;
	// restore afterwards. On Place failure the radius growth and origin step
	// are already applied and the deadline reschedule in runDueTasks provides the retry.
	origFactory := m.Factory
	m.Factory = builder
	_, _, ok = Place(m, cand.DefKey, m.Terrain) // [PLAN_11 C8][C12]
	m.Factory = origFactory
	if !ok {
		return
	}
}

func (m *Manager) doPositioning(tick uint32, w *units.World, econ *economy.Service) {
	_, _ = tick, econ
	if w == nil || econ == nil {
		return
	}
	builder := findBuilder(w, m.Player)
	if builder == nil {
		return
	}
	cand, ok := Select(m, builder, econ) // [PLAN_11 C5-C7]
	if !ok {
		return
	}
	// Same Select → Place → QueueBuild chain as construction [PLAN_11 C8+C12].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Place placeholder falls through to generic build-command path so AI still builds valid structures [PLAN_11 Explicit unknowns].
	origFactory := m.Factory
	m.Factory = builder
	_, _, ok = Place(m, cand.DefKey, m.Terrain) // [PLAN_11 C8][C12]
	m.Factory = origFactory
	if !ok {
		return
	}
}

// Dispatch iterates the ten players per-tick entry [08 "Established AI-facing data and rooted planner"] [PLAN_11 C1].
// Outer gate controller ∈ {1,2,3} and index !=10 dispatches through manager pointer; inner gate controller==2 is enforced inside Manager.Tick [08][PLAN_11 C1].
// Both gates required.
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
