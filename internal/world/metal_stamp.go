package world

// SeedFeatureMetalDeposits is the map-load pass that turns indestructible
// metal-bearing features into the extractor economy [05 R-FEAT-01 §7].
//
// It runs once, after the uniform surface-metal seed of Terrain.ApplySchema
// and after every terrain-file and mission-file feature has been stamped. It
// walks the plot in row-major order and, for each *anchor* cell whose feature
// definition has `metal != 0` **and** `indestructible = 1`, writes the low
// byte of the truncated definition metal into every cell of that definition's
// footprint. Fringe cells are not anchors and never drive a write; the write
// itself is bounds-checked per cell, so a footprint that runs off the map
// simply loses the off-map cells.
//
// This is the only writer of the metal byte after the uniform seed. A
// reclaimable rock with a nonzero `metal` (`indestructible = 0`) does not
// seed: its metal is a reclaim reward only. Removing or replacing a feature
// later never restores or rewrites the byte, so this pass is not re-run at
// runtime — a deposit is indestructible anyway.
//
// [05 R-FEAT-01 §7] is an explicit correction to the earlier conclusion in
// [05 R-PROD-01 §6] that the canonical terrain version leaves every cell on
// the uniform seed. The bounded negative over the *terrain loader* still
// stands — the four-byte attribute record carries no metal — but this
// separate pass does write per-cell metal, and it is what makes stock
// `RockMetal*` deposits (`metal` 86..223, `indestructible=1`) worth mining.
func (t *Terrain) SeedFeatureMetalDeposits() {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return
	}
	if len(t.Plot) < int(t.CellW)*int(t.CellH) {
		return
	}
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			def, ok := t.FeatureDefAt(t.Plot[int(cz*t.CellW+cx)].Feature())
			if !ok || def == nil {
				continue
			}
			// Both conditions, not either: a reclaimable rock authored with
			// metal is reclaim reward only [05 R-FEAT-01 §7].
			if def.Metal == 0 || !def.Indestructible {
				continue
			}
			// "float -> int, low byte": the parser already truncated the
			// authored value toward zero [05 R-FEAT-01 §1], so the store is
			// the low byte of that integer.
			deposit := uint8(def.Metal)
			for dz := int32(0); dz < def.FootprintZ; dz++ {
				for dx := int32(0); dx < def.FootprintX; dx++ {
					fx, fz := cx+dx, cz+dz
					if fx < 0 || fz < 0 || fx >= t.CellW || fz >= t.CellH {
						continue // off-map cells are skipped, not clamped
					}
					t.Plot[int(fz*t.CellW+fx)].SetMetal(deposit)
				}
			}
		}
	}
}
