package triggers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/units"
)

// Kind identifies one of the eighteen victory/defeat trigger types
// [08 "Victory and defeat triggers"] [08 "Victory trigger types"] [08 "Defeat trigger types"] [GAP T10].
type Kind uint8

const (
	// Victory triggers [08 "Victory trigger types"] in builder probe order [GAP T10].
	KindKillEnemyCommander  Kind = iota // 0 flag-only 0xC
	KindDestroyAllUnits                 // 1 flag-only 0xC
	KindKillAllMobileUnits              // 2 flag-only 0xC
	KindBuildUnitType                   // 3 type+count 0x30
	KindCaptureUnitType                 // 4 type+count 0x30
	KindKillAllOfType                   // 5 type+count 0x32
	KindKillUnitType                    // 6 type+count 0x32
	KindMoveUnitToRadius                // 7 type+X/Z/radius 0x40
	KindUnitTypePassesX                 // 8 type/boundary 0x14
	KindUnitTypePassesZ                 // 9 type/boundary 0x14
	KindVictoryTimerRunsOut             // 10 timer 0x10
	// Defeat triggers [08 "Defeat trigger types"].
	KindCommanderKilled      // 11 flag-only 0xC
	KindAllUnitsKilled       // 12 flag-only 0xC
	KindAllUnitsKilledOfType // 13 type only 0x36 (canonicalizing string shape)
	KindUnitTypeKilled       // 14 type+count 0x32
	KindDeathTimerRunsOut    // 15 timer 0x10
	KindAnyUnitPassesX       // 16 boundary 0x14
	KindAnyUnitPassesZ       // 17 boundary 0x14

	KindCount = 18
)

