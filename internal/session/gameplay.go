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
	// Normalize answers with a word this build can select, so the error below
	// cannot occur; binding the default rather than nothing keeps a session
	// whole if a caller ever reaches this with an unselectable word.
	if err := s.SetRules(string(mode.Normalize())); err != nil {
		s.Gameplay = gameplay.Modern
		s.BindRules(ModernRuleSet())
	}
}
