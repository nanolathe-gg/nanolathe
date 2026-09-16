// Observer publication and retirement: C2, C3, C5, C6 [PLAN_05 WU-05-2].

package visibility

// Publish publishes an observer's footprint [03 §3.2] C2-C6.
//
// Mode bit 2 selects the raster shape only; both paths write the same word mask
// and the same per-player byte refcount [C2].
func (s *Service) Publish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) {
	if s == nil || !validPlayer(owner) || s.wordMask == nil || s.W == 0 || s.H == 0 {
		return
	}
	changed := false
	visit := func(idx int) {
		// Disabled stores are prefilled by rebuild and are not publication
		// targets. This also makes the dirty result describe the selected mode.
		if s.mode.HistoryEnabled() {
			changed = s.setWordBit(idx, owner) || changed
		}
		if s.mode.CurrentEnabled() {
			changed = s.incByteGrid(idx, owner) || changed
		}
	}
	if s.mode&ModeTerrainRay != 0 {
		s.walkTerrainRay(cx, cz, heightByte, radius, visit)
	} else {
		s.walkSpriteMask(cx, cz, radius, visit)
	}
	// Only a LOCAL player's cell change clears the fog-cache-valid bit and wakes
	// the composer; remote players' changes dirty nothing [03 §3.2] C15.
	if owner == s.local && changed {
		s.invalidatePresentation()
	}
}

// Unpublish removes an observer's contribution from the byte refcount. The word
// mask never decrements — it is cleared only by a full rebuild [03 §3.2] C4.
func (s *Service) Unpublish(owner PlayerID, cx, cz int32, heightByte uint8, radius int32) bool {
	if s == nil || !validPlayer(owner) || s.wordMask == nil || s.W == 0 || s.H == 0 {
		return false
	}
	changed := false
	visit := func(idx int) {
		if s.mode.CurrentEnabled() {
			changed = s.decByteGrid(idx, owner) || changed
		}
	}
	if s.mode&ModeTerrainRay != 0 {
		s.walkTerrainRay(cx, cz, heightByte, radius, visit)
	} else {
		s.walkSpriteMask(cx, cz, radius, visit)
	}
	if owner == s.local && changed {
		s.invalidatePresentation()
	}
	return changed
}

// spriteShapeIndex quantizes a sight radius to a shape index [03 §3.2] C2 [P0-18].
//
// Common quantization q = floor(radius/32) via (s+(s>>31&0x1F))>>5 then idx=clamp(q-5,0,nsMask-1).
// The -5 is an INDEX bias, not a radius reduction: shape k covers a radius of
// k+5 tiles, so the subtraction is undone by the frame geometry. The division
// floors via signed bias (s>>31 &0x1F) which differs from truncation only for negative radii,
// where the clamp absorbs the difference but the formula is preserved for bit-exactness [P0-18].
func (s *Service) spriteShapeIndex(radius int32) int {
	n := s.shapes.Count()
	if n == 0 {
		return -1
	}
	q := int(floorDiv32(radius)) - 5 // [P0-18] q=floor(r/32) via (s+(s>>31&0x1F))>>5
	if q < 0 {
		q = 0
	}
	if q >= n {
		q = n - 1
	}
	return q
}

// rayTableIndex quantizes a sight radius to the terrain-ray table GROUP
// g = clamp(floor(radius/32), 0, numtables-1) [03 §3.2] C2 [P0-18][SC9].
//
// The table-by-index accessor is one-based, so the record read for group g is
// g-1 — and that record was filled from the section named TABLE g, because the
// loader builds each slot's section name from the slot plus one. The two
// off-by-ones cancel: group g walks TABLE g [03 R-COMP-02 §1]. walkTerrainRay
// hands g-1 to raySpokes, which indexes the compiled slot list.
func (s *Service) rayTableIndex(radius int32) int {
	n := s.rayTableCount()
	if n == 0 {
		return -1
	}
	q := int(floorDiv32(radius)) // [P0-18] q=floor(r/32) via (s+(s>>31&0x1F))>>5
	if q < 0 {
		q = 0
	}
	if q >= n {
		q = n - 1
	}
	return q
}

