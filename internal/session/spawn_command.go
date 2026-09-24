package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// applySpawnCommand is Nanolathe Modern policy, not a retail cheat:
// DESIGN_INTERFACE_HUD_INPUT "Modern spawn command". Validation precedes the
// ordinary allocator so rejected requests cannot consume creation RNG draws.
func (s *Session) applySpawnCommand(c HumanSpawnCommand, tick uint32) {
	if s.Gameplay.Normalize() != gameplay.Modern {
		return
	}
	say := func(text string) {
		if s.publication != nil && s.publication.events != nil {
			s.publication.events.EmitAnnounce(frame.Event{Tick: tick, StatusText: text, StatusClass: 4, AnnounceSlot: 10})
		}
	}
	if s.Catalog == nil || s.World == nil || s.Units == nil || s.Econ == nil || int(s.LocalOwner) >= len(s.Econ.Players) || !s.Econ.Players[s.LocalOwner].Exists {
		say("Cannot spawn without an active local player")
		return
	}
	def, ok := s.Catalog.Unit(c.Unit)
	if !ok || def == nil {
		say(fmt.Sprintf("Unknown unit: %s", c.Unit))
		return
	}
	x, y, z, reason := s.checkSpawnPlacement(def, c.X, c.Y, c.Z)
	if reason != "" {
		say(reason)
		return
	}
	h, err := s.Units.Create(def, s.LocalOwner, x, y, z)
	if err != nil {
		say("Cannot spawn: unit limit reached or unit assets unavailable")
		return
	}
	if s.Movement != nil {
		s.Movement.EnsureUnit(s.Units.Unit(h))
	}
	// The ordinary phase-5 observer pass and tick-end publication expose the
	// new unit. No early visibility publication and no resource grant belong here.
	say(fmt.Sprintf("Spawned %s", def.UnitName))
}

// checkSpawnPlacement is the spawn command's validation, shared with the
// Survival director (DESIGN_SURVIVAL §6.5): mission placement's structure
// snapping and height, then bounds, terrain, features, occupancy and building
// yards. It draws nothing. An empty reason admits the returned position.
func (s *Session) checkSpawnPlacement(def *content.UnitDef, px, py, pz numeric.Fixed) (x, y, z numeric.Fixed, reason string) {
	// Reuse mission placement for structure snapping and authored waterline
	// height; mobiles retain the captured terrain point [08 R-ENTRY-01 §6].
	x, y, z = missionPlacementPosition(s.World, def, mission.UnitPlacement{X: int32(px), Y: int32(py), Z: int32(pz)})
	fx, fz := world.FootprintForUnit(s.Catalog, def)
	extent, err := world.NewFootprintExtent(fx, fz)
	if err != nil {
		return 0, 0, 0, "Cannot spawn: invalid unit footprint"
	}
	cx, cz := world.PlacementAnchor(x, z, fx, fz)
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		return 0, 0, 0, "Cannot spawn: point outside the map"
	}
	rules, err := world.PlacementRulesForUnit(s.Catalog, def)
	if err != nil {
		return 0, 0, 0, "Cannot spawn: missing movement profile"
	}
	query := world.PlacementQuery{Rect: rect, Rules: rules, Mobile: def.BMCode != 0}
	if !query.Mobile {
		query.Yard, err = world.ParseYardMap(def.YardMap, int(fx), int(fz))
		if err != nil {
			return 0, 0, 0, "Cannot spawn: invalid building yard"
		}
	}
	if _, err = s.World.CheckPlacement(query); err != nil {
		return 0, 0, 0, "Cannot spawn: location blocked or unsuitable for this unit"
	}
	// The testing command conservatively excludes all existing building yards,
	// and tests occupancy/features over the whole footprint, including open
	// yard cells. This is policy, not a change to ordinary construction.
	for cz := rect.MinZ(); cz < rect.MaxZ(); cz++ {
		for cx := rect.MinX(); cx < rect.MaxX(); cx++ {
			if s.World.PlotAt(cx, cz).StructureYard() {
				return 0, 0, 0, "Cannot spawn: location overlaps a building yard"
			}
		}
	}
	if !query.Mobile {
		query.Mobile, query.Yard = true, nil
		query.SkipTerrainAggregates = true
		if _, err = s.World.CheckPlacement(query); err != nil {
			return 0, 0, 0, "Cannot spawn: location blocked"
		}
	}
	return x, y, z, ""
}
