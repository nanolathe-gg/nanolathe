package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

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