// floorDiv32 implements q = floor(radius/32) via (s+(s>>31&0x1F))>>5 [P0-18][03 §3.2].
func floorDiv32(s int32) int32 {
	return (s + (s >> 31 & 0x1F)) >> 5
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
	g := s.rayTableIndex(radius)
	if g < 0 {
		return
	}
	// Group g reads slot g-1, which the loader filled from the section named
	// TABLE g, so group g walks TABLE g and covers exactly g cells — the same
	// extent the sprite-mask raster's index bias produces [03 R-COMP-02 §1].
	// The surviving off-by-one is at the top: the clamp stops at numtables-1
	// while the one-based accessor would need numtables, so the last loaded
	// table (TABLE 9 with the reference install's declared nine) is unreachable,
	// as are its three undeclared sections (SC9).
	//
	// Group 0 (sightdistance < 32) reads the record 16 bytes BEFORE the table
	// list's storage in retail, and what those bytes hold at run time is still
	// Unknown [03 R-COMP-02 §1]; the doc sanctions an empty line list as a
	// stated divergence, so only the origin is admitted. That divergence is
	// unobservable on stock content: WU-19-158's census of all 278 stock
	// definitions found every one authoring a sightdistance, the smallest being
	// 55, so the lowest group any stock unit selects is 1 and group 0 is never
	// reached [03 R-COMP-02 §1]. It becomes visible only under a mod, and only
	// a retail capture of such a unit could settle what retail draws there.
	var spokes [][]step
	if g >= 1 {
		spokes = s.raySpokes(g - 1)
	}
	// Origin admitted unconditionally when the selected authored table exists
	// [C5]. A missing table is an absent asset, not a synthetic one.
	if uint32(cx) < uint32(s.W) && uint32(cz) < uint32(s.H) {
		visit(int(cz*s.W + cx))
	}
	for _, spoke := range spokes {
		retainedNum, retainedDen := int32(-1), int32(0)
		for _, step := range spoke {
			gx, gz := cx+step.dx, cz+step.dz
			// Unsigned bounds BEFORE any terrain read [C5]. A spoke that leaves
			// the map ends there — it does not resume on the far side.
			// (The attested pseudocode `continue`s; on monotone radial
			// spokes every later step is also out of bounds, so break is
			// equivalent and cheaper.)
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
// On a refresh the old footprint is removed first — current coverage must be
// enabled, and, ray branch only, the old height byte must have been nonzero;
// the sprite branch carries no such guard [03 R-VIS-01 §2] — the new origin is
// stored, and the new raster publishes. Only terrain-ray rejects an out-of-bounds
// observer cell; sprite masks clip their footprint even when the center is off-map.
//
// Publish and Unpublish remain the unconditional primitives underneath; this is
// the state machine that decides whether to call them.
func (s *Service) Refresh(id ObserverID, ob Observer) {
	if s == nil || !validPlayer(ob.Owner) || s.wordMask == nil {
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
	storedCX, storedCZ := ob.CX, ob.CZ
	storedByte := ob.HeightByte
	if !ray {
		storedCX, storedCZ = s.spriteStoredOrigin(ob.CX, ob.CZ, ob.Radius)
		storedByte = uint8(quantized)
	}

	old, had := s.footprints[id]
	if had {
		moved := old.owner != ob.Owner || old.storedCX != storedCX || old.storedCZ != storedCZ
		changed := old.storedByte != storedByte
		if ray {
			d := int32(ob.HeightByte) - int32(old.storedByte)
			if d < 0 {
				d = -d
			}
			changed = d > 5 // strictly more than 5 [C6]
		}
		if !moved && !changed {
			return // throttled even when no active contribution remains [C6]
		}
		if old.live {
			s.removeFootprint(old)
		}
	}

	next := footprint{
		owner: ob.Owner, cx: ob.CX, cz: ob.CZ, heightByte: ob.HeightByte,
		radius: ob.Radius, quantized: quantized,
		storedCX: storedCX, storedCZ: storedCZ, storedByte: storedByte,
	}
	// Only the ray branch rejects an off-map observer cell. Circular masks
	// still publish their clipped overlap [03 R-VIS-01 §2].
	if ray && (uint32(ob.CX) >= uint32(s.W) || uint32(ob.CZ) >= uint32(s.H)) {
		s.footprints[id] = next
		return
	}
	next.live = true
	s.footprints[id] = next
	s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
}

// RetireObserver removes the stored current-coverage footprint, rather than
// reconstructing one from a unit that may already have moved or changed owner.
// It returns whether local presentation was dirtied [03 §3.2].
func (s *Service) RetireObserver(id ObserverID) bool {
	if s == nil || s.footprints == nil {
		return false
	}
	old, ok := s.footprints[id]
	if !ok {
		return false
	}
	dirty := s.removeFootprint(old)
	delete(s.footprints, id)
	return dirty
}

// removeFootprint decides the removal by branch, because the stored coverage
// byte means something different in each [03 R-VIS-01 §2] "The stored coverage
// byte carries two different quantities":
//
//   - ray branch: heightByte is the emitter height byte, and retail guards its
//     removal call on storedByte != 0 — kept below.
//   - sprite branch: the "stored coverage byte" retail guards nothing on is
//     the quantized SHAPE INDEX, which this footprint keeps in `quantized`, not
//     `heightByte`; index 0 is a legitimate published shape (any sightdistance
//     below 192 clamps to it), so gating removal on `heightByte == 0` — a field
//     that is not even the sprite branch's stored byte — leaked coverage for
//     every such unit that moved. Retail's sprite removal call carries NO
//     nonzero guard at all [03 R-VIS-01 §2] "Retail edge, stated as a
//     contract"; `old.live` (already required above) is what bounds retail's
//     own unbalanced first-refresh decrement — a record that was never
//     actually published is never removed here, without reproducing that
//     decrement itself.
func (s *Service) removeFootprint(old footprint) bool {
	if s == nil || !old.live || !s.mode.CurrentEnabled() {
		return false
	}
	if s.mode&ModeTerrainRay != 0 && old.heightByte == 0 {
		return false // ray branch's storedByte != 0 guard [03 R-VIS-01 §2]
	}
	changed := s.Unpublish(old.owner, old.cx, old.cz, old.heightByte, old.radius)
	return old.owner == s.local && changed
}

// Forget drops an observer's stored footprint without touching the grids. The
// caller unpublishes first when it wants the coverage removed.
func (s *Service) Forget(id ObserverID) {
	if s != nil && s.footprints != nil {
		delete(s.footprints, id)
	}
}
