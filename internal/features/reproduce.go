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
	// Only the plot's attached-instance bit gates the source. Resting sprite
	// convenience records are not attached runtime instances [05 R-FEAT-01 §12].
	if cell.FlagByte()&0x01 != 0 {
		return
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
	// Both fields are zero-extended stored bytes. The roll comparison is
	// signed, after that extension [05 R-FEAT-01 §12].
	reproduce := int32(uint8(def.Reproduce))
	if int32(roll) >= reproduce {
		return
	}
	area := uint32(uint8(def.ReproduceArea))
	// Bounds zero and one return zero without drawing; the target test must
	// still run. X then Z preserves the shared RNG order [01 §7.3].
	dx := int(sim.Uint32n(area)) - int(area>>1)
	dz := int(sim.Uint32n(area)) - int(area>>1)
	tx := idx%w + dx
	// Retail divides by height here even on rectangular maps [05 R-FEAT-01 §12].
	tz := idx/h + dz
	if tx < 0 || tx >= w || tz < 0 || tz >= h {
		return
	}
	targetIdx := tz*w + tx
	if targetIdx < 0 || targetIdx >= len(s.Terrain.Plot) {
		return
	}
	targetCell := s.Terrain.Plot[targetIdx]
	// The source ground occupant is tested after target lookup and both
	// offset calls. A target must be exactly empty [05 R-FEAT-01 §12].
	if s.Terrain.Plot[idx].OccupantA() != 0 || !targetCell.IsEmpty() {
		return
	}
	// Spawn goes through the common feature placement helper with no
	// position/velocity override and the neutral side [06 §13.1].
	s.spawnFeatureAt(tx, tz, def)
}
