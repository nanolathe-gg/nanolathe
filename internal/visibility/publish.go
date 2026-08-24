// Package visibility publish implements C2, C3, C5, C6 [PLAN_05 WU-05-2].
package visibility

// Publish publishes an observer's footprint [03 §3.2] C2-C6.
//
// Mode bit 2 selects the raster shape only; both paths write the same word mask
// and the same per-player byte refcount [C2].
func (s *Service) Publish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	if s == nil || s.wordMask == nil || s.W == 0 || s.H == 0 {
		return
	}
	if s.mode&ModeTerrainRay != 0 {
		s.walkTerrainRay(cx, cz, heightByte, radius, func(idx int) {
			s.setWordBit(idx, owner)
			s.incByteGrid(idx, owner)
		})
	} else {
		s.walkSpriteMask(cx, cz, radius, func(idx int) {
			s.setWordBit(idx, owner)
			s.incByteGrid(idx, owner)
		})
	}
	// Only a LOCAL player's cell change clears the fog-cache-valid bit and wakes
	// the composer; remote players' changes dirty nothing [03 §3.2] C15.
	if owner == s.local {
		s.fog.valid = false
	}
}

// Unpublish removes an observer's contribution from the byte refcount. The word
// mask never decrements — it is cleared only by a full rebuild [03 §3.2] C4.
func (s *Service) Unpublish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	if s == nil || s.byteGrids[owner] == nil || s.W == 0 || s.H == 0 {
		return
	}
	if s.mode&ModeTerrainRay != 0 {
		s.walkTerrainRay(cx, cz, heightByte, radius, func(idx int) { s.decByteGrid(idx, owner) })
	} else {
		s.walkSpriteMask(cx, cz, radius, func(idx int) { s.decByteGrid(idx, owner) })
	}
	if owner == s.local {
		s.fog.valid = false
	}
}

// spriteShapeIndex quantizes a sight radius to a shape index [03 §3.2] C2.
//
// The -5 is an INDEX bias, not a radius reduction: shape k covers a radius of
// k+5 tiles, so the subtraction is undone by the frame geometry. Reading the
// result as a radius shrinks every unit's sight by five tiles.
func (s *Service) spriteShapeIndex(radius int32) int {
	n := s.shapes.Count()
	if n == 0 {
		return -1
	}
	q := int(radius/32) - 5
	if q < 0 {
		q = 0
	}
	if q >= n {
		q = n - 1
	}
	return q
}

// rayTableIndex quantizes a sight radius to a LOS.TDF table index [03 §3.2] C2.
// The terrain-ray path divides by 32 WITHOUT the -5 and clamps into the parsed
// table range.
func (s *Service) rayTableIndex(radius int32) int {
	n := s.rayTableCount()
	if n == 0 {
		return -1
	}
	q := int(radius / 32)
	if q < 0 {
		q = 0
	}
	if q >= n {
		q = n - 1
	}
	return q
}

// walkSpriteMask visits every covered cell of the authored shape [03 §3.2] C3.
//
// Clipping is start-inclusive/end-exclusive: right and bottom ends clip to the
// half-resolution grid, negative left and top origins skip to max(0, -origin),
// every bounds compare is unsigned so signed underflow cannot wrap into border
// cells, and only opaque mask bytes touch the grids.
func (s *Service) walkSpriteMask(cx, cz, radius int32, visit func(idx int)) {
	i := s.spriteShapeIndex(radius)
	shape := s.shapes.Shape(i)
	if shape == nil {
		// No authored shape table: publish nothing rather than synthesize a
		// footprint. A fabricated circle is worse than an absent one — it is
		// indistinguishable from real coverage at every call site.
		return
	}
	ox := cx - shape.AnchorX
	oz := cz - shape.AnchorY
	startX, startZ := int32(0), int32(0)
	if ox < 0 {
		startX = -ox // skip to max(0, -origin) [C3]
	}
	if oz < 0 {
		startZ = -oz
	}
	for dz := startZ; dz < shape.H; dz++ {
		gz := oz + dz
		if uint32(gz) >= uint32(s.H) { // unsigned compare [C3]
			continue
		}
		for dx := startX; dx < shape.W; dx++ {
			gx := ox + dx
			if uint32(gx) >= uint32(s.W) {
				continue
			}
			if !shape.Covers(dx, dz) { // transparent sentinel [C3]
				continue
			}
			visit(int(gz*s.W + gx))
		}
	}
}

