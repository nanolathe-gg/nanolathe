package triggers

import (
	"fmt"
	"strings"
)

// Kind identifies one of the eighteen victory/defeat trigger types
// [08 "Victory and defeat triggers"] [08 "Victory trigger types"] [08 "Defeat trigger types"] [GAP T10].
type Kind uint8

const (
	// Victory triggers [08 "Victory trigger types"] in builder probe order [GAP T10].
	KindKillEnemyCommander  Kind = iota
	KindDestroyAllUnits          // 1 flag-only 0xC
	KindKillAllMobileUnits       // 2 flag-only 0xC
	KindBuildUnitType            // 3 name-only
	KindCaptureUnitType          // 4 name-only
	KindKillAllOfType            // 5 name-only
	KindKillUnitType             // 6 type+count 0x32
	KindMoveUnitToRadius         // 7 type+X/Z/radius 0x40
	KindUnitTypePassesX          // 8 type/boundary 0x14
	KindUnitTypePassesZ          // 9 type/boundary 0x14
	KindVictoryTimerRunsOut      // 10 timer 0x10
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

// RecordSize reports the established allocation identity for each trigger
// record. Go keeps named fields rather than reproducing this packed layout
// [08 R-TRIG-01 §2][I13].
func (k Kind) RecordSize() int {
	switch k {
	case KindKillEnemyCommander, KindDestroyAllUnits, KindCommanderKilled:
		return 12
	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		return 16
	case KindAllUnitsKilled:
		return 16
	case KindKillAllMobileUnits, KindAnyUnitPassesX, KindAnyUnitPassesZ:
		return 20
	case KindCaptureUnitType:
		return 44
	case KindKillUnitType, KindUnitTypeKilled:
		return 48
	case KindBuildUnitType, KindKillAllOfType:
		return 50
	case KindUnitTypePassesX, KindUnitTypePassesZ:
		return 52
	case KindAllUnitsKilledOfType:
		return 54
	case KindMoveUnitToRadius:
		return 64
	default:
		return 12
	}
}

// VTable records the six established dispatch slots: poll, unit removal,
// capture, creation, save and load [08 R-TRIG-01 §2].
type VTable struct {
	Kind       Kind
	Name       string
	RecordSize int // bytes, see Kind.RecordSize
	Slots      int
}

// VTables is the eighteen-entry vtable map covering all condition types
// [08 "Trigger object"] [GAP T10].
var VTables = [KindCount]VTable{
	{Kind: KindKillEnemyCommander, Name: "KillEnemyCommander", RecordSize: 12, Slots: 6},
	{Kind: KindDestroyAllUnits, Name: "DestroyAllUnits", RecordSize: 12, Slots: 6},
	{Kind: KindKillAllMobileUnits, Name: "KillAllMobileUnits", RecordSize: 20, Slots: 6},
	{Kind: KindBuildUnitType, Name: "BuildUnitType", RecordSize: 50, Slots: 6},
	{Kind: KindCaptureUnitType, Name: "CaptureUnitType", RecordSize: 44, Slots: 6},
	{Kind: KindKillAllOfType, Name: "KillAllOfType", RecordSize: 50, Slots: 6},
	{Kind: KindKillUnitType, Name: "KillUnitType", RecordSize: 48, Slots: 6},
	{Kind: KindMoveUnitToRadius, Name: "MoveUnitToRadius", RecordSize: 64, Slots: 6},
	{Kind: KindUnitTypePassesX, Name: "UnitTypePassesX", RecordSize: 52, Slots: 6},
	{Kind: KindUnitTypePassesZ, Name: "UnitTypePassesZ", RecordSize: 52, Slots: 6},
	{Kind: KindVictoryTimerRunsOut, Name: "VictoryTimerRunsOut", RecordSize: 16, Slots: 6},
	{Kind: KindCommanderKilled, Name: "CommanderKilled", RecordSize: 12, Slots: 6},
	{Kind: KindAllUnitsKilled, Name: "AllUnitsKilled", RecordSize: 16, Slots: 6},
	{Kind: KindAllUnitsKilledOfType, Name: "AllUnitsKilledOfType", RecordSize: 54, Slots: 6},
	{Kind: KindUnitTypeKilled, Name: "UnitTypeKilled", RecordSize: 48, Slots: 6},
	{Kind: KindDeathTimerRunsOut, Name: "DeathTimerRunsOut", RecordSize: 16, Slots: 6},
	{Kind: KindAnyUnitPassesX, Name: "AnyUnitPassesX", RecordSize: 20, Slots: 6},
	{Kind: KindAnyUnitPassesZ, Name: "AnyUnitPassesZ", RecordSize: 20, Slots: 6},
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

// IsANYTYPE reports whether the unit-type token is the wildcard literal.
// Only MoveUnitToRadius and UnitTypePassesX/Z interpret it as a wildcard;
// name-only families retain it as an ordinary name [08 R-TRIG-01 §2].
func IsANYTYPE(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "ANYTYPE") }

// Trigger is a polymorphic mission condition record.
// Every record leads with a vtable pointer whose table covers all eighteen
// types, with the completed flag adjacent [08 "Trigger object"].
// Go uses named fields per [I13]; byte sizes are identity via Kind.RecordSize().
type Trigger struct {
	Kind                      Kind     // 18 kinds [08 "Victory and defeat triggers"] [C15]
	Type                      string   // copied name, or empty wildcard for the three supported families
	Args                      [3]int32 // authored ints: count/threshold/ticks/X/Z/radius [C14]
	Completed                 bool
	Celebrated                bool
	CenterReady               bool
	CenterX, CenterY, CenterZ int32
}

// Evaluation lives in eval.go: the tick-driven poll slot, the three
// notification slots, and the AND/OR combination of the two queues
// [08 "Evaluation"].

// SecondsToTicks converts authored seconds to the absolute 30 Hz deadline,
// preserving signed 32-bit wrap [08 R-TRIG-01 §2, §4].
func SecondsToTicks(seconds int32) int32 { return int32(int64(seconds) * 30) }

// New constructs a trigger of the given kind with type and up to three args.
// For timer kinds, pass seconds converted through SecondsToTicks.
func New(kind Kind, typ string, args ...int32) *Trigger {
	t := &Trigger{Kind: kind, Type: typ}
	for i := 0; i < len(args) && i < 3; i++ {
		t.Args[i] = args[i]
	}
	// Only the radius and typed-boundary families recognize ANYTYPE, and they
	// store it as an empty name [08 R-TRIG-01 §2].
	if IsANYTYPE(typ) && (kind == KindMoveUnitToRadius || kind == KindUnitTypePassesX || kind == KindUnitTypePassesZ) {
		t.Type = ""
	}
	return t
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
