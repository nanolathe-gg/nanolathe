package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// tactics: the scripted strategy, economy and production with the tactical
// army (squads, influence maps, Lanchester prediction, threat-aware
// routing, micro). Ablation switches: route=0 micro=0 raid=0 defend=0
// escort=0 budget=0.
func init() {
	registerBrain("tactics", func(params map[string]string) aikit.Brain {
		return core.New("tactics", core.ScriptStrategy{}, &core.ScriptEconomy{}, tactics.New(tactics.ParamsFrom(params)), &core.ScriptProduction{})
	})
}
