package world

import "github.com/nanolathe/nanolathe/formats"

// PlotCell is the 13-byte runtime terrain cell [03 §2.2][GAP T14][P0-17] W*H*4 → 13B typed table.
//
// Layout per [02 "Terrain file"], [GAP T14] and the runtime writer model
// (notes/terrain/01_attribute_cells.md §3.2):
//
//	byte 0x00  2  layer-A mobile occupancy short, signed LE; load zeroes it, unit
//	              stomp/unstomp stamp and clear it
//	byte 0x02  2  layer-B mobile occupancy short, signed LE; same lifecycle
//	byte 0x04  1  height byte
//	byte 0x05  1  derived floor MAX \
//	byte 0x06  1  derived floor MIN / average is the sampled floor height [02 "Terrain file"]
//	byte 0x07  1  metal content byte (SurfaceMetal) [02 "Terrain file"]
//	byte 0x08  2  feature reference u16 LE: 0xFFFF none, 0xFFFE fringe, 0xFFFD void hole, <0xFFFB real index [GAP T14]
//	byte 0x0A  1  anchor DZ (signed offset at fringe members) — relocation scales this byte by the row stride
//	byte 0x0B  1  anchor DX
//	byte 0x0C  1  flags: bit 0 live instance, bit 2 never-seen fog, bits 3-6 placer nibble [02 "Terrain file"][03 §3.3][GAP T17]
//	              bit 7 is preserved by &0xD7|0x50 but no isolated reader — TODO(T23) platform residual [P1-15].
//
// The first four bytes are typed by their runtime writers; several flag bits
// remain not fully known — keep raw and do not gate gameplay on them
// [PLAN_04 C6][PLAN_04 C13].
// This byte-array layout is the deliberate exception to INVARIANTS I13.
type PlotCell [13]byte

// Sentinel values for the feature field at byte 0x08 [GAP T14][02 "Terrain file"].
const (
	PlotFeatureNone   uint16 = 0xFFFF // empty — no feature [GAP T14]
	PlotFeatureFringe uint16 = 0xFFFE // footprint-fringe member, resolve via anchor [GAP T14]
	PlotFeatureVoid   uint16 = 0xFFFD // void hole (lava fill / map-edge strip) [GAP T14]

	// plotFeatureRealLimit is the exclusive upper bound for real feature
	// indices. Consumers test feature < 0xFFFB before dereferencing, so
	// 0xFFFB and 0xFFFC act as further void thresholds [GAP T14][02 "Terrain file"].
	plotFeatureRealLimit uint16 = 0xFFFB
)

// Height returns the terrain height byte, byte 0x04 [GAP T14][02 "Terrain file"].
func (p PlotCell) Height() uint8 { return p[4] }

// MinHeight returns the derived floor minimum at byte 0x06. Byte order is the
// runtime field convention: byte 0x05 carries the max and byte 0x06 the min
// (notes/terrain/01_attribute_cells.md §3.2 rows 5/6; slope gates read byte 5 as
// the max). TODO(question): the low two bits of hmin are masked off on init
// (&0xFC) in the writer model — reservation purpose unknown.
func (p PlotCell) MinHeight() uint8 { return p[6] }

// MaxHeight returns the derived floor maximum at byte 0x05 [GAP T14][02 "Terrain file"].
func (p PlotCell) MaxHeight() uint8 { return p[5] }

// Metal returns the metal content byte at byte 0x07 [02 "Terrain file"].
// Stored raw; placement metal extraction sums (cellMetal+1) at placement time.
func (p PlotCell) Metal() uint8 { return p[7] }

// Feature returns the feature reference at byte 0x08 as little-endian u16
// with sentinels per [GAP T14]: 0xFFFF none, 0xFFFE fringe, 0xFFFD void,
// <0xFFFB real feature-table index (0xFFFB/0xFFFC also void thresholds).
func (p PlotCell) Feature() uint16 { return uint16(p[8]) | uint16(p[9])<<8 }

