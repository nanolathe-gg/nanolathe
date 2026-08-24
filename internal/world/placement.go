// Package world provides placement validation and yard-map handling.
//
// Yard-map control bytes and geothermal/metal contracts are per
// [04 §6.2], [05 "Terrain metal extraction"], [05 "Geothermal requirement"] and [GAP T15].
package world

import (
	"fmt"
	"strings"

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

// ParseYardMap parses a yard-map string into row-major control bytes sized by
// the packed footprint extents [04 §6.2] C10.
//
//   - Whitespace (space, tab, \r, \n) is stripped; the remaining characters must
//     be exactly footX*footZ drawn from the ten control characters above.
//   - A single-character input is repeated to fill the footprint (e.g. "o" with
//     a 2x2 footprint becomes "oooo"). TODO(question): research describes the
//     mapping as one-to-one into a buffer sized by the packed footprint extents
//     [05 "Geothermal requirement"] and does not mention a shorthand. This
//     accepts authored data that retail might reject; it never rejects data
//     retail accepts, so it is the safe direction (I11).
//   - Unknown characters are rejected with a diagnostic.
//
// Non-building classes do not allocate a yard-map buffer; callers should not
// invoke this parser when the definition has no yard map [05 "Geothermal requirement"].
func ParseYardMap(s string, footX, footZ int) ([]YardCell, error) {
	if footX <= 0 || footZ <= 0 {
		return nil, fmt.Errorf("world: invalid footprint %dx%d [04 §6.2]", footX, footZ)
	}
	// Strip whitespace [fmt tdf] yard maps are authored with spaces for readability.
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	if compact == "" {
		return nil, fmt.Errorf("world: empty yardmap for %dx%d [04 §6.2]", footX, footZ)
	}
	if len(compact) == 1 {
		compact = strings.Repeat(compact, footX*footZ)
	}
	expected := footX * footZ
	if len(compact) != expected {
		return nil, fmt.Errorf("world: yardmap length %d != footprint %dx%d=%d [04 §6.2]", len(compact), footX, footZ, expected)
	}
	out := make([]YardCell, expected)
	for i, ch := range compact {
		var b YardCell
		switch ch {
		case '.':
			b = 0x00 // [04 §6.2] C10
		case 'C':
			b = 0x35 // [04 §6.2] C10
		case 'G':
			b = 0x8f // [04 §6.2] C10 bit7 geothermal [05 "Geothermal requirement"]
		case 'O':
			b = 0x2b // [04 §6.2] C10
		case 'Y':
			b = 0x31 // [04 §6.2] C10
		case 'c':
			b = 0x2d // [04 §6.2] C10
		case 'f':
			b = 0x6f // [04 §6.2] C10
		case 'o':
			b = 0x2f // [04 §6.2] C10
		case 'w':
			b = 0x37 // [04 §6.2] C10
		case 'y':
			b = 0x29 // [04 §6.2] C10
		default:
			return nil, fmt.Errorf("world: unknown yardmap character %q at %d [04 §6.2]", ch, i)
		}
		out[i] = b
	}
	return out, nil
}

// featureClass is how the footprint validator sees one covered cell's feature
// reference, after the fringe hop [04 §6.2].
type featureClass uint8

const (
	// featureEmpty is the empty sentinel: nothing occupies the cell.
	featureEmpty featureClass = iota
	// featureReal is a bounds-checked index that binds to a catalog definition.
	featureReal
	// featureBlocked covers the three reserved sentinels just above the real
	// band, which "behave as occupied", and any reference that cannot be
	// resolved — an out-of-range index, an unbound name, or a fringe cell whose
	// anchor hop leads nowhere. [04 §6.2] makes all of these blocking for bit 5
	// and non-satisfying for bit 7, so they share a class.
	featureBlocked
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
		return featureBlocked, nil
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil || cell.IsEmpty() {
		return featureEmpty, nil
	}
	// Void sentinels, and fringe cells whose hop found no real anchor.
	return featureBlocked, nil
}

// ValidatePlacement checks a building placement at cell (cx,cz) against the
// terrain, using the unit's yard map and footprint rectangle [04 §6.2],
// [05 "Geothermal requirement"].
//
// The validator bounds-checks the rectangle first, then applies the yard byte's
// per-cell bits. Implemented here:
//
//	bit 1-2  reject any nonzero occupant other than the passed self identity
//	bit 5    the cell must be free of blocking features
//	bit 6    fail when the resolved feature is not reclaimable
//	bit 7    the geothermal requirement (character G)
//
// Geothermal is the documented rule: "If any covered cell's yard byte has bit 7
// set, validation succeeds only when at least one covered cell holds a feature
// whose catalog entry carries the geothermal flag." An unresolvable reference
// does not satisfy it [04 §6.2], [05 "Geothermal requirement"].
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
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if len(yard) != footX*footZ {
		return fmt.Errorf("world: yard length %d != footprint %dx%d=%d", len(yard), footX, footZ, footX*footZ)
	}
	// The validator bounds-checks the rectangle against the map first [04 §6.2].
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
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

			// TODO(question): bit 3 enables slope sampling over the footprint
			// and bit 4 enables height tracking [04 §6.2]. Research names both
			// roles but not the thresholds they compare against, and the
			// movement profile's slope limits live in a different record.

			// Bit 5: the cell must be free of blocking features [04 §6.2].
			if y&0x20 != 0 && class != featureEmpty {
				return fmt.Errorf("world: cell %d,%d blocked by feature [04 §6.2]", px, pz)
			}

			// Bit 6: fail when the resolved feature carries the
			// non-reclaimable flag [04 §6.2]. An unresolvable reference is the
			// out-of-range case and blocks here too.
			if y&0x40 != 0 {
				switch class {
				case featureReal:
					if !def.Reclaimable {
						return fmt.Errorf("world: cell %d,%d holds a non-reclaimable feature [04 §6.2]", px, pz)
					}
				case featureBlocked:
					return fmt.Errorf("world: cell %d,%d holds an unresolvable feature reference [04 §6.2]", px, pz)
				}
			}

			// Bit 7: the geothermal requirement [05 "Geothermal requirement"].
			if y&0x80 != 0 {
				geothermalNeeded = true
			}
			if class == featureReal && def.Geothermal {
				geothermalFound = true
			}
		}
	}
	if geothermalNeeded && !geothermalFound {
		return fmt.Errorf("world: geothermal requirement not satisfied [05 %q]", "Geothermal requirement")
	}
	return nil
}

// SampleMetal computes the metal a placed extractor samples from its footprint
// [05 "Terrain metal extraction"]:
//
//	sampled metal = extracts-metal multiplier x sum(cell metal byte + 1)
//
// Every cell contributes at least one, so a zero-metal cell still adds one. The
// result is stored on the unit instance at placement and never resampled: later
// terrain or feature changes do not change an already stored amount.
//
// The metal field must have been seeded by ApplySchema first; sampling before
// that is an error rather than a plausible wrong number.
//
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
