// Package utility is a utility-based computer player for the Modern AI
// research framework (docs/MODERN_AI_RESEARCH.md). Every decision scores
// explicit candidate actions with integer response curves, multiplies the
// considerations, and picks the best; commitments and a hysteresis margin
// keep builders from flip-flopping. It supplies the strategy, economy and
// production layers of a core.Brain; the army layer is core.WaveArmy.
//
// See README.md in this directory for the considerations, parameters and
// measured results.
package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Policies returns the three utility layers over one shared model, for
// composing with any army layer. The strategy draws a style per game and
// jitters (DefaultVariety); Strategy.SetVariety selects otherwise, and
// Variety{Style: "balanced"} is the deterministic brain. Every parameter
// is clamped into its documented range first, as ParseParams does, so a
// literal (Params{} included) cannot reach a zero divisor.
func Policies(p Params) (*Strategy, *Economy, *Production) {
	p.clampAll()
	s := &shared{p: p}
	pr := &Production{s: s}
	return &Strategy{s: s, pr: pr, vr: variety{v: DefaultVariety()}}, &Economy{s: s}, pr
}

// New composes the standalone utility brain: the three utility layers and
// the scripted wave army.
func New(p Params) *core.Brain {
	st, ec, pr := Policies(p)
	return core.New("utility", st, ec, &core.WaveArmy{}, pr)
}
