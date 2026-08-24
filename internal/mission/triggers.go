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
// Assignments are visited in authored order, which is what fixes queue order:
// defeat is an OR and does not care, but victory is an AND whose per-tick poll
// order is part of the shared RNG-free evaluation sequence.
//
// A key that is not one of the eighteen condition names is skipped —
// `[GlobalHeader]` carries the whole mission-global block, and only a handful
// of its keys are conditions.
func DecodeTriggers(global *formats.Section) (victory, defeat []*triggers.Trigger) {
	if global == nil {
		return nil, nil
	}
	for _, item := range global.Assignments() {
		t, ok := triggers.ParseCondition(item.OriginalKey, item.Value)
		if !ok {
			continue
		}
		if t.Kind.IsVictory() {
			victory = append(victory, t)
		} else {
			defeat = append(defeat, t)
		}
	}
	return victory, defeat
}
