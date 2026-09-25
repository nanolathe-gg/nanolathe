package main

// The optional victory cue (DESIGN_INTERFACE_HUD_INPUT §3.16): ProTA 4.8's
// renderer plays the `Victory Condition` alias when the local viewer's won
// result first shows, in every session kind. It is a host presentation
// preference, `presentation.victoryCue`, off by default; off, only retail's
// campaign trigger cue plays [08 R-TRIG-01 §8].

import "github.com/nanolathe-gg/nanolathe/internal/frame"

// victoryCueAlias is the alias the campaign trigger cue plays, played through
// the same by-name cue (research/extensions/prota-engine.md "Victory cue on
// multiplayer and skirmish wins").
const victoryCueAlias = "Victory Condition"

// victoryCueWindow is the hook's re-trigger distance in global ticks.
const victoryCueWindow = 300

// victoryCue is the hook's one stored value: the global tick it last ran on,
// zero at process start. It is refreshed on every call, so a win shown for
// many frames plays once.
type victoryCue struct {
	last uint32
}

// due reports whether the cue plays at tick, then stores tick. It plays when
// tick is lower than the stored value or more than 300 ticks after it. Both
// edges of the shipped hook follow: a first win shown at tick 300 or earlier
// in a process plays nothing, and a later battle plays only when its tick is
// below the stored one or more than 300 above it.
func (v *victoryCue) due(tick uint32) bool {
	play := tick < v.last || tick-v.last > victoryCueWindow
	v.last = tick
	return play
}

// processVictoryCue is process-wide, as the hook's value is: it outlives a
// battle and a content reload.
var processVictoryCue victoryCue

// serviceVictoryCue runs once per host frame while the committed result is
// shown. The hook sits in the branch that draws the local won title, which
// the latch reaches only for the local viewer's win, so a loss, a draw or a
// resignation never calls it [07 §11][08 R-TRIG-01 §6].
//
// TODO(question): the research names a composer gate before both end titles
// without describing it, and Nanolathe draws no in-battle title, so the cue
// treats every shown local win as passing that gate. Settled by: a
// description of that gate in [07 §11].
func (b *battleSession) serviceVictoryCue(cur *frame.Frame) {
	if b != nil && processVictoryCue.step(b.hostPreferences().VictoryCue != 0, cur) {
		b.playUICue(nil, victoryCueAlias)
	}
}

// step is one host frame of the hook: whether the cue plays for the committed
// frame cur. With the preference off it neither plays nor stores anything.
func (v *victoryCue) step(enabled bool, cur *frame.Frame) bool {
	if !enabled || cur == nil || !resultWon(cur.Result) {
		return false
	}
	return v.due(cur.Tick)
}
