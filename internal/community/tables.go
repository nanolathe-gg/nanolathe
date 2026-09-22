package community

import (
	"fmt"
	"strings"
)

const Mainline = "prota"

var common = Features{
	ReclaimToggleKeepsBuild:  true,
	StructureRotation:        true,
	AreaDamageDedupCap:       true,
	TransportedExplosions:    true,
	AntinukeCircularCoverage: true,
	AlliedJammingIgnored:     true,
	ResurrectionFinalization: true,
	WeaponTargetKeys:         true,
	Veterancy:                true,
	SchemaUnits:              true,
	ScriptPorts:              true,

	RepairRate: RepairRate{
		RepairMultiplier:   1,
		SelfHealMultiplier: 1,
	},
	ProjectileCapacity: 3000,
	ExplosionCapacity:  3000,
	DebrisCapacity:     1000,
	PathStepAllowance:  66650,
	UnitLimit:          1500,
}

// Table returns one shipped tdraw build profile. The constants are the
// effective compile-time matrix and shipped simulation preference defaults in
// community-patch-engine.md sections 3.1 and 4.1.
func Table(name string) (Features, error) {
	f := common
	switch name {
	case "prota":
		f.ConstructionKickout = true
		f.GuardingBuildersHold = true
		f.PatrollingBuilderFilters = true
		f.AreaDamageOverflow = true
		f.GridClaimTieBreak = true
		f.OffMapAircraftMarginTiles = 1
		setSnap(&f, 3, 3, 1, 1)
	case "escalation":
		f.ConstructionKickout = true
		f.GuardingBuildersHold = true
		f.PatrollingBuilderFilters = true
		f.AreaDamageOverflow = true
		f.GridClaimTieBreak = true
		f.BuildWeaponSlotGuard = true
		f.AirCorpseFall = true
		f.RepairRate = RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 3}
		f.OffMapAircraftMarginTiles = 32
		setSnap(&f, 0, 0, 1, 1)
	case "ota":
		f.OffMapAircraftMarginTiles = 1
		setSnap(&f, 0, 0, 0, 0)
	case "tazero":
		f.ConstructionKickout = true
		f.GuardingBuildersHold = true
		f.PatrollingBuilderFilters = true
		f.OffMapAircraftMarginTiles = 1
		setSnap(&f, 3, 3, 1, 1)
	case "bta":
		f.ConstructionKickout = true
		f.GuardingBuildersHold = true
		f.PatrollingBuilderFilters = true
		f.OffMapAircraftMarginTiles = 1
		setSnap(&f, 1, 1, 1, 1)
	case "mayhem", "twilight":
		f.ConstructionKickout = true
		f.GuardingBuildersHold = true
		f.PatrollingBuilderFilters = true
		f.AreaDamageOverflow = true
		f.GridClaimTieBreak = true
		f.OffMapAircraftMarginTiles = 32
		setSnap(&f, 3, 3, 1, 1)
	default:
		return Features{}, featureError(
			"resolve community features",
			fmt.Sprintf("<table %q>", name),
			"embedded community tables",
			"one of "+strings.Join(tableNames(), ", "),
			nil,
		)
	}
	return f, nil
}

func setSnap(f *Features, mexDefault, mexMax, wreckDefault, wreckMax int) {
	f.MexSnap = mexMax != 0
	f.WreckSnap = wreckMax != 0
	f.MexSnapRadius = mexDefault
	f.WreckSnapRadius = wreckDefault
	f.MexSnapRadiusMax = mexMax
	f.WreckSnapRadiusMax = wreckMax
}

func tableNames() []string {
	return []string{"ota", "prota", "escalation", "tazero", "bta", "mayhem", "twilight"}
}
