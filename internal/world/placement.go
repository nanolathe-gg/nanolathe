// Placement validation and yard-map handling.
//
// Yard-map control bytes and geothermal/metal contracts are per
// [04 §6.2], [05 "Terrain metal extraction"], [05 "Geothermal requirement"] and [GAP T15].

package world

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

const (
	placementHalfCell = int64(worldUnitsPerCell / 2)
	placementMinInt32 = -1 << 31
	placementMaxInt32 = 1<<31 - 1
)

// ErrInvalidFootprint reports a negative or unconstructed extent, or an empty
// extent passed to a consumer that requires covered cells (such as a building).
// Mobile empty rectangles are established by [04 R-P0-08-C].
var ErrInvalidFootprint = errors.New("world: invalid footprint extents")

// ErrPlacementOverflow reports an input which cannot be represented by the
// cell or world-coordinate types without wrapping.
var ErrPlacementOverflow = errors.New("world: placement arithmetic overflow")

// These sentinels let construction distinguish permanent content failures
// from an otherwise valid footprint that is temporarily occupied. The error
// text remains descriptive for existing diagnostic consumers [04 §6.4].
var (
	ErrMissingPlacementDefinition = errors.New("world: placement definition unavailable")
	ErrMissingMovementProfile     = errors.New("world: movement profile unavailable")
	ErrUnclassifiedMobile         = errors.New("world: mobile placement domain unavailable")
)

// FootprintExtent is an immutable width/depth pair in map cells. Keeping the
// pair typed prevents a width/depth rectangle from being confused with a
// world-space point. Construct it with NewFootprintExtent so all public
// placement conversions reject invalid dimensions explicitly.
type FootprintExtent struct {
	width       int32
	depth       int32
	initialized bool // distinguishes an authored empty pair from a missing extent
}

// NewFootprintExtent validates and constructs an authored footprint extent.
func NewFootprintExtent(width, depth int32) (FootprintExtent, error) {
	if width < 0 || depth < 0 {
		return FootprintExtent{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, width, depth)
	}
	return FootprintExtent{width: width, depth: depth, initialized: true}, nil
}

func checkedPlacementArea(width, depth int32) (int, error) {
	if width <= 0 || depth <= 0 {
		return 0, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, width, depth)
	}
	area, ok := placementArea(width, depth)
	if !ok {
		return 0, ErrPlacementOverflow
	}
	return area, nil
}

// placementArea is checkedPlacementArea without the error: the cell count of
// a width x depth rectangle, and false exactly where checkedPlacementArea
// fails.
func placementArea(width, depth int32) (int, bool) {
	if width <= 0 || depth <= 0 {
		return 0, false
	}
	area := int64(width) * int64(depth)
	if area > int64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(area), true
}

// Width is the extent's cell width.
func (e FootprintExtent) Width() int32 { return e.width }

// Depth is the extent's cell depth.
func (e FootprintExtent) Depth() int32 { return e.depth }

// FootprintAnchor is the snapped north-west origin of a footprint rectangle,
// expressed in map-cell coordinates. It is deliberately distinct from a
// ModelWorldPosition: retail validates occupancy at this origin but positions
// a mobile model at the footprint midpoint (and a factory product at its
// authored exit transform).
type FootprintAnchor struct {
	cellX int32
	cellZ int32
}

// NewFootprintAnchor constructs an anchor from cell coordinates.
func NewFootprintAnchor(cellX, cellZ int32) FootprintAnchor {
	return FootprintAnchor{cellX: cellX, cellZ: cellZ}
}

// CellX is the anchor's cell X.
func (a FootprintAnchor) CellX() int32 { return a.cellX }

// CellZ is the anchor's cell Z.
func (a FootprintAnchor) CellZ() int32 { return a.cellZ }

// Cell is the anchor as a lattice coordinate.
func (a FootprintAnchor) Cell() Cell { return Cell{X: a.cellX, Z: a.cellZ} }

// FootprintRect is a validated half-open rectangle [MinX,MaxX) ×
// [MinZ,MaxZ) in map cells. The endpoint check prevents anchor+extent from
// silently wrapping at int32 boundaries.
type FootprintRect struct {
	anchor FootprintAnchor
	extent FootprintExtent
	maxX   int32
	maxZ   int32
}

// NewFootprintRect constructs the half-open validation rectangle for anchor
// and extent.
func NewFootprintRect(anchor FootprintAnchor, extent FootprintExtent) (FootprintRect, error) {
	if !extent.initialized || extent.width < 0 || extent.depth < 0 {
		return FootprintRect{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, extent.width, extent.depth)
	}
	maxX := int64(anchor.cellX) + int64(extent.width)
	maxZ := int64(anchor.cellZ) + int64(extent.depth)
	if maxX < placementMinInt32 || maxX > placementMaxInt32 || maxZ < placementMinInt32 || maxZ > placementMaxInt32 {
		return FootprintRect{}, fmt.Errorf("%w: rectangle origin (%d,%d), extent (%d,%d)", ErrPlacementOverflow, anchor.cellX, anchor.cellZ, extent.width, extent.depth)
	}
	return FootprintRect{anchor: anchor, extent: extent, maxX: int32(maxX), maxZ: int32(maxZ)}, nil
}

// Anchor is the rectangle's snapped top-left cell.
func (r FootprintRect) Anchor() FootprintAnchor { return r.anchor }

// Extent is the rectangle's cell width and depth.
func (r FootprintRect) Extent() FootprintExtent { return r.extent }

// MinX is the rectangle's first cell column.
func (r FootprintRect) MinX() int32 { return r.anchor.cellX }

// MinZ is the rectangle's first cell row.
func (r FootprintRect) MinZ() int32 { return r.anchor.cellZ }

// MaxX is the rectangle's last cell column, inclusive.
func (r FootprintRect) MaxX() int32 { return r.maxX }

// MaxZ is the rectangle's last cell row, inclusive.
func (r FootprintRect) MaxZ() int32 { return r.maxZ }

// Width is the rectangle's cell width.
func (r FootprintRect) Width() int32 { return r.extent.width }

// Depth is the rectangle's cell depth.
func (r FootprintRect) Depth() int32 { return r.extent.depth }

// Contains reports whether a cell lies in this rectangle's half-open bounds.
func (r FootprintRect) Contains(cellX, cellZ int32) bool {
	return cellX >= r.MinX() && cellX < r.MaxX() && cellZ >= r.MinZ() && cellZ < r.MaxZ()
}

// ModelWorldPosition is a model/unit position in authoritative world fixed
// units. It is separate from FootprintAnchor and FootprintRect because a
// factory's QueryBuildInfo transform is retained verbatim even when the
// independently snapped validation rectangle has another geometric center.
// Y is retained even though placement anchor snapping uses only X/Z.
type ModelWorldPosition struct {
	x numeric.Fixed
	y numeric.Fixed
	z numeric.Fixed
}

// NewModelWorldPosition constructs an authored or derived model/world
// position.
func NewModelWorldPosition(x, y, z numeric.Fixed) ModelWorldPosition {
	return ModelWorldPosition{x: x, y: y, z: z}
}

// X is the position's world X.
func (p ModelWorldPosition) X() numeric.Fixed { return p.x }

