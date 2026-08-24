package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
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
		if rng.Global.Sim != nil {
			r = rng.Global.Sim.Uint32n(900) // [08] bound 900 (I4) [P0-02][PLAN_11 C9]
		} else if m.RNG != nil {
			r = m.RNG.Uint32n(900)
		}
		return tick + 30 + r // [08] tick+30+RNG(900) [P0-02]
	case TaskOther150:
		var r uint32
		if rng.Global.Sim != nil {
			r = rng.Global.Sim.Uint32n(150) // [08] bound 150 (I4) [P0-02]
		} else if m.RNG != nil {
			r = m.RNG.Uint32n(150)
		}
		return tick + 30 + r // [08] tick+30+RNG(150) [P0-02]
	case TaskEmpty, TaskNullSub:
		return 0 // stays 0 [P0-02]
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
	// Iterate completed units in pool asc (I1) stable ordering, no map iteration.
	for _, u := range w.Iter() {
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
			if 2*metalStock > energyStock && netEnergy >= 1 {
				// Draw RNG(5) only when branch taken [P0-02 §5]
				var draw uint32
				if rng.Global.Sim != nil {
					draw = rng.Global.Sim.Uint32n(5)
				} else if m.RNG != nil {
					draw = m.RNG.Uint32n(5)
				} else {
					draw = 1 // default non-zero to enable
				}
				if draw != 0 {
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					_ = draw
					// Placeholder: toggle would set unit active; we just respect draw count for determinism.
					// No privileged mutation; we don't actually toggle here beyond RNG consumption.
				} else {
					// RNG 0 => remain off, no enable call [P0-02]
				}
			} else {
				// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			}
		} else {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			if u.Def.Builder && u.Def.BuildCostMetal == 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
				// In real code would check queue[0x17]==null and queue[0x42]&8==0 etc. We stub as no-op but keep structure.
				// Candidate selection via Select would be called but we are in resource task, not construction.
				// For now, just keep placeholder without queue mutation to preserve determinism and not invent queue state.
			}
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doWave(tick uint32, w *units.World, econ *economy.Service, threshold int32, min, max int) {
	_, _, _, _, _, _ = tick, w, econ, threshold, min, max
	// Placeholder: no group population writer located within bounded displacement search [P0-02 §6] NEGATIVE-BOUNDED.
	// We keep the deadline and threshold logic exact, but group iteration is empty as static.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Keep TODO(T25) for AI transport geometry only; wave geometry thresholds are now established.
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doRegroup(tick uint32, w *units.World, econ *economy.Service, peer TaskKind) {
	_, _, _, _ = tick, w, econ, peer
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doExplore(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m *Manager) doRally(tick uint32, w *units.World, econ *economy.Service) {
	_, _, _ = tick, w, econ
	// Placeholder: would walk group via RNG 0x10000 etc. Currently empty -> no-op.
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
