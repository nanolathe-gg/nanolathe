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
// [02 §5 "Movement class record"] — unauthored means unlimited. Unit
// consumers should use NewScratchProfile so their complete FBI record remains
// unit-local; a zeroed Profile is only an absent/uninitialized surface.
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

// NewScratchProfile adapts the complete movement scratch record retained on a
// unit definition into the movement Profile used by all consumers. The unit
// compiler fills these fields from the FBI's movement keys using the startup
// template and the movement-record width conversions; an unresolved or blank
// movementclass therefore still has a unit-local profile [02 §5 "Movement
// class record"][04 §6.1 R-DOC04-A].
func NewScratchProfile(d *content.UnitDef) Profile {
	if d == nil {
		return Profile{}
	}
	return Profile{
		FootPrintX:    clampInt16(d.FootprintX),
		FootPrintZ:    clampInt16(d.FootprintZ),
		MaxWaterDepth: narrowInt16(d.MaxWaterDepth),
		MinWaterDepth: narrowInt16(d.MinWaterDepth),
		MaxSlope:      clampUint8(d.MaxSlope),
		BadSlope:      clampUint8(d.BadSlope),
		MaxWaterSlope: clampUint8(d.MaxWaterSlope),
		BadWaterSlope: clampUint8(d.BadWaterSlope),
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
	// The height byte lives on PlotCell (Height()) [02 "Terrain file"][fmt tnt];
	// the derived Min/Max pair (MinHeight()/MaxHeight()) is averaged by
	// CoarseHeightAt [02 "Terrain file"][03 §2.3] C8. Depth uses the single-sample height
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
// The attribute map's feature-reference field carries: 0xFFFF none,
// 0xFFFE fringe (anchor stored as a signed DX/DZ delta pair), 0xFFFD void
// hole, otherwise index into the feature table [fmt tnt]; plot expands this
// to the 13-byte cell with the
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
		// Resolve through the anchor's signed int8 DX/DZ deltas
		// (AnchorDXSigned/AnchorDZSigned) [02 "Terrain file"][docs/SPEC_CONFLICTS SC6].
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

// Slope reaches the movement COST through one value and one only: the class
// layer's stamped 2-bit tier at the candidate anchor [04 R-SLOPE-01 §5].
//
// The search's single slope-dependent term is `terrainTerm = (passability > 1)
// ? 0 : 30`, read off the stamped layer — see internal/path.SteepCost and the
// layer binding in integrate.go. That stamped value is the per-cell tier of
// classifyCell (each cell on its OWN derived maximum/minimum pair, medium
// split on `hmin < seaLevel`, `slope <= Bad` -> 3, `slope > Max` -> 0, else 1)
// taken as the MINIMUM over the footprint and demoted from 3 to 1 when any
// cell of the one-cell ring is below 3 — ClassifyFootprint and
// ClassLayer.classify in profile_footprint.go and layer.go.
//
// There is no separate cost sampling, no footprint aggregate of heights for
// cost, and no "max cardinal neighbour difference" anywhere. The magnitude
// never reaches the cost: a slope one above `Bad` and a slope equal to `Max`
// both cost the same 30. Tier 1 is passable and costs 30; tiers 3 and 2 cost
// nothing extra; a stamped 0 is costed only when the cell carries the
// ray-visited bit, and then also pays the 30.
//
// Nor is there a slope term in the mover: the speed update takes no terrain
// slope input, and what [04 R-MOV-01 §5] calls a slope speed penalty is the
// pitch cap of [R-MOV-01 §4], fed by the four-corner conform's pitch, on
// non-`upright`, non-`floater` ground movers only.
//
// Retired here (WU-19-68): a `slopeAt` helper that returned the maximum
// cardinal neighbour height difference, kept as "the minimal non-inventing
// choice" while the sampling was open, together with the two markers asking
// which sampling cost used. It had no caller — the layer had already taken
// over — and §5 names it as the site to replace, not to keep.

// Classify implements the three-state classifier [04 §6.1][04 R-SLOPE-01 §2].
//
// It is ClassifyFootprint under another name, so the rule is that function's:
// each covered cell on its OWN derived maximum/minimum pair, the minimum tier
// over the footprint, and a clear result demoted to steep unless the one-cell
// ring is clear too. Blocked comes from a cell outside the map, a blocking
// feature or void sentinel [04 §6.2][fmt tnt], a depth outside the record's
// band (`hmin < seaLevel − MaxWaterDepth` or `hmax > seaLevel − MinWaterDepth`,
// the startup template's ±10000 standing in for an omitted key), or a slope
// above the hard limit. The medium split is per cell, `hmin >= seaLevel`
// selecting the land pair and otherwise the water pair.
//
// Returns ClassSteep when the slope exceeds the soft BadSlope/BadWaterSlope but
// not the hard Max, otherwise ClassClear. Both are passable; only Blocked
// rejects.
//
// The marker retired here (WU-19-68) asked whether the BadSlope tier was a
// cost or a blocker. [04 R-SLOPE-01 §5] answers it: tier 1 is passable and
// costs the search's flat 30, and that 30 is the whole of slope's contribution
// to cost — the magnitude never reaches it. The two sentences this comment
// used to carry about a per-footprint `bMax−bMin` aggregate and a `<`-not-`<=`
// comparison described the structure placement validator's yard-map walk, not
// this classifier, and are withdrawn.
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