// String returns the authored OTA key for the kind [08 "Victory and defeat triggers"].
func (k Kind) String() string {
	switch k {
	case KindKillEnemyCommander:
		return "KillEnemyCommander"
	case KindDestroyAllUnits:
		return "DestroyAllUnits"
	case KindKillAllMobileUnits:
		return "KillAllMobileUnits"
	case KindBuildUnitType:
		return "BuildUnitType"
	case KindCaptureUnitType:
		return "CaptureUnitType"
	case KindKillAllOfType:
		return "KillAllOfType"
	case KindKillUnitType:
		return "KillUnitType"
	case KindMoveUnitToRadius:
		return "MoveUnitToRadius"
	case KindUnitTypePassesX:
		return "UnitTypePassesX"
	case KindUnitTypePassesZ:
		return "UnitTypePassesZ"
	case KindVictoryTimerRunsOut:
		return "VictoryTimerRunsOut"
	case KindCommanderKilled:
		return "CommanderKilled"
	case KindAllUnitsKilled:
		return "AllUnitsKilled"
	case KindAllUnitsKilledOfType:
		return "AllUnitsKilledOfType"
	case KindUnitTypeKilled:
		return "UnitTypeKilled"
	case KindDeathTimerRunsOut:
		return "DeathTimerRunsOut"
	case KindAnyUnitPassesX:
		return "AnyUnitPassesX"
	case KindAnyUnitPassesZ:
		return "AnyUnitPassesZ"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// IsVictory reports whether the kind belongs to the victory queue [08 "Default triggers"].
func (k Kind) IsVictory() bool { return k <= KindVictoryTimerRunsOut }

// IsDefeat reports whether the kind belongs to the defeat queue.
func (k Kind) IsDefeat() bool { return k >= KindCommanderKilled }

// RecordSize returns the retail record size in bytes for the kind's polymorphic
// allocation, including the leading vtable pointer and adjacent completed flag.
// The eighteen-entry vtable map and per-condition sizes are established
// [08 "Trigger object"] [GAP T10] [08 "Trigger object"].
// Observed buckets: 0xC flag-only (12), 0x10 timer (16), 0x14 boundary (20),
// 0x30/0x32 string+count (48/50), 0x36 canonicalizing string (54), 0x40 radius (64)
// [notes/campaign/00_missions.md §5.2] [GAP T10].
// TODO(question): exact assignment of which string+count variant is 0x30 vs 0x32
// is not closed beyond the bucket ranges; this mapping uses representative
// values within the observed buckets and is one line to correct if a probe
// distinguishes them.
func (k Kind) RecordSize() int {
	switch k {
	case KindKillEnemyCommander, KindDestroyAllUnits, KindKillAllMobileUnits, KindCommanderKilled, KindAllUnitsKilled:
		return 0x0C // 12 flag-only [GAP T10] [notes/campaign/00_missions.md §5.2]
	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		return 0x10 // 16 timer [GAP T10]
	case KindUnitTypePassesX, KindUnitTypePassesZ, KindAnyUnitPassesX, KindAnyUnitPassesZ:
		return 0x14 // 20 boundary [GAP T10]
	case KindBuildUnitType, KindCaptureUnitType:
		return 0x30 // 48 string+count variant A
	case KindKillAllOfType, KindKillUnitType, KindUnitTypeKilled:
		return 0x32 // 50 string+count variant B
	case KindAllUnitsKilledOfType:
		return 0x36 // 54 canonicalizing string shape [GAP T10]
	case KindMoveUnitToRadius:
		return 0x40 // 64 radius largest [GAP T10] [08 "Trigger object"]
	default:
		return 0x0C
	}
}

// VTable is the per-condition virtual table entry. Retail stores an 8-dword
// table per trigger (shared destructor/helper slots plus one checker slot)
// with the completed flag adjacent to the vtable pointer [08 "Trigger object"]
// [notes/campaign/00_missions.md §5.3]. Go models the identity, not the
// layout [I13].
type VTable struct {
	Kind       Kind
	Name       string
	RecordSize int // bytes, see Kind.RecordSize
	// TODO(question): exact 8-slot function identities beyond the checker
	// slot are not needed for the poll contract; the table shape is
	// preserved for completeness.
	Slots int // always 8 per [notes/campaign/00_missions.md §5.3]
}

// VTables is the eighteen-entry vtable map covering all condition types
// [08 "Trigger object"] [GAP T10].
var VTables = [KindCount]VTable{
	{Kind: KindKillEnemyCommander, Name: "KillEnemyCommander", RecordSize: 0x0C, Slots: 8},
	{Kind: KindDestroyAllUnits, Name: "DestroyAllUnits", RecordSize: 0x0C, Slots: 8},
	{Kind: KindKillAllMobileUnits, Name: "KillAllMobileUnits", RecordSize: 0x0C, Slots: 8},
	{Kind: KindBuildUnitType, Name: "BuildUnitType", RecordSize: 0x30, Slots: 8},
	{Kind: KindCaptureUnitType, Name: "CaptureUnitType", RecordSize: 0x30, Slots: 8},
	{Kind: KindKillAllOfType, Name: "KillAllOfType", RecordSize: 0x32, Slots: 8},
	{Kind: KindKillUnitType, Name: "KillUnitType", RecordSize: 0x32, Slots: 8},
	{Kind: KindMoveUnitToRadius, Name: "MoveUnitToRadius", RecordSize: 0x40, Slots: 8},
	{Kind: KindUnitTypePassesX, Name: "UnitTypePassesX", RecordSize: 0x14, Slots: 8},
	{Kind: KindUnitTypePassesZ, Name: "UnitTypePassesZ", RecordSize: 0x14, Slots: 8},
	{Kind: KindVictoryTimerRunsOut, Name: "VictoryTimerRunsOut", RecordSize: 0x10, Slots: 8},
	{Kind: KindCommanderKilled, Name: "CommanderKilled", RecordSize: 0x0C, Slots: 8},
	{Kind: KindAllUnitsKilled, Name: "AllUnitsKilled", RecordSize: 0x0C, Slots: 8},
	{Kind: KindAllUnitsKilledOfType, Name: "AllUnitsKilledOfType", RecordSize: 0x36, Slots: 8},
	{Kind: KindUnitTypeKilled, Name: "UnitTypeKilled", RecordSize: 0x32, Slots: 8},
	{Kind: KindDeathTimerRunsOut, Name: "DeathTimerRunsOut", RecordSize: 0x10, Slots: 8},
	{Kind: KindAnyUnitPassesX, Name: "AnyUnitPassesX", RecordSize: 0x14, Slots: 8},
	{Kind: KindAnyUnitPassesZ, Name: "AnyUnitPassesZ", RecordSize: 0x14, Slots: 8},
}

// KindByName maps the authored condition name (case-insensitive) to its Kind
// [08 "Victory and defeat triggers"]. ANYTYPE is not a kind; it is a wildcard
// token accepted wherever a unit type is expected [08 "Victory and defeat triggers"] [C14].
var KindByName = func() map[string]Kind {
	m := make(map[string]Kind, KindCount)
	for i := 0; i < KindCount; i++ {
		k := Kind(i)
		m[strings.ToLower(k.String())] = k
	}
	return m
}()

// IsANYTYPE reports whether the unit-type token is the wildcard literal
// ANYTYPE, accepted wherever a unit type is expected [08 "Victory and defeat triggers"] [C14].
// Recognition is case-insensitive; retail compares via boundary conditions.
func IsANYTYPE(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "ANYTYPE") }

// Trigger is a polymorphic mission condition record.
// Every record leads with a vtable pointer whose table covers all eighteen
// types, with the completed flag adjacent [08 "Trigger object"].
// Go uses named fields per [I13]; byte sizes are identity via Kind.RecordSize().
type Trigger struct {
	Kind      Kind     // 18 kinds [08 "Victory and defeat triggers"] [C15]
	Type      string   // unit type name or "" or "ANYTYPE" [C14]
	Args      [3]int32 // authored ints: count/threshold/ticks/X/Z/radius [C14]
	Completed bool     // adjacent to vtable pointer [08 "Trigger object"]
}

// Event classifies the context that caused a poll invocation.
// Exact event taxonomy beyond death/capture/tick is residual
// [08 "Evaluation"] TODO(question).
type Event uint8

const (
	EventTick Event = iota // periodic poll with no unit event
	EventUnitDied
	EventUnitCaptured
	EventUnitCreated
	EventUnitBuilt
)

// Context is the per-poll input to a trigger checker.
// Evaluators are pure polls taking the trigger and a context and mutating
// only their own completed flag [08 "Evaluation"] [C17].
// Coordinates are signed world units; thresholds compare with ±2 tolerance [C17].
type Context struct {
	Tick  uint32      // authoritative global tick [08 "Trigger object"] [08 "Evaluation"]
	Unit  *units.Unit // subject unit, if any (may be nil for timer/boundary without unit) [08 "Evaluation"]
	X, Z  int32       // signed world coordinate carried in context for boundary checks [08 "Evaluation"] [C17]
	Event Event       // death/capture flag for type-gated checks [08 "Evaluation"]
}

// SecondsToTicks converts authored seconds to authoritative ticks at 30 Hz
// [08 "Trigger object"] [08 "Evaluation"] [C17].
func SecondsToTicks(seconds int32) int32 { return seconds * 30 }

// New constructs a trigger of the given kind with type and up to three args.
// For timer kinds, pass seconds; the caller should convert via SecondsToTicks
// or let NewTimer do it.
func New(kind Kind, typ string, args ...int32) *Trigger {
	t := &Trigger{Kind: kind, Type: typ}
	for i := 0; i < len(args) && i < 3; i++ {
		t.Args[i] = args[i]
	}
	// Normalize ANYTYPE to canonical spelling for comparisons [C14].
	if IsANYTYPE(typ) {
		t.Type = "ANYTYPE"
	}
	return t
}

// NewTimer constructs a timer trigger storing seconds×30 ticks
// [08 "Trigger object"] [08 "Evaluation"].
func NewTimer(kind Kind, seconds int32) *Trigger {
	return New(kind, "", SecondsToTicks(seconds))
}

// DefaultVictory returns the injected default victory condition when no
// authored victory exists: a destroy-all-units-class trigger
// [08 "Default triggers"] [C16].
func DefaultVictory() *Trigger {
	return New(KindDestroyAllUnits, "")
}

// DefaultDefeat returns the injected default defeat condition when no
// authored defeat exists: an all-units-killed-class trigger
// [08 "Default triggers"] [C16].
func DefaultDefeat() *Trigger {
	return New(KindAllUnitsKilled, "")
}

// EnsureDefaults inserts default triggers when queues are empty, guaranteeing
// one win and one lose condition [08 "Default triggers"] [C16].
// It returns the possibly-extended slices; input slices are not mutated beyond
// the append.
func EnsureDefaults(victory []*Trigger, defeat []*Trigger) ([]*Trigger, []*Trigger) {
	if len(victory) == 0 {
		victory = append(victory, DefaultVictory())
	}
	if len(defeat) == 0 {
		defeat = append(defeat, DefaultDefeat())
	}
	return victory, defeat
}

// ParseArgs parses the two authored argument formats [C14] [08 "Victory and defeat triggers"]:
//
//	<name>,<int>
//	<name>,<int>,<int>,<int>
//
// The literal ANYTYPE is accepted wherever a unit type is expected [C14].
// Boundary conditions are recognized by their <type or ANYTYPE, boundary> shape.
// Returns the type string, up to three ints, and whether the parse hit the
// four-argument form.
func ParseArgs(s string) (typ string, args [3]int32, isFour bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", args, false, fmt.Errorf("triggers: empty args")
	}
	// Split on comma. Retail uses scan format %[a-zA-Z],%i and %[a-zA-Z],%i,%i,%i [notes/campaign/00_missions.md §5.1].
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) == 2 {
		// <name>,<int>
		typ = parts[0]
		v, err2 := strconv.ParseInt(parts[1], 10, 32)
		if err2 != nil {
			return "", args, false, fmt.Errorf("triggers: parse int %q: %w", parts[1], err2)
		}
		args[0] = int32(v)
		return typ, args, false, nil
	}
	if len(parts) == 4 {
		// <name>,<int>,<int>,<int>
		typ = parts[0]
		for i := 0; i < 3; i++ {
			v, err2 := strconv.ParseInt(parts[1+i], 10, 32)
			if err2 != nil {
				return "", args, false, fmt.Errorf("triggers: parse int %q: %w", parts[1+i], err2)
			}
			args[i] = int32(v)
		}
		return typ, args, true, nil
	}
	// Single int boundary case for AnyUnitPassesX/Z: "<int>" without name
	if len(parts) == 1 {
		v, err2 := strconv.ParseInt(parts[0], 10, 32)
		if err2 != nil {
			return "", args, false, fmt.Errorf("triggers: expected <name>,<int> or <name>,<int>,<int>,<int> got %q", s)
		}
		args[0] = int32(v)
		return "", args, false, nil
	}
	return "", args, false, fmt.Errorf("triggers: expected <name>,<int> or <name>,<int>,<int>,<int> got %q", s)
}

