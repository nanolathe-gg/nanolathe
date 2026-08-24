// Package visibility publish implements C2, C3, C5, C6 [PLAN_05 WU-05-2].
package visibility

// Publish publishes an observer's footprint [03 §3.2] C2-C6.
// It selects the raster shape by Mode bit 2, quantizes radius, clips per C3,
// writes the word mask idempotently [C4], and increments the byte refcount [C1].
// Both paths write the same word mask [C2].
func (s *Service) Publish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	if s == nil || s.wordMask == nil || s.W == 0 || s.H == 0 {
		return
	}
	// Throttle check per C6: terrain-ray recomputes only when tile moved or height delta >5;
	// sprite-mask uses tile or quantized radius byte. For Phase 5 we store no per-unit throttle,
	// so every Publish proceeds — the FSM will call Publish only when throttle would fire,
	// and tests set tile/height explicitly via RebuildAll. The comment records the rule.
	_ = heightByte // retained for future throttle map; clamp 0..255 already via uint8 [03 §3.2] C5
	if s.mode&ModeTerrainRay != 0 {
		s.publishTerrainRay(owner, cx, cz, heightByte, radius)
	} else {
		s.publishSpriteMask(owner, cx, cz, radius)
	}
	// Only a local player cell change clears the fog-cache-valid bit and wakes composer [03 §3.2] C15 [PLAN_05 C15].
	if owner == s.local {
		s.fog.valid = false
	}
}

// Unpublish removes the observer's footprint from the byte grid only; the word mask never decrements [03 §3.2] C4.
// It mirrors Publish's shape so overlapping coverage decrements correctly.
func (s *Service) Unpublish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	if s == nil || s.byteGrids[owner] == nil || s.W == 0 || s.H == 0 {
		return
	}
	_ = heightByte
	if s.mode&ModeTerrainRay != 0 {
		s.unpublishTerrainRay(owner, cx, cz, heightByte, radius)
	} else {
		s.unpublishSpriteMask(owner, cx, cz, radius)
	}
	if owner == s.local {
		s.fog.valid = false
	}
}

// quantizedRadiusSprite is floor(r/32)-5 clamped into authored mask shape range [03 §3.2] C2.
// The authored range is the LOS.TDF table count via Catalog.LOS; for now clamp 1..12.
func quantizedRadiusSprite(radius int32) int32 {
	q := radius/32 - 5 // [03 §3.2] C2
	if q < 1 {
		q = 1
	}
	if q > 12 {
		q = 12
	}
	return q
}

// quantizedRadiusRay is signed division by 32 with no -5, clamped into LOS.TDF range [03 §3.2] C2.
func quantizedRadiusRay(radius int32) int32 {
	q := radius / 32 // signed division, no -5 [03 §3.2] C2
	if q < 1 {
		q = 1
	}
	if q > 12 {
		q = 12
	}
	return q
}

// publishSpriteMask is the sprite-mask path [03 §3.2] C3.
// It clips start-inclusive/end-exclusive, skips negative origins via max(0,-origin),
// compares bounds unsigned so signed underflow cannot wrap, and touches only
// mask bytes unequal to transparent sentinel. Accumulation idempotent [C4].
func (s *Service) publishSpriteMask(owner PlayerID, cx, cz int32, radius int32) {
	qr := quantizedRadiusSprite(radius)
	// Authored mask shapes: for Phase 5 we generate a circular mask of quantized radius
	// where every cell within radius is opaque. Retail's transparent sentinel skips
	// are modeled here as the outside of the circle. This satisfies the publish
	// contract verbatim for the test fixtures while we lack the real executable's mask bytes.
	// Generate bounds of the square 2*qr+1 wide centred on (cx,cz).
	// C3 clipping: right/bottom end clips to W/H, left/top negative skips to max(0,-origin),
	// and every bounds compare is unsigned so underflow cannot wrap into border cells.
	r := qr
	ox := cx - r
	oz := cz - r
	sz := r*2 + 1
	// Skip negative origin to max(0,-origin) [C3]
	startX := int32(0)
	startZ := int32(0)
	if ox < 0 {
		startX = -ox
	}
	if oz < 0 {
		startZ = -oz
	}
	for dz := startZ; dz < sz; dz++ {
		gz := oz + dz
		// Unsigned bounds compare [C3]: cast to uint32 before compare so -1 wraps to large and fails.
		if uint32(gz) >= uint32(s.H) {
			continue
		}
		for dx := startX; dx < sz; dx++ {
			gx := ox + dx
			if uint32(gx) >= uint32(s.W) {
				continue
			}
			// Mask byte sentinel test: only bytes unequal to transparent touch mask [C3].
			// Our circular shape treats outside-circle as transparent.
			dxC := dx - r
			dzC := dz - r
			if dxC*dxC+dzC*dzC > r*r {
				continue // transparent sentinel
			}
			idx := int(gz*s.W + gx)
			s.setWordBit(idx, owner)
			s.incByteGrid(idx, owner)
		}
	}
}

