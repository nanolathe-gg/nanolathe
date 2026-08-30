// Package world provides placement validation and yard-map handling.
//
// Yard-map control bytes and geothermal/metal contracts are per
// [04 §6.2], [05 "Terrain metal extraction"], [05 "Geothermal requirement"] and [GAP T15].
package world

import (
	"errors"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

const (
	placementHalfCell = int64(worldUnitsPerCell / 2)
	placementMinInt32 = -1 << 31
	placementMaxInt32 = 1<<31 - 1
)

// ErrInvalidFootprint reports a zero or negative footprint extent. A footprint
// is an authored count of covered cells, so there is no meaningful rectangle
// for a non-positive extent.
var ErrInvalidFootprint = errors.New("world: footprint extents must be positive")

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
	width int32
	depth int32
}

// NewFootprintExtent validates and constructs an authored footprint extent.
func NewFootprintExtent(width, depth int32) (FootprintExtent, error) {
	if width <= 0 || depth <= 0 {
		return FootprintExtent{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, width, depth)
	}
	return FootprintExtent{width: width, depth: depth}, nil
}

func checkedPlacementArea(width, depth int32) (int, error) {
	if width <= 0 || depth <= 0 {
		return 0, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, width, depth)
	}
	area := int64(width) * int64(depth)
	if area > int64(int(^uint(0)>>1)) {
		return 0, ErrPlacementOverflow
	}
	return int(area), nil
}

func (e FootprintExtent) Width() int32 { return e.width }
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

func (a FootprintAnchor) CellX() int32 { return a.cellX }
func (a FootprintAnchor) CellZ() int32 { return a.cellZ }
func (a FootprintAnchor) Cell() Cell   { return Cell{X: a.cellX, Z: a.cellZ} }

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
	if extent.width <= 0 || extent.depth <= 0 {
		return FootprintRect{}, fmt.Errorf("%w: %dx%d", ErrInvalidFootprint, extent.width, extent.depth)
	}
	maxX := int64(anchor.cellX) + int64(extent.width)
	maxZ := int64(anchor.cellZ) + int64(extent.depth)
	if maxX < placementMinInt32 || maxX > placementMaxInt32 || maxZ < placementMinInt32 || maxZ > placementMaxInt32 {
		return FootprintRect{}, fmt.Errorf("%w: rectangle origin (%d,%d), extent (%d,%d)", ErrPlacementOverflow, anchor.cellX, anchor.cellZ, extent.width, extent.depth)
	}
	return FootprintRect{anchor: anchor, extent: extent, maxX: int32(maxX), maxZ: int32(maxZ)}, nil
}

func (r FootprintRect) Anchor() FootprintAnchor { return r.anchor }
func (r FootprintRect) Extent() FootprintExtent { return r.extent }
func (r FootprintRect) MinX() int32             { return r.anchor.cellX }
func (r FootprintRect) MinZ() int32             { return r.anchor.cellZ }
func (r FootprintRect) MaxX() int32             { return r.maxX }
func (r FootprintRect) MaxZ() int32             { return r.maxZ }
func (r FootprintRect) Width() int32            { return r.extent.width }
func (r FootprintRect) Depth() int32            { return r.extent.depth }

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

func (p ModelWorldPosition) X() numeric.Fixed { return p.x }
func (p ModelWorldPosition) Y() numeric.Fixed { return p.y }
func (p ModelWorldPosition) Z() numeric.Fixed { return p.z }

// MobilePlacement carries all coordinate products of a mobile picked point:
// the snapped anchor, its validation rectangle, and the derived footprint
// midpoint used as the model/unit position.
type MobilePlacement struct {
	anchor FootprintAnchor
	rect   FootprintRect
	model  ModelWorldPosition
}

func (p MobilePlacement) Anchor() FootprintAnchor           { return p.anchor }
func (p MobilePlacement) Rect() FootprintRect               { return p.rect }
func (p MobilePlacement) ModelPosition() ModelWorldPosition { return p.model }

// FactoryPlacement carries the two intentionally independent factory
// products: the QueryBuildInfo model/world transform and the snapped
// footprint rectangle used for validation.
type FactoryPlacement struct {
	anchor FootprintAnchor
	rect   FootprintRect
	model  ModelWorldPosition
}

func (p FactoryPlacement) Anchor() FootprintAnchor           { return p.anchor }
func (p FactoryPlacement) Rect() FootprintRect               { return p.rect }
func (p FactoryPlacement) ModelPosition() ModelWorldPosition { return p.model }

