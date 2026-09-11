package features

// ReleaseRemovedAt completes runtime teardown after an external owner has
// removed the plot footprint. Resurrection calls it synchronously so later
// same-tick stamps can use the returned arena slot [05 R-WORK-01 §7 phase 5]
// [05 R-FEAT-01 §4 step 4]. It performs no successor or animation transition.
func (s *Service) ReleaseRemovedAt(cx, cz int) {
	if s == nil || s.Terrain == nil || cx < 0 || cz < 0 || cx >= int(s.Terrain.CellW) || cz >= int(s.Terrain.CellH) {
		return
	}
	s.deleteInstance(cz*int(s.Terrain.CellW) + cx)
}
