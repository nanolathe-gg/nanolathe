package aikit

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/survival"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The util+tac brain's parameters: one key vocabulary for the arena's player
// specs, the settings file's modernAI block, the desktop --ai flag and a
// save's record (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Configuration"). Each layer owns its keys:
//
//   - the utility parameters, utility.Specs (name, default and range);
//   - the variety switches that utility.VarietyFrom reads: the style and its
//     jitter, the attack-value switches, the front rules, the opening
//     switches, and the personality and its traits;
//   - the tactics army's switches and knobs that tactics.ParamsFrom reads
//     (internal/aikit/brains/tactics/README.md);
//   - the survival brain's keys, survival.Specs, which only a Survival
//     battle's computer survivors read (survival.go).
//
// NewUtilTac builds the brain from any set and reads only the keys its
// layers define, as the arena always has. ValidateParams is the strict check
// a configuration must pass before a battle.

// UtilTac is the util+tac brain — utility strategy, economy and production,
// tactics army — and the two layers that report to the arena.
type UtilTac struct {
	Brain    *core.Brain
	Strategy *utility.Strategy
	Army     *tactics.Army
}

// NewUtilTac builds the util+tac brain from player-spec parameters. Keys no
// layer defines are ignored, so an arena spec may carry persona keys, a
// label or another brain's switches; a value a layer rejects is an error.
// With no parameters it is the default brain: the tuned utility weights, a
// style and a personality drawn per game with jitter, and every tactics
// component on, tempered by the personality.
func NewUtilTac(params map[string]string) (UtilTac, error) {
	p, err := utility.ParseParams(UtilityParams(params))
	if err != nil {
		return UtilTac{}, err
	}
	v, err := utility.VarietyFrom(params)
	if err != nil {
		return UtilTac{}, err
	}
	st, ec, pr := utility.Policies(p)
	if err := st.SetVariety(v); err != nil {
		return UtilTac{}, err
	}
	tp := tactics.ParamsFrom(params)
	tp.Temper = armyTemper{st}
	army := tactics.New(tp)
	return UtilTac{Brain: core.New("util+tac", st, ec, army, pr), Strategy: st, Army: army}, nil
}

// armyTemper hands the tactics army the utility strategy's drawn
// personality (its aggression and raid appetite) at the army's Init, which
// core runs after the strategy's (utility README §13.15).
type armyTemper struct{ st *utility.Strategy }

func (t armyTemper) ArmyTemper() tactics.Temper {
	a := t.st.ArmyTemper()
	return tactics.Temper{Engage: a.Engage, RaidPct: a.RaidPct, HarassPct: a.HarassPct}
}

// UtilityParams keeps the keys the utility layers define.
func UtilityParams(params map[string]string) map[string]string {
	out := map[string]string{}
	for i := range utility.Specs {
		if v, ok := params[utility.Specs[i].Name]; ok {
			out[utility.Specs[i].Name] = v
		}
	}
	return out
}

// varietyKeys are the keys utility.VarietyFrom reads, which validates their
// values itself: the switches, then the personality's trait keys
// (utility.TraitKeys).
var varietyKeys = append([]string{"style", "jitter", "att_curve", "att_share", "att_floor", "att_lag", "att_build", "tower_time", "wide_base", "open_reclaim", "open_reclaim_hi", "open_reclaim_end", "open_army", "open_fam", "open_follow",
	"personality"}, utility.TraitKeys[:]...)

// wordKeys take a word rather than an integer: a style, a personality, or
// pv's "main".
var wordKeys = [...]string{"style", "personality", "pv"}

// tacticsKey is one key tactics.ParamsFrom reads and the values that it
// acts on. ParamsFrom ignores a value it cannot use, so the strict check
// names what it accepts: a switch is 0 or 1; a knob is a 32-bit integer of
// at least min; pv takes "main".
type tacticsKey struct {
	name string
	knob bool
	min  int64
}