// Y is the position's world Y.
func (p ModelWorldPosition) Y() numeric.Fixed { return p.y }

// Z is the position's world Z.
func (p ModelWorldPosition) Z() numeric.Fixed { return p.z }

// MobilePlacement carries all coordinate products of a mobile picked point:
// the snapped anchor, its validation rectangle, and the derived footprint
// midpoint used as the model/unit position.
type MobilePlacement struct {
	anchor FootprintAnchor
	rect   FootprintRect
	model  ModelWorldPosition
}

// Rect is the placement's validation rectangle.
func (p MobilePlacement) Rect() FootprintRect { return p.rect }

// ModelPosition is the footprint midpoint used as the unit position.
func (p MobilePlacement) ModelPosition() ModelWorldPosition { return p.model }

// FactoryPlacement carries the two intentionally independent factory
// products: the QueryBuildInfo model/world transform and the snapped
// footprint rectangle used for validation.
type FactoryPlacement struct {
	anchor FootprintAnchor
	rect   FootprintRect
	model  ModelWorldPosition
}

// Anchor is the placement's snapped top-left cell.
func (p FactoryPlacement) Anchor() FootprintAnchor { return p.anchor }

// Rect is the placement's validation rectangle.
func (p FactoryPlacement) Rect() FootprintRect { return p.rect }

// ModelPosition is the QueryBuildInfo transform the placement was built from.
func (p FactoryPlacement) ModelPosition() ModelWorldPosition { return p.model }

// snapPlacementCell implements retail's signed arithmetic-shift formula:
// (picked - (extent << 19) + (1 << 19)) >> 20 [07 §9].
func snapPlacementCell(p numeric.Fixed, extent int32) (int32, error) {
	if extent < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidFootprint, extent)
	}
	halfExtent := int64(extent) * placementHalfCell
	value := int64(p)
	if value < -1<<63+halfExtent {
		return 0, ErrPlacementOverflow
	}
	value -= halfExtent
	if value > 1<<63-1-placementHalfCell {
		return 0, ErrPlacementOverflow
	}
	value += placementHalfCell
	cell := value >> 20 // signed arithmetic shift: floor for negative values
	if cell < placementMinInt32 || cell > placementMaxInt32 {
		return 0, fmt.Errorf("%w: snapped cell %d", ErrPlacementOverflow, cell)
	}
	return int32(cell), nil
}

// SnapFootprintAnchor derives a checked typed anchor from a picked world
// point and extent. It performs no validation side effects.
func SnapFootprintAnchor(px, pz numeric.Fixed, extent FootprintExtent) (FootprintAnchor, error) {
	if !extent.initialized || extent.width < 0 || extent.depth < 0 {
		return FootprintAnchor{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, extent.width, extent.depth)
	}
	x, err := snapPlacementCell(px, extent.width)
	if err != nil {
		return FootprintAnchor{}, fmt.Errorf("x: %w", err)
	}
	z, err := snapPlacementCell(pz, extent.depth)
	if err != nil {
		return FootprintAnchor{}, fmt.Errorf("z: %w", err)
	}
	return NewFootprintAnchor(x, z), nil
}

// CenterForFootprint derives the mobile model/world midpoint from an anchor:
// (extent + 2*anchor) << 19 [07 §9].
func CenterForFootprint(anchor FootprintAnchor, extent FootprintExtent) (ModelWorldPosition, error) {
	if !extent.initialized || extent.width < 0 || extent.depth < 0 {
		return ModelWorldPosition{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, extent.width, extent.depth)
	}
	center := func(cell, foot int32) numeric.Fixed {
		return numeric.Fixed((int64(foot) + 2*int64(cell)) * placementHalfCell)
	}
	return NewModelWorldPosition(center(anchor.cellX, extent.width), 0, center(anchor.cellZ, extent.depth)), nil
}

// snapMobilePlacementHorizontal derives the anchor and half-open rectangle
// for a mobile build picked at (px,pz). The caller must supply the separately
// resolved site Y before exposing a model/world position.
func snapMobilePlacementHorizontal(px, pz numeric.Fixed, extent FootprintExtent) (MobilePlacement, error) {
	anchor, err := SnapFootprintAnchor(px, pz, extent)
	if err != nil {
		return MobilePlacement{}, err
	}
	rect, err := NewFootprintRect(anchor, extent)
	if err != nil {
		return MobilePlacement{}, err
	}
	model, err := CenterForFootprint(anchor, extent)
	if err != nil {
		return MobilePlacement{}, err
	}
	return MobilePlacement{anchor: anchor, rect: rect, model: model}, nil
}

// SnapMobilePlacement derives the anchor, half-open rectangle, and model/world
// position for a mobile build picked at (px,pz). Horizontal anchor snapping
// uses only X/Z; the separately resolved site Y is carried verbatim.
func SnapMobilePlacement(px, siteY, pz numeric.Fixed, extent FootprintExtent) (MobilePlacement, error) {
	placement, err := snapMobilePlacementHorizontal(px, pz, extent)
	if err != nil {
		return MobilePlacement{}, err
	}
	placement.model.y = siteY
	return placement, nil
}

// SnapFactoryPlacement derives the validation anchor/rectangle from the
// original QueryBuildInfo model/world position while retaining that position
// exactly as the product's model/unit center [05 "Factory production lifecycle"].
func SnapFactoryPlacement(queryBuildInfo ModelWorldPosition, extent FootprintExtent) (FactoryPlacement, error) {
	anchor, err := SnapFootprintAnchor(queryBuildInfo.x, queryBuildInfo.z, extent)
	if err != nil {
		return FactoryPlacement{}, err
	}
	rect, err := NewFootprintRect(anchor, extent)
	if err != nil {
		return FactoryPlacement{}, err
	}
	return FactoryPlacement{anchor: anchor, rect: rect, model: queryBuildInfo}, nil
}

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

// Selects reports whether this yard cell owns the ground occupancy word in
// the requested yard state [04 R-COLL-01 §4]. Open yards select bit 1;
// closed yards select bit 2. The selector is shared by placement,
// construction, and movement so a cell is never blocked in only one layer.
func (y YardCell) Selects(open bool) bool {
	if open {
		return y&0x02 != 0
	}
	return y&0x04 != 0
}

// TestsOccupancy reports whether placement must reject a foreign mobile
// occupant on this yard cell [04 §6.2].
func (y YardCell) TestsOccupancy() bool { return y&0x06 != 0 }

