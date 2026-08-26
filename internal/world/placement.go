// Package world provides placement validation and yard-map handling.
//
// Yard-map control bytes and geothermal/metal contracts are per
// [04 §6.2], [05 "Terrain metal extraction"], [05 "Geothermal requirement"] and [GAP T15].
package world

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
)

// YardCell is a yard-map control byte per [04 §6.2] C10 [GAP T15].
//
// Ten control bytes are exactly:
//
//	'.' 0x00  'C' 0x35  'G' 0x8f  'O' 0x2b  'Y' 0x31
//	'c' 0x2d  'f' 0x6f  'o' 0x2f  'w' 0x37  'y' 0x29
//
// Per-cell bits drive visibility (bit0), occupancy (bits1-2), slope (bit3),
// height (bit4), feature-free (bit5), blocked-class (bit6), and geothermal
// requirement (bit7) [04 §6.2][05 "Geothermal requirement"].
type YardCell uint8

// ParseYardMap fills a footX*footZ row-major yard buffer from an authored
// YardMap string, exactly as retail's definition compiler does [04 §6.2] C10.
//
// The retail loop walks the footprint cell by cell and the string character by
// character, and the two walks are not required to keep step:
//
//   - A character outside the ten-entry table advances the string without
//     consuming a cell. That is how the spaces stock authors use to lay a yard
//     map out in rows disappear, and it also silently drops typos.
//   - The string pointer advances only when the *next* character is not the
//     terminator, so once the string runs out the final character repeats for
//     every cell still unfilled. `YardMap=o` over a 4x4 footprint is sixteen
//     `o` cells, and ARMSILO's nine characters over 5x5 fill the remaining
//     sixteen with its last character.
//   - Characters past the last cell are never read. ARMSOLAR authors 27
//     characters for a 5x5 footprint; the trailing two are ignored.
//
// Forty-six of the 126 stock yard maps disagree with their own footprint, so
// rejecting a length mismatch — as this parser used to — makes those buildings
// unplaceable. There is no error case left but a degenerate footprint: an empty
// string yields an all-`.` buffer rather than reading past the terminator the
// way retail does.
//
// Non-building classes carry no yard map at all: retail parses this only when
// the definition's BMcode is zero [04 §6.2].
func ParseYardMap(s string, footX, footZ int) ([]YardCell, error) {
	if footX <= 0 || footZ <= 0 {
		return nil, fmt.Errorf("world: invalid footprint %dx%d [04 §6.2]", footX, footZ)
	}
	out := make([]YardCell, footX*footZ)
	src := []byte(s)
	at := 0
	for cell := range out {
		// Skip anything the table does not name, then take the character.
		var b YardCell
		for {
			if at >= len(src) {
				// Only reachable from an empty or wholly unusable string;
				// retail would run off the end of its buffer here.
				b = 0x00
				break
			}
			v, ok := yardControlByte(src[at])
			if ok {
				b = v
				// Park on the last character so it repeats for the rest.
				if at+1 < len(src) {
					at++
				}
				break
			}
			at++
		}
		out[cell] = b
	}
	return out, nil
}

// yardControlByte maps one authored yard-map character to its control byte
// [04 §6.2] C10. The second result is false for every character outside the
// ten-entry table, which retail skips rather than rejecting.
func yardControlByte(ch byte) (YardCell, bool) {
	switch ch {
	case '.':
		return 0x00, true // [04 §6.2] C10
	case 'C':
		return 0x35, true // [04 §6.2] C10
	case 'G':
		return 0x8f, true // [04 §6.2] C10 bit7 geothermal [05 "Geothermal requirement"]
	case 'O':
		return 0x2b, true // [04 §6.2] C10
	case 'Y':
		return 0x31, true // [04 §6.2] C10
	case 'c':
		return 0x2d, true // [04 §6.2] C10
	case 'f':
		return 0x6f, true // [04 §6.2] C10
	case 'o':
		return 0x2f, true // [04 §6.2] C10
	case 'w':
		return 0x37, true // [04 §6.2] C10
	case 'y':
		return 0x29, true // [04 §6.2] C10
	}
	return 0, false
}

// featureClass is how the footprint validator sees one covered cell's feature
// reference, after the fringe hop [04 §6.2].
type featureClass uint8

