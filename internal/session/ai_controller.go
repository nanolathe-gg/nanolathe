package session

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// aiControllerCloser is what a stateful computer-player controller kept in
// ai.Manager.Ext offers to stop its own work. The Modern AI host
// (internal/aikit) has it: Close waits for an asynchronous think in flight and
// stops the worker goroutine. The session calls it without importing the
// controller's package, so the retail manager stays the only type it knows.
type aiControllerCloser interface{ Close() }

// releaseReplacedAIController discards the controller a manager's previous
// think step kept in Ext when a bind replaces that step with a different one
// (docs/DESIGN_GAMEPLAY_RULES.md §5). A controller belongs to the planner
// that created it: left in place, it would stop being stepped under the new
// set, and a switch back would resume it with a stale observation and apply
// its stale pending command batch. Discarding it makes a switch back start a
// fresh controller from observation, exactly as a battle load does.
//
// The step is compared by type, not by value: a re-projection of the same set
// on every unit allocation binds the same type and keeps the controller. A
// manager with no bound step runs the retail step, which never creates a
// controller, so anything in its Ext was installed by the host (the AI arena)
// and is kept.
func releaseReplacedAIController(m *ai.Manager, next ai.Planner) {
	if m == nil || m.Ext == nil || m.Planner == nil || reflect.TypeOf(m.Planner) == reflect.TypeOf(next) {
		return
	}
	closeAIController(m)
}

// closeAIController stops and drops a manager's controller. A controller
// without a Close method has no work to stop and is simply dropped.
func closeAIController(m *ai.Manager) {
	if m == nil || m.Ext == nil {
		return
	}
	if closer, ok := m.Ext.(aiControllerCloser); ok {
		closer.Close()
	}
	m.Ext = nil
}

// aiBattleShared is the battle's one ai.BattleShared slot, created on first
// use. It belongs to this session, so two battles never share it, and it is
// not saved: a restored battle is a new session with an empty slot.
func (s *Session) aiBattleShared() *ai.BattleShared {
	if s.aiShared == nil {
		s.aiShared = &ai.BattleShared{}
	}
	return s.aiShared
}

// closeAIControllers stops every computer player's controller at battle exit,
// in ascending player order, so an asynchronous worker does not outlive its
// battle.
func (s *Session) closeAIControllers() {
	if s == nil {
		return
	}
	for player := range s.AI {
		closeAIController(s.AI[player])
	}
}

// plannerFor is the think step manager m runs under a bound set whose own
// step is setStep: the Modern AI controller's for a computer player marked
// Modern, the set's own for everyone else (docs/DESIGN_SESSIONS_AI_SAVE.md
// "Modern AI computer player", "Per-player selection"). The mark is a lobby
// choice, not a rule, so it survives a rule-set switch; the set still
// decides every rule the player plays under.
func (s *Session) plannerFor(m *ai.Manager, setStep ai.Planner) ai.Planner {
	if m != nil && m.Controller == ai.ControllerModern && s.modernAI != nil {
		return s.modernAI
	}
	return setStep
}

// The Modern AI controller's think step is installed in one slot rather than
// registered as a rule set (docs/DESIGN_GAMEPLAY_RULES.md "The Modern AI
// controller"). It is not a rule set: it chooses who decides for a computer
// player, never a rule, and nothing selects it by name. A computer player's
// lobby mark (ai.Manager.Controller) is the only choice, made per player in
// whatever set the battle binds. mods/aikit fills the slot from an init, so a
// build that does not link it cannot compose a player marked Modern.
var (
	modernAIMu   sync.Mutex
	modernAIStep ai.ModernAIStep
)

