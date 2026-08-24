package triggers

import (
	"strconv"
	"strings"
)

// Authored mission end conditions are `[GlobalHeader]`-level assignments whose
// KEY is the condition name and whose VALUE is the argument list
// [fmt ota "Mission end conditions"]. Retail parses the value with a scan
// format, so a condition that wants fewer arguments than the author supplied
// simply stops reading, and one that wants more leaves the remainder at its
// initial zero [08 "Victory and defeat triggers"].
//
// That is why absent counts are not an error here. `BuildUnitType=ARMSY;` and
// `KillUnitType=CORLAB, 1;` are both attested spellings of the same condition
// family; the first leaves the count at zero, and every evaluator that reads a
// count treats zero and one alike — the build check clamps up, and the
// countdown conditions complete on their first matching notification either
// way. No default is invented.

// argShape is how many of the scan-format fields a kind actually consumes.
type argShape uint8

const (
	shapeFlag     argShape = iota // no arguments; the authored value is inert
	shapeTimer                    // one integer, authored in seconds
	shapeBoundary                 // one integer threshold, no type token
	shapeType                     // a unit-type token, optional trailing count
	shapeTypeInt                  // a unit-type token and one integer
	shapeTypeXZR                  // a unit-type token and three integers
)

// shapeOf returns the argument shape for a kind, from the two tables in
// [08 "Victory trigger types"] and [08 "Defeat trigger types"].
func shapeOf(k Kind) argShape {
	switch k {
	case KindKillEnemyCommander, KindDestroyAllUnits, KindKillAllMobileUnits,
		KindCommanderKilled, KindAllUnitsKilled:
		return shapeFlag
	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		return shapeTimer
	case KindAnyUnitPassesX, KindAnyUnitPassesZ:
		return shapeBoundary
	case KindUnitTypePassesX, KindUnitTypePassesZ:
		return shapeTypeInt
	case KindMoveUnitToRadius:
		return shapeTypeXZR
	case KindBuildUnitType, KindCaptureUnitType, KindKillAllOfType,
		KindKillUnitType, KindUnitTypeKilled, KindAllUnitsKilledOfType:
		return shapeType
	default:
		return shapeFlag
	}
}

// ParseCondition builds a trigger from one authored assignment. key is the
// condition name and value is everything to the right of the `=`
// [fmt ota "Mission end conditions"] [08 "Victory and defeat triggers"].
//
// It reports false when the key is not one of the eighteen condition names,
// which is the common case: `[GlobalHeader]` carries dozens of unrelated keys
// and the builder probes it for condition names rather than the reverse.
func ParseCondition(key, value string) (*Trigger, bool) {
	k, ok := KindByName[strings.ToLower(strings.TrimSpace(key))]
	if !ok {
		return nil, false
	}
	fields := splitArgs(value)
	t := &Trigger{Kind: k}
	switch shapeOf(k) {
	case shapeFlag:
		// The authored `=1` is inert: presence of the key is the condition.

	case shapeTimer:
		// Timers store seconds×30 as an absolute tick deadline
		// [08 "Trigger object"] C17.
		t.Args[0] = SecondsToTicks(intAt(fields, 0))

	case shapeBoundary:
		t.Args[0] = intAt(fields, 0)

	case shapeType:
		t.Type = typeAt(fields, 0)
		t.Args[0] = intAt(fields, 1)

	case shapeTypeInt:
		t.Type = typeAt(fields, 0)
		t.Args[0] = intAt(fields, 1)

	case shapeTypeXZR:
		t.Type = typeAt(fields, 0)
		t.Args[0] = intAt(fields, 1)
		t.Args[1] = intAt(fields, 2)
		t.Args[2] = intAt(fields, 3)
	}
	return t, true
}

// splitArgs splits the authored value on commas and trims each field. An empty
// value yields no fields, which leaves every argument at its initial zero.
func splitArgs(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// typeAt returns field i as a unit-type token, canonicalizing the ANYTYPE
// wildcard to its documented spelling [08 "Victory and defeat triggers"] C14.
func typeAt(fields []string, i int) string {
	if i >= len(fields) {
		return ""
	}
	if IsANYTYPE(fields[i]) {
		return "ANYTYPE"
	}
	return fields[i]
}

// intAt returns field i parsed as an integer, or zero when the field is absent
// or non-numeric — the scan format's behavior when it stops matching.
func intAt(fields []string, i int) int32 {
	if i >= len(fields) {
		return 0
	}
	v, err := strconv.ParseInt(fields[i], 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}
