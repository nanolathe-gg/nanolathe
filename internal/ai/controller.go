// Who decides for a computer player: the Classic think step the bound rule set
// supplies, or the Modern AI controller. docs/DESIGN_SESSIONS_AI_SAVE.md
// "Modern AI computer player" ("Per-player selection") owns the policy; the
// session projects the choice onto each manager's Planner.

package ai

import (
	"fmt"
	"strings"
)

// Controller is one computer player's controller choice. It is a lobby
// choice, not a rule: a battle may mix both kinds in any gameplay mode, and
// the bound rule set still decides every rule each player plays under. The
// zero value is Classic, so a setup, a fixture or a save that never names one
// plays the think step it always played.
type Controller uint8

const (
	// ControllerClassic is the bound rule set's own think step: the retail
	// step under Strict 3.1 and Community 3.9, the retail step with Modern
	// wave air targets under Modern.
	ControllerClassic Controller = iota
	// ControllerModern is the Modern AI computer player (internal/aikit),
	// whatever rule set is bound.
	ControllerModern
)

// The words a host, a settings file and a save's record spell a Controller
// with.
const (
	ControllerClassicWord = "classic"
	ControllerModernWord  = "modern"
)

// String is the controller's word.
func (c Controller) String() string {
	if c == ControllerModern {
		return ControllerModernWord
	}
	return ControllerClassicWord
}

// ParseController reads a controller word, ignoring case and surrounding
// space. Anything but the two words is an error.
func ParseController(word string) (Controller, error) {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case ControllerClassicWord:
		return ControllerClassic, nil
	case ControllerModernWord:
		return ControllerModern, nil
	}
	return ControllerClassic, fmt.Errorf("ai: controller %q: want %s or %s", word, ControllerClassicWord, ControllerModernWord)
}

// ModernAIStep is implemented by a think step that runs the Modern AI
// controller. ControlsModernAI reports whether it does so for manager m: the
// game's Modern AI step (mods/aikit) always does, while the AI arena's host
// step does only for a manager the arena gave a brain. A step that does not implement it —
// the retail step and ModernPlanner — never does.
//
// The session asks it, outside the think step, to decide which players a
// Modern order policy for Modern AI players covers
// (docs/DESIGN_UNITS_ORDERS_COB.md "Modern AI move retention"). It must be
// a pure read of m.
type ModernAIStep interface {
	Planner
	ControlsModernAI(m *Manager) bool
}

// ModernAIDecides reports whether manager m's bound think step runs the
// Modern AI controller for it. It says nothing about whether the player is a
// computer player; the caller checks that.
func (m *Manager) ModernAIDecides() bool {
	if m == nil {
		return false
	}
	step, ok := m.planner().(ModernAIStep)
	return ok && step.ControlsModernAI(m)
}