// ParseLine parses a full authored line like "KillUnitType=CORLAB, 1" or
// "AnyUnitPassesX=4500" into a Trigger, handling the condition name,
// ANYTYPE wildcard, and seconds×30 for timer kinds [C14][C15][C17].
// Missing value for flag-only kinds yields a trigger with no args.
func ParseLine(line string) (*Trigger, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("triggers: empty line")
	}
	// Split at first '=' as TDF assignment would [08 "Victory and defeat triggers"].
	eq := strings.IndexByte(line, '=')
	var key, rest string
	if eq >= 0 {
		key = strings.TrimSpace(line[:eq])
		rest = strings.TrimSpace(line[eq+1:])
		// Strip trailing ';' as TDF does [fmt tdf].
		rest = strings.TrimSuffix(rest, ";")
		rest = strings.TrimSpace(rest)
	} else {
		// No '=', treat whole line as key with no args (flag-only)
		key = strings.TrimSpace(line)
	}
	kind, ok := KindByName[strings.ToLower(key)]
	if !ok {
		return nil, fmt.Errorf("triggers: unknown condition %q", key)
	}
	t := &Trigger{Kind: kind}
	// Flag-only kinds have no args [08 "Trigger object"] [GAP T10].
	switch kind {
	case KindKillEnemyCommander, KindDestroyAllUnits, KindKillAllMobileUnits, KindCommanderKilled, KindAllUnitsKilled:
		// none [08 "Victory trigger types"] [08 "Defeat trigger types"]
		if rest != "" && rest != "1" && rest != "0" {
			// Tolerate stray value but ignore per silent-malformed principle [C13] analog.
		}
		return t, nil
	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		if rest == "" {
			return nil, fmt.Errorf("triggers: timer %q requires seconds", key)
		}
		sec, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(rest, ";")), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("triggers: parse timer %q: %w", rest, err)
		}
		t.Args[0] = SecondsToTicks(int32(sec)) // [08 "Trigger object"] seconds×30
		return t, nil
	case KindAnyUnitPassesX, KindAnyUnitPassesZ:
		if rest == "" {
			return nil, fmt.Errorf("triggers: %q requires boundary", key)
		}
		// Single int boundary [08 "Defeat trigger types"].
		b, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(rest, ";")), 10, 32)
		if err != nil {
			// Try as maybe with type? but Any* is boundary only.
			return nil, fmt.Errorf("triggers: parse boundary %q: %w", rest, err)
		}
		t.Args[0] = int32(b)
		return t, nil
	case KindAllUnitsKilledOfType:
		if rest == "" {
			return nil, fmt.Errorf("triggers: %q requires type", key)
		}
		// Type only, no count [08 "Defeat trigger types"].
		typ := strings.TrimSpace(strings.TrimSuffix(rest, ";"))
		// May be comma-separated with trailing int? but spec says type only.
		if idx := strings.IndexByte(typ, ','); idx >= 0 {
			typ = strings.TrimSpace(typ[:idx])
		}
		t.Type = typ
		if IsANYTYPE(typ) {
			t.Type = "ANYTYPE"
		}
		return t, nil
	default:
		// Remaining kinds: type+count or type+boundary or type+radius
		if rest == "" {
			return nil, fmt.Errorf("triggers: %q requires args", key)
		}
		typ, args, _, err := ParseArgs(rest)
		if err != nil {
			return nil, err
		}
		t.Type = typ
		if IsANYTYPE(typ) {
			t.Type = "ANYTYPE"
		}
		t.Args = args
		// Normalize timer-like count storage for MoveRadius: args are X,Z,Radius.
		return t, nil
	}
}

