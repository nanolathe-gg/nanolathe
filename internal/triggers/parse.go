package triggers

import (
	"strconv"
	"strings"
)

// Authored mission end conditions are `[GlobalHeader]`-level assignments whose
// KEY is the condition name and whose VALUE is the argument list
// [fmt ota "Mission end conditions"]. Retail parses the value with a scan
// format for the argument families. Name-only records instead copy the whole
// value, and numeric conversion failures retain the record's initial zero
// [08 R-TRIG-01 §2]. No fallback value is invented.

// argShape is how many of the scan-format fields a kind actually consumes.
type argShape uint8

const (
	shapeFlag     argShape = iota // nonzero presence gate; no stored arguments
	shapeTimer                    // one integer, authored in seconds
	shapeBoundary                 // one integer threshold, no type token
	shapeType                     // a unit-type token only
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
	case KindKillUnitType, KindUnitTypeKilled, KindUnitTypePassesX, KindUnitTypePassesZ:
		return shapeTypeInt
	case KindMoveUnitToRadius:
		return shapeTypeXZR
	case KindBuildUnitType, KindCaptureUnitType, KindKillAllOfType, KindAllUnitsKilledOfType:
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
		// Flag records are present only for a nonzero integer value.
		if intAt(fields, 0) == 0 {
			return nil, false
		}

	case shapeTimer:
		// Timers store seconds×30 as an absolute tick deadline with no
		// saturation (wrap preserved); poll uses an unsigned due comparison
		// [08 R-TRIG-01 §2, §4].
		seconds := intAt(fields, 0)
		if seconds <= 0 {
			return nil, false
		}
		t.Args[0] = SecondsToTicks(seconds)

	case shapeBoundary:
		// Boundary thresholds stored after arithmetic >>4 [08 "Evaluation"].
		boundary, valid := intAtOK(fields, 0)
		if !valid || boundary < 0 {
			return nil, false
		}
		t.Args[0] = boundary >> 4

	case shapeType:
		// These four records copy the whole authored value as the name. A comma
		// and count are therefore literal name bytes, not arguments [08
		// R-TRIG-01 §2, §4].
		t.Type = strings.TrimSpace(value)

	case shapeTypeInt:
		// Boundary threshold stored after arithmetic >>4 [08 "Evaluation"].
		t.Type = typeAt(fields, 0)
		t.Args[0] = scanIntAt(fields, 1)
		if k == KindUnitTypePassesX || k == KindUnitTypePassesZ {
			t.Args[0] >>= 4
		}

	case shapeTypeXZR:
		t.Type = typeAt(fields, 0)
		t.Args[0] = scanIntAt(fields, 1)
		t.Args[1] = scanIntAt(fields, 2)
		t.Args[2] = scanIntAt(fields, 3)
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
	name := fields[i]
	for j, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			name = name[:j]
			break
		}
	}
	if IsANYTYPE(name) {
		return ""
	}
	return name
}

// intAt returns field i parsed as a complete base-ten integer. This is the
// ordinary authored integer accessor used by flags, timers, and AnyUnit
// boundaries; it is distinct from the scan-family conversion below.
func intAt(fields []string, i int) int32 {
	v, _ := intAtOK(fields, i)
	return v
}

func intAtOK(fields []string, i int) (int32, bool) {
	if i >= len(fields) {
		return 0, false
	}
	v, err := strconv.ParseInt(fields[i], 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(v), true
}

// scanIntAt applies the C %i conversion used by the type-plus-integer argument
// families: optional sign, hexadecimal 0x prefix, leading-zero octal, and
// decimal otherwise. Conversion stops at the first digit not valid for the
// selected base [08 R-TRIG-01 §2].
func scanIntAt(fields []string, i int) int32 {
	if i < len(fields) {
		if v, ok := scanInt(fields[i]); ok {
			return v
		}
	}
	// TODO(question): A failed or missing retail %i conversion may leave a
	// preceding stack value in the record; settle this with a static trace of
	// the fixed trigger builder's argument stack writers [08 R-TRIG-01 §2].
	return 0
}

func scanInt(s string) (int32, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	start := 0
	if s[0] == '+' || s[0] == '-' {
		start++
		if start == len(s) {
			return 0, false
		}
	}
	end := start
	switch {
	case len(s)-start >= 2 && s[start] == '0' && (s[start+1] == 'x' || s[start+1] == 'X'):
		end = start + 2
		digits := end
		for end < len(s) && isHexDigit(s[end]) {
			end++
		}
		if end == digits {
			return 0, false
		}
	case s[start] == '0':
		end++
		for end < len(s) && s[end] >= '0' && s[end] <= '7' {
			end++
		}
	default:
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == start {
			return 0, false
		}
	}

	v, err := strconv.ParseInt(s[:end], 0, 32)
	if err != nil {
		return 0, false
	}
	return int32(v), true
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'f') ||
		(b >= 'A' && b <= 'F')
}
