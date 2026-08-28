package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// IsGeothermal reports whether a definition carries the geothermal flag [02 "Feature record"] [05 "Geothermal requirement"].
func IsGeothermal(def *content.FeatureDef) bool {
	if def == nil {
		return false
	}
	return def.Geothermal
}

// VentPersistsAfterBuildingRemoval is the persistence invariant: after a building that required geothermal is removed,
// the vent's Plot feature remains at its anchor cell with the geothermal flag still set [05 "Geothermal requirement"] [P1-10].
// This helper asserts that invariant for tests without mutating state.
func VentPersistsAfterBuildingRemoval(t *world.Terrain, cx, cz int, ventDef *content.FeatureDef) bool {
	if t == nil || ventDef == nil || !ventDef.Geothermal {
		return false
	}
	if cx < 0 || cz < 0 || cx >= int(t.CellW) || cz >= int(t.CellH) {
		return false
	}
	idx := cz*int(t.CellW) + cx
	if idx < 0 || idx >= len(t.Plot) {
		return false
	}
	feat := t.Plot[idx].Feature()
	if feat == world.PlotFeatureNone || feat == world.PlotFeatureFringe || feat == world.PlotFeatureVoid {
		// Might be fringe: resolve
		if res, ok := world.ResolveFeature(t.Plot, int(t.CellW), int(t.CellH), cx, cz); ok {
			feat = res
		} else {
			return false
		}
	}
	def, ok := t.FeatureDefAt(feat)
	return ok && def != nil && def.Geothermal
}

// ExtractorOverlapPolicy documents the overlap rule [05 "Terrain metal extraction"] [P1-10][P1-15].
// Two extractors may sample overlapping cells unless the placement and occupancy rules prevent the overlap.
// SampleMetal sums (cellMetal+1) at placement time once and never resamples; overlapping is allowed but yield is per-extractor.
// Placement's occupancy bits 1-2 reject any nonzero occupant other than self [04 §6.2], so overlapping extractor placement
// fails only when occupancy is stamped, not merely because metal was previously sampled.
func ExtractorOverlapPolicy() string {
	return "overlap allowed unless occupancy bits 1-2 block [04 §6.2][05 \"Terrain metal extraction\"] [P1-10]"
}
