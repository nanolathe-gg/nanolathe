// The computer player's think-step seam: the point where a per-player manager
// decides what to do with its state on a tick the session dispatched to it.
// docs/DESIGN_GAMEPLAY_RULES.md owns the seam contract; the retail step's own
// behaviour is [08 "Dispatch gates and order sinks"].

package ai

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Planner is the per-player think step the session dispatches once per tick
// for each computer player, through Manager.Tick. It is the one decision the
// seam replaces: the manager keeps owning its own bookkeeping — the strategic
// record, the ten task deadlines, the nine group vectors, the session
// bindings, and everything a save writes and a restore rebuilds — and a bound
// planner only answers what to do with that state this tick.
//
// The arguments are exactly what the session's before-deadline hook passes:
// the global tick, the live unit world and the economy service whose
// per-player settlement walk the step rides
// [05 "Authoritative settlement order"].
//
// A replacement answers the same step from the same Manager. It may not draw
// from the simulation stream except through the manager's own accessor, in the
// order the retail step draws, because that stream's call order is the whole
// future of the battle: a planner that draws differently is a Modern gameplay
// policy with its own contract, and Strict 3.1 always binds RetailPlanner.
//
// Implementations are zero size or used by pointer and build no closure per
// call, like every other rule-set seam (docs/DESIGN_GAMEPLAY_RULES.md
// "Allocation rules"). One indirect call per player per tick is
// request granularity, not per-node work.
type Planner interface {
	Step(m *Manager, tick uint32, w *units.World, econ *economy.Service)
}

// RetailPlanner runs the retail manager step unchanged: the two dispatch
// gates, the thirty-entry classification cadence, the ascending task sweep,
// weapon maintenance and the strategic refresh, in that order
// [08 "Dispatch gates and order sinks"]. It is the Strict 3.1 answer and,
// today, the Modern one as well — no Modern planner exists.
//
// It is zero size, so binding it into the interface allocates nothing, and it
// holds no state, so one value serves every player and every session. A set
// assembled outside this package composes the retail step by calling this
// method, which is why it is exported: the retail body itself stays
// unexported so the manager's internals are not widened into an API.
type RetailPlanner struct{}

// Step runs the retail think step for m.
func (RetailPlanner) Step(m *Manager, tick uint32, w *units.World, econ *economy.Service) {
	m.retailStep(tick, w, econ)
}

// planner answers with the step this manager runs. A nil field is the retail
// step: a fixture, a manager a restore rebuilt, and the retail baseline all
// want the same answer, and making that the zero value keeps an unbound
// manager on the executable's behaviour rather than on no behaviour at all.
func (m *Manager) planner() Planner {
	if m.Planner != nil {
		return m.Planner
	}
	return RetailPlanner{}
}
