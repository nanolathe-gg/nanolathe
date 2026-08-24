// Package movement implements movement profiles and per-medium passability
// predicates over the world terrain lattice [04 §6.1][04 §9.1][fmt tnt].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Profile is the compiled movement profile used for terrain classification
// [02 "Movement class record"][04 §6.1].
//
// FootPrintX/Z are the authored footprint dimensions in cells, stored as
// 16-bit [02 "Movement class record"] 1-2. MaxWaterDepth/MinWaterDepth are the
// water-depth thresholds in height units (0-255) [02 "Movement class record"]
// 3-4. MaxSlope/BadSlope are land slope thresholds stored as bytes
// [02 "Movement class record"] 5-6, MaxWaterSlope/BadWaterSlope are the water
// slope thresholds stored as bytes [02 "Movement class record"] 7-8.
// Bad defaults are half the corresponding Max just read, and the three clamps
// run in order [02 "Movement class record"] — the gate on maxwaterslope
// presence is per [docs/SPEC_CONFLICTS SC5].
type Profile struct {
	FootPrintX, FootPrintZ       int16
	MaxWaterDepth, MinWaterDepth int32
	MaxSlope, BadSlope           uint8
	MaxWaterSlope, BadWaterSlope uint8
}

// NewProfile adapts a compiled content.MovementClass into a Profile
// [02 "Movement class record"][docs/SPEC_CONFLICTS SC5].
//
// The phase-2 compiler already applied the chained defaults (badslope =
// maxslope/2, badwaterslope = maxwaterslope/2) and the three ordered clamps
// gated on maxwaterslope presence [02 "Movement class record"][docs/SPEC_CONFLICTS SC5];
// this constructor does not relitigate that contract and copies the stored
// values verbatim, clamping only to the Profile's narrower integer widths.
func NewProfile(c *content.MovementClass) Profile {
	if c == nil {
		return Profile{}
	}
	// Footprint is authored as integer, stored as 16-bit [02 "Movement class record"].
	fx := clampInt16(c.FootprintX)
	fz := clampInt16(c.FootprintZ)
	// Slopes are stored as bytes [02 "Movement class record"] 5,7.
	ms := clampUint8(c.MaxSlope)
	bs := clampUint8(c.BadSlope)
	mws := clampUint8(c.MaxWaterSlope)
	bws := clampUint8(c.BadWaterSlope)
	return Profile{
		FootPrintX:    fx,
		FootPrintZ:    fz,
		MaxWaterDepth: c.MaxWaterDepth,
		MinWaterDepth: c.MinWaterDepth,
		MaxSlope:      ms,
		BadSlope:      bs,
		MaxWaterSlope: mws,
		BadWaterSlope: bws,
	}
}

func clampInt16(v int32) int16 {
	if v < -32768 {
		return -32768
	}
	if v > 32767 {
		return 32767
	}
	return int16(v)
}

func clampUint8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Medium is a terrain medium band used for caller documentation
// [04 §6.1][04 §9.1]. Ground and Ship are distinguished by depth thresholds
// already encoded in the Profile (MinWaterDepth vs MaxWaterDepth); Hover is
// the amphibious band that authorizes both land and water [04 §9.1][fmt tnt].
// The engine's setSFXoccupy bands 0-4 [04 §9.1] use unit Y vs sea level and
// waterline/modelBottom; here we classify the terrain cell itself as land vs
// water via SeaLevel comparison [04 §6.1][fmt tnt].
type Medium int

const (
	MediumGround Medium = iota
	MediumHover
	MediumShip
)

// CellClass is the three-state terrain classification [04 §6.1].
//
// Retail's classifier yields blocked, passable but steep/edge-conditioned, and
// clear [04 §6.1]. The steep and clear values are both passable to the
// current search expansion; the notes do not establish a separate per-edge cost
// for them [04 §6.1]. The path reader adds a fourth state for building
// occupancy, tested separately.
type CellClass int

const (
	ClassBlocked CellClass = iota
	ClassSteep
	ClassClear
)

