package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/session"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// The arena's research rule set is registered by the arena itself rather than
// by mods/aikit: the game links every set in mods/all.go, and "aikit" — Modern
// rules, full computer income and a think step that runs whatever brain the
// arena installs, the retail step otherwise — is a harness, not a way to play
// (docs/DESIGN_GAMEPLAY_RULES.md §8).
// "aikit-retail-income" is the same harness with the retail income discount
// (-income retail).
func init() {
	session.RegisterRuleSet(aikitmod.ArenaSet, aikitmod.ArenaRuleSet)
	session.RegisterRuleSet(aikitmod.ArenaRetailIncomeSet, aikitmod.ArenaRetailIncomeRuleSet)
}
