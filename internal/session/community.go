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
}

func (s *Session) orderCommunity() community.Features {
	f := s.Community
	return community.Features{
		GuardingBuildersHold: f.GuardingBuildersHold, PatrollingBuilderFilters: f.PatrollingBuilderFilters,
		ReclaimToggleKeepsBuild: f.ReclaimToggleKeepsBuild, ConstructionKickout: f.ConstructionKickout,
		WeaponTargetKeys: f.WeaponTargetKeys, Veterancy: f.Veterancy, BuildWeaponSlotGuard: f.BuildWeaponSlotGuard,
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

func unitVisibilityTarget(u *units.Unit, status uint32) visibility.Target {
	if u == nil {
		return visibility.Target{}
	}
	min, max := u.Def.BoundingExtents()
	return visibility.TargetFromBounds(visibility.Target{
		UnitID: uint16(u.Handle), Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z,
		Hidden: u.Hidden, Status: status, OriginX: u.X, OriginY: u.Y, OriginZ: u.Z,
		Flying:     u.Move.ModeMirror == 2,
		FootprintX: int32(u.CachedOccupancyX), FootprintZ: int32(u.CachedOccupancyZ),
		FootprintSizeX: int32(u.FootprintSizeX), FootprintSizeZ: int32(u.FootprintSizeZ),
	}, min, max)
}

// PreservePreparedBuildToggle projects the bound order rule into host input.
// It reads only rule configuration, never mutable unit or visibility state.
func (s *Session) PreservePreparedBuildToggle(prepared bool, status uint8) bool {
	return s != nil && s.Rules.Orders != nil && s.Rules.Orders.PreserveBuildToggle(prepared, status, s.Community.ReclaimToggleKeepsBuild)
}
