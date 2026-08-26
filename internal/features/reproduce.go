package features

// reproduceTick handles the one-cell-per-tick walker descending from W*H-1
// with wrap-skip where cell W*H-1 is never scanned [05 "Feature catalog and placement"] [06 §13.1] [GAP T21] (I4).

func (s *Service) reproduceTick() {
	if s.Terrain == nil {
		s.LastReproIdx = -1
		return
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	total := w * h
	if total <= 1 {
		// With one cell total, W*H-1 is the only cell and never scanned.
		s.LastReproIdx = -1
		return
	}
	// Ensure cursor initialized to W*H-1 if out of range.
	if s.cursor < -1 || s.cursor >= total {
		s.cursor = total - 1
	}
	// Descending step.
	s.cursor--
	if s.cursor < 0 {
		s.cursor = total - 1
	}
	// Wrap-skip: cell W*H-1 NEVER scanned [06 §13.1].
	if s.cursor == total-1 {
		s.LastReproIdx = -1
		return
	}
	idx := s.cursor
	s.LastReproIdx = idx

	if idx < 0 || idx >= len(s.Terrain.Plot) {
		return
	}
	cell := s.Terrain.Plot[idx]

	// Eligibility: anchor <0xFFFB AND animation bit 0 clear [PLAN C25] [06 §13.1].
	// anchor <0xFFFB means real feature index [GAP T14]; IsRealFeature covers it.
	if !cell.IsRealFeature() {
		return
	}
	// Animation bit 0 clear: the plot flag byte's bit 0 — the same byte
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// (notes/features/03_environmental_tails.md). A map-authored cell with the
	// bit set must not reproduce even with no live instance, so the plot byte
	// is the source of truth; the definition's Animating flag is only the
	// fallback when the plot flag is clear.
	if cell.FlagByte()&0x01 != 0 {
		return
	}
	if inst, ok := s.instances[idx]; ok && inst != nil {
		if inst.Status&0x01 != 0 {
			return
		}
	}
	sim := s.sim()
	if sim == nil {
		return
	}
	// simRNG(100) consumed EVEN WHEN reproduce==0 (I4 draw-count rule) [03 §5.1.2] [06 §13.1].
	roll := sim.Uint32n(100)

	def, ok := s.Terrain.FeatureDefAt(cell.Feature())
	if !ok || def == nil {
		return
	}
	reproduce := def.Reproduce // integer percentage [02 "Feature record"]
	if reproduce <= 0 {
		// Draw already consumed, no spawn; this is the classic regression [I4].
		return
	}
	if int32(roll) >= reproduce {
		return
	}
	// On passing roll dx/dz = simRNG(area) − area/2 [05 "Feature catalog and placement"] [06 §13.1].
	area := def.ReproduceArea
	if area <= 0 {
		return
	}
	// One draw each [06 §13.1].
	dx := int(sim.Uint32n(uint32(area))) - int(area/2) // [05 "Feature catalog and placement"]
	dz := int(sim.Uint32n(uint32(area))) - int(area/2)
	cx := idx % w
	cz := idx / w
	tx := cx + dx
	tz := cz + dz
	if tx < 0 || tx >= w || tz < 0 || tz >= h {
		return
	}
	targetIdx := tz*w + tx
	if targetIdx < 0 || targetIdx >= len(s.Terrain.Plot) {
		return
	}
	targetCell := s.Terrain.Plot[targetIdx]
	// Target cell must be in-bounds and free (empty sentinel 0xFFFF) [06 §13.1].
	if !targetCell.IsEmpty() {
		return
	}
	// Source cell must still hold the reproducing feature [06 §13.1].
	if s.Terrain.Plot[idx].Feature() != cell.Feature() {
		return
	}
	// Spawn goes through the common feature placement helper with no
	// position/velocity override and the neutral side [06 §13.1].
	s.spawnFeatureAt(tx, tz, def)
}