// ParseYardMap fills a footX*footZ row-major yard buffer from an authored
// YardMap string using the retail character rules and a bounded exhausted-
// input policy [04 §6.2] C10 [fmt fbi].
//
// The retail loop walks the footprint cell by cell and the string character by
// character, and the two walks are not required to keep step:
//
//   - A character outside the ten-entry table advances the string without
//     consuming a cell. That is how the spaces stock authors use to lay a yard
//     map out in rows disappear, and it also silently drops typos.
//   - After a recognized character, the string pointer advances only when
//     the *next* character is not the terminator, so that character repeats for
//     every cell still unfilled. `YardMap=o` over a 4x4 footprint is sixteen
//     `o` cells, and ARMSILO's nine characters over 5x5 fill the remaining
//     sixteen with its last character.
//   - Characters past the last cell are never read. ARMSOLAR authors 27
//     characters for a 5x5 footprint; the trailing two are ignored.
//
// Forty-six of the 126 stock yard maps disagree with their own footprint, so
// rejecting a length mismatch — as this parser used to — makes those buildings
// unplaceable. Empty or exhausted unrecognized input receives an all-`o`
// remainder as host policy; retail walks beyond the terminator [fmt fbi].
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
				// TODO(question): retail reads beyond the terminator here;
				// those adjacent bytes are not determined by authored text.
				// Keep an occupied `o` remainder as bounded host policy [fmt fbi].
				b = 0x2f
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

// PlacementRules carries authored terrain limits for one placement query.
// ProfileResolved is the explicit provenance bit: production must reject an
// unresolved profile rather than inventing a threshold [04 §6.1][02
// "Movement class record"].
type PlacementRules struct {
	Domain        content.MobilityDomain
	MaxSlope      int32
	MaxWaterSlope int32
	MaxWaterDepth int32
	MinWaterDepth int32
	Waterline     int32
	Terrain       bool // legacy provenance marker; production sets ProfileResolved
	// ProfileResolved means every aggregate terrain limit came from the
	// produced definition's compiled movement/fallback profile. Canonical
	// production placement must set this; legacy adapters leave it false.
	ProfileResolved bool
}

// PlacementRulesForUnit resolves the same compiled movement/FBI profile used
// by construction. Preview and commit callers must share this resolver so a
// zero-value, unresolved profile cannot make the ghost disagree with the sim
// [R-P0-08][07 §9].
func PlacementRulesForUnit(cat *content.Catalog, def *content.UnitDef) (PlacementRules, error) {
	if def == nil {
		return PlacementRules{}, fmt.Errorf("%w: nil product definition [04 §6.4]", ErrMissingPlacementDefinition)
	}
	domain := def.MobilityDomain
	// Definitions assembled directly by tests and older callers predate the
	// compiled field. Derive only the established class split as an adapter;
	// compiled definitions always carry the value above [04 §6.4].
	if domain == content.MobilityUnknown {
		if def.BMCode == 0 {
			domain = content.MobilityFixed
		} else if def.CanFly {
			domain = content.MobilityAircraft
		} else if def.MovementClass != "" {
			domain = content.MobilityGround
		}
	}
	rules := PlacementRules{Domain: domain, Waterline: def.Waterline}
	if domain == content.MobilityAircraft {
		// Aircraft use the mobile occupancy/feature branch but do not require a
		// ground terrain profile merely to acquire a factory exit [04 §6.4].
		rules.ProfileResolved = true
		return rules, nil
	}
	if cat != nil && def.MovementClass != "" {
		if mc, ok := cat.Movement[content.CanonicalKey(def.MovementClass)]; ok && mc != nil {
			rules.MaxSlope = mc.MaxSlope
			rules.MaxWaterSlope = mc.MaxWaterSlope
			rules.MaxWaterDepth = mc.MaxWaterDepth
			rules.MinWaterDepth = mc.MinWaterDepth
			rules.Terrain = true
			rules.ProfileResolved = true
			return rules, nil
		}
	}
	if def.BMCode != 0 && def.FootprintX >= 0 && def.FootprintZ >= 0 && (def.FootprintX == 0 || def.FootprintZ == 0) {
		// No cell consumes a terrain limit or class on an empty mobile
		// rectangle [04 R-P0-08-C]. Keep its domain and provenance unchanged.
		return rules, nil
	}
	if cat != nil && def.MovementClass != "" {
		return PlacementRules{}, fmt.Errorf("%w %q [04 §6.4]", ErrMissingMovementProfile, def.MovementClass)
	}
	if domain == content.MobilityFixed {
		rules.MaxSlope = def.MaxSlope
		rules.MaxWaterDepth = def.MaxWaterDepth
		rules.MinWaterDepth = def.MinWaterDepth
		rules.Terrain = true
		rules.ProfileResolved = true
		return rules, nil
	}
	return PlacementRules{}, fmt.Errorf("%w: class-less product %q has no compiled placement profile [04 §6.4]", ErrUnclassifiedMobile, def.UnitName)
}

// FootprintForUnit resolves the compiled footprint for a unit definition per
// [07 §9] "The site": the footprint comes from the movement profile copied into
// the definition at compile time, falling back to the authored FBI extent.
// This helper is shared by the HUD ghost preview and the sim so the two
// cannot diverge on movement-class footprints (C-7). The result is clamped to
// at least 1 on each axis for legacy malformed/building inputs; an authored
// empty mobile pair remains empty [04 R-P0-08-C].
func FootprintForUnit(cat *content.Catalog, def *content.UnitDef) (footX, footZ int32) {
	if def == nil {
		return 1, 1
	}
	footX, footZ = def.FootprintX, def.FootprintZ
	if cat != nil && def.MovementClass != "" {
		if mc, ok := cat.Movement[content.CanonicalKey(def.MovementClass)]; ok && mc != nil {
			// The linked class wins over FBI values, including zeros. Keep
			// the legacy building normalization below outside this mobile
			// closure [04 R-P0-08-C].
			if def.BMCode != 0 {
				footX, footZ = mc.FootprintX, mc.FootprintZ
			}
			if mc.FootprintX > 0 {
				footX = mc.FootprintX
			}
			if mc.FootprintZ > 0 {
				footZ = mc.FootprintZ
			}
		}
	}
	if def.BMCode != 0 && footX >= 0 && footZ >= 0 && (footX == 0 || footZ == 0) {
		return footX, footZ // empty mobile footprint [04 R-P0-08-C]
	}
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	return footX, footZ
}

// MobileOccupancy answers which unit identity holds a cell in the
// mover-written half of retail's ground word [04 R-COLL-01 §4]. Zero means
// free. It is read-only and is consulted on exactly the cells whose control
// byte carries the occupancy bits, so a yard byte that does not test
// occupancy is not affected by it [05 "control-byte bit roles in the
// footprint validator"].
//
// Terrain holds one of these for the whole battle rather than each query
// carrying its own; see Terrain.Movers for why.
type MobileOccupancy interface {
	CellOccupant(cellX, cellZ int32) uint16
}

