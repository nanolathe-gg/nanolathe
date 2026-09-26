package community

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RepairRateOverrides carries field-level changes to RepairRate. Pointers
// distinguish an absent value from an explicit false or zero.
type RepairRateOverrides struct {
	Enabled            *bool `json:"enabled,omitempty"`
	RepairMultiplier   *int  `json:"repairMultiplier,omitempty"`
	SelfHealMultiplier *int  `json:"selfHealMultiplier,omitempty"`
}

// Overrides is one feature-table source. Table, when present, replaces the
// current value before the remaining fields in this source are applied.
// Pointer fields preserve explicit false and zero values from JSON.
type Overrides struct {
	Table string `json:"table,omitempty"`

	ConstructionKickout      *bool `json:"constructionKickout,omitempty"`
	GuardingBuildersHold     *bool `json:"guardingBuildersHold,omitempty"`
	PatrollingBuilderFilters *bool `json:"patrollingBuilderFilters,omitempty"`
	ReclaimToggleKeepsBuild  *bool `json:"reclaimToggleKeepsBuild,omitempty"`
	StructureRotation        *bool `json:"structureRotation,omitempty"`
	AreaDamageOverflow       *bool `json:"areaDamageOverflow,omitempty"`
	AreaDamageDedupCap       *bool `json:"areaDamageDedupCap,omitempty"`
	GridClaimTieBreak        *bool `json:"gridClaimTieBreak,omitempty"`
	TransportedExplosions    *bool `json:"transportedExplosions,omitempty"`
	BuildWeaponSlotGuard     *bool `json:"buildWeaponSlotGuard,omitempty"`
	AntinukeCircularCoverage *bool `json:"antinukeCircularCoverage,omitempty"`
	AlliedJammingIgnored     *bool `json:"alliedJammingIgnored,omitempty"`
	ResurrectionFinalization *bool `json:"resurrectionFinalization,omitempty"`
	WeaponTargetKeys         *bool `json:"weaponTargetKeys,omitempty"`
	Veterancy                *bool `json:"veterancy,omitempty"`
	SchemaUnits              *bool `json:"schemaUnits,omitempty"`
	AirCorpseFall            *bool `json:"airCorpseFall,omitempty"`
	ScriptPorts              *bool `json:"scriptPorts,omitempty"`
	MexSnap                  *bool `json:"mexSnap,omitempty"`
	WreckSnap                *bool `json:"wreckSnap,omitempty"`

	AIDifficultyIncome     *bool `json:"aiDifficultyIncome,omitempty"`
	AIStockpileProducts    *bool `json:"aiStockpileProducts,omitempty"`
	TargetLockRelease      *bool `json:"targetLockRelease,omitempty"`
	AIApplianceEnergy      *bool `json:"aiApplianceEnergy,omitempty"`
	AIBuilderStopThreshold *bool `json:"aiBuilderStopThreshold,omitempty"`

	WorkingWeaponsAutonomous *bool `json:"workingWeaponsAutonomous,omitempty"`
	AttackSingleSlotTake     *bool `json:"attackSingleSlotTake,omitempty"`
	MapFeatureOwnerEleven    *bool `json:"mapFeatureOwnerEleven,omitempty"`
	ResurrectionTextFix      *bool `json:"resurrectionTextFix,omitempty"`
	HealTimeBitmask          *bool `json:"healTimeBitmask,omitempty"`
	AIBuilderPlacementLimit  *int  `json:"aiBuilderPlacementLimit,omitempty"`

	RepairRate                *RepairRateOverrides `json:"repairRate,omitempty"`
	OffMapAircraftMarginTiles *int                 `json:"offMapAircraftMarginTiles,omitempty"`
	ProjectileCapacity        *int                 `json:"projectileCapacity,omitempty"`
	ExplosionCapacity         *int                 `json:"explosionCapacity,omitempty"`
	DebrisCapacity            *int                 `json:"debrisCapacity,omitempty"`
	PathStepAllowance         *int                 `json:"pathStepAllowance,omitempty"`
	UnitLimit                 *int                 `json:"unitLimit,omitempty"`
	MexSnapRadius             *int                 `json:"mexSnapRadius,omitempty"`
	WreckSnapRadius           *int                 `json:"wreckSnapRadius,omitempty"`
}

