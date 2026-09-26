// Package aikit installs the Modern AI computer player: the recommended
// util+tac brain (utility economy and production, tactical army), the
// persona picked by the lobby's difficulty. Its think step is the one the
// session gives every computer player the lobby, the command line or a save
// marks Modern, in whatever rule set the battle binds, Strict 3.1 included
// (session.RegisterModernAI; docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI
// computer player", "Per-player selection"). It is not a rule set and has no
// name to select: a player marked Classic plays its set's own think step.
//
// The brain's parameters may be configured — the settings file's modernAI
// block or `--ai key=value,...` — with the arena's player-spec keys, and both
// the game and the arena's util+tac build the brain with NewUtilTac
// (params.go; docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player").
// With none configured each game draws a personality and jitters its opening
// from the player's private generator.
//
// The arena's research sets, "aikit" and "aikit-retail-income", are composed
// here (ArenaRuleSet, ArenaRetailIncomeRuleSet) but not registered: only the
// arena tools register them (cmd/ai-arena/ruleset.go), so the game, which
// links every set in mods/all.go, never offers them.
package aikit

import (
	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ArenaSet is the name the arena tools register ArenaRuleSet under. This
// package does not register it.
const ArenaSet = "aikit"

func init() {
	session.RegisterModernAI(utilTacPlanner{})
}

// ArenaRuleSet is the arena's research set: Modern rules whose think step is
// aikit.HostPlanner, so the arena installs each player's brain in the
// manager's Ext and a manager without one runs the retail step. It pays every
// computer player in full, brain and retail planner alike, so a brain's
// strength never comes from a difficulty discount
// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income"). It is
// exported for the arena tools to register.
func ArenaRuleSet() session.RuleSet {
	return session.RuleSet{Base: gameplay.Modern, Planner: aikit.HostPlanner{}, ComputerIncome: session.FullComputerIncome{}}
}

// ArenaRetailIncomeSet is the name the arena registers
// ArenaRetailIncomeRuleSet under.
const ArenaRetailIncomeSet = "aikit-retail-income"

// ArenaRetailIncomeRuleSet is ArenaRuleSet with the retail income discount
// in place of full income: every computer player, retail planner and Modern
// brain alike, is credited by the battle's difficulty word as in a 3.1
// battle (half on easy, seven tenths on medium, all on hard [05 R-ECO-01
// §3]). The arena selects it with -income retail to measure a brain against
// the retail planner at each difficulty on equal income. Not a way to play:
// the arena gives a brain to a manager directly and never marks the player
// Modern, so the game's full income for Modern AI players does not apply.
func ArenaRetailIncomeRuleSet() session.RuleSet {
	return session.RuleSet{Base: gameplay.Modern, Planner: aikit.HostPlanner{}, ComputerIncome: session.ModernComputerIncome{}}
}

// utilTacPlanner is zero size so the rule set may cache it; each manager's
// host lives in its Ext and is built on the manager's first step.
type utilTacPlanner struct{}

func (utilTacPlanner) Step(m *ai.Manager, tick uint32, w *units.World, econ *economy.Service) {
	aikit.StepLazy(m, tick, w, econ, newUtilTacHost)
}

// ControlsModernAI implements ai.ModernAIStep: every manager this step runs
// is a computer player marked Modern, whether its controller has begun or
// not.
func (utilTacPlanner) ControlsModernAI(*ai.Manager) bool { return true }

// newUtilTacHost builds a computer player's controller: the util+tac brain
// with the player's configured parameters (ai.Manager.ControllerParams,
// resolved by the session at battle entry or restored from a save) and the
// persona of the battle's difficulty. It parses the manager's canonical text
// and reads no file or setting. A Survival battle's computer buddy plays the
// survival brain instead (survival.go), unless configured survival=0.
func newUtilTacHost(m *ai.Manager) *aikit.Host {
	if kv, ok := survivalPlay(m); ok {
		return newSurvivalHost(m, kv)
	}
	return aikit.NewHost(m, controllerBrain(m.ControllerParams), personaFor(m))
}

// controllerBrain builds the brain for canonical parameter text. Every host
// entry point — the settings file, the --ai flag, a save's record — has
// validated the text before the battle began, so a failure here is a
// manager composed around them (a test fixture); it plays the default brain
// rather than stopping the battle.
func controllerBrain(params string) *core.Brain {
	if kv, err := ValidateParamsText(params); err == nil {
		if b, err := NewUtilTac(kv); err == nil {
			return b.Brain
		}
	}
	b, _ := NewUtilTac(nil)
	return b.Brain
}

// personaFor maps the battle's difficulty to a built-in persona. A computer
// player marked Modern is paid in full in every rule set, so the lobby
// difficulty changes only the persona, never its economy. The session picks the difficulty layer of
// the configured parameters by the same reading (session.ControllerDifficulty).
// Every persona thinks on the controller's worker goroutine: the game is the
// synchronous host's (TestAsyncHostPlaysTheSynchronousGameRetail), and the
// think leaves the simulation thread, a third of the controllers' work there
// on an eight-player match (docs/MODERN_AI_RESEARCH.md §5.1).
func personaFor(m *ai.Manager) aikit.Persona {
	var p aikit.Persona
	switch session.ControllerDifficulty(m.Profile) {
	case ai.DifficultyEasy:
		p = aikit.PersonaEasy
	case ai.DifficultyHard:
		p = aikit.PersonaHard
	default:
		p = aikit.PersonaMed
	}
	p.Async = true
	return p
}