// walkTerrainRay visits every cell the horizon rule admits [03 §3.2] C5.
//
// The origin cell is admitted unconditionally before any spoke is walked. Each
// spoke step bounds-checks unsigned BEFORE any terrain read, and is admitted
// only when its height-relative slope strictly exceeds the retained horizon —
// equality fails. The retained numerator/distance pair starts at (-1, 0) per
// spoke and step distances count from one:
//
//	admit iff retainedNumerator*stepDistance < candidateDiff*retainedDistance
//
// The candidate difference comes from the LOW byte of the aggregated two-byte
// terrain word. After admission the HIGH byte is tested with the identical
// comparison against the same retained pair, and only then does the pair become
// that high-byte difference and step distance.
func (s *Service) walkTerrainRay(cx, cz int32, heightByte uint8, radius int32, visit func(idx int)) {
	if s.terrain == nil {
		return
	}
	spokes := s.raySpokes(s.rayTableIndex(radius))
	if len(spokes) == 0 {
		return
	}
	// Origin admitted unconditionally [C5].
	if uint32(cx) < uint32(s.W) && uint32(cz) < uint32(s.H) {
		visit(int(cz*s.W + cx))
	}
	for _, spoke := range spokes {
		retainedNum, retainedDen := int32(-1), int32(0)
		for _, step := range spoke {
			gx, gz := cx+step.dx, cz+step.dz
			// Unsigned bounds BEFORE any terrain read [C5]. A spoke that leaves
			// the map ends there — it does not resume on the far side.
			if uint32(gx) >= uint32(s.W) || uint32(gz) >= uint32(s.H) {
				break
			}
			lo, hi := s.terrain.LOSHeightWord(gx, gz)
			candidateDiff := int32(lo) - int32(heightByte)
			// Strictly greater; an exact tie never admits [C5]. With the
			// retained pair still (-1, 0) the right side is zero and the left
			// is negative, so the first candidate on each spoke admits.
			if int64(retainedNum)*int64(step.dist) >= int64(candidateDiff)*int64(retainedDen) {
				continue
			}
			visit(int(gz*s.W + gx))
			// The high byte gates the horizon UPDATE, not the admission [C5].
			highDiff := int32(hi) - int32(heightByte)
			if int64(retainedNum)*int64(step.dist) < int64(highDiff)*int64(retainedDen) {
				retainedNum, retainedDen = highDiff, step.dist
			}
		}
	}
}

// Refresh is the throttled per-observer entry point [03 §3.2] C6.
//
// Nothing is recomputed unless something the raster depends on moved. In
// terrain-ray mode that is: coverage tile X changed, tile Y changed, or the
// observer height byte moved by MORE than 5. Sprite-mask mode substitutes a
// changed quantized-radius byte for the height test.
//
// On a refresh the old footprint is removed first (only when current coverage
// is enabled and the old height byte was nonzero), the new origin is stored,
// an out-of-bounds new origin stores an empty footprint and returns, and only
// then does the new raster publish.
//
// Publish and Unpublish remain the unconditional primitives underneath; this is
// the state machine that decides whether to call them.
func (s *Service) Refresh(id ObserverID, ob Observer) {
	if s == nil || s.wordMask == nil {
		return
	}
	if s.footprints == nil {
		s.footprints = make(map[ObserverID]footprint)
	}
	ray := s.mode&ModeTerrainRay != 0
	quantized := int32(s.spriteShapeIndex(ob.Radius))
	if ray {
		quantized = int32(s.rayTableIndex(ob.Radius))
	}

	old, had := s.footprints[id]
	if had && old.live {
		moved := old.cx != ob.CX || old.cz != ob.CZ
		var changed bool
		if ray {
			d := int32(ob.HeightByte) - int32(old.heightByte)
			if d < 0 {
				d = -d
			}
			changed = d > 5 // strictly more than 5 [C6]
		} else {
			changed = old.quantized != quantized
		}
		if !moved && !changed {
			return // throttled: nothing recomputed [C6]
		}
		// Remove the old current-coverage footprint, gated exactly as [C6]
		// states: only when current coverage is enabled and the stored height
		// byte was nonzero.
		if s.mode&ModeCurrentEnabled != 0 && old.heightByte != 0 {
			s.Unpublish(old.owner, old.cx, old.cz, old.heightByte, old.radius)
		}
	}

	next := footprint{
		owner:      ob.Owner,
		cx:         ob.CX,
		cz:         ob.CZ,
		heightByte: ob.HeightByte,
		radius:     ob.Radius,
		quantized:  quantized,
	}
	// An out-of-bounds origin stores an empty footprint and returns [C6].
	if uint32(ob.CX) >= uint32(s.W) || uint32(ob.CZ) >= uint32(s.H) {
		s.footprints[id] = next
		return
	}
	next.live = true
	s.footprints[id] = next
	s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
}

// Forget drops an observer's stored footprint without touching the grids. The
// caller unpublishes first when it wants the coverage removed.
func (s *Service) Forget(id ObserverID) {
	if s != nil && s.footprints != nil {
		delete(s.footprints, id)
	}
}