// The two bytes at byte 0x0A carry three different meanings depending on what the
// cell is, and research does not agree on the first of them:
//
//   - At a FRINGE member, they locate the anchor cell. [02 "Terrain file"] reads
//     them as "signed offsets locating their anchor cell", explicitly marked
//     supported inference; [04 §6.2] instead says "the cell stores target-cell
//     coordinates and the resolver re-reads that cell's feature identifier".
//     Both readings are exposed below; the resolver uses the offset reading.
//     TODO(question): which is it? An absolute-coordinate pair cannot fit two
//     bytes on maps wider than 256 cells, which is most of the retail corpus,
//     so the offset reading is the only one that can be literally true at this
//     width — but that argument is ours, not research's.
//   - At an ANCHOR with a live instance attached, they are the instance slot
//     index.
//   - At an ANCHOR with no instance, they accumulate blast damage against hit
//     points. Attachment clears the accumulator role, so the two are never
//     simultaneous [02 "Terrain file"].
//
// Phase 5 (feature lifecycle) and phase 8 (blast accumulation) own the anchor
// roles; this package only writes the fringe role.

// AnchorDX returns the raw anchor X offset byte, byte 0x0B. Byte order is the
// runtime field convention: the relocation pass scales the byte at 0x0A by the row stride,
// which makes byte 0x0A the Z axis and byte 0x0B the X axis
// (notes/terrain/01_attribute_cells.md §3.2 rows 0xA/0xB).
func (p PlotCell) AnchorDX() uint8 { return p[0xB] }

// AnchorDZ returns the raw anchor Z offset byte, byte 0x0A [GAP T14], [02 "Terrain file"].
func (p PlotCell) AnchorDZ() uint8 { return p[0xA] }

// AnchorDXSigned returns the signed offset reading of byte 0x0B, used at fringe
// members to reach the anchor cell [02 "Terrain file"].
func (p PlotCell) AnchorDXSigned() int8 { return int8(p[0xB]) }

// AnchorDZSigned returns the signed offset reading of byte 0x0A.
func (p PlotCell) AnchorDZSigned() int8 { return int8(p[0xA]) }

// AnchorWord returns bytes 0x0A..0x0B as one little-endian u16. This is the anchor
// cell's reading: the attached instance slot index, or the accumulated blast
// damage when no instance is attached [02 "Terrain file"].
func (p PlotCell) AnchorWord() uint16 { return uint16(p[0xA]) | uint16(p[0xB])<<8 }

// SetAnchorWord writes bytes 0x0A..0x0B as one little-endian u16 [02 "Terrain file"].
func (p *PlotCell) SetAnchorWord(v uint16) {
	p[0xA] = byte(v)
	p[0xB] = byte(v >> 8)
}

// Occupied reports the live-instance present bit, byte 0x0C bit 0 [GAP T14][02 "Terrain file"].
// This is the adjudicated reading of the flag byte [03 §3.3]; see also
// FlagByte/PlacerNibble for the full byte [GAP T17].
func (p PlotCell) Occupied() bool { return p[0xC]&0x01 != 0 }

// FlagByte returns the raw flag byte, byte 0x0C [02 "Terrain file"][03 §3.3].
// Bits: 0 live instance, 1 no-build blocker, 2 never-seen fog, 3-6 placer
// nibble, 7 unobserved. The 0x04 unexplored marker is presentation state
// owned by phase 5 — this package stores the byte but never gates gameplay on it [PLAN_04 C13].
func (p PlotCell) FlagByte() uint8 { return p[0xC] }

// StructureYard reports the completed-building yard mark at flag-byte bit 1.
// The building stamp sets it on every yard cell whose control byte carries
// bit 0, independent of the current open/closed selection [04 R-COLL-01 §4].
func (p PlotCell) StructureYard() bool { return p[0xC]&0x02 != 0 }

// IsUnexplored reports the never-explored/fogged marker, byte 0x0C bit 0x04 [03 §3.3].
// Presentation-owned; do not gate simulation on this bit [PLAN_04 C13].
func (p PlotCell) IsUnexplored() bool { return p[0xC]&0x04 != 0 }

