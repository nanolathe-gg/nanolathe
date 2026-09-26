package survival

import (
	"fmt"
	"strconv"
)

// ParamSpec documents one survival key: the player-spec vocabulary of the
// util+tac layers gains these (mods/aikit ValidateParams).
type ParamSpec struct {
	Name              string
	Default, Min, Max int32
	Doc               string
}

// Specs lists the survival layer's keys. "survival" is read by the play
// set's controller, not by Params: 0 plays the skirmish util+tac brain in a
// Survival battle instead.
var Specs = [...]ParamSpec{
	{"survival", 1, 0, 1, "switch: a Survival battle's computer survivors play the survival brain (0: the skirmish util+tac brain)"},
	{"sv_tower", 22, 0, 100, "percent of the income received that the survival layer keeps standing as towers of its own planning"},
	{"sv_walls", 1, 0, 1, "switch: wall segments in front of towered sectors, with corridors between"},
	{"sv_claim", 34, 0, 100, "percent of constructors the survival layer may take for towers, walls and repairs (doubled while a wave is warned)"},
}

// ParamsFrom reads the survival keys over the defaults; other keys are
// ignored. A value that is not an integer inside its range is an error.
func ParamsFrom(kv map[string]string) (Params, error) {
	p := DefaultParams()
	for i := range Specs {
		sp := &Specs[i]
		s, ok := kv[sp.Name]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || strconv.FormatInt(n, 10) != s || n < int64(sp.Min) || n > int64(sp.Max) {
			return p, fmt.Errorf("survival: %s=%q: want an integer in %d..%d (%s)", sp.Name, s, sp.Min, sp.Max, sp.Doc)
		}
		switch sp.Name {
		case "sv_tower":
			p.TowerShare = int32(n)
		case "sv_walls":
			p.Walls = int32(n)
		case "sv_claim":
			p.ClaimShare = int32(n)
		}
	}
	return p, nil
}

// Enabled reports whether a configuration leaves the survival brain on.
func Enabled(kv map[string]string) bool {
	return kv["survival"] != "0"
}