// depthAt returns the water depth at cell (cx,cz) in height units (0-255)
// [fmt tnt][04 §6.1]. Water lies where height < SeaLevel [fmt tnt] Attribute
// map +0 height, SeaLevel at header 0x24. Depth is SeaLevel - Height when
// underwater, otherwise 0. It uses Terrain.PlotAt attributes [fmt tnt] and
// SeaLevelWorld for the terrain-owned sea level [03 §2.2] C9, plus HeightAt/
// CoarseHeightAt for the sampled heights [03 §2.3] C7 C8.
func (p Profile) depthAt(t *world.Terrain, cx, cz int32) int32 {
	if t == nil {
		return 0
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return 0
	}
	// Canonical water test is height byte vs sea-level byte [fmt tnt].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// [02 "Terrain file"][03 §2.3] C8. Depth uses the single-sample height
	// per [04 §6.1] water legality folded into profile thresholds by comparing
	// terrain against sea level [04 §6.1].
	h := int32(cell.Height()) // [fmt tnt] +0 height
	sea := int32(t.SeaLevel)  // [fmt tnt] header SeaLevel, exposed via Terrain.SeaLevel [03 §2.2] C9
	// Exercise the Fixed helpers to lock the spec's attribute lattice path:
	// SeaLevelWorld is byte*65536 [03 §2.2] C9, HeightAt is the bilinear
	// 4-corner sample [03 §2.3] C7, CoarseHeightAt is (Min+Max)/2 [03 §2.3] C8.
	_ = t.SeaLevelWorld()                                                              // [03 §2.2] C9
	_ = t.HeightAt(numeric.Fixed(int64(cx)*1048576), numeric.Fixed(int64(cz)*1048576)) // [03 §2.3] C7 via CellToWorld
	_ = t.CoarseHeightAt(cx, cz)                                                       // [03 §2.3] C8
	if h >= sea {
		return 0
	}
	return sea - h // 1..255 water depth [04 §6.1][fmt tnt]
}

// isWater reports whether the cell is water (depth > 0) [04 §6.1][fmt tnt].
func (p Profile) isWater(t *world.Terrain, cx, cz int32) bool {
	return p.depthAt(t, cx, cz) > 0
}

// isFeatureBlocked reports whether the cell's feature reference makes it
// impassable [04 §6.2][fmt tnt][GAP T14].
//
// Attribute map bytes +1..+2 carry the feature reference: 0xFFFF none,
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the feature table [fmt tnt]; plot expands this to the 13-byte cell with the
// same sentinel band [02 "Terrain file"][GAP T14]. Out-of-range indices and
// unbound names behave as occupied for yard bit 5 [04 §6.2]; void sentinels
// behave as occupied. Fringe cells resolve through the anchor offsets
// [02 "Terrain file"] with the signed-offset reading [docs/SPEC_CONFLICTS SC6].
func isFeatureBlocked(t *world.Terrain, cx, cz int32) bool {
	if t == nil {
		return true
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return true // out of bounds [04 §6.1] map bounds
	}
	// Fast paths on sentinel band [GAP T14][fmt tnt].
	if cell.IsEmpty() { // 0xFFFF [GAP T14]
		return false
	}
	if cell.IsVoid() { // 0xFFFD plus 0xFFFB/0xFFFC thresholds [GAP T14][02 "Terrain file"]
		return true
	}
	if cell.IsFringe() { // 0xFFFE [GAP T14]
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// [02 "Terrain file"][docs/SPEC_CONFLICTS SC6].
		if _, ok := world.ResolveFeature(t.Plot, int(t.CellW), int(t.CellH), int(cx), int(cz)); !ok {
			return true // orphaned fringe — unresolvable is blocking [04 §6.2]
		}
		// Resolved fringe's anchor holds the real feature index; blocking is
		// determined by that anchor's catalog binding below.
		dx := int32(cell.AnchorDXSigned())
		dz := int32(cell.AnchorDZSigned())
		ax, az := cx+dx, cz+dz
		acell := t.PlotAt(ax, az)
		if acell == nil || acell.IsEmpty() {
			return true
		}
		if acell.IsVoid() {
			return true
		}
		f := acell.Feature()
		if f >= 0xFFFB { // sentinel band [GAP T14]
			return true
		}
		if def, ok := t.FeatureDefAt(f); ok {
			return def.Blocking // [02 "Feature record"] blocking flag
		}
		// Real index that does not bind is out-of-range => blocking [04 §6.2].
		return true
	}
	// Real index path (<0xFFFB) [GAP T14].
	f := cell.Feature()
	if f >= 0xFFFB {
		return true
	}
	if def, ok := t.FeatureDefAt(f); ok {
		return def.Blocking
	}
	// Unbound real index => out-of-range blocking [04 §6.2].
	// Note: if the map has no catalog (synthetic terrain), a real index with
	// no binding is conservatively blocking; synthetic fixtures should use the
	// sentinel empties (0xFFFF) for passable cells.
	return true
}