// PlacerNibble returns bits 3-6 of the flag byte, byte 0x0C (placer argument
// nibble written at stamp time) [03 §3.3][02 "Terrain file"].
// Map load stamps 10; corpse stamps pass the dying unit's player slot.
func (p PlotCell) PlacerNibble() uint8 { return (p[0xC] >> 3) & 0x0F }

// IsRealFeature reports whether the feature field holds a real feature-table
// index (< 0xFFFB) [GAP T14].
func (p PlotCell) IsRealFeature() bool { return p.Feature() < plotFeatureRealLimit }

// IsFringe reports whether the cell is a fringe member (0xFFFE) [GAP T14].
func (p PlotCell) IsFringe() bool { return p.Feature() == PlotFeatureFringe }

// IsVoid reports whether the cell is a void hole. This includes the
// enumerated 0xFFFD sentinel and the two further thresholds 0xFFFB/0xFFFC
// which consumers also treat as void [GAP T14][02 "Terrain file"].
func (p PlotCell) IsVoid() bool {
	f := p.Feature()
	return f == PlotFeatureVoid || f == 0xFFFC || f == 0xFFFB
}

// IsEmpty reports whether the cell has no feature (0xFFFF) [GAP T14].
func (p PlotCell) IsEmpty() bool { return p.Feature() == PlotFeatureNone }

// OccupantA returns the layer-A mobile occupancy short, byte 0x00, as signed
// LE — the GROUND plane. Load zeroes it; the occupancy stamper writes the
// holder's pool index there for every mode-1 mover and for the yard-selected
// cells of a building-class unit, and clears it on the way out
// [03 §2.2][04 R-COLL-01 §4]. Yard-map bits 1-2 compare against it [04 §6.2],
// and the mobile footprint validator reads this word and no other
// [04 R-COLL-01 §2].
func (p PlotCell) OccupantA() int16 { return int16(uint16(p[0]) | uint16(p[1])<<8) }

// OccupantB returns the layer-B mobile occupancy short, byte 0x02 — the AIR
// plane, written by mode-2 (airborne) movers only [03 §2.2][04 R-COLL-01 §4].
// The projectile contact test reads the ground word then this one
// [06 R-DMG-01 §7]; the occupancy/placement tests do not read it at all
// [04 R-COLL-01 §2].
func (p PlotCell) OccupantB() int16 { return int16(uint16(p[2]) | uint16(p[3])<<8) }

// SetOccupantA stamps or clears the layer-A (ground) occupancy short, byte 0x00.
func (p *PlotCell) SetOccupantA(v int16) {
	u := uint16(v)
	p[0] = byte(u)
	p[1] = byte(u >> 8)
}

// SetOccupantB stamps or clears the layer-B (air) occupancy short, byte 0x02.
func (p *PlotCell) SetOccupantB(v int16) {
	u := uint16(v)
	p[2] = byte(u)
	p[3] = byte(u >> 8)
}

// RawUnknown returns bytes 0x02..0x03 as bytes. They are the air
// occupancy word, not an unknown pair — [03 §2.2] types the first four bytes as
// "two uint16 mobile planes" and [04 R-COLL-01 §4] names their writers. Read
// them through OccupantB; this accessor stays for callers that want the raw
// pair and must not be used to zero the word.
func (p PlotCell) RawUnknown() [2]byte { return [2]byte{p[2], p[3]} }

// SetHeight sets the height byte, byte 0x04.
func (p *PlotCell) SetHeight(v uint8) { p[4] = v }

// SetMinHeight sets the derived floor minimum, byte 0x06.
func (p *PlotCell) SetMinHeight(v uint8) { p[6] = v }

// SetMaxHeight sets the derived floor maximum, byte 0x05.
func (p *PlotCell) SetMaxHeight(v uint8) { p[5] = v }

// SetMetal sets the metal content byte, byte 0x07.
func (p *PlotCell) SetMetal(v uint8) { p[7] = v }

