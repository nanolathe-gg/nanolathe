package world

import "fmt"

// ExtractorOverlapResult describes the overlap check for a pending extractor placement [05 "Terrain metal extraction"] [P1-10][P1-15].
type ExtractorOverlapResult struct {
	OverlapsExisting bool // true when any footprint cell's occupancy is nonzero (would be blocked by yard bits 1-2)
	Sample           float32
}

// CheckExtractorOverlap reports whether a new extractor at (cx,cz) would overlap an existing extractor's sampled patch [05 "Terrain metal extraction"].
// Overlap here means occupancy collision (yard bits 1-2 would reject), not merely metal-byte reuse.
// Metal sampling itself allows overlapping sums unless occupancy prevents it [05 "Terrain metal extraction"].
// Malformed/custom: zero/negative footprint is clamped to 1x1 for the check [P1-I05].
func (t *Terrain) CheckExtractorOverlap(cx, cz int32, footX, footZ int, extractsMetal float32) (ExtractorOverlapResult, error) {
	if t == nil {
		return ExtractorOverlapResult{}, fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
		return ExtractorOverlapResult{}, fmt.Errorf("world: extractor footprint out of bounds")
	}
	overlaps := false
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			cell := t.PlotAt(cx+int32(dx), cz+int32(dz))
			if cell == nil {
				continue
			}
			if cell.OccupantA() != 0 || cell.OccupantB() != 0 {
				overlaps = true
			}
		}
	}
	sample, err := t.SampleMetal(cx, cz, footX, footZ, extractsMetal)
	if err != nil {
		// Malformed: if metal not seeded, treat sample as footprint count * extractsMetal
		if extractsMetal != 0 {
			sample = float32(footX*footZ) * extractsMetal
		}
		return ExtractorOverlapResult{OverlapsExisting: overlaps, Sample: sample}, nil
	}
	return ExtractorOverlapResult{OverlapsExisting: overlaps, Sample: sample}, nil
}