// PlacementQuery is the immutable input to the canonical placement legality
// predicate. A non-nil Yard describes a building yard map. Mobile products set
// Mobile and leave Yard nil; their footprint terrain and occupancy checks apply
// to every covered cell [04 §6.1][07 §9].
type PlacementQuery struct {
	Rect   FootprintRect
	Yard   []YardCell
	Rules  PlacementRules
	Self   uint16
	Mobile bool
	// AdmitOccupant is a command-preview exception supplied by the owning
	// construction rules. Allocation leaves it nil and still requires clearance.
	AdmitOccupant func(uint16) bool
	// Viewer is the blocker's fourth argument: the player record the human
	// build-cursor preview passes [04 R-P0-08-B §1]. A nil value is retail's
	// NULL player, which every other caller passes — the computer player's
	// exhaustive metal-spot helper and the placement validator's own
	// delegation — and under which the two occupancy rejections (the
	// structure-yard mark of yard bit 0, and the ground occupant of bits 1–2)
	// apply unconditionally. A non-nil value runs the known-site gate: the
	// footprint centre is projected onto the 32-world-unit LOS grid with the
	// height shear, an off-grid or currently unseen site is rejected outright,
	// and only then does the mapping option decide whether the two occupancy
	// rejections apply at all.
	Viewer PlacementViewer
	// SkipTerrainAggregates marks a query from a caller outside the inline
	// terrain-check mode (mode value 1) [04 §6.4]: the bounds, unit-occupancy
	// and blocking-feature gates still apply, but the slope/height/water
	// aggregates do not. No retail caller in the single-player path passes a
	// mode other than 1 — the factory exit included [04 R-FAC-02 §4] — so
	// this is only for callers that already skip aggregates by domain.
	SkipTerrainAggregates bool
}

// PlacementViewer is the player record the build-cursor preview hands the
// footprint blocker [04 R-P0-08-B §1]. It is an interface because the grids it
// reads belong to the visibility service, which is built on top of this
// package; the world side owns only the projection.
//
// The alias is always the LOCAL viewing slot's bit — no other player's
// visibility is ever consulted — and the computer player's placement never
// enters this gate.
type PlacementViewer interface {
	// ExploredExtent is the player's explored-grid width and height in LOS
	// cells. A projected cell at or beyond either rejects the footprint.
	ExploredExtent() (w, h int32)
	// LocallyVisible reports whether the global per-cell visibility word at
	// (vx,vz) carries the local viewing slot's bit.
	LocallyVisible(vx, vz int32) bool
	// Explored reports whether this player's explored-grid byte at (vx,vz) is
	// non-zero.
	Explored(vx, vz int32) bool
	// MappingOption is the LOS-mode word's bit 1, the mapping/fog option. Under
	// it the occupancy rejections are gated on Explored; without it they are
	// gated on the visibility bit already required, so they always apply.
	MappingOption() bool
}

// PlacementResult contains the only derived value placement consumers need
// after legality succeeds. SiteHeight is the aggregate height published to a
// build order/ghost [07 §9][05 "Geothermal requirement"].
type PlacementResult struct {
	Rect              FootprintRect
	SiteHeight        int32
	OccupantsAdmitted bool // Command preview accepted through AdmitOccupant.
}

// CheckPlacement is the one canonical, read-only placement legality function
// for preview, commit, AI, and factory exits. It checks the typed half-open
// rectangle against the entry bounds of the product's CLASS — strict on both
// edges for a building [05 R-ECO-02 §1], the low cell admitted for a mobile
// product [04 R-COLL-01 §2] — before walking cells in row-major order and
// applying that same class's gates [04 R-P0-08 "class split"]:
//
//   - a building walks its compiled yard bytes and ends on the rectangle
//     aggregate — one slope span against MaxSlope with no water pair, the
//     bit-4 height peak, and the two water-depth gates;
//   - a mobile product has no yard map and no rectangle aggregate anywhere:
//     feature blocking and ground occupancy apply to every covered cell, and
//     terrain legality is decided cell by cell on that cell's own derived pair
//     (mobileCellRefusal).
//
// Both classes run the terrain half only in the inline terrain-check mode; the
// site height is published either way.
//
// The gates themselves are placementGates, which PlacementLegal shares; this
// function words the refusal as an error.
func (t *Terrain) CheckPlacement(q PlacementQuery) (PlacementResult, error) {
	result, refusal := t.placementGates(q)
	if refusal.reason != placementAccepted {
		return PlacementResult{}, t.placementError(q, refusal)
	}
	return result, nil
}

// PlacementLegal reports whether CheckPlacement accepts q. It runs the same
// gates in the same order — AdmitOccupant and the viewer's grid are consulted
// exactly as CheckPlacement consults them — and only skips wording the
// refusal. A site search that tries thousands of anchors and keeps none of the
// messages asks this: an error formatted for every rejected anchor was more
// than half of a failed factory search (docs/MODERN_AI_RESEARCH.md §5.1).
func (t *Terrain) PlacementLegal(q PlacementQuery) bool {
	_, refusal := t.placementGates(q)
	return refusal.reason == placementAccepted
}

// placementReason names the validator gate that refused a query; the zero
// value is acceptance. placementError words each one as the error
// CheckPlacement has always returned for it.
type placementReason uint8

const (
	placementAccepted placementReason = iota
	refuseNilTerrain
	refuseRectangleExtent
	refuseEntryBounds
	refuseTerrainDimensions
	refusePlotUninitialized
	refuseRectangleArea
	refuseYardLength
	refuseUnseenSite
	refusePlotCell
	refuseStructureYard
	refuseOccupant
	refuseMover
	refuseBlockingFeature
	refuseFeatureSentinel
	refuseIndestructibleFeature
	refuseCellWaterDepth
	refuseCellMinWaterDepth
	refuseCellSlope
	refuseCellWaterSlope
	refuseGeothermal
	refuseSlope
	refuseHeightPeak
	refuseWaterDepth
	refuseMinWaterDepth
)

// placementRefusal is a refused gate and what its message quotes: the
// refusing cell, the compared figures that are not already on the query, and
// the refusing feature.
type placementRefusal struct {
	reason  placementReason
	cx, cz  int32
	a, b    int32
	feature *content.FeatureDef
}

// placementEntryBounds is a class's entry bounds (see placementGates): the
// class as the refusal names it, the lowest admitted anchor cell, and the
// highest admitted rectangle end on each axis.
func (t *Terrain) placementEntryBounds(mobile bool) (class string, minCell, maxX, maxZ int32) {
	if mobile {
		return "mobile", 0, t.CellW, t.CellH
	}
	return "building", 1, t.CellW - 1, t.CellH - 1
}

