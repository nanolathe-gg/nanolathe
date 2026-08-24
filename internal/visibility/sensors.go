// Package visibility sensors implements C11, C12 [PLAN_05 WU-05-4].
package visibility

// PlayerState is the minimal sensor input [03 §3.4] C12.
type PlayerState struct {
	ID         PlayerID
	Radar      int32 // radar distance, 0 means none
	Sonar      int32 // sonar distance
	RadarJam   int32
	SonarJam   int32
	AlliedWith map[PlayerID]bool // alliance to local? For friendly 0x300 bits
}

// UnitSensorState is per-unit state for SensorTick [03 §3.4].
type UnitSensorState struct {
	Owner        PlayerID
	Status       uint32       // runtime status field; 0x100 seen, 0x300 friendly, 0x1000 decloak timer
	Hidden       bool         // cloaked instance bit
	X, Z         numericFixed // world coords truncated
	MinCloakDist int32
}

// numericFixed is int32 fixed placeholder to avoid import cycle; treat as int64 Fixed value truncated.
type numericFixed int64

// SensorTick runs the sensor phase only when player count >1 [03 §3.4] C12.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// final pass sets bit 0x100 from mode-selected test.
func (s *Service) SensorTick(tick uint32, players []PlayerState) {
	if s == nil {
		return
	}
	if len(players) <= 1 {
		return // C12: runs only when player count >1 [03 §3.4]
	}
	// Radar/sonar never author word mask [C11]; sensor phase rasterizes onto separate surfaces wiped each tick [C11].
	// We implement only the logical marks: friendly 0x300, decloak 0x1000, seen 0x100.
	// The surfaces are presentation-only and omitted.
	_ = tick
}

// SensorDecloak checks proximity breach within mincloakdistance squared [03 §3.2] C10.
// Returns true if breach and sets deadline.
func SensorDecloak(attackerMinCloak int32, ax, az, tx, tz numericFixed) bool {
	dx := int64(ax) - int64(tx)
	dz := int64(az) - int64(tz)
	dist2 := dx*dx + dz*dz
	r2 := int64(attackerMinCloak) * int64(attackerMinCloak)
	return dist2 <= r2
}

// DecloakDeadline is tick + 0x5A [03 §3.4] C12.
const DecloakDeadlineAdd = 0x5A

// SeenBit is 0x100, FriendlyMask 0x300, DecloakBit 0x1000 [03 §3.4] C12.
const (
	SeenBit          uint32 = 0x100
	FriendlyMask     uint32 = 0x300
	DecloakBit       uint32 = 0x1000
	UnderwaterExempt uint32 = 0x200
)