// RegisterModernAI installs the Modern AI controller's think step. Like
// RegisterRuleSet it is an init-time call, and it panics rather than
// reporting, because each refusal is a build mistake: a nil step; a second
// step, which would make the Modern AI depend on link order; or a step that
// is not zero size, since every session in the process shares the one value
// and the controller's state belongs in each manager's Ext
// (docs/DESIGN_GAMEPLAY_RULES.md §3).
func RegisterModernAI(step ai.ModernAIStep) {
	if step == nil {
		panic("nanolathe: Modern AI registration needs a think step: logical path <mods>, providers searched [session Modern AI step], expected a zero-size ai.ModernAIStep")
	}
	if reflect.TypeOf(step).Size() != 0 {
		panic(fmt.Sprintf("nanolathe: Modern AI think step %T holds state: logical path <mods>, providers searched [session Modern AI step], expected a zero-size ai.ModernAIStep", step))
	}
	modernAIMu.Lock()
	defer modernAIMu.Unlock()
	if modernAIStep != nil {
		panic(fmt.Sprintf("nanolathe: duplicate Modern AI think step %T: logical path <mods>, providers searched [session Modern AI step], expected one registration", step))
	}
	modernAIStep = step
}

// registeredModernAI is the installed Modern AI think step, or nil.
func registeredModernAI() ai.ModernAIStep {
	modernAIMu.Lock()
	defer modernAIMu.Unlock()
	return modernAIStep
}

// resolveModernAI finds the Modern AI controller's think step once per
// battle, outside any tick. A build that does not link mods/aikit cannot play
// a computer player marked Modern, and saying so beats quietly playing it as
// Classic.
func (s *Session) resolveModernAI(player uint8) error {
	if s.modernAI != nil {
		return nil
	}
	step := registeredModernAI()
	if step == nil {
		return fmt.Errorf("nanolathe: Modern AI computer player unavailable: logical path player %d, providers searched [session Modern AI step], expected the think step mods/aikit installs (session.RegisterModernAI)", int(player)+1)
	}
	s.modernAI = step
	return nil
}

// setAIController gives manager m its controller choice and projects it onto
// the manager's step. It runs outside any tick: at battle entry and at a load,
// before any controller has begun.
func (s *Session) setAIController(m *ai.Manager, c ai.Controller) error {
	if m == nil {
		return nil
	}
	if c == ai.ControllerModern {
		if err := s.resolveModernAI(m.Player); err != nil {
			return err
		}
	}
	m.Controller = c
	next := s.plannerFor(m, s.Rules.Planner)
	releaseReplacedAIController(m, next)
	m.Planner = next
	// A Modern player is paid in full whatever the bound set answers
	// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income").
	s.projectComputerIncome()
	return nil
}

// applyAIControllers gives each computer player's manager the controller its
// lobby row chose (SkirmishPlayer.AI). Only a live computer row counts: the
// human's and an observer's rows are never computer players, and the Survival
// attacker's passive manager is the wave director's, never a brain's. A fresh
// skirmish entry calls it once the managers exist and before the battle-entry
// prime can step one; a restored battle takes its save's record instead
// (restoreAIControllers).
func applyAIControllers(s *Session, cfg SkirmishConfig) error {
	for i := 0; i < cfg.NumPlayers && i < len(cfg.Players) && i < len(s.AI); i++ {
		m := s.AI[i]
		row := cfg.Players[i]
		if m == nil || m.Passive || !row.IsComputer() || row.AI == ai.ControllerClassic {
			continue
		}
		if err := s.setAIController(m, row.AI); err != nil {
			return err
		}
	}
	return nil
}

// ModernAIPlayer reports whether owner is a computer player the Modern AI
// controller decides for: a live computer slot (control byte 2) whose
// manager is not the Survival attacker's and whose bound step runs the Modern
// AI for it (ai.ModernAIStep). A player marked Modern qualifies in every set,
// and in the AI arena so does a player the arena gave a brain. The order
// binding hands it to the Modern order policies that cover only these players
// (docs/DESIGN_UNITS_ORDERS_COB.md "Modern AI move retention"). It is a pure
// read.
func (s *Session) ModernAIPlayer(owner uint8) bool {
	if s == nil || s.Econ == nil || int(owner) >= len(s.AI) || int(owner) >= len(s.Econ.Players) {
		return false
	}
	m := s.AI[owner]
	if m == nil || m.Passive || s.Econ.Players[owner].ControllerState != 2 {
		return false
	}
	return m.ModernAIDecides()
}