// placementGates is the validator CheckPlacement documents, every gate in
// order, returning the refusing gate instead of an error.
func (t *Terrain) placementGates(q PlacementQuery) (PlacementResult, placementRefusal) {
	admitted := false
	admit := func(occupant uint16) bool {
		if q.AdmitOccupant == nil || !q.AdmitOccupant(occupant) {
			return false
		}
		admitted = true
		return true
	}
	if t == nil {
		return PlacementResult{}, placementRefusal{reason: refuseNilTerrain}
	}
	if !q.Rect.extent.initialized || q.Rect.Width() < 0 || q.Rect.Depth() < 0 ||
		(!q.Mobile && (q.Rect.Width() == 0 || q.Rect.Depth() == 0)) {
		return PlacementResult{}, placementRefusal{reason: refuseRectangleExtent}
	}
	// Entry bounds, by CLASS. The two sides of the shared validator do not
	// admit the same rectangle:
	//
	//   - the BUILDING blocker is strict on both edges [05 R-ECO-02 §1], and
	//     the computer player's own copy of the same entry test states it
	//     identically [08 R-AI-03 §7.1]: with the anchor cell (x,z) and the
	//     footprint (fx,fz) in cells it requires x >= 1, z >= 1,
	//     x + fx < mapWidthCells and z + fz < mapHeightCells, all strict. So a
	//     building footprint can never cover column 0 or row 0, and its last
	//     covered column and row are at most mapWidth-2 and mapHeight-2. The
	//     half-open rectangle carries x + fx as MaxX, so the high pair is
	//     MaxX < CellW and MaxZ < CellH.
	//   - the MOBILE side rejects only cellX < 0, cellZ < 0 and the upper pair
	//     [04 R-COLL-01 §2]: column 0 and row 0 are enterable by a mover.
	//
	// Retail's mobile upper edge is strict too (cellX + fx >= width rejects for
	// every non-airborne mode), but its verdict there is the caller's movement
	// mode — an airborne mover is ACCEPTED off-map — and this validator has no
	// mode argument to reproduce that with, so the mobile half keeps the
	// half-open ceiling it has always had.
	// TODO(question): the mobile half's strict upper edge and its mode-2
	// off-map acceptance [04 R-COLL-01 §2 steps 1-4] are unmodelled here; what
	// would settle it is a census of which Nanolathe callers pass an airborne
	// product to this function and whether the mover commit path needs the
	// mode verdict rather than an error.
	_, minCell, maxX, maxZ := t.placementEntryBounds(q.Mobile)
	if q.Rect.MinX() < minCell || q.Rect.MinZ() < minCell || q.Rect.MaxX() > maxX || q.Rect.MaxZ() > maxZ {
		return PlacementResult{}, placementRefusal{reason: refuseEntryBounds}
	}
	if q.Mobile && (q.Rect.Width() == 0 || q.Rect.Depth() == 0) {
		// The empty mobile loop reads no plot cells, but retains retail
		// strict upper bounds even on its zero axis [04 R-P0-08-C].
		if q.Rect.MaxX() >= t.CellW || q.Rect.MaxZ() >= t.CellH {
			return PlacementResult{}, placementRefusal{reason: refuseEntryBounds}
		}
		return PlacementResult{Rect: q.Rect, SiteHeight: int32(t.SeaLevel) - q.Rules.Waterline}, placementRefusal{}
	}
	plotArea, ok := placementArea(t.CellW, t.CellH)
	if !ok {
		return PlacementResult{}, placementRefusal{reason: refuseTerrainDimensions}
	}
	if t.Plot == nil || len(t.Plot) < plotArea {
		return PlacementResult{}, placementRefusal{reason: refusePlotUninitialized}
	}
	area, ok := placementArea(q.Rect.Width(), q.Rect.Depth())
	if !ok {
		return PlacementResult{}, placementRefusal{reason: refuseRectangleArea}
	}
	if !q.Mobile && len(q.Yard) != area {
		return PlacementResult{}, placementRefusal{reason: refuseYardLength}
	}

	// The known-site gate of [04 R-P0-08-B §1]. With retail's null player it is
	// not entered at all and the two occupancy rejections apply
	// unconditionally; with a player record it runs once for the whole
	// footprint, before the cell walk, and can reject outright.
	occupancyApplies := true
	if q.Viewer != nil {
		var seen bool
		if occupancyApplies, seen = t.knownSiteGate(q); !seen {
			return PlacementResult{}, placementRefusal{reason: refuseUnseenSite}
		}
	}

	sea := int32(t.SeaLevel)
	// Terrain legality applies only when the caller requests the inline
	// terrain-check mode (the recovered mode value 1); other modes add no
	// second terrain-legality rule after the bounds check [04 §6.4]
	// [04 R-COLL-01 §2 "class dispatch"]. Queries outside it keep siteHeight
	// from the plain bounds pass for allocation height.
	skipTerrain := q.SkipTerrainAggregates || q.Rules.Domain == content.MobilityAircraft || !q.Rules.ProfileResolved

	minLow, maxHigh, bit4Max := int32(255), int32(0), int32(0)
	geothermalNeeded, geothermalFound := false, false
	for dz := int32(0); dz < q.Rect.Depth(); dz++ {
		for dx := int32(0); dx < q.Rect.Width(); dx++ {
			idx := int(dz*q.Rect.Width() + dx)
			yard := YardCell(0)
			if q.Mobile {
				// A mobile product has no yard map: the inline footprint loop
				// checks feature blocking and unit occupancy on every covered
				// cell [04 R-P0-08 "mobile terrain validator"]. The synthesized
				// byte carries those two gates only — bits 1-2 occupancy and
				// bit 5 blocking-feature. Bit 3 rides along to collect the
				// published site height below; it is NOT a legality aggregate
				// on this path, and bit 4's separate height maximum is a yard
				// participation a yardless mobile product never contributes to.
				yard = 0x2e
			} else {
				yard = q.Yard[idx]
			}
			cx, cz := q.Rect.MinX()+dx, q.Rect.MinZ()+dz
			cell := t.PlotAt(cx, cz)
			if cell == nil {
				return PlacementResult{}, placementRefusal{reason: refusePlotCell, cx: cx, cz: cz}
			}
			class, def := t.classifyCell(cx, cz)

			// Yard bit 0 is the STRUCTURE-YARD mark, not a visibility or
			// minimap lookup: for a covered cell whose yard byte carries it,
			// the blocker rejects when the cell's flag-byte bit 1 is set. The
			// building stamp sets that bit on every cell whose own yard byte
			// has bit 0 and the building clear resets it [04 R-COLL-01 §4], so
			// the mark reads "a completed or stamped building's yard already
			// covers this cell". It is a building-versus-building test and
			// reads no occupant identity, no LOS word and no fog surface
			// [04 R-P0-08-B §1]. The visibility half of the old description is
			// the blocker's known-site gate, applied once above.
			if occupancyApplies && yard&0x01 != 0 && cell.StructureYard() {
				return PlacementResult{}, placementRefusal{reason: refuseStructureYard, cx: cx, cz: cz}
			}
			if occupancyApplies && yard&0x06 != 0 {
				// The occupancy test reads the cell's GROUND word only:
				// "Only the ground word is read; the air word is never
				// consulted, so a landed or hovering airborne unit never blocks
				// a ground mover through this test" [04 R-COLL-01 §2]. Reading
				// both words was invisible while nothing wrote the air one;
				// WU-19-20 gave mode-2 movers that word [04 R-COLL-01 §4], and
				// reading it here jams a stock aircraft plant — its own hovering
				// products hold the air word over the exit rectangle and every
				// later product's placement is refused (construction's
				// four-aircraft liveness run stalls at two).
				if occ := cell.OccupantA(); occ != 0 && uint16(occ) != q.Self && !admit(uint16(occ)) {
					return PlacementResult{}, placementRefusal{reason: refuseOccupant, cx: cx, cz: cz}
				}
				// The same test on the other half of the split ground word.
				// "Bits 1-2 reject any nonzero occupant other than the passed
				// self identity" [05 "control-byte bit roles in the footprint
				// validator"], and every placement caller passes a null self
				// identity, so a unit standing on the rectangle rejects it —
				// the builder that issued the order included
				// [04 R-COLL-01 §2][04 R-COLL-01 §6].
				if t.Movers != nil {
					if occ := t.Movers.CellOccupant(cx, cz); occ != 0 && occ != q.Self && !admit(occ) {
						return PlacementResult{}, placementRefusal{reason: refuseMover, cx: cx, cz: cz}
					}
				}
			}
			if yard&0x20 != 0 {
				switch class {
				case featureReal:
					if def.Blocking {
						return PlacementResult{}, placementRefusal{reason: refuseBlockingFeature, cx: cx, cz: cz, feature: def}
					}
				case featureVoid:
					return PlacementResult{}, placementRefusal{reason: refuseFeatureSentinel, cx: cx, cz: cz}
				}
			}
			if yard&0x40 != 0 && class == featureReal && def.Indestructible {
				return PlacementResult{}, placementRefusal{reason: refuseIndestructibleFeature, cx: cx, cz: cz, feature: def}
			}
			if yard&0x80 != 0 {
				geothermalNeeded = true
				if class == featureReal && def.Geothermal {
					geothermalFound = true
				}
			}

			// The mobile class's terrain legality is PER CELL and has no
			// rectangle aggregate at all [04 R-P0-08 "mobile terrain
			// validator"][04 R-SLOPE-01 §3]. Each covered cell is judged on
			// its own derived pair, in the order of [04 R-COLL-01 §2]
			// steps 3-5, so a footprint whose cells are each legal is legal
			// however far apart their heights lie.
			if q.Mobile && !skipTerrain {
				if reason, slope := mobileCellRefusal(cell, sea, q.Rules); reason != placementAccepted {
					return PlacementResult{}, placementRefusal{reason: reason, cx: cx, cz: cz, a: slope}
				}
			}

			// Building yards select the aggregate samples with bit 3; a mobile
			// product samples every covered cell for the published site height
			// alone [04 R-P0-08].
			if q.Mobile || yard&0x08 != 0 {
				if h := int32(cell.MinHeight()); h < minLow {
					minLow = h
				}
				if h := int32(cell.MaxHeight()); h > maxHigh {
					maxHigh = h
				}
			}
			// The separate height maximum is yard bit 4's participation only;
			// a yardless mobile product never sets it, so the `bit4Max >
			// siteHeight` gate below cannot reject a sloped factory exit
			// [04 R-FAC-02 §6][04 §6.4]. An earlier build sampled every mobile
			// cell here and every stock lab on a slope then failed its exit
			// validation forever.
			if yard&0x10 != 0 {
				if h := int32(cell.MaxHeight()); h > bit4Max {
					bit4Max = h
				}
			}
		}
	}
	if geothermalNeeded && !geothermalFound {
		return PlacementResult{}, placementRefusal{reason: refuseGeothermal}
	}

	// With no sampled cell at all the published height is the definition's
	// waterline against sea level, rather than an invented terrain sample
	// [04 R-P0-08].
	siteHeight := sea - q.Rules.Waterline
	if maxHigh >= minLow {
		siteHeight = minLow
	}
	// Everything below is the BUILDING class's rectangle aggregate. It is the
	// structure placement validator's yard-map walk and exists nowhere else in
	// the engine [04 R-P0-08 "footprint aggregates and strict comparisons"]
	// [04 R-SLOPE-01 §3 "Bounded census"]: a mobile product's terrain legality
	// was already decided cell by cell inside the walk above, and the mobile
	// path publishes only siteHeight from here.
	if q.Mobile || skipTerrain {
		return PlacementResult{Rect: q.Rect, SiteHeight: siteHeight, OccupantsAdmitted: admitted}, placementRefusal{}
	}
	// The yard walk compares the rectangle span against the land MaxSlope
	// alone — there is no water pair on this path [04 R-SLOPE-01 §3 "Bounded
	// census"] — and the comparison is strict, so a span exactly equal to the
	// limit passes [04 R-P0-08].
	if maxHigh >= minLow && maxHigh-minLow > q.Rules.MaxSlope {
		return PlacementResult{}, placementRefusal{reason: refuseSlope, a: maxHigh - minLow}
	}
	if bit4Max > siteHeight {
		return PlacementResult{}, placementRefusal{reason: refuseHeightPeak, a: bit4Max, b: siteHeight}
	}
	if minLow < sea-q.Rules.MaxWaterDepth {
		return PlacementResult{}, placementRefusal{reason: refuseWaterDepth}
	}
	maxSample := maxHigh
	if bit4Max > maxSample {
		maxSample = bit4Max
	}
	// The upper waterline band is unconditional, including when the authored
	// value is zero. Land profiles use the established -10000 template value
	// to disable this gate [04 §6.1, §6.4][05 "Geothermal requirement"].
	if maxSample > sea-q.Rules.MinWaterDepth {
		return PlacementResult{}, placementRefusal{reason: refuseMinWaterDepth}
	}
	return PlacementResult{Rect: q.Rect, SiteHeight: siteHeight, OccupantsAdmitted: admitted}, placementRefusal{}
}

