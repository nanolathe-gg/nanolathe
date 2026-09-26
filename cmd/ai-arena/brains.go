package main

import (
	"fmt"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// BrainFactory builds a prototype brain from player-spec parameters.
type BrainFactory func(params map[string]string) aikit.Brain

// brainFactories is the arena tool's name table. Each prototype package adds
// itself from its own file (brain_<name>.go) so parallel work never edits a
// shared file. It is consulted only while parsing command-line flags.
var brainFactories = map[string]BrainFactory{
	"scripted": func(map[string]string) aikit.Brain {
		return core.New("scripted", core.ScriptStrategy{}, &core.ScriptEconomy{}, &core.WaveArmy{}, &core.ScriptProduction{})
	},
}

func registerBrain(name string, f BrainFactory) {
	if _, dup := brainFactories[name]; dup {
		panic("ai-arena: duplicate brain " + name)
	}
	brainFactories[name] = f
}

// newBrain constructs a prototype brain by name. "retail" returns nil: the
// player runs the bound retail planner.
func newBrain(name string, params map[string]string) (aikit.Brain, error) {
	if name == "retail" {
		return nil, nil
	}
	f := brainFactories[name]
	if f == nil {
		names := make([]string, 0, len(brainFactories))
		for n := range brainFactories {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown brain %q (have retail, %v)", name, names)
	}
	return f(params), nil
}