// ComputerAI is a host's controller choice for one lobby row, as a command
// line names it: "<row>=<classic|modern>", rows numbered 1 to 10 as the
// lobby numbers them, or "all=<classic|modern>" for every computer row of
// the battle (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Per-player selection").
type ComputerAI struct {
	Row        int // 1..SkirmishMaxPlayers, or ComputerAIEveryRow
	Controller ai.Controller
}

// ComputerAIEveryRow is the Row of an "all=<classic|modern>" choice: every
// computer row of the battle, which a choice naming the row itself
// overrides.
const ComputerAIEveryRow = 0

// computerAIEveryWord is how a command line spells ComputerAIEveryRow.
const computerAIEveryWord = "all"

// ParseComputerAI reads one "<row>=<classic|modern>" or
// "all=<classic|modern>" choice.
func ParseComputerAI(text string) (ComputerAI, error) {
	rowText, word, ok := strings.Cut(text, "=")
	rowText = strings.TrimSpace(rowText)
	row := ComputerAIEveryRow
	if !ok || !strings.EqualFold(rowText, computerAIEveryWord) {
		var err error
		row, err = strconv.Atoi(rowText)
		if !ok || err != nil || row < 1 || row > SkirmishMaxPlayers || strconv.Itoa(row) != rowText {
			return ComputerAI{}, fmt.Errorf("session: computer AI %q: want <row>=<classic|modern> with a lobby row from 1 to %d, or all=<classic|modern>", text, SkirmishMaxPlayers)
		}
	}
	c, err := ai.ParseController(word)
	if err != nil {
		return ComputerAI{}, fmt.Errorf("session: computer AI %q: %w", text, err)
	}
	return ComputerAI{Row: row, Controller: c}, nil
}

// ApplyComputerAI marks the named rows' computer players. An "all" choice
// marks every computer row — not the human, an observer, an empty row or the
// Survival attacker — and a choice naming a row overrides it whatever the
// order. A named row must be a computer player of this setup and be named
// once, "all" may be given once, and it must find a computer row, so a
// command line can never quietly mark nobody.
func (c *SkirmishConfig) ApplyComputerAI(choices []ComputerAI) error {
	var named [SkirmishMaxPlayers]bool
	attacker := c.survivalAttacker()
	every := false
	for _, choice := range choices {
		if choice.Row != ComputerAIEveryRow {
			continue
		}
		if every {
			return fmt.Errorf("session: computer AI %s is named twice", computerAIEveryWord)
		}
		every = true
		marked := false
		for i := 0; i < c.NumPlayers && i < SkirmishMaxPlayers; i++ {
			if c.Players[i].IsComputer() && i != attacker {
				c.Players[i].AI = choice.Controller
				marked = true
			}
		}
		if !marked {
			return fmt.Errorf("session: computer AI %s: this battle has no computer player", computerAIEveryWord)
		}
	}
	for _, choice := range choices {
		if choice.Row == ComputerAIEveryRow {
			continue
		}
		i := choice.Row - 1
		if i < 0 || i >= SkirmishMaxPlayers {
			return fmt.Errorf("session: computer AI row %d: want a lobby row from 1 to %d", choice.Row, SkirmishMaxPlayers)
		}
		if named[i] {
			return fmt.Errorf("session: computer AI row %d is named twice", choice.Row)
		}
		named[i] = true
		if i >= c.NumPlayers || !c.Players[i].IsComputer() || i == attacker {
			return fmt.Errorf("session: computer AI row %d is not a computer player of this battle", choice.Row)
		}
		c.Players[i].AI = choice.Controller
	}
	return nil
}