// tacticsKeys lists them in tactics.Params field order. air and naval are
// also utility switches (utility.Specs), which check them first.
var tacticsKeys = [...]tacticsKey{
	{name: "route"}, {name: "micro"}, {name: "raid"}, {name: "defend"}, {name: "escort"}, {name: "budget"},
	{name: "air"}, {name: "naval"},
	{name: "em", knob: true, min: minKnob}, {name: "rm", knob: true, min: minKnob}, {name: "nm", knob: true, min: minKnob},
	{name: "posture"}, {name: "pv"}, {name: "pagg", knob: true, min: minKnob},
	{name: "passage"}, {name: "unseen"}, {name: "harass"},
	{name: "hn", knob: true, min: 1}, {name: "hv", knob: true, min: 0},
	{name: "probe"}, {name: "tour"},
	{name: "sm", knob: true, min: 1}, {name: "raidv", knob: true, min: 1},
}

// minKnob is the least signed 32-bit value: the margin and aggression knobs
// take either sign.
const minKnob = -1 << 31

// ValidateParams checks a parameter set strictly, for a configuration: every
// key must be one a util+tac layer reads, and every value one that layer
// uses as written — a utility parameter an integer inside its documented
// range (ParseParams would clamp it), a switch 0 or 1. The arena's lenient
// reading is NewUtilTac's. Keys are checked in order, so the first error is
// always the same one.
func ValidateParams(params map[string]string) error {
	for _, k := range slices.Sorted(maps.Keys(params)) {
		if err := validateParam(k, params[k]); err != nil {
			return err
		}
	}
	return nil
}

func validateParam(key, value string) error {
	// Every value but a style, a personality or pv's word is an integer,
	// spelled plainly: "+1" or "01" would read as 1 in one layer and not in
	// another (a tactics switch is off only for exactly "0").
	n, err := strconv.ParseInt(value, 10, 32)
	plain := err == nil && strconv.FormatInt(n, 10) == value
	if !plain && !slices.Contains(wordKeys[:], key) && knownParam(key) {
		return fmt.Errorf("aikit: %s=%q: want an integer", key, value)
	}
	for i := range utility.Specs {
		sp := &utility.Specs[i]
		if sp.Name != key {
			continue
		}
		if n < int64(sp.Min) || n > int64(sp.Max) {
			return fmt.Errorf("aikit: %s=%q: want an integer in %d..%d (%s)", key, value, sp.Min, sp.Max, sp.Doc)
		}
		return nil
	}
	if slices.Contains(varietyKeys, key) {
		_, err := utility.VarietyFrom(map[string]string{key: value})
		return err
	}
	for i := range survival.Specs {
		if survival.Specs[i].Name == key {
			_, err := survival.ParamsFrom(map[string]string{key: value})
			return err
		}
	}
	for i := range tacticsKeys {
		tk := &tacticsKeys[i]
		if tk.name != key {
			continue
		}
		switch {
		case key == "pv":
			if value != "main" {
				return fmt.Errorf("aikit: pv=%q: want main (measure the main squad alone)", value)
			}
		case tk.knob:
			if n < tk.min {
				return fmt.Errorf("aikit: %s=%q: want an integer of at least %d", key, value, tk.min)
			}
		default:
			if n != 0 && n != 1 {
				return fmt.Errorf("aikit: %s=%q: want 0 or 1", key, value)
			}
		}
		return nil
	}
	return fmt.Errorf("aikit: unknown parameter %q (the keys are utility.Specs, the variety switches, the tactics switches and survival.Specs; docs/DESIGN_SESSIONS_AI_SAVE.md \"Modern AI computer player\")", key)
}

// knownParam reports a key some util+tac layer reads.
func knownParam(key string) bool {
	for i := range utility.Specs {
		if utility.Specs[i].Name == key {
			return true
		}
	}
	for i := range tacticsKeys {
		if tacticsKeys[i].name == key {
			return true
		}
	}
	for i := range survival.Specs {
		if survival.Specs[i].Name == key {
			return true
		}
	}
	return slices.Contains(varietyKeys, key)
}

// ValidateParamsText parses canonical or flag text (session.ParseAIParams)
// and validates it; it returns the pairs as a map.
func ValidateParamsText(text string) (map[string]string, error) {
	pairs, err := session.ParseAIParams(text)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.Key] = p.Value
	}
	if err := ValidateParams(out); err != nil {
		return nil, err
	}
	return out, nil
}