// snapPlacementCell implements retail's signed arithmetic-shift formula:
// (picked - (extent << 19) + (1 << 19)) >> 20 [07 §9].
func snapPlacementCell(p numeric.Fixed, extent int32) (int32, error) {
	if extent <= 0 {
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
	if extent.width <= 0 || extent.depth <= 0 {
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
	if extent.width <= 0 || extent.depth <= 0 {
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

// FactoryPlacementFromQueryBuildInfo constructs a factory placement from all
// three authored QueryBuildInfo world coordinates.
func FactoryPlacementFromQueryBuildInfo(x, y, z numeric.Fixed, extent FootprintExtent) (FactoryPlacement, error) {
	return SnapFactoryPlacement(NewModelWorldPosition(x, y, z), extent)
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
		if !def.BMCode {
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
// at least 1 on each axis so a definition that authors neither still occupies a
// square [04 §6.2].
func FootprintForUnit(cat *content.Catalog, def *content.UnitDef) (footX, footZ int32) {
	if def == nil {
		return 1, 1
	}
	footX, footZ = def.FootprintX, def.FootprintZ
	if cat != nil && def.MovementClass != "" {
		if mc, ok := cat.Movement[content.CanonicalKey(def.MovementClass)]; ok && mc != nil {
			if mc.FootprintX > 0 {
				footX = mc.FootprintX
			}
			if mc.FootprintZ > 0 {
				footZ = mc.FootprintZ
			}
		}
	}
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	return footX, footZ
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
	// SkipTerrainAggregates marks a query from a caller outside the inline
	// terrain-check mode (mode value 1) [04 §6.4]: the bounds, unit-occupancy
	// and blocking-feature gates still apply, but the slope/height/water
	// aggregates do not. No retail caller in the single-player path passes a
	// mode other than 1 — the factory exit included [04 R-FAC-02 §4] — so
	// this is only for callers that already skip aggregates by domain.
	SkipTerrainAggregates bool
}

// PlacementResult contains the only derived value placement consumers need
// after legality succeeds. SiteHeight is the aggregate height published to a
// build order/ghost [07 §9][05 "Geothermal requirement"].
type PlacementResult struct {
	Rect       FootprintRect
	SiteHeight int32
}

// CheckPlacement is the one canonical, read-only placement legality function
// for preview, commit, AI, and factory exits. It checks the typed half-open
// rectangle before walking cells in row-major order, then applies class-
// specific feature/occupancy/yard and aggregate terrain gates [R-P0-08].
func (t *Terrain) CheckPlacement(q PlacementQuery) (PlacementResult, error) {
	if t == nil {
		return PlacementResult{}, fmt.Errorf("world: nil terrain")
	}
	if q.Rect.Width() <= 0 || q.Rect.Depth() <= 0 {
		return PlacementResult{}, fmt.Errorf("%w: rectangle dimensions %dx%d", ErrInvalidFootprint, q.Rect.Width(), q.Rect.Depth())
	}
	if q.Rect.MinX() < 0 || q.Rect.MinZ() < 0 || q.Rect.MaxX() > t.CellW || q.Rect.MaxZ() > t.CellH {
		return PlacementResult{}, fmt.Errorf("world: placement rectangle [%d,%d)x[%d,%d) out of bounds %dx%d", q.Rect.MinX(), q.Rect.MaxX(), q.Rect.MinZ(), q.Rect.MaxZ(), t.CellW, t.CellH)
	}
	plotArea, err := checkedPlacementArea(t.CellW, t.CellH)
	if err != nil {
		return PlacementResult{}, fmt.Errorf("world: invalid terrain dimensions: %w", err)
	}
	if t.Plot == nil || len(t.Plot) < plotArea {
		return PlacementResult{}, fmt.Errorf("world: terrain plot not initialized")
	}
	area, err := checkedPlacementArea(q.Rect.Width(), q.Rect.Depth())
	if err != nil {
		return PlacementResult{}, err
	}
	if !q.Mobile && len(q.Yard) != area {
		return PlacementResult{}, fmt.Errorf("world: yard length %d != rectangle %dx%d=%d", len(q.Yard), q.Rect.Width(), q.Rect.Depth(), area)
	}

	minLow, maxHigh, bit4Max := int32(255), int32(0), int32(0)
	geothermalNeeded, geothermalFound := false, false
	for dz := int32(0); dz < q.Rect.Depth(); dz++ {
		for dx := int32(0); dx < q.Rect.Width(); dx++ {
			idx := int(dz*q.Rect.Width() + dx)
			yard := YardCell(0)
			if q.Mobile {
				// Mobile placement checks occupancy/features and samples the
				// complete footprint, not an authored yard map [04 §6.1].
				yard = 0x2e // occupancy + blocking feature + slope + height
			} else {
				yard = q.Yard[idx]
			}
			cx, cz := q.Rect.MinX()+dx, q.Rect.MinZ()+dz
			cell := t.PlotAt(cx, cz)
			if cell == nil {
				return PlacementResult{}, fmt.Errorf("world: plot cell %d,%d out of range", cx, cz)
			}
			class, def := t.classifyCell(cx, cz)

			// TODO(question): yard bit 0's exact player/visibility alias and
			// mode matrix remain unresolved [R-P0-08][03 §3.2]. Keep the
			// authoritative visibility gate named but do not guess a player.
			if yard&0x06 != 0 {
				for _, occ := range [2]int16{cell.OccupantA(), cell.OccupantB()} {
					if occ != 0 && uint16(occ) != q.Self {
						return PlacementResult{}, fmt.Errorf("world: cell %d,%d occupied [04 §6.2]", cx, cz)
					}
				}
			}
			if yard&0x20 != 0 {
				switch class {
				case featureReal:
					if def.Blocking {
						return PlacementResult{}, fmt.Errorf("world: cell %d,%d blocked by feature %s [04 §6.2]", cx, cz, def.CanonicalKey)
					}
				case featureVoid:
					return PlacementResult{}, fmt.Errorf("world: cell %d,%d holds an occupied feature sentinel [04 §6.2]", cx, cz)
				}
			}
			if yard&0x40 != 0 && class == featureReal && def.Indestructible {
				return PlacementResult{}, fmt.Errorf("world: cell %d,%d holds an indestructible feature %s [04 §6.2]", cx, cz, def.CanonicalKey)
			}
			if yard&0x80 != 0 {
				geothermalNeeded = true
				if class == featureReal && def.Geothermal {
					geothermalFound = true
				}
			}

			// Building yards select the aggregate samples with bits 3/4;
			// mobile products sample every covered cell [R-P0-08].
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
		return PlacementResult{}, fmt.Errorf("world: geothermal requirement not satisfied [05 %q]", "Geothermal requirement")
	}

	sea := int32(t.SeaLevel)
	// Aggregate terrain legality only applies when the caller requests the
	// inline terrain-check mode [04 §6.4]; queries outside it (factory exit
	// spots) keep siteHeight from the plain bounds pass for allocation height.
	skipAggregates := q.SkipTerrainAggregates || q.Rules.Domain == content.MobilityAircraft || !q.Rules.ProfileResolved
	siteHeight := sea - q.Rules.Waterline
	if maxHigh >= minLow {
		siteHeight = minLow
		if !skipAggregates {
			water := sea > minLow
			limit := q.Rules.MaxSlope
			// Building yards use MaxSlope. Only the inline mobile path selects
			// MaxWaterSlope from the complete footprint's water state [04 §6.1].
			if q.Mobile && water {
				limit = q.Rules.MaxWaterSlope
			}
			if maxHigh-minLow > limit {
				return PlacementResult{}, fmt.Errorf("world: placement slope %d exceeds limit %d [04 §6.1]", maxHigh-minLow, limit)
			}
		}
	}
	if !skipAggregates && bit4Max > siteHeight {
		return PlacementResult{}, fmt.Errorf("world: placement height peak %d exceeds site height %d [05 %q]", bit4Max, siteHeight, "Geothermal requirement")
	}
	if !skipAggregates && minLow < sea-q.Rules.MaxWaterDepth {
		return PlacementResult{}, fmt.Errorf("world: placement water depth exceeds %d [05 %q]", q.Rules.MaxWaterDepth, "Geothermal requirement")
	}
	maxSample := maxHigh
	if bit4Max > maxSample {
		maxSample = bit4Max
	}
	// The upper waterline band is unconditional, including when the authored
	// value is zero. Land profiles use the established -10000 template value
	// to disable this gate [04 §6.1, §6.4][05 "Geothermal requirement"].
	if !skipAggregates && maxSample > sea-q.Rules.MinWaterDepth {
		return PlacementResult{}, fmt.Errorf("world: placement is deeper than minimum water depth %d [05 %q]", q.Rules.MinWaterDepth, "Geothermal requirement")
	}
	return PlacementResult{Rect: q.Rect, SiteHeight: siteHeight}, nil
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