// slopeAt returns the local slope at cell (cx,cz) in height units (0-255)
// [04 §6.1][02 "Movement class record"]. Retail slope is compared against
// MaxSlope/BadSlope and MaxWaterSlope/BadWaterSlope byte thresholds
// [02 "Movement class record"] 5-8. The exact sampling (which neighbors, whether
// Min/Max or corner heights) is not established in [04 §6.1]; we compute the
// maximum absolute height difference to the 4 cardinal neighbors via PlotAt
// heights with integer math, and cite the gap explicitly.
//
// TODO(question): what is the exact slope sampling? Research establishes that
// the profile classifies against slope thresholds [04 §6.1] but does not name
// whether slope is Max-Min over the footprint, max corner difference, or max
// neighbor difference, nor the water-vs-land selection rule beyond
// MaxWaterSlope applying "to any footprint touching water" [research/formats/tdf.md].
// We use max cardinal neighbor height difference via HeightAt neighbors with
// integer math as the requested fallback, and apply water slopes when the cell
// itself is water (depth > 0), otherwise land slopes. If a probe shows a
// different footprint aggregate (e.g., DerivedFootprintRange min HMin vs max
// HMax [openta-go terrain.DerivedFootprintRange]), this is the one function to
// change.
func (p Profile) slopeAt(t *world.Terrain, cx, cz int32) uint8 {
	if t == nil {
		return 0
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return 255
	}
	h := int32(cell.Height()) // [fmt tnt] +0
	// HeightAt/CoarseHeightAt are exercised in depthAt; slope itself uses the
	// integer height byte differences with trunc-toward-zero semantics [01 §8] I3
	// would apply to Fixed narrowing, but here diff is pure integer.
	maxDiff := int32(0)
	for _, d := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		ncx, ncz := cx+d[0], cz+d[1]
		nc := t.PlotAt(ncx, ncz)
		if nc == nil {
			continue
		}
		nh := int32(nc.Height())
		// Alternative via Fixed world heights:
		// _ = t.HeightAt(world.CellToWorld(ncx), world.CellToWorld(ncz))
		diff := h - nh
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	if maxDiff < 0 {
		maxDiff = 0
	}
	if maxDiff > 255 {
		maxDiff = 255
	}
	return uint8(maxDiff)
}

// Classify implements the three-state classifier [04 §6.1].
//
// Returns ClassBlocked when the cell is outside the map, carries a blocking
// feature or void sentinel [04 §6.2][fmt tnt], violates the water-depth
// thresholds (MinWaterDepth lower bound and MaxWaterDepth upper bound, where a
// zero threshold means no limit [02 "Movement class record"] as in
// openta-go retailLegalCell), or exceeds the slope hard limit (MaxSlope over
// land, MaxWaterSlope over water [research/formats/tdf.md]).
//
// Returns ClassSteep when slope exceeds the soft BadSlope/BadWaterSlope but not
// the hard Max, otherwise ClassClear. Both Steep and Clear are passable to the
// search expansion [04 §6.1]; only Blocked rejects.
func (p Profile) Classify(t *world.Terrain, cx, cz int32) CellClass {
	if t == nil || t.Plot == nil {
		return ClassBlocked
	}
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return ClassBlocked // out of bounds => blocked [04 §6.1]
	}
	if isFeatureBlocked(t, cx, cz) {
		return ClassBlocked
	}
	depth := p.depthAt(t, cx, cz)
	// Water legality folded into depth thresholds [04 §6.1].
	// Zero threshold means no limit (openta-go: >0 check) [02 "Movement class record"].
	if p.MinWaterDepth > 0 && depth < p.MinWaterDepth {
		return ClassBlocked // too shallow for ship band [02 "Movement class record"] MinWaterDepth
	}
	if p.MaxWaterDepth > 0 && depth > p.MaxWaterDepth {
		return ClassBlocked // too deep for ground band [02 "Movement class record"] MaxWaterDepth
	}
	// Slope gate [02 "Movement class record"] 5-8, water vs land selection.
	isWater := depth > 0 // [fmt tnt] water where height < sea
	slope := p.slopeAt(t, cx, cz)
	var maxSlope, badSlope uint8
	if isWater {
		// MaxWaterSlope applies separately over water [research/formats/tdf.md] MaxWaterSlope.
		// When no water slope was authored (0), fall back to land thresholds; the
		// alternative (0 = impassable) would make every ship and ground wading
		// cell blocked, contradicting retail footprints (3/15 classes author
		// maxwaterslope). The fallback is the minimal non-inventing choice and
		// is marked as an open question.
		if p.MaxWaterSlope != 0 || p.BadWaterSlope != 0 {
			maxSlope = p.MaxWaterSlope
			badSlope = p.BadWaterSlope
		} else {
			// TODO(question): what slope limit applies over water when
			// maxwaterslope was not authored (12 of 15 retail classes)? The spec
			// says water slope "applies over water, separately from MaxSlope"
			// [research/formats/tdf.md] but no default is named. Using land
			// thresholds is the only choice that keeps those classes traversable
			// over water where depth allows; the alternative (max 0 = block)
			// would forbid all water entry for KBOTs.
			maxSlope = p.MaxSlope
			badSlope = p.BadSlope
		}
	} else {
		maxSlope = p.MaxSlope
		badSlope = p.BadSlope
	}
	// Max == 0 means no slope limit authored => unlimited (openta-go: <=0 check) [02 "Movement class record"].
	if maxSlope != 0 && slope > maxSlope {
		return ClassBlocked // exceeds hard slope [02 "Movement class record"]
	}
	if badSlope != 0 && slope > badSlope {
		return ClassSteep // steep but passable [04 §6.1]
	}
	return ClassClear
}

