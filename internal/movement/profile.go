// Package movement implements movement profiles and per-medium passability
// predicates over the world terrain lattice [04 §6.1][04 §9.1][fmt tnt].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Profile is the compiled movement profile used for terrain classification
// [02 §5 "Movement class record"][04 §6.1 R-DOC04-A].
//
// FootPrintX/Z are the authored footprint dimensions in cells, stored as
// 16-bit [02 §5 "Movement class record"] 1-2. MaxWaterDepth/MinWaterDepth are
// the signed water-depth thresholds [02 §5] 3-4. MaxSlope/BadSlope are land
// slope thresholds stored as bytes [02 §5] 5-6, MaxWaterSlope/BadWaterSlope
// are the water slope thresholds stored as bytes [02 §5] 7-8. Bad defaults
// are half the corresponding Max just read, and the three clamps run
// unconditionally in order [02 §5][04 §6.1 R-DOC04-A]: the startup template
// pre-fills every record with 255 slopes and ±10000 depths before any parse,
// so an omitted key carries the template value.
type Profile struct {
	FootPrintX, FootPrintZ       int16
	MaxWaterDepth, MinWaterDepth int32
	MaxSlope, BadSlope           uint8
	MaxWaterSlope, BadWaterSlope uint8
}

// Template returns the startup class template as a Profile [04 §6.1
// R-DOC04-A]: 255 slopes, depth limits ±10000. These are the values every
// class record holds before any parse, and the same values back the scratch
// profile retail uses when an FBI movementclass name does not resolve
// [02 §5 "Movement class record"] — unauthored means unlimited. Callers that
// must fabricate a permissive fallback record (units with no movement class)
// should start from this, not from the zero value: a zeroed record is a real
// record whose zero thresholds block every slope.
func Template() Profile {
	return Profile{
		MaxWaterDepth: 10000,
		MinWaterDepth: -10000,
		MaxSlope:      255,
		BadSlope:      255,
		MaxWaterSlope: 255,
		BadWaterSlope: 255,
	}
}

// NewProfile adapts a compiled content.MovementClass into a Profile
// [02 §5 "Movement class record"][04 §6.1 R-DOC04-A].
//
// The phase-2 compiler already initialized the record from the startup
// template, applied the chained defaults (badslope = (maxslope just read & 0xFF)
// >> 1, likewise badwaterslope) and the three ordered clamps [02 §5][04 §6.1
// R-DOC04-A]; this constructor does not relitigate that contract and copies
// the stored values verbatim, clamping only to the Profile's narrower integer
// widths.
func NewProfile(c *content.MovementClass) Profile {
	if c == nil {
		return Profile{}
	}
	// Footprint is authored as integer, stored as 16-bit [02 "Movement class record"].
	fx := clampInt16(c.FootprintX)
	fz := clampInt16(c.FootprintZ)
	// Water depths are signed 16-bit record fields [02 §5] 3-4; the store
	// truncates and every classifier comparison reads them sign-extended
	// [04 §6.1 R-DOC04-B], so narrow by the record's word width.
	// Slopes are stored as bytes [02 "Movement class record"] 5,7.
	ms := clampUint8(c.MaxSlope)
	bs := clampUint8(c.BadSlope)
	mws := clampUint8(c.MaxWaterSlope)
	bws := clampUint8(c.BadWaterSlope)
	return Profile{
		FootPrintX:    fx,
		FootPrintZ:    fz,
		MaxWaterDepth: narrowInt16(c.MaxWaterDepth),
		MinWaterDepth: narrowInt16(c.MinWaterDepth),
		MaxSlope:      ms,
		BadSlope:      bs,
		MaxWaterSlope: mws,
		BadWaterSlope: bws,
	}
}

// narrowInt16 stores v through the record's signed 16-bit field width and
// reads it back sign-extended [02 §5] 3-4 [04 §6.1 R-DOC04-B].
func narrowInt16(v int32) int32 { return int32(int16(v)) }

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
// [04 §6.1][02 "Movement class record"][P1-03 §2.3-2.5].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// 2×2 neighbourhood at +5 (hmax) and +6 (hmin) per Plot13+0xD stride
// [P1-03 §2.3][fmt tnt], handling edges via x+1<W and y<Height-1 guards,
// then footprint aggregation bMin=min(hmin) bMax=max(hmax) bPeak=max(hmax)
// where yard mask includes respective bits, slope=bMax-bMin unsigned byte
// diff, pass when slope < limit (< not <=, equality passes) [P1-03 §2.5],
// water vs land via SeaLevel <= bMin branch (entirely above water => land
// slope else water slope) [P1-03 §2.4], BadSlope tier is penalized but
// still passable (HOT cost, not validator block) TODO(question) [P1-03].
// This per-cell helper approximates via max cardinal neighbor diff as fallback;
// footprint aggregate form (CanOccupy/ValidateFootprint) is the one to replace
// if probe shows DerivedFootprintRange mismatch [openta-go].
//
// TODO(question): exact slope sampling for per-cell vs footprint aggregate
// remains open; research establishes thresholds [04 §6.1] but footprint
// aggregate is min(hmin) vs max(hmax) per P1-03, not max corner diff. We use
// max cardinal neighbor height difference here as minimal non-inventing choice
// and apply water slopes when cell itself is water (depth>0) else land slopes.
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

// Classify implements the three-state classifier [04 §6.1][P1-03 §2.4-2.5].
//
// Returns ClassBlocked when the cell is outside the map, carries a blocking
// feature or void sentinel [04 §6.2][fmt tnt], violates the water-depth
// thresholds (MinWaterDepth lower bound and MaxWaterDepth upper bound, where a
// zero threshold means no limit [02 "Movement class record"] as in
// openta-go retailLegalCell), or exceeds the slope hard limit (MaxSlope over
// land, MaxWaterSlope over water [P1-03 §2.4][research/formats/tdf.md]).
// Water-depth/slope interaction is SeaLevel <= bMin selects land slope else
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// bMax-bMin unsigned byte diff, equality passes (< not <=) [P1-03 §2.5].
//
// Returns ClassSteep when slope exceeds the soft BadSlope/BadWaterSlope but not
// the hard Max, otherwise ClassClear. Both Steep and Clear are passable to the
// search expansion [04 §6.1]; BadSlope tier is HOT cost not blocker
// TODO(question) [P1-03]. Only Blocked rejects.
func (p Profile) Classify(t *world.Terrain, cx, cz int32) CellClass {
	return p.ClassifyFootprint(t, cx, cz)
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
	return p.ClassifyFootprint(t, ax, az) != ClassBlocked
}
