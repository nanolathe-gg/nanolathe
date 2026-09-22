package community

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RepairRate configures the proportional repair helper. A disabled helper in
// a shipped table still carries its source multipliers (both one); the zero
// value is reserved for the retail identity.
type RepairRate struct {
	Enabled            bool `json:"enabled"`
	RepairMultiplier   int  `json:"repairMultiplier"`
	SelfHealMultiplier int  `json:"selfHealMultiplier"`
}

// Features is the complete Community 3.9 table resolved for one session. Its
// zero value is the retail identity used by Strict 3.1.
//
// Snap maxima are part of the value because the patch's per-profile maximum
// bounds the player-configured radius. They are selected only by Table; an
// Overrides value may change a radius but cannot change its evidence-backed
// cap (community-patch-engine.md CP-CON-6).
type Features struct {
	ConstructionKickout      bool `json:"constructionKickout"`
	GuardingBuildersHold     bool `json:"guardingBuildersHold"`
	PatrollingBuilderFilters bool `json:"patrollingBuilderFilters"`
	ReclaimToggleKeepsBuild  bool `json:"reclaimToggleKeepsBuild"`
	StructureRotation        bool `json:"structureRotation"`
	AreaDamageOverflow       bool `json:"areaDamageOverflow"`
	AreaDamageDedupCap       bool `json:"areaDamageDedupCap"`
	GridClaimTieBreak        bool `json:"gridClaimTieBreak"`
	TransportedExplosions    bool `json:"transportedExplosions"`
	BuildWeaponSlotGuard     bool `json:"buildWeaponSlotGuard"`
	AntinukeCircularCoverage bool `json:"antinukeCircularCoverage"`
	AlliedJammingIgnored     bool `json:"alliedJammingIgnored"`
	ResurrectionFinalization bool `json:"resurrectionFinalization"`
	WeaponTargetKeys         bool `json:"weaponTargetKeys"`
	Veterancy                bool `json:"veterancy"`
	SchemaUnits              bool `json:"schemaUnits"`
	AirCorpseFall            bool `json:"airCorpseFall"`
	ScriptPorts              bool `json:"scriptPorts"`
	MexSnap                  bool `json:"mexSnap"`
	WreckSnap                bool `json:"wreckSnap"`

	RepairRate                RepairRate `json:"repairRate"`
	OffMapAircraftMarginTiles int        `json:"offMapAircraftMarginTiles"`
	ProjectileCapacity        int        `json:"projectileCapacity"`
	ExplosionCapacity         int        `json:"explosionCapacity"`
	DebrisCapacity            int        `json:"debrisCapacity"`
	PathStepAllowance         int        `json:"pathStepAllowance"`
	UnitLimit                 int        `json:"unitLimit"`
	MexSnapRadius             int        `json:"mexSnapRadius"`
	WreckSnapRadius           int        `json:"wreckSnapRadius"`
	MexSnapRadiusMax          int        `json:"mexSnapRadiusMax"`
	WreckSnapRadiusMax        int        `json:"wreckSnapRadiusMax"`
}

// Digest returns the SHA-256 digest of the canonical JSON representation of
// the resolved value. Features contains no maps or optional encodings, so the
// representation and digest are stable across calls.
func (f Features) Digest() string {
	b, _ := json.Marshal(f) // Features contains only JSON's primitive value kinds.
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