const (
	// featureEmpty is the empty sentinel: nothing occupies the cell. It also
	// covers a fringe cell whose anchor hop leads nowhere — retail's hop reads
	// the anchor's reference and, finding no real feature there, falls out of
	// every one of the three feature branches with a zero result, exactly as an
	// empty cell does.
	featureEmpty featureClass = iota
	// featureReal is a bounds-checked index that binds to a catalog definition.
	featureReal
	// featureVoid is a reference that resolves to no definition but still
	// occupies: the three reserved sentinels just above the real band, and an
	// index past the end of the feature catalog. Both take retail's "blocking"
	// answer for bit 5 without ever reaching a definition, so bits 6 and 7 —
	// which read flags off a definition — treat them as absent.
	featureVoid
)

// classifyCell resolves the feature reference covering (cx,cz) [04 §6.2]:
//
//	"the empty sentinel resolves empty; identifiers below the sentinel band are
//	real and bounds-checked against the catalog (out-of-range behaves as
//	blocking for bit 5 and non-satisfying for bit 7); the three reserved
//	sentinels just above the real band behave as occupied; and the multi-cell
//	successor sentinel follows the successor hop"
//
// This is the single resolver. Placement used to carry three more copies of the
// hop with subtly different out-of-bounds behaviour.
func (t *Terrain) classifyCell(cx, cz int32) (featureClass, *content.FeatureDef) {
	if t == nil || t.Plot == nil {
		return featureEmpty, nil
	}
	feature, ok := ResolveFeature(t.Plot, int(t.CellW), int(t.CellH), int(cx), int(cz))
	if ok {
		if def, bound := t.FeatureDefAt(feature); bound {
			return featureReal, def
		}
		// A real index that does not bind is the out-of-range case.
		return featureVoid, nil
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil || cell.IsEmpty() || cell.IsFringe() {
		// An unresolved fringe cell is the dead hop: not occupied.
		return featureEmpty, nil
	}
	return featureVoid, nil
}

// ValidatePlacement checks a building placement at cell (cx,cz) against the
// terrain, using the unit's yard map and footprint rectangle [04 §6.2],
// [05 "Geothermal requirement"] [P1-10][P1-15].
//
// The validator bounds-checks the rectangle first, then applies the yard byte's
// per-cell bits. Implemented here:
//
//	bit 1-2  reject any nonzero occupant other than the passed self identity
//	bit 5    the cell must be free of blocking features
//	bit 6    fail when the resolved feature is not reclaimable
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// Geothermal is the documented rule: "If any covered cell's yard byte has bit 7
// set, validation succeeds only when at least one covered cell holds a feature
// whose catalog entry carries the geothermal flag." An unresolvable reference
// does not satisfy it [04 §6.2], [05 "Geothermal requirement"] [P1-10 at-least-one].
// Persistence is read-only: vent remains in grid under plant and leaves grid on destruction with no restore [P1-10].
//
// Outside map rectangle: generic placement blocked (return error), mode 2 factory pad search passes [P1-15].
// Use ValidatePlacementWithMode for mode-discriminated call.
//
// Not implemented, each with its own TODO below: bit 0 (enemy-visibility
// occupancy), bit 3 (slope sampling), bit 4 (height tracking), and the
// slope/height/water checks a satisfied geothermal requirement passes through
// to. Mobile products use a different, inline terrain loop entirely [04 §6.2];
// that path belongs to movement, not here.
//
// self is the placing unit's identity, or 0 during construction, where any
// occupant rejects.
func (t *Terrain) ValidatePlacement(cx, cz int32, yard []YardCell, footX, footZ int, self uint16) error {
	return t.ValidatePlacementWithMode(cx, cz, yard, footX, footZ, self, 0)
}

// ValidatePlacementWithMode is the mode-discriminated validator [P1-15].
// mode==2 is the factory exit pad search fallback where OOB returns pass (1) instead of blocked (0) [P1-15].
// Generic mode (0) returns blocked for OOB. Yard and geothermal rules are identical in both modes.
func (t *Terrain) ValidatePlacementWithMode(cx, cz int32, yard []YardCell, footX, footZ int, self uint16, mode int) error {
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if len(yard) != footX*footZ {
		return fmt.Errorf("world: yard length %d != footprint %dx%d=%d", len(yard), footX, footZ, footX*footZ)
	}
	// The validator bounds-checks the rectangle against the map first [04 §6.2][P1-15].
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
		if mode == 2 {
			return nil // mode 2 factory pass allows OOB as fallback [P1-15]
		}
		return fmt.Errorf("world: placement %d,%d %dx%d out of bounds %dx%d", cx, cz, footX, footZ, t.CellW, t.CellH)
	}
	if t.Plot == nil || len(t.Plot) < int(t.CellW*t.CellH) {
		return fmt.Errorf("world: terrain plot not initialized")
	}

	geothermalNeeded := false
	geothermalFound := false
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			y := yard[dz*footX+dx]
			px, pz := cx+int32(dx), cz+int32(dz)
			cell := t.PlotAt(px, pz)
			if cell == nil {
				return fmt.Errorf("world: plot cell %d,%d out of range", px, pz)
			}
			class, def := t.classifyCell(px, pz)

			// TODO(question): bit 0 gates an enemy-visibility occupancy test
			// [04 §6.2]. It needs the placing player's visibility state, which
			// phase 5 owns; this package has no player context.

			// Bits 1-2: reject any nonzero mobile occupant other than the
			// requester [04 §6.2]. The occupants live in the layer-A/B
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// stomp/unstomp (notes/terrain/01_attribute_cells.md §3.2 rows
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): which of bit 1 / bit 2 maps to layer A vs B is
			// not established; both layers reject until it is.
			if y&0x06 != 0 {
				for _, occ := range [2]int16{cell.OccupantA(), cell.OccupantB()} {
					if occ != 0 && uint16(occ) != self {
						return fmt.Errorf("world: cell %d,%d occupied [04 §6.2]", px, pz)
					}
				}
			}

			// Bits 3 and 4 accumulate the height aggregates that feed the
			// slope/height/water gate [05 "Geothermal requirement"]. The
			// aggregation is in SiteHeight, which is what the ghost and the
			// order need; the gate itself is not run here.
			//
			// TODO(question): the gate compares against the *movement
			// profile's* MaxSlope, MaxWaterDepth and MinWaterDepth, copied into
			// the definition at compile time from the named movementclass or,
			// for a class-less building, from a profile built out of the
			// definition's own authored keys. The unit catalog does not carry
			// that fallback profile yet, so the limits are unavailable for the
			// building classes this validator serves. A gate fed with zeroes
			// would reject sites retail accepts, which is strictly worse than
			// not running it: omitting it can only ever be more permissive.

			// Bit 5: the cell must be free of blocking features [04 §6.2].
			// "Blocking" is the feature definition's own authored flag, not the
			// mere presence of a feature: the validator resolves the reference
			// to a definition and reads its blocking bit. Trees and wreckage
			// carry it; metal patches, which are 3x3 features sitting exactly
			// where an extractor wants to go, do not — and a mex whose yard map
			// is all `o` is unplaceable on its own deposit if presence alone
			// blocks. A reference that occupies without resolving keeps the
			// blocking answer.
			if y&0x20 != 0 {
				switch class {
				case featureReal:
					if def.Blocking {
						return fmt.Errorf("world: cell %d,%d blocked by feature %s [04 §6.2]", px, pz, def.CanonicalKey)
					}
				case featureVoid:
					return fmt.Errorf("world: cell %d,%d holds an occupied feature sentinel [04 §6.2]", px, pz)
				}
			}

			// Bit 6: fail when the resolved feature is indestructible
			// [04 §6.2]. The validator reads the high byte of the definition's
			// flag word and tests its bit 1, which is the word's bit 9 —
			// `indestructible`, not `reclaimable`. A reference that resolves to
			// no definition reaches no flag and so does not fail here.
			if y&0x40 != 0 && class == featureReal && def.Indestructible {
				return fmt.Errorf("world: cell %d,%d holds an indestructible feature %s [04 §6.2]", px, pz, def.CanonicalKey)
			}

			// Bit 7: the geothermal requirement [05 "Geothermal requirement"].
			// Retail resolves the feature only inside this branch, so a vent
			// under a cell whose yard byte is not `G` satisfies nothing.
			if y&0x80 != 0 {
				geothermalNeeded = true
				if class == featureReal && def.Geothermal {
					geothermalFound = true
				}
			}
		}
	}
	if geothermalNeeded && !geothermalFound {
		return fmt.Errorf("world: geothermal requirement not satisfied [05 %q]", "Geothermal requirement")
	}
	return nil
}