// SetFeature sets the feature reference at byte 0x08 (little-endian) with
// sentinels per [GAP T14]. All values including 0xFFFF/0xFFFE/0xFFFD
// round-trip verbatim [PLAN_04 C6].
func (p *PlotCell) SetFeature(v uint16) {
	p[8] = byte(v)
	p[9] = byte(v >> 8)
}

// SetAnchor sets the raw anchor offset bytes 0x0A/0x0B [GAP T14].
func (p *PlotCell) SetAnchor(dx, dz uint8) {
	p[0xA] = dx
	p[0xB] = dz
}

// SetAnchorSigned sets the anchor offsets at bytes 0x0A/0x0B as signed int8
// deltas to the anchor cell, the supported-inference interpretation at
// fringe members [02 "Terrain file"].
func (p *PlotCell) SetAnchorSigned(dx, dz int8) {
	// Byte order is the runtime field convention: byte 0x0A carries DZ (the relocation pass
	// scales it by the row stride) and byte 0x0B carries DX
	// (notes/terrain/01_attribute_cells.md §3.2 rows 0xA/0xB).
	p[0xA] = uint8(dz)
	p[0xB] = uint8(dx)
}

// SetOccupied sets or clears the live-instance bit, byte 0x0C bit 0 [GAP T14].
func (p *PlotCell) SetOccupied(v bool) {
	if v {
		p[0xC] |= 0x01
	} else {
		p[0xC] &^= 0x01
	}
}

// SetStructureYard updates only the completed-building yard mark, preserving
// live-instance, fog, placer, and residual flag bits [04 R-COLL-01 §4].
func (p *PlotCell) SetStructureYard(v bool) {
	if v {
		p[0xC] |= 0x02
	} else {
		p[0xC] &^= 0x02
	}
}

// SetFlagByte overwrites the entire flag byte, byte 0x0C. Caller must preserve
// the unexplored/placer semantics if needed [03 §3.3][GAP T17].
func (p *PlotCell) SetFlagByte(v uint8) { p[0xC] = v }

// ResolveFeature resolves the feature-table index for the cell at (cx,cz)
// in a row-major plot of dimensions cellW x cellH.
//
//   - If the cell holds a real index (<0xFFFB) it is returned directly.
//   - If it holds the fringe sentinel (0xFFFE) the signed anchor offsets
//     (the cell's byte 0xB for X and byte 0xA for Z in the field convention
//     [P1-07 §2.2], typed as AnchorDX 0xB / AnchorDZ 0xA with Z scaled by row
//     stride) are followed to the anchor cell, whose feature is returned when
//     that anchor holds a real index. Offsets are signed i8 [P1-07 §2.2]
//     [02 "Terrain file"].
//   - Otherwise (none/void/threshold or out-of-bounds anchor) it reports not found.
//
// This mirrors the retail indirection where fringe cells carry no index
// themselves and are resolved through the anchor cell [GAP T14][02 "Terrain file"].
// Floor quant is >>4 for AOE tile quant with floor bias and >>20 for world→cell
// (1<<20 =16*65536) [P1-07 §4].
func ResolveFeature(plot []PlotCell, cellW, cellH int, cx, cz int) (uint16, bool) {
	if cellW <= 0 || cellH <= 0 {
		return 0, false
	}
	if cx < 0 || cx >= cellW || cz < 0 || cz >= cellH {
		return 0, false
	}
	if len(plot) < cellW*cellH {
		return 0, false
	}
	idx := cz*cellW + cx
	f := plot[idx].Feature()
	if f < plotFeatureRealLimit {
		return f, true
	}
	if f == PlotFeatureFringe {
		dx := int(plot[idx].AnchorDXSigned())
		dz := int(plot[idx].AnchorDZSigned())
		ax := cx + dx
		az := cz + dz
		if ax < 0 || ax >= cellW || az < 0 || az >= cellH {
			return 0, false
		}
		aIdx := az*cellW + ax
		af := plot[aIdx].Feature()
		if af < plotFeatureRealLimit {
			return af, true
		}
	}
	return 0, false
}

