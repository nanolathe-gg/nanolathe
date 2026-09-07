package mission

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/triggers"
)

// DecodeTriggers reads the mission's end conditions out of `[GlobalHeader]`
// and splits them into the victory and defeat queues
// [fmt ota "Mission end conditions"] [08 "Victory and defeat triggers"] C14.
//
// In retail data these keys always sit at the global level, never inside a
// schema, so this reads the global section and not the selected schema.
// Keys are probed in the fixed eighteen-entry vocabulary order. The resolved
// section lookup collapses an identical duplicate spelling to its last value,
// and this loop builds at most one record for each key [08 R-TRIG-01 §2].
//
// A key that is not one of the eighteen condition names is skipped —
// `[GlobalHeader]` carries the whole mission-global block, and only a handful
// of its keys are conditions.
func DecodeTriggers(global *formats.Section) (victory, defeat []*triggers.Trigger) {
	if global == nil {
		return triggers.EnsureDefaults(nil, nil)
	}
	for i := 0; i < triggers.KindCount; i++ {
		kind := triggers.Kind(i)
		value, authored := global.FirstValue(kind.String())
		if !authored {
			continue
		}
		t, present := triggers.ParseCondition(kind.String(), value)
		if !present {
			continue
		}
		if t.Kind.IsVictory() {
			victory = append(victory, t)
		} else {
			defeat = append(defeat, t)
		}
	}
	// Defaults belong to this mission's queues rather than to an evaluator's
	// temporary poll slice, so their satisfied and celebrated state persists
	// through the save image [08 "Default triggers"] [08 R-TRIG-01 §8].
	return triggers.EnsureDefaults(victory, defeat)
}