// placementError words a refusal exactly as the validator always has: the
// same text, and the same sentinels and wrapping for errors.Is.
func (t *Terrain) placementError(q PlacementQuery, r placementRefusal) error {
	switch r.reason {
	case refuseNilTerrain:
		return fmt.Errorf("world: nil terrain")
	case refuseRectangleExtent:
		return fmt.Errorf("%w: rectangle dimensions %dx%d", ErrInvalidFootprint, q.Rect.Width(), q.Rect.Depth())
	case refuseEntryBounds:
		class, minCell, maxX, maxZ := t.placementEntryBounds(q.Mobile)
		if q.Mobile && (q.Rect.Width() == 0 || q.Rect.Depth() == 0) {
			maxX, maxZ = t.CellW-1, t.CellH-1
		}
		return fmt.Errorf("world: placement rectangle [%d,%d)x[%d,%d) out of bounds %dx%d: the %s entry bounds require anchor >= %d and rectangle end <= %d,%d", q.Rect.MinX(), q.Rect.MaxX(), q.Rect.MinZ(), q.Rect.MaxZ(), t.CellW, t.CellH, class, minCell, maxX, maxZ)
	case refuseTerrainDimensions:
		_, err := checkedPlacementArea(t.CellW, t.CellH)
		return fmt.Errorf("world: invalid terrain dimensions: %w", err)
	case refusePlotUninitialized:
		return fmt.Errorf("world: terrain plot not initialized")
	case refuseRectangleArea:
		_, err := checkedPlacementArea(q.Rect.Width(), q.Rect.Depth())
		return err
	case refuseYardLength:
		area, _ := placementArea(q.Rect.Width(), q.Rect.Depth())
		return fmt.Errorf("world: yard length %d != rectangle %dx%d=%d", len(q.Yard), q.Rect.Width(), q.Rect.Depth(), area)
	case refuseUnseenSite:
		return fmt.Errorf("world: placement site is not currently visible to the local viewer [04 R-P0-08-B §1]")
	case refusePlotCell:
		return fmt.Errorf("world: plot cell %d,%d out of range", r.cx, r.cz)
	case refuseStructureYard:
		return fmt.Errorf("world: cell %d,%d already lies under a building yard [04 R-P0-08-B §1]", r.cx, r.cz)
	case refuseOccupant:
		return fmt.Errorf("world: cell %d,%d occupied [04 §6.2]", r.cx, r.cz)
	case refuseMover:
		return fmt.Errorf("world: cell %d,%d occupied by a mover [04 R-COLL-01 §2]", r.cx, r.cz)
	case refuseBlockingFeature:
		return fmt.Errorf("world: cell %d,%d blocked by feature %s [04 §6.2]", r.cx, r.cz, r.feature.CanonicalKey)
	case refuseFeatureSentinel:
		return fmt.Errorf("world: cell %d,%d holds an occupied feature sentinel [04 §6.2]", r.cx, r.cz)
	case refuseIndestructibleFeature:
		return fmt.Errorf("world: cell %d,%d holds an indestructible feature %s [04 §6.2]", r.cx, r.cz, r.feature.CanonicalKey)
	case refuseCellWaterDepth:
		return fmt.Errorf("world: cell %d,%d %w", r.cx, r.cz, fmt.Errorf("water depth exceeds %d [04 R-COLL-01 §2]", q.Rules.MaxWaterDepth))
	case refuseCellMinWaterDepth:
		return fmt.Errorf("world: cell %d,%d %w", r.cx, r.cz, fmt.Errorf("is deeper than minimum water depth %d [04 R-COLL-01 §2]", q.Rules.MinWaterDepth))
	case refuseCellSlope:
		return fmt.Errorf("world: cell %d,%d %w", r.cx, r.cz, fmt.Errorf("slope %d exceeds limit %d [04 R-COLL-01 §2]", r.a, q.Rules.MaxSlope))
	case refuseCellWaterSlope:
		return fmt.Errorf("world: cell %d,%d %w", r.cx, r.cz, fmt.Errorf("slope %d exceeds water limit %d [04 R-COLL-01 §2]", r.a, q.Rules.MaxWaterSlope))
	case refuseGeothermal:
		return fmt.Errorf("world: geothermal requirement not satisfied [05 %q]", "Geothermal requirement")
	case refuseSlope:
		return fmt.Errorf("world: placement slope %d exceeds limit %d [04 §6.4]", r.a, q.Rules.MaxSlope)
	case refuseHeightPeak:
		return fmt.Errorf("world: placement height peak %d exceeds site height %d [05 %q]", r.a, r.b, "Geothermal requirement")
	case refuseWaterDepth:
		return fmt.Errorf("world: placement water depth exceeds %d [05 %q]", q.Rules.MaxWaterDepth, "Geothermal requirement")
	case refuseMinWaterDepth:
		return fmt.Errorf("world: placement is deeper than minimum water depth %d [05 %q]", q.Rules.MinWaterDepth, "Geothermal requirement")
	}
	return fmt.Errorf("world: placement refused by unworded gate %d", r.reason)
}

