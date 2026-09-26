package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// CommunitySources retains the composition inputs needed when the selected
// rule set changes at the command boundary (DESIGN_COMMUNITY_PATCH §3.2).
// Registered declarations are inserted between content and player settings.
type CommunitySources struct {
	Content     []community.Overrides
	Player      community.Overrides
	CommandLine []community.Overrides
}

// ResolveCommunity resolves one complete value before any service consumes it.
// Strict ignores all declarations, including registered feature overrides.
func ResolveCommunity(mode gameplay.Mode, sources CommunitySources) (community.Features, error) {
	set := RuleSetForMode(mode)
	return resolveCommunity(set, sources)
}

func resolveCommunity(set RuleSet, sources CommunitySources) (community.Features, error) {
	if set.Base == gameplay.Strict31 {
		return community.Features{}, nil
	}
	all := make([]community.Overrides, 0, len(sources.Content)+len(sources.CommandLine)+2)
	all = append(all, sources.Content...)
	all = append(all, set.Features, sources.Player)
	all = append(all, sources.CommandLine...)
	return community.Resolve(set.Base == gameplay.Strict31, all...)
}

// projectCommunity copies each owner's inputs beside its already bound rules.
// Tick code reads these fields through its seam; it never resolves a table.
func (s *Session) projectCommunity() {
	f := s.Community
	if s.Vis != nil {
		state := &s.Vis.Community
		state.AlliedJammingIgnored = f.AlliedJammingIgnored
		state.OffMapAircraftMarginTiles = f.OffMapAircraftMarginTiles
		if state.Allied == nil {
			state.Allied = s.visibilityAllied
		}
		if state.OffMap == nil {
			state.OffMap = s.visibilityOffMap
		}
	}
	if s.Combat != nil {
		areaOverflowWasEnabled := s.Combat.Community.AreaDamageOverflow
		s.Combat.Community = community.Features{
			AreaDamageOverflow: f.AreaDamageOverflow, AreaDamageDedupCap: f.AreaDamageDedupCap,
			TransportedExplosions: f.TransportedExplosions, BuildWeaponSlotGuard: f.BuildWeaponSlotGuard,
			AntinukeCircularCoverage: f.AntinukeCircularCoverage, WeaponTargetKeys: f.WeaponTargetKeys,
			Veterancy: f.Veterancy, AirCorpseFall: f.AirCorpseFall,
			OffMapAircraftMarginTiles: f.OffMapAircraftMarginTiles,
			TargetLockRelease:         f.TargetLockRelease,
		}
		if areaOverflowWasEnabled != f.AreaDamageOverflow {
			// Q13 refreshes this snapshot only after a completed tick. Discard an
			// index when its live feature gate changes so the first re-enabled
			// projectile phase cannot consume handles left before Strict's bypass.
			s.Combat.InvalidateCommunityAreaIndex()
		}
	}
	if s.Build != nil {
		s.Build.Community = community.Features{
			ConstructionKickout: f.ConstructionKickout, StructureRotation: f.StructureRotation,
			ResurrectionFinalization: f.ResurrectionFinalization, RepairRate: f.RepairRate,
			HealTimeBitmask: f.HealTimeBitmask,
		}
		s.Build.PrepareRepairBanks(s.Units)
		if s.Build.OrderBinding != nil {
			s.Build.OrderBinding.Community = s.orderCommunity()
			if s.Build.OrderBinding.BuilderOptions == nil {
				s.Build.OrderBinding.BuilderOptions = s.builderOptionsForOwner
			}
		}
	}
	if s.Movement != nil {
		s.Movement.Community = community.Features{GridClaimTieBreak: f.GridClaimTieBreak}
	}
	if s.Econ != nil {
		s.Econ.Community = community.Features{AIDifficultyIncome: f.AIDifficultyIncome}
	}
	// The computer players are player-indexed with nil holes: a direct indexed
	// walk, never a map range [I1]. A manager composed later takes the same
	// copy at construction (initializeBattleAI).
	for player := range s.AI {
		if s.AI[player] != nil {
			s.AI[player].Community = s.aiCommunity()
		}
	}
}

// aiCommunity is the computer player's copy of the table: the three ProTA 4.8
// package AI switches (DESIGN_COMMUNITY_PATCH §4.7).
func (s *Session) aiCommunity() community.Features {
	f := s.Community
	return community.Features{
		AIStockpileProducts: f.AIStockpileProducts, AIApplianceEnergy: f.AIApplianceEnergy,
		AIBuilderStopThreshold:  f.AIBuilderStopThreshold,
		AIBuilderPlacementLimit: f.AIBuilderPlacementLimit,
	}
}

func (s *Session) orderCommunity() community.Features {
	f := s.Community
	return community.Features{
		GuardingBuildersHold: f.GuardingBuildersHold, PatrollingBuilderFilters: f.PatrollingBuilderFilters,
		ReclaimToggleKeepsBuild: f.ReclaimToggleKeepsBuild, ConstructionKickout: f.ConstructionKickout,
		WeaponTargetKeys: f.WeaponTargetKeys, Veterancy: f.Veterancy, BuildWeaponSlotGuard: f.BuildWeaponSlotGuard,
		// The ProTA 4.8 package's order switches (DESIGN_COMMUNITY_PATCH §4.7).
		WorkingWeaponsAutonomous: f.WorkingWeaponsAutonomous, AttackSingleSlotTake: f.AttackSingleSlotTake,
		ResurrectionTextFix: f.ResurrectionTextFix,
	}
}

func (s *Session) visibilityAllied(viewer, other visibility.PlayerID) bool {
	return s != nil && s.Econ != nil && viewer < 10 && other < 10 && s.Econ.Players[viewer].Allies[other]
}

func (s *Session) visibilityOffMap(unitID uint16) bool {
	if s == nil || s.Movement == nil {
		return false
	}
	filing := s.Movement.OverlapFiling(int(unitID))
	return filing != nil && filing.Filed && filing.OffMap
}

// unitVisibilityTarget is one unit's visibility query by value; the fields
// and the hull are fillUnitVisibilityTarget's.
func unitVisibilityTarget(u *units.Unit, status uint32) visibility.Target {
	var t visibility.Target
	fillUnitVisibilityTarget(&t, u, status)
	return t
}

// PreservePreparedBuildToggle projects the bound order rule into host input.
// It reads only rule configuration, never mutable unit or visibility state.
func (s *Session) PreservePreparedBuildToggle(prepared bool, status uint8) bool {
	return s != nil && s.Rules.Orders != nil && s.Rules.Orders.PreserveBuildToggle(prepared, status, s.Community.ReclaimToggleKeepsBuild)
}