// Poll evaluates the trigger against the context and mutates only its own
// completed flag (pure poll) [08 "Evaluation"] [C17]. It returns true when
// the condition is now satisfied (Completed is set). Already-completed
// triggers stay completed. Unstated details are TODO(question).
//
// Decoded bodies handled verbatim per [08 "Evaluation"] [C17]:
//
//	KillUnitType decrements countdown and completes at zero or below;
//	boundary compares signed world coordinate vs threshold with abs-diff <3 (±2);
//	timers compare tick count against seconds×30.
//
// Type-gated vs ANYTYPE handling per [08 "Victory and defeat triggers"] [C14].
func (t *Trigger) Poll(c Context) bool {
	if t == nil {
		return false
	}
	if t.Completed {
		return true
	}
	switch t.Kind {
	case KindKillUnitType, KindUnitTypeKilled:
		// Countdown: while subject unit type matches authored type,
		// decrement count, completing at zero or below [08 "Evaluation"] [C17].
		// ANYTYPE matches any type [C14].
		if t.Type != "" && !IsANYTYPE(t.Type) {
			if c.Unit == nil || c.Unit.Def == nil {
				return false
			}
			if !strings.EqualFold(c.Unit.Def.UnitName, t.Type) {
				// Also try Type string if present? UnitName is canonical.
				// Fallback to Def.Name? Use UnitName as identity.
				return false
			}
		}
		// Only count when the event is a relevant kill/death?
		// Doc says "while the subject unit's type matches the authored type it decrements"
		// implying each Poll with matching unit decrements. We gate on EventUnitDied
		// or EventUnitCaptured/Created where appropriate, but for purity allow any
		// Poll with matching Unit to count. TODO(question): exact event gating for
		// KillUnitType vs UnitTypeKilled alliance semantics [08 "Evaluation"].
		if c.Event == EventUnitDied || c.Event == EventTick {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			t.Args[0]--
			if t.Args[0] <= 0 {
				t.Completed = true
				return true
			}
		}
		return false

	case KindBuildUnitType, KindCaptureUnitType, KindKillAllOfType, KindAllUnitsKilledOfType:
		// Type-gated annihilation/capture checks [08 "Evaluation"]:
		// compare trigger's type string against subject unit type together with
		// death/capture event flags.
		// TODO(question): alliance/team semantics, capture vs kill distinction,
		// and whether Build checks creation events are not closed [08 "Evaluation"].
		if t.Type != "" && !IsANYTYPE(t.Type) {
			if c.Unit == nil || c.Unit.Def == nil {
				return false
			}
			if !strings.EqualFold(c.Unit.Def.UnitName, t.Type) {
				return false
			}
		}
		switch t.Kind {
		case KindBuildUnitType:
			if c.Event == EventUnitCreated || c.Event == EventUnitBuilt {
				// Type matched above; could also count via countdown if Args[0] holds count
				// For now, single build completes when any matching unit appears unless count >1.
				if t.Args[0] > 1 {
					t.Args[0]--
					return false
				}
				t.Completed = true
				return true
			}
		case KindCaptureUnitType:
			if c.Event == EventUnitCaptured {
				if t.Args[0] > 1 {
					t.Args[0]--
					return false
				}
				t.Completed = true
				return true
			}
		default: // KillAllOfType, AllUnitsKilledOfType
			if c.Event == EventUnitDied {
				if t.Args[0] > 1 {
					// For KillAllOfType the Args[0] may be count, but AllUnitsKilledOfType has no count; treat as 1.
					if t.Kind == KindAllUnitsKilledOfType {
						t.Completed = true
						return true
					}
					t.Args[0]--
					return false
				}
				// TODO(question): need world scan to know if *all* of type are dead; stub counts single event.
				t.Completed = true
				return true
			}
		}
		return false

	case KindUnitTypePassesX, KindUnitTypePassesZ, KindAnyUnitPassesX, KindAnyUnitPassesZ:
		// Boundary conditions compare signed world coordinate against threshold,
		// satisfied when absolute difference is below three world units (±2)
		// [08 "Evaluation"] [C17].
		isX := t.Kind == KindUnitTypePassesX || t.Kind == KindAnyUnitPassesX
		isAny := t.Kind == KindAnyUnitPassesX || t.Kind == KindAnyUnitPassesZ
		if !isAny {
			// Type-gated: check ANYTYPE wildcard [C14] [08 "Victory and defeat triggers"].
			if t.Type != "" && !IsANYTYPE(t.Type) {
				if c.Unit == nil || c.Unit.Def == nil {
					return false
				}
				if !strings.EqualFold(c.Unit.Def.UnitName, t.Type) {
					return false
				}
			}
		}
		var pos, thresh int32
		if isX {
			pos = c.X
			thresh = t.Args[0]
		} else {
			pos = c.Z
			thresh = t.Args[0]
		}
		// Abs diff <3 i.e. -2..+2 inclusive [C17].
		diff := pos - thresh
		if diff < 0 {
			diff = -diff
		}
		if diff < 3 {
			t.Completed = true
			return true
		}
		return false

	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		// Timer triggers compare tick count against stored seconds×30 deadline
		// [08 "Trigger object"] [08 "Evaluation"] [C17].
		if int32(c.Tick) >= t.Args[0] {
			t.Completed = true
			return true
		}
		return false

	case KindMoveUnitToRadius:
		// Largest record carrying X/Y/Z plus radius [08 "Trigger object"] [GAP T10].
		// Evaluator body not fully decoded; implemented as 2D radius check with
		// Euclidean distance <= radius, gated by type/ANYTYPE [C14].
		// TODO(question): exact axis (X/Z vs X/Y) and distance metric not closed [08 "Evaluation"].
		if t.Type != "" && !IsANYTYPE(t.Type) {
			if c.Unit == nil || c.Unit.Def == nil {
				return false
			}
			if !strings.EqualFold(c.Unit.Def.UnitName, t.Type) {
				return false
			}
		}
		cx := t.Args[0]
		cz := t.Args[1]
		rad := t.Args[2]
		dx := c.X - cx
		dz := c.Z - cz
		// Use hypot without float: check dx*dx + dz*dz <= rad*rad.
		// Guard against overflow with int64 [08 "Trigger object"] radius payload.
		if int64(dx)*int64(dx)+int64(dz)*int64(dz) <= int64(rad)*int64(rad) {
			// Also allow tolerance as for boundary? Not specified; keep strict.
			// Provide small tolerance via <3? Not for radius. Keep exact.
			t.Completed = true
			return true
		}
		return false

	case KindKillEnemyCommander, KindDestroyAllUnits, KindKillAllMobileUnits, KindCommanderKilled, KindAllUnitsKilled:
		// Flag-only triggers; decoded evaluators for DestroyAllUnits show a
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// self-satisfied from first poll [notes/campaign/10_trigger_evaluator.md].
		// Other flag-only bodies are pure polls checking global state.
		// TODO(question): exact global scan semantics and team filtering not closed [08 "Evaluation"].
		// Stub: for DestroyAllUnits / AllUnitsKilled etc, we require an explicit context hint via Event.
		// If caller polls with Event completeness, we complete.
		// For determinism, require EventUnitDied for defeat all-killed, etc.
		switch t.Kind {
		case KindKillEnemyCommander:
			if c.Event == EventUnitDied && c.Unit != nil && c.Unit.Def != nil && c.Unit.Def.Commander {
				// Commander died; check enemy vs own? Alliance semantics TODO(question).
				t.Completed = true
				return true
			}
		case KindCommanderKilled:
			if c.Event == EventUnitDied && c.Unit != nil && c.Unit.Def != nil && c.Unit.Def.Commander {
				t.Completed = true
				return true
			}
		case KindDestroyAllUnits, KindKillAllMobileUnits, KindAllUnitsKilled:
			// TODO(question): need world unit count scan; stub to complete only on explicit EventTick with hint?
			// For now, never auto-complete except via external helper EvaluateAll that can check counts.
			return false
		}
		return false
	default:
		return false
	}
}