// mobileCellRefusal is the mobile class's terrain test for ONE covered cell,
// [04 R-COLL-01 §2] "the mode-1 scan" steps 3-5, in that order:
//
//  3. deep:    hmin < seaLevel − MaxWaterDepth  → reject
//  4. shallow: hmax > seaLevel − MinWaterDepth  → reject
//  5. slope:   slope = hmax − hmin; when slope > MaxSlope, a land cell
//     (hmin >= seaLevel) rejects, otherwise slope > MaxWaterSlope
//     rejects
//
// Every comparison is signed and STRICT, so a slope or a depth exactly at the
// limit is legal. `hmin`/`hmax` are this cell's own derived pair, never a
// rectangle aggregate [04 R-SLOPE-01 §3]. Step 5's branch is the per-cell pair
// selection of [04 R-P0-08] — the land pair when the cell's `hmin >= SeaLevel`,
// the water pair otherwise — because the compiled class clamps MaxSlope down to
// MaxWaterSlope before either reaches the definition [04 §6.1].
//
// This is the same arithmetic as movement's commit-cell validator
// (internal/movement/profile_footprint.go, IsPassableCommitCell). The two
// cannot share code: movement depends on world, so the dependency may not run
// the other way.
//
// It returns the refusing step, and the cell's slope for the two slope steps.
func mobileCellRefusal(cell *PlotCell, sea int32, rules PlacementRules) (placementReason, int32) {
	low, high := int32(cell.MinHeight()), int32(cell.MaxHeight())
	if low < sea-rules.MaxWaterDepth {
		return refuseCellWaterDepth, 0
	}
	if high > sea-rules.MinWaterDepth {
		return refuseCellMinWaterDepth, 0
	}
	if slope := high - low; slope > rules.MaxSlope {
		if low >= sea {
			return refuseCellSlope, slope
		}
		if slope > rules.MaxWaterSlope {
			return refuseCellWaterSlope, slope
		}
	}
	return placementAccepted, 0
}

// knownSiteGate is the build-cursor preview's half of the footprint blocker
// [04 R-P0-08-B §1], exactly:
//
//  1. take the footprint centre in world units — `(footX + 2·cellX) × 8`,
//     `(footZ + 2·cellZ) × 8` — and sample its terrain height; the visibility
//     cell is `vx = worldX >> 5`, `vz = (worldZ − (height >> 1)) >> 5`, the
//     32-world-unit LOS grid with the height shear of [03 §2.1];
//  2. `vx` at or beyond the player's explored-grid width, or `vz` at or beyond
//     its height, REJECTS the footprint;
//  3. the global per-cell visibility word at (vx,vz) must carry the local
//     viewing slot's bit, or the footprint is REJECTED — a site the local
//     viewer cannot currently see is unplaceable from the cursor;
//  4. the gate for the two occupancy rejections is then: under the mapping
//     option, the passed player's explored-grid byte at (vx,vz) is non-zero;
//     without that option it is the visibility bit already tested in step 3, so
//     the rejections always apply.
//
// The only case in which a visible cursor site skips the occupancy rejections
// is the mapping option over a site the local player has never explored, which
// cannot be visible at step 3 in ordinary play — so in practice the preview
// applies both rejections whenever it reaches them.
//
// The second result is false when steps 2 or 3 reject; the first is the step-4
// gate for the caller's cell walk.
func (t *Terrain) knownSiteGate(q PlacementQuery) (occupancyApplies, ok bool) {
	worldX := (q.Rect.Width() + 2*q.Rect.MinX()) * 8
	worldZ := (q.Rect.Depth() + 2*q.Rect.MinZ()) * 8
	height := int32(t.HeightAt(numeric.FixedFromInt(int64(worldX)), numeric.FixedFromInt(int64(worldZ))).Raw() >> 16)
	vx := worldX >> 5
	vz := (worldZ - (height >> 1)) >> 5
	gw, gh := q.Viewer.ExploredExtent()
	// Unsigned bounds, so a negative projection wraps high and rejects rather
	// than indexing backwards — the same shape the visibility sampler uses.
	if uint32(vx) >= uint32(gw) || uint32(vz) >= uint32(gh) {
		return false, false
	}
	if !q.Viewer.LocallyVisible(vx, vz) {
		return false, false
	}
	if q.Viewer.MappingOption() {
		return q.Viewer.Explored(vx, vz), true
	}
	return true, true
}

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