// SiteHeight returns the ground height retail draws a build site at and stores
// as the MOBILEBUILD order's Y [07 §9][04 §6.2].
//
// The footprint validator tracks two aggregates while it walks the yard map,
// over exactly those cells whose yard byte carries bit 3 — the bit research
// names as enabling slope sampling: the minimum of the cell's low height and
// the maximum of its high height. When at least one cell carried the bit, the
// site height is that minimum. When none did, the aggregates are still at their
// initial 255 and 0, the maximum compares below the minimum, and retail falls
// back to `SeaLevel - waterline` instead.
//
// The same two aggregates also feed the slope and water gates that can reject a
// site outright [05 "Geothermal requirement"]. Those gates are not implemented;
// see the bit 3/4 note in ValidatePlacementWithMode for why. Height derivation
// is independent of them and is what the ghost and the order need, so it is
// separated out here.
func (t *Terrain) SiteHeight(cx, cz int32, yard []YardCell, footX, footZ int, waterline int32) int32 {
	if t == nil {
		return 0
	}
	sea := int32(t.SeaLevel)
	if footX <= 0 || footZ <= 0 || len(yard) != footX*footZ {
		return sea
	}
	minLow, maxHigh := int32(255), int32(0)
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			if yard[dz*footX+dx]&0x08 == 0 {
				continue
			}
			cell := t.PlotAt(cx+int32(dx), cz+int32(dz))
			if cell == nil {
				continue
			}
			if h := int32(cell.MinHeight()); h < minLow {
				minLow = h
			}
			if h := int32(cell.MaxHeight()); h > maxHigh {
				maxHigh = h
			}
		}
	}
	if maxHigh < minLow { // no cell carried bit 3
		return sea - waterline
	}
	return minLow
}

