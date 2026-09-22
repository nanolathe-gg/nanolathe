package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// SetGameplay configures a detached session. Live adapters enqueue HumanGameplay
// so changes happen at the authoritative command boundary, before combat.
//
// The word selects a whole RuleSet, bound here once so no phase reads it
// (docs/DESIGN_GAMEPLAY_RULES.md). It is a reserved word or the name of a
// registered set; the session keeps the selected set's base as its own word,
// so everything that asks a strict-versus-modern question still has one
// answer. SetRules is the same selection with a reportable error.
func (s *Session) SetGameplay(mode gameplay.Mode) {
	if s == nil {
		return
	}
	// Normalize preserves the vocabulary fallback. Invalid feature declarations
	// leave the current binding intact; callers needing the diagnostic use
	// SetRules, and human input validates declarations before enqueueing.
	_ = s.SetRules(string(mode.Normalize()))
}