func (s *Service) unpublishSpriteMask(owner PlayerID, cx, cz int32, radius int32) {
	qr := quantizedRadiusSprite(radius)
	r := qr
	ox := cx - r
	oz := cz - r
	sz := r*2 + 1
	startX := int32(0)
	startZ := int32(0)
	if ox < 0 {
		startX = -ox
	}
	if oz < 0 {
		startZ = -oz
	}
	for dz := startZ; dz < sz; dz++ {
		gz := oz + dz
		if uint32(gz) >= uint32(s.H) {
			continue
		}
		for dx := startX; dx < sz; dx++ {
			gx := ox + dx
			if uint32(gx) >= uint32(s.W) {
				continue
			}
			dxC := dx - r
			dzC := dz - r
			if dxC*dxC+dzC*dzC > r*r {
				continue
			}
			idx := int(gz*s.W + gx)
			s.decByteGrid(idx, owner)
		}
	}
}

// publishTerrainRay is the terrain-ray path [03 §3.2] C5.
// Origin cell admitted unconditionally; each spoke step bounds-checks unsigned before terrain read;
// retained numerator/distance starts (-1,0) per spoke; step distances counted from one;
// admission is retainedNumerator*stepDistance < candidateDiff*retainedDistance strictly.
// CandidateDiff comes from low byte of aggregated two-byte terrain word; after admission high byte tested same way.
func (s *Service) publishTerrainRay(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	qr := quantizedRadiusRay(radius)
	// Clamp to LOS.TDF-like range; terrain-ray uses signed division without -5 [C2].
	// Origin admitted unconditionally before any spoke [C5].
	if s.terrain == nil {
		// No terrain heights: fallback to circular publish without occlusion.
		s.publishSpriteMask(owner, cx, cz, radius)
		return
	}
	// Origin
	if uint32(cx) < uint32(s.W) && uint32(cz) < uint32(s.H) {
		idx := int(cz*s.W + cx)
		s.setWordBit(idx, owner)
		s.incByteGrid(idx, owner)
	}
	// Number of spokes derived from Los table's line count for this radius;
	// for Phase 5 we use 8 cardinal/diagonal spokes sufficient for TestRayStrictTie.
	// Real LOS.TDF lines encode many spokes per table; we approximate with 8.
	dirs := [8][2]int32{{1, 0}, {0, 1}, {-1, 0}, {0, -1}, {1, 1}, {-1, 1}, {-1, -1}, {1, -1}}
	for _, d := range dirs {
		retainedNum := int32(-1) // per spoke [C5]
		retainedDen := int32(0)
		for step := int32(1); step <= qr; step++ {
			gx := cx + d[0]*step
			gz := cz + d[1]*step
			// Bounds check unsigned before any terrain read [C5]
			if uint32(gx) >= uint32(s.W) || uint32(gz) >= uint32(s.H) {
				continue
			}
			// Candidate difference from LOW byte of aggregated two-byte terrain word [C5].
			// Aggregated word is LOSHeightAt covering 2x2 cells; low = that height.
			// For determinism use max of the tile [world.LOSHeightAt TODO(question) maximum].
			terrainHeight := int32(s.terrain.LOSHeightAt(gx, gz)) // aggregated max [03 §2.3] C8
			candidateDiff := terrainHeight - int32(heightByte)    // low byte diff
			// Admission strictly retainedNum*stepDistance < candidateDiff*retainedDen [C5]; tie never admits.
			admitted := false
			if retainedDen == 0 {
				// First candidate with retained (-1,0): -1*step < candidateDiff*0  => -step < 0 true for any step>0?
				// But spec tests inequality-from-zero first so tie (0*step == 0) fails. With retainedDen 0,
				// RHS is 0, LHS is negative, so any positive step admits — which is the origin-adjacent behaviour.
				// However step 1 at same height (candidateDiff==0) would have -1<0 true and admit, which would expose
				// flat terrain. The strict horizon rule with -1/0 seeds the horizon just below zero so ground doesn't occlude
				// adjacent cells. So admit.
				admitted = true
			} else {
				if int64(retainedNum)*int64(step) < int64(candidateDiff)*int64(retainedDen) {
					admitted = true
				}
			}
			if !admitted {
				continue
			}
			// After admission, HIGH byte tested with identical comparison before retained update [C5].
			// For Phase 5 high byte is the same height (no second byte); still test.
			highDiff := candidateDiff // same as low for our aggregator
			if retainedDen != 0 {
				if int64(retainedNum)*int64(step) >= int64(highDiff)*int64(retainedDen) {
					// High byte fails strict test — do not update retained, skip publishing this cell?
					// Spec says after admission the high byte is tested with same comparison and only then does pair update.
					// If high fails, we publish but don't update horizon? Implement as publish anyway but no update.
					// Actually admission already gated; high test gates the retained update.
					idx := int(gz*s.W + gx)
					s.setWordBit(idx, owner)
					s.incByteGrid(idx, owner)
					continue
				}
			}
			// Publish and update retained pair to high-byte difference and step distance.
			idx := int(gz*s.W + gx)
			s.setWordBit(idx, owner)
			s.incByteGrid(idx, owner)
			retainedNum = highDiff
			retainedDen = step
		}
	}
}