// ValidatePlacement validates a cell/yard footprint through the canonical
// placement predicate. It remains as the public cell-coordinate entry point
// used by legacy test fixtures and callers outside the production graph; all
// production placement paths construct PlacementQuery directly.
func (t *Terrain) ValidatePlacement(cx, cz int32, yard []YardCell, footX, footZ int, self uint16) error {
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	extent, err := NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		return err
	}
	rect, err := NewFootprintRect(NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		return err
	}
	_, err = t.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Self: self})
	return err
}

// SiteHeight returns the ground height retail draws a build site at and stores
// as a structure's Y [05 "Established fact — the placement helper that writes a
// structure's Y"][07 §9][04 §6.2].
//
// A structure therefore stands at the MINIMUM height under its footprint, not
// at the terrain height under its origin, so one straddling a plateau edge is
// partly buried and its origin can sit well below its own centre's ground. That
// is retail's, confirmed against two independent producers of the aggregate; do
// not "correct" a buried structure by raising it to the terrain under its
// origin.
//
// The footprint validator tracks two aggregates while it walks the yard map,
// over exactly those cells whose yard byte carries bit 3 — the bit research
// names as enabling slope sampling: the minimum of the cell's low height and
// the maximum of its high height. When at least one cell carried the bit, the
// site height is that minimum. When none did, the aggregates are still at their
// initial 255 and 0, the maximum compares below the minimum, and retail falls
// back to `SeaLevel - waterline` instead.
//
// The same two aggregates also feed the slope and water gates in
// CheckPlacement. This legacy helper only returns the derived height; callers
// that need legality must use the canonical typed query.
func (t *Terrain) SiteHeight(cx, cz int32, yard []YardCell, footX, footZ int, waterline int32) int32 {
	if t == nil {
		return 0
	}
	sea := int32(t.SeaLevel)
	area, err := checkedPlacementArea(int32(footX), int32(footZ))
	if err != nil || len(yard) != area {
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

// SampleMetal computes the metal an extractor samples from its footprint at
// creation [05 R-PROD-01 §6]:
//
//	sampled metal = extracts-metal multiplier x Σ(cell metal byte + 1)
//
// Every covered cell contributes at least one, so a zero-metal cell still adds
// one. The rate is stored once on the unit and never resampled, so later
// terrain or feature changes do not move it. A feature's own metal field is a
// reclaim reward and is not part of this sum [05 R-FEAT-01 §7].
//
// On a canonical map the metal byte is the uniform schema value written to
// every cell, not a per-cell raster: the four-byte attribute record never
// touches it, and the shipped corpus's fourth attribute byte is uniformly zero
// [05 R-PROD-01 §6][fmt tnt]. A legacy map's per-cell bytes come from its
// eight-byte attribute record instead, which is the only varying source.
//
// The metal field must have been seeded by ApplySchema first; sampling before
// that is an error rather than a plausible wrong number.
//
// The intermediate's shape is settled [05 R-PROD-01 §6-A]: the accumulator is
// sixteen bits of Σ(metalByte + 1) over the in-bounds footprint cells, and the
// rate is `float32( ((float)(int32)(accumulator << 16)) × extractsmetal × 2⁻¹⁶ )`
// evaluated left to right with the only narrowing at the store. Because
// `accumulator << 16` is exact as a floating value and `extractsmetal` is a
// single, that rounds once at the store to the same single as
// `float32(accumulator) × extractsmetal` for every accumulator below 0x8000 —
// which is what this computes. There is no other rounding to preserve. The one
// corner is the sign: at 0x8000 and above the shifted word loads as a negative
// 32-bit integer and retail's rate goes negative, where a wider or unsigned
// accumulator diverges; no shipped footprint reaches it, and the wrap is
// visible in SampleMetalWithFootprintSum's second return value below.
func (t *Terrain) SampleMetal(cx, cz int32, footX, footZ int, extractsMetal float32) (float32, error) {
	rate, _, err := t.SampleMetalWithFootprintSum(cx, cz, footX, footZ, extractsMetal)
	return rate, err
}

// SampleMetalWithFootprintSum is SampleMetal plus the raw footprint
// accumulator the creator also hands to the unit's script.
//
// The accumulator is Σ(cell metal byte + 1) over the stamped footprint, kept
// to sixteen bits [05 R-PROD-01 §6]. Immediately after storing the rate the
// creator starts a deferred `SetSpeed` whose single argument is that
// accumulator sign-extended from sixteen bits, and stock extractor scripts
// size their animation rate from it [04 R-COB-04 §9]. Returning it here is the
// only way a creation path can issue that callback without walking the
// footprint a second time.
//
// The rate itself is unchanged: still float32(Σ(byte+1)) × extractsMetal over
// a wide accumulator, so the modulo-65536 wrap retail's sixteen-bit
// accumulator would take is visible in the second return value only. Reaching
// it needs Σ(byte+1) ≥ 65536, which no shipped footprint approaches
// [05 R-PROD-01 §6]; the rate-side sign corner is the one named above, and
// [05 R-PROD-01 §6-A] states that it is the only divergence a wider
// accumulator produces.
//
// The rectangle may leave the map. The walk resolves each coordinate through a
// per-cell bounds test — 0 ≤ x < cell width and 0 ≤ z < cell height, else no
// cell — and an off-map coordinate contributes nothing at all, not even the
// +1, while the in-bounds cells of the same rectangle still accumulate
// [05 R-PROD-01 §6]. *Correction:* this function previously rejected the whole
// sample with an out-of-bounds error whenever any part of the rectangle left
// the map, so an extractor placed against a map edge stored a rate of zero
// instead of its partial sum. That rejection was ours, not retail's; the
// established walk has no rectangle-level bounds test. Callers that must
// refuse an off-map footprint — CheckExtractorOverlap is the one — carry their
// own bounds test and are unaffected.
func (t *Terrain) SampleMetalWithFootprintSum(cx, cz int32, footX, footZ int, extractsMetal float32) (float32, uint16, error) {
	if t == nil {
		return 0, 0, fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return 0, 0, fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if t.Plot == nil || len(t.Plot) < int(t.CellW*t.CellH) {
		return 0, 0, fmt.Errorf("world: terrain plot not initialized")
	}
	if !t.metalSeeded {
		// An unseeded metal field reads as zero everywhere, which is
		// indistinguishable from a genuinely metal-free map and would silently
		// scale every extractor's yield down to the bare footprint count.
		// Battle setup must call ApplySchema first
		// [05 "Terrain metal extraction"].
		return 0, 0, fmt.Errorf("world: surface metal not seeded; call Terrain.ApplySchema before sampling [05 %q]", "Terrain metal extraction")
	}
	// Outer loop over the Z extent from the stamped Z cell, inner over the X
	// extent from the stamped X cell [05 R-PROD-01 §6].
	sum := int64(0)
	for dz := 0; dz < footZ; dz++ {
		z := cz + int32(dz)
		if z < 0 || z >= t.CellH {
			continue // off-map row: no cell, no +1
		}
		for dx := 0; dx < footX; dx++ {
			x := cx + int32(dx)
			if x < 0 || x >= t.CellW {
				continue // off-map column: no cell, no +1
			}
			sum += int64(t.Plot[z*t.CellW+x].Metal()) + 1
		}
	}
	return float32(sum) * extractsMetal, uint16(sum), nil
}