// UnmarshalJSON keeps Overrides closed: unknown JSON keys are configuration
// errors rather than ignored feature names.
func (o *Overrides) UnmarshalJSON(data []byte) error {
	type plain Overrides
	if err := decodeClosed(data, (*plain)(o)); err != nil {
		return featureError(
			"decode community feature overrides",
			"<settings gameplayFeatures>",
			"JSON",
			"known lowerCamelCase feature fields",
			err,
		)
	}
	return nil
}

func decodeClosed(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// Resolve starts from the mainline table and applies sources in order. Strict
// resolution ignores every source, including malformed ones, and returns the
// retail identity.
func Resolve(strict bool, sources ...Overrides) (Features, error) {
	if strict {
		return Features{}, nil
	}
	f, err := Table(Mainline)
	if err != nil {
		return Features{}, err
	}
	for i, source := range sources {
		f, err = apply(f, source, fmt.Sprintf("<community source %d>", i+1))
		if err != nil {
			return Features{}, err
		}
	}
	return f, nil
}

func apply(base Features, o Overrides, logicalPath string) (Features, error) {
	var err error
	if o.Table != "" {
		base, err = Table(o.Table)
		if err != nil {
			return Features{}, err
		}
	}

	applyBool(&base.ConstructionKickout, o.ConstructionKickout)
	applyBool(&base.GuardingBuildersHold, o.GuardingBuildersHold)
	applyBool(&base.PatrollingBuilderFilters, o.PatrollingBuilderFilters)
	applyBool(&base.ReclaimToggleKeepsBuild, o.ReclaimToggleKeepsBuild)
	applyBool(&base.StructureRotation, o.StructureRotation)
	applyBool(&base.AreaDamageOverflow, o.AreaDamageOverflow)
	applyBool(&base.AreaDamageDedupCap, o.AreaDamageDedupCap)
	applyBool(&base.GridClaimTieBreak, o.GridClaimTieBreak)
	applyBool(&base.TransportedExplosions, o.TransportedExplosions)
	applyBool(&base.BuildWeaponSlotGuard, o.BuildWeaponSlotGuard)
	applyBool(&base.AntinukeCircularCoverage, o.AntinukeCircularCoverage)
	applyBool(&base.AlliedJammingIgnored, o.AlliedJammingIgnored)
	applyBool(&base.ResurrectionFinalization, o.ResurrectionFinalization)
	applyBool(&base.WeaponTargetKeys, o.WeaponTargetKeys)
	applyBool(&base.Veterancy, o.Veterancy)
	applyBool(&base.SchemaUnits, o.SchemaUnits)
	applyBool(&base.AirCorpseFall, o.AirCorpseFall)
	applyBool(&base.ScriptPorts, o.ScriptPorts)
	applyBool(&base.MexSnap, o.MexSnap)
	applyBool(&base.WreckSnap, o.WreckSnap)
	applyBool(&base.AIDifficultyIncome, o.AIDifficultyIncome)
	applyBool(&base.AIStockpileProducts, o.AIStockpileProducts)
	applyBool(&base.TargetLockRelease, o.TargetLockRelease)
	applyBool(&base.AIApplianceEnergy, o.AIApplianceEnergy)
	applyBool(&base.AIBuilderStopThreshold, o.AIBuilderStopThreshold)
	applyBool(&base.WorkingWeaponsAutonomous, o.WorkingWeaponsAutonomous)
	applyBool(&base.AttackSingleSlotTake, o.AttackSingleSlotTake)
	applyBool(&base.MapFeatureOwnerEleven, o.MapFeatureOwnerEleven)
	applyBool(&base.ResurrectionTextFix, o.ResurrectionTextFix)
	applyBool(&base.HealTimeBitmask, o.HealTimeBitmask)
	applyInt(&base.AIBuilderPlacementLimit, o.AIBuilderPlacementLimit)

	if o.RepairRate != nil {
		applyBool(&base.RepairRate.Enabled, o.RepairRate.Enabled)
		applyInt(&base.RepairRate.RepairMultiplier, o.RepairRate.RepairMultiplier)
		applyInt(&base.RepairRate.SelfHealMultiplier, o.RepairRate.SelfHealMultiplier)
	}
	applyInt(&base.OffMapAircraftMarginTiles, o.OffMapAircraftMarginTiles)
	applyInt(&base.ProjectileCapacity, o.ProjectileCapacity)
	applyInt(&base.ExplosionCapacity, o.ExplosionCapacity)
	applyInt(&base.DebrisCapacity, o.DebrisCapacity)
	applyInt(&base.PathStepAllowance, o.PathStepAllowance)
	applyInt(&base.UnitLimit, o.UnitLimit)
	applyInt(&base.MexSnapRadius, o.MexSnapRadius)
	applyInt(&base.WreckSnapRadius, o.WreckSnapRadius)

	if err := validate(base); err != nil {
		return Features{}, featureError(
			"apply community feature overrides",
			logicalPath,
			"community feature table",
			"values within the documented feature bounds",
			err,
		)
	}
	base.MexSnapRadius = min(base.MexSnapRadius, base.MexSnapRadiusMax)
	base.WreckSnapRadius = min(base.WreckSnapRadius, base.WreckSnapRadiusMax)
	return base, nil
}

func validate(f Features) error {
	if f.RepairRate.RepairMultiplier < 1 || f.RepairRate.RepairMultiplier > 100 {
		return fmt.Errorf("repairRate.repairMultiplier %d outside 1..100", f.RepairRate.RepairMultiplier)
	}
	if f.RepairRate.SelfHealMultiplier < 1 || f.RepairRate.SelfHealMultiplier > 100 {
		return fmt.Errorf("repairRate.selfHealMultiplier %d outside 1..100", f.RepairRate.SelfHealMultiplier)
	}
	values := []struct {
		name  string
		value int
	}{
		{"aiBuilderPlacementLimit", f.AIBuilderPlacementLimit},
		{"offMapAircraftMarginTiles", f.OffMapAircraftMarginTiles},
		{"projectileCapacity", f.ProjectileCapacity},
		{"explosionCapacity", f.ExplosionCapacity},
		{"debrisCapacity", f.DebrisCapacity},
		{"pathStepAllowance", f.PathStepAllowance},
		{"unitLimit", f.UnitLimit},
		{"mexSnapRadius", f.MexSnapRadius},
		{"wreckSnapRadius", f.WreckSnapRadius},
	}
	for _, item := range values {
		if err := validateParsedInt(item.name, item.value); err != nil {
			return fmt.Errorf("%s %d: %w", item.name, item.value, err)
		}
	}
	return nil
}

func applyBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func applyInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

// ParseOverride parses one --gameplay-feature name=value argument.
func ParseOverride(text string) (Overrides, error) {
	name, value, ok := strings.Cut(text, "=")
	if !ok || name == "" || value == "" {
		return Overrides{}, featureError("parse gameplay feature", "<command line>", "community features", "name=value", fmt.Errorf("got %q", text))
	}
	o := Overrides{}
	if name == "table" {
		if _, err := Table(value); err != nil {
			return Overrides{}, err
		}
		o.Table = value
		return o, nil
	}

	if dst := boolField(&o, name); dst != nil {
		v, err := parseBool(value)
		if err != nil {
			return Overrides{}, featureError("parse gameplay feature", "<command line>", "community features", "name=true or name=false", err)
		}
		*dst = &v
		return o, nil
	}
	if dst := intField(&o, name); dst != nil {
		v, err := strconv.Atoi(value)
		if err != nil {
			return Overrides{}, featureError("parse gameplay feature", "<command line>", "community features", "a decimal integer value", err)
		}
		if err := validateParsedInt(name, v); err != nil {
			return Overrides{}, featureError("parse gameplay feature", "<command line>", "community features", "a value within the documented feature bounds", err)
		}
		*dst = &v
		return o, nil
	}
	return Overrides{}, featureError("parse gameplay feature", "<command line>", "community features", "a known lowerCamelCase feature name", fmt.Errorf("got %q", name))
}

func featureError(what, logicalPath, providers, expected string, cause error) error {
	message := fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s", what, logicalPath, providers, expected)
	if cause == nil {
		return fmt.Errorf("%s", message)
	}
	return fmt.Errorf("%s: %w", message, cause)
}

func validateParsedInt(name string, value int) error {
	if value < 0 {
		return fmt.Errorf("expected a non-negative integer")
	}
	switch name {
	// Representation bounds prevent a digest describing silently clamped pools.
	case "projectileCapacity":
		if value > 32767 {
			return fmt.Errorf("exceeds compact marker limit 32767")
		}
	case "explosionCapacity":
		if value > 65535 {
			return fmt.Errorf("exceeds fragment identity limit 65535")
		}
	case "debrisCapacity":
		if value > int(^uint(0)>>1)/100000 {
			return fmt.Errorf("overflows backing store size")
		}
	case "aiBuilderPlacementLimit":
		if int64(value) > 1<<31-1 {
			return fmt.Errorf("exceeds signed AI comparison limit")
		}
	case "pathStepAllowance":
		if int64(value) > 1<<31-1 {
			return fmt.Errorf("exceeds signed scheduler limit")
		}
	case "unitLimit":
		if value != 0 && (value < 20 || value > 3276) {
			return fmt.Errorf("expected 0 or a value in 20..3276")
		}
	case "repairRate.repairMultiplier", "repairRate.selfHealMultiplier":
		if value < 1 || value > 100 {
			return fmt.Errorf("expected a value in 1..100")
		}
	}
	return nil
}

func boolField(o *Overrides, name string) **bool {
	switch name {
	case "healTimeBitmask":
		return &o.HealTimeBitmask
	case "constructionKickout":
		return &o.ConstructionKickout
	case "guardingBuildersHold":
		return &o.GuardingBuildersHold
	case "patrollingBuilderFilters":
		return &o.PatrollingBuilderFilters
	case "reclaimToggleKeepsBuild":
		return &o.ReclaimToggleKeepsBuild
	case "structureRotation":
		return &o.StructureRotation
	case "areaDamageOverflow":
		return &o.AreaDamageOverflow
	case "areaDamageDedupCap":
		return &o.AreaDamageDedupCap
	case "gridClaimTieBreak":
		return &o.GridClaimTieBreak
	case "transportedExplosions":
		return &o.TransportedExplosions
	case "buildWeaponSlotGuard":
		return &o.BuildWeaponSlotGuard
	case "antinukeCircularCoverage":
		return &o.AntinukeCircularCoverage
	case "alliedJammingIgnored":
		return &o.AlliedJammingIgnored
	case "resurrectionFinalization":
		return &o.ResurrectionFinalization
	case "weaponTargetKeys":
		return &o.WeaponTargetKeys
	case "veterancy":
		return &o.Veterancy
	case "schemaUnits":
		return &o.SchemaUnits
	case "airCorpseFall":
		return &o.AirCorpseFall
	case "scriptPorts":
		return &o.ScriptPorts
	case "mexSnap":
		return &o.MexSnap
	case "wreckSnap":
		return &o.WreckSnap
	case "aiDifficultyIncome":
		return &o.AIDifficultyIncome
	case "aiStockpileProducts":
		return &o.AIStockpileProducts
	case "targetLockRelease":
		return &o.TargetLockRelease
	case "aiApplianceEnergy":
		return &o.AIApplianceEnergy
	case "aiBuilderStopThreshold":
		return &o.AIBuilderStopThreshold
	case "workingWeaponsAutonomous":
		return &o.WorkingWeaponsAutonomous
	case "attackSingleSlotTake":
		return &o.AttackSingleSlotTake
	case "mapFeatureOwnerEleven":
		return &o.MapFeatureOwnerEleven
	case "resurrectionTextFix":
		return &o.ResurrectionTextFix
	case "repairRate.enabled":
		if o.RepairRate == nil {
			o.RepairRate = &RepairRateOverrides{}
		}
		return &o.RepairRate.Enabled
	default:
		return nil
	}
}

func intField(o *Overrides, name string) **int {
	switch name {
	case "aiBuilderPlacementLimit":
		return &o.AIBuilderPlacementLimit
	case "offMapAircraftMarginTiles":
		return &o.OffMapAircraftMarginTiles
	case "projectileCapacity":
		return &o.ProjectileCapacity
	case "explosionCapacity":
		return &o.ExplosionCapacity
	case "debrisCapacity":
		return &o.DebrisCapacity
	case "pathStepAllowance":
		return &o.PathStepAllowance
	case "unitLimit":
		return &o.UnitLimit
	case "mexSnapRadius":
		return &o.MexSnapRadius
	case "wreckSnapRadius":
		return &o.WreckSnapRadius
	case "repairRate.repairMultiplier":
		if o.RepairRate == nil {
			o.RepairRate = &RepairRateOverrides{}
		}
		return &o.RepairRate.RepairMultiplier
	case "repairRate.selfHealMultiplier":
		if o.RepairRate == nil {
			o.RepairRate = &RepairRateOverrides{}
		}
		return &o.RepairRate.SelfHealMultiplier
	default:
		return nil
	}
}

func parseBool(text string) (bool, error) {
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false")
	}
}