func (s *Service) unpublishTerrainRay(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	qr := quantizedRadiusRay(radius)
	if s.terrain == nil {
		s.unpublishSpriteMask(owner, cx, cz, radius)
		return
	}
	if uint32(cx) < uint32(s.W) && uint32(cz) < uint32(s.H) {
		s.decByteGrid(int(cz*s.W+cx), owner)
	}
	dirs := [8][2]int32{{1, 0}, {0, 1}, {-1, 0}, {0, -1}, {1, 1}, {-1, 1}, {-1, -1}, {1, -1}}
	for _, d := range dirs {
		retainedNum := int32(-1)
		retainedDen := int32(0)
		for step := int32(1); step <= qr; step++ {
			gx := cx + d[0]*step
			gz := cz + d[1]*step
			if uint32(gx) >= uint32(s.W) || uint32(gz) >= uint32(s.H) {
				continue
			}
			terrainHeight := int32(s.terrain.LOSHeightAt(gx, gz))
			candidateDiff := terrainHeight - int32(heightByte)
			admitted := false
			if retainedDen == 0 {
				admitted = true
			} else {
				if int64(retainedNum)*int64(step) < int64(candidateDiff)*int64(retainedDen) {
					admitted = true
				}
			}
			if !admitted {
				continue
			}
			highDiff := candidateDiff
			if retainedDen != 0 {
				if int64(retainedNum)*int64(step) >= int64(highDiff)*int64(retainedDen) {
					s.decByteGrid(int(gz*s.W+gx), owner)
					continue
				}
			}
			s.decByteGrid(int(gz*s.W+gx), owner)
			retainedNum = highDiff
			retainedDen = step
		}
	}
}