// ExpandPlot builds the row-major plot grid from a TNT attribute slice
// [03 §2.2], [02 "Terrain file"], [GAP T14].
//
// This is the ONLY expansion path. Terrain.Load calls it; nothing else may
// hand-write plot bytes, because a second path is how the derived fields ended
// up unpopulated in the first place.
//
// Preserved verbatim from the source cell: the height byte (byte 0x04) and the
// feature reference (byte 0x08), including every sentinel.
//
// Derived here: the floor pair at bytes 0x05/0x06 (see deriveFloorPair). Left for
// later passes, which own the data this package does not have: the metal byte
// at byte 0x07 (Terrain.ApplySchema, from the mission's uniform SurfaceMetal
// [05 "Terrain metal extraction"]) and the fringe anchor offsets at
// bytes 0x0A/0x0B (the source-order feature stamper in feature_stamp.go, which
// needs catalog footprints).
//
// The occupancy shorts at bytes 0x00/0x02 are load-zeroed (unit stomp/unstomp
// stamp them at runtime). The flag byte (byte 0x0C) is stamped with placer value
// 10 in the nibble — `&0xd7|0x50` per cell, preserving bits 0,1,2 and 7
// [03 §3.3]; runtime writer model. The fog bit stays clear;
// unexplored marking is phase-5/composer state (PLAN_04 C13).
func ExpandPlot(attrs []formats.TNTAttribute, cellW, cellH int) []PlotCell {
	if cellW <= 0 || cellH <= 0 {
		return nil
	}
	plot := make([]PlotCell, cellW*cellH)
	limit := len(plot)
	if len(attrs) < limit {
		limit = len(attrs)
	}
	for i := 0; i < limit; i++ {
		plot[i][4] = attrs[i].Height
		plot[i].SetFeature(attrs[i].Feature)
		// Legacy (0x1020) records carry a per-cell metal seed in attribute
		// byte 6, copied to byte 0x07 here; canonical records have no metal byte
		// (zero), and the uniform SurfaceMetal write happens in
		// Terrain.ApplySchema [02 "Terrain file"].
		plot[i][7] = attrs[i].Metal
		// Map load stamps placer value 10 into the flag byte's nibble,
		// preserving bits 0,1,2,7: `&0xd7|0x50` [03 §3.3]; runtime writer
		// model.
		plot[i][0xC] = plot[i][0xC]&0xd7 | 0x50
		// attrs[i].Unknown is byte 3 of the source cell: zero in all cells of
		// all retail maps, meaning unknown [fmt tnt]. Not carried across.
	}
	deriveFloorPair(plot, cellW, cellH)
	return plot
}

// deriveFloorPair fills the derived local minimum and maximum, bytes 0x05/0x06,
// whose average is the sampled floor height [02 "Terrain file"], [03 §2.3].
//
// TODO(question): research establishes that the pair exists, that it is
// derived at load, and that CoarseHeightAt averages it — but not the
// derivation rule. The reading used here follows from the source format: a
// TNT height byte is "the height *of the cell's corner*" [fmt tnt], so the
// cell's floor spans its four corners, and its local minimum and maximum are
// the min and max of those four samples. That makes the average track the
// bilinear query of [03 §2.3], which is what "their average is the sampled
// floor height" implies. Corners past the last row/column clamp inward.
func deriveFloorPair(plot []PlotCell, cellW, cellH int) {
	at := func(x, z int) uint8 {
		if x >= cellW {
			x = cellW - 1
		}
		if z >= cellH {
			z = cellH - 1
		}
		return plot[z*cellW+x][4]
	}
	for z := 0; z < cellH; z++ {
		for x := 0; x < cellW; x++ {
			lo := at(x, z)
			hi := lo
			for _, h := range [3]uint8{at(x+1, z), at(x, z+1), at(x+1, z+1)} {
				if h < lo {
					lo = h
				}
				if h > hi {
					hi = h
				}
			}
			cell := &plot[z*cellW+x]
			// Byte order is the runtime field convention: byte 0x05 carries the max,
			// byte 0x06 the min (notes/terrain/01_attribute_cells.md §3.2).
			cell[5] = hi
			cell[6] = lo
		}
	}
}