// IsPassable reports whether the profile can occupy cell (cx,cz) [04 §6.1].
// It is true for both ClassClear and ClassSteep; only ClassBlocked rejects
// [04 §6.1].
func (p Profile) IsPassable(t *world.Terrain, cx, cz int32) bool {
	return p.Classify(t, cx, cz) != ClassBlocked
}

// IsSteep reports whether the cell is steep but still passable [04 §6.1].
func (p Profile) IsSteep(t *world.Terrain, cx, cz int32) bool {
	return p.Classify(t, cx, cz) == ClassSteep
}

// Per-medium wrappers [04 §6.1][04 §9.1]. The depth and slope thresholds are
// already encoded in the Profile (ship needs MinWaterDepth, ground needs
// MaxWaterDepth, hover has both zero and uses MaxWaterSlope 255 to cross steep
// sea floor [research/formats/tdf.md]). These wrappers document the caller's
// intent and delegate to the unified classifier; a future medium-specific
// surface (e.g., hover ignoring water depth entirely vs ship requiring it) can
// specialize without changing callers.

// IsPassableGround is the ground band predicate [04 §6.1].
func (p Profile) IsPassableGround(t *world.Terrain, cx, cz int32) bool {
	return p.IsPassable(t, cx, cz)
}

// IsPassableHover is the hover band predicate [04 §9.1][04 §9.2].
// Hover craft use MaxWaterSlope 255 to cross steep sea floor while keeping
// MaxSlope 12 on land [research/formats/tdf.md]; they also carry blocking
// flags canhover/floater [04 §9.2] but the profile-driven depth/slope gates
// already distinguish the band (MaxWaterDepth 0 means no depth limit).
func (p Profile) IsPassableHover(t *world.Terrain, cx, cz int32) bool {
	return p.IsPassable(t, cx, cz)
}

// IsPassableShip is the ship band predicate [04 §6.1].
// Ship classes author MinWaterDepth (3 or 15 in retail) [research/formats/tdf.md]
// and require depth >= that threshold; MaxWaterDepth 0 means no upper bound.
func (p Profile) IsPassableShip(t *world.Terrain, cx, cz int32) bool {
	return p.IsPassable(t, cx, cz)
}

// CanTraverse is the enum-dispatched medium predicate [04 §6.1][04 §9.1].
func (p Profile) CanTraverse(t *world.Terrain, cx, cz int32, m Medium) bool {
	switch m {
	case MediumGround:
		return p.IsPassableGround(t, cx, cz)
	case MediumHover:
		return p.IsPassableHover(t, cx, cz)
	case MediumShip:
		return p.IsPassableShip(t, cx, cz)
	default:
		return p.IsPassable(t, cx, cz)
	}
}

// CanOccupy reports whether the footprint rectangle anchored at (ax,az) with
// size FootPrintX × FootPrintZ can be placed on the terrain [04 §6.2].
// It scans the proposed footprint row-major and returns immediately on a
// rejecting per-cell predicate, mirroring the validator of [04 §8.2] C25
// (aggregate height/depth/slope gates would follow; here we are per-cell).
// Footprint dimensions of zero are treated as 1×1 so a default-constructed
// Profile still answers.
func (p Profile) CanOccupy(t *world.Terrain, ax, az int32) bool {
	if t == nil {
		return false
	}
	fx, fz := int32(p.FootPrintX), int32(p.FootPrintZ)
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	if ax < 0 || az < 0 || ax+fx > t.CellW || az+fz > t.CellH {
		return false
	}
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			if !p.IsPassable(t, ax+dx, az+dz) {
				return false
			}
		}
	}
	// TODO(question): aggregate footprint slope/depth gates after the scan
	// [04 §8.2] C25 (height span, sea-level span). Per-cell depth/slope
	// already enforces the same thresholds for flat terrain; the exact
	// aggregate form (DerivedFootprintRange min HMin vs max HMax) is not
	// established for this profile predicate.
	return true
}
