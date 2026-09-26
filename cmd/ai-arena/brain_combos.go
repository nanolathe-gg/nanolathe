package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// Combinations of the prototype layers. A player spec's parameters go to
// every layer; the utility layers read only the names they define, plus the
// variety switches style=<name|random> (default balanced) and jitter=0|1
// (default 1). style=balanced,jitter=0 is the deterministic brain.
//
// util+tac is built by the game's Modern AI builder (aikitmod.NewUtilTac),
// so an arena spec and a game's Modern AI player configured with the same
// keys (settings modernAI, --ai) play the same brain.
func init() {
	registerBrain("util+tac", func(params map[string]string) aikit.Brain {
		b, err := aikitmod.NewUtilTac(params)
		if err != nil {
			panic(err)
		}
		return reportingBrain{b.Brain, b.Strategy, b.Army}
	})
}

// reportingBrain publishes the utility strategy's style, opening and first
// push and the tactics army's engagement counters to the arena result
// (aikit.Reporter); everything else, Explain included, is the composed
// brain's own. Either part may be nil.
type reportingBrain struct {
	*core.Brain
	st   *utility.Strategy
	army *tactics.Army
}

func (b reportingBrain) Report(add func(name string, value int64)) {
	if b.st != nil {
		b.st.Report(add)
	}
	if b.army != nil {
		b.army.Report(add)
	}
}
