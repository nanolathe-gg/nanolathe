package aikit_test

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// hostTestRuleSet binds aikit.HostPlanner the way mods/aikit's arena set
// does; internal packages may not link the mod list (internal/architecture).
const hostTestRuleSet = "aikit-host-test"

func init() {
	session.RegisterRuleSet(hostTestRuleSet, func() session.RuleSet {
		return session.RuleSet{Base: gameplay.Modern, Planner: aikit.HostPlanner{}, ComputerIncome: session.FullComputerIncome{}}
	})
}

// A brain's thinks, and the preparation that runs behind the first one, may
// run on another core; the game must not notice. Two personas that differ
// only in Async play the same match: every sample, command count and build
// is equal.
func TestAsyncHostPlaysTheSynchronousGameRetail(t *testing.T) {
	root := testsupport.RetailRoot(t)
	play := func(async bool) headless.ArenaResult {
		t.Helper()
		var players []headless.ArenaPlayer
		for side, persona := range []aikit.Persona{aikit.PersonaHard, aikit.PersonaMed} {
			persona.Async = async
			st, ec, pr := utility.Policies(utility.DefaultParams())
			brain := core.New("util+tac", st, ec, tactics.New(tactics.DefaultParams()), pr)
			players = append(players, headless.ArenaPlayer{Label: persona.Name, Brain: brain, Persona: persona, Side: side})
		}
		res, err := headless.RunArenaMatch(headless.ArenaRequest{
			Root: root, Map: "The Pass", Seed: 7, TickLimit: 3600,
			Gameplay: gameplay.Mode(hostTestRuleSet), Players: players,
		})
		if err != nil {
			t.Fatalf("async %v: %v", async, err)
		}
		// Timings are the host's, not the game's.
		res.WallSeconds, res.TickMeanUS, res.TickP99US = 0, 0, 0
		res.Host = headless.ArenaHostTiming{}
		for i := range res.Players {
			res.Players[i].Cost = headless.ArenaCost{}
		}
		return res
	}
	sync, async := play(false), play(true)
	if !reflect.DeepEqual(sync, async) {
		for i := range sync.Players {
			a, b := sync.Players[i], async.Players[i]
			if !reflect.DeepEqual(a, b) {
				t.Errorf("player %d: synchronous %+v\nasynchronous %+v", i, a.Commands, b.Commands)
			}
		}
		t.Fatal("the asynchronous host played a different game")
	}
}