// SampleMetal computes the metal a placed extractor samples from its footprint
// [05 "Terrain metal extraction"] [P1-10][P1-15]:
//
//	sampled metal = extracts-metal multiplier x sum(cell metal byte + 1)
//
// Every cell contributes at least one, so a zero-metal cell still adds one. The
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// and never resampled [P1-10]:Σ(byte+1)*extractsMetal once, [P1-15] uniform char write.
// Later terrain or feature changes do not change an already stored amount.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Pools 0x100 catalog / 0x800 anim slots / WH*0xD grid silent fail with successor 0xFFFF [P1-10][P1-15].
// TNT unk3 byte uniformly 0 corpus-wide, not a metal raster [P1-15].
//
// The metal field must have been seeded by ApplySchema first; sampling before
// that is an error rather than a plausible wrong number.
//
// TODO(question): varying per-cell metal file beyond uniform SurfaceMetal byte remains TODO(question) [P1-15];
// per-cell metal beyond uniform not shipped (uniform SurfaceMetal seeds every cell via char write) [P1-15].
// TODO(question): retail "performs the intermediate sum with fixed-point-shaped
// integer arithmetic and then converts it to a single-precision value", and
// notes that an exact compatibility mode must preserve that conversion and
// rounding order [05 "Terrain metal extraction"]. The algebraic result is the
// clean-room contract and is what this computes; the exact intermediate shape
// is not recovered.
func (t *Terrain) SampleMetal(cx, cz int32, footX, footZ int, extractsMetal float32) (float32, error) {
	if t == nil {
		return 0, fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return 0, fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
		return 0, fmt.Errorf("world: sample %d,%d %dx%d out of bounds %dx%d", cx, cz, footX, footZ, t.CellW, t.CellH)
	}
	if t.Plot == nil || len(t.Plot) < int(t.CellW*t.CellH) {
		return 0, fmt.Errorf("world: terrain plot not initialized")
	}
	if !t.metalSeeded {
		// An unseeded metal field reads as zero everywhere, which is
		// indistinguishable from a genuinely metal-free map and would silently
		// scale every extractor's yield down to the bare footprint count.
		// Battle setup must call ApplySchema first
		// [05 "Terrain metal extraction"].
		return 0, fmt.Errorf("world: surface metal not seeded; call Terrain.ApplySchema before sampling [05 %q]", "Terrain metal extraction")
	}
	sum := int64(0)
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			sum += int64(t.Plot[(cz+int32(dz))*t.CellW+cx+int32(dx)].Metal()) + 1
		}
	}
	return float32(sum) * extractsMetal, nil
}