// Evaluate iterates the victory and defeat arrays calling each checker.
// It is a helper for the tick site that was never decompiled and is inferred
// from vtable layout [GAP T10] [08 "Evaluation"].
// TODO(question): the tick site that iterates the victory/defeat arrays was
// never decompiled; dispatch pattern is inferred [PLAN_10 explicit unknown].
// Poll from a dedicated kernel registration and note at call site.
func Evaluate(victory []*Trigger, defeat []*Trigger, c Context) (victoryDone bool, defeatDone bool) {
	// Victory = AND across queue; defeat = OR; victory evaluated first ⇒ simultaneous = victory
	// [notes/campaign/10_trigger_evaluator.md] TODO(question): ordering when multiple fire,
	// short-circuit rules, and team semantics not fully closed [08 "Evaluation"].
	for _, t := range victory {
		if t == nil {
			continue
		}
		t.Poll(c)
	}
	for _, t := range defeat {
		if t == nil {
			continue
		}
		t.Poll(c)
	}
	// Compute done: victory requires all completed (AND), defeat requires any completed (OR).
	if len(victory) > 0 {
		all := true
		for _, t := range victory {
			if t == nil || !t.Completed {
				all = false
				break
			}
		}
		victoryDone = all
	}
	if len(defeat) > 0 {
		for _, t := range defeat {
			if t != nil && t.Completed {
				defeatDone = true
				break
			}
		}
	}
	// Simultaneous = victory (victory evaluated first) [notes/campaign/10_trigger_evaluator.md].
	if victoryDone && defeatDone {
		defeatDone = false
	}
	return victoryDone, defeatDone
}
