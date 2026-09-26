// The visibility predicate: C8, C9, C10 [PLAN_05 WU-05-3] P0-11.

package visibility

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// underwaterExempt is the runtime status bit that exempts a unit from the
// below-sea-level rejection [03 §3.2] C8 step 3 P0-11. The sensor phase sets it on
// owned and allied units via FriendlyMask 0x300 alias [03 §3.4] P0-11, which is why they never need the test.
const underwaterExempt uint32 = 0x200

// Target is the gameplay visibility query [03 §3.2] C8.
//
// X, Y and Z are the first hull probe in 16.16 world coordinates, formed by
// TargetFromBounds for units. The extents are the definition's full signed
// bounding spans, also 16.16 [06 §3.1][03 §3.2].
type Target struct {
	UnitID  uint16
	Owner   PlayerID
	X, Y, Z numeric.Fixed

	// Community off-map visibility uses the unit origin before TargetFromBounds
	// applies the hull offset. OffMap is the movement overlap filing's bucket
	// identity, not the flight integrator's vertical-bypass state. FootprintX/Z
	// and sizes are the unit's cached occupancy stamp [community patch engine
	// behavior CP-ENV-1(a)].
	OriginX, OriginY, OriginZ numeric.Fixed
	Flying                    bool
	OffMap                    bool
	FootprintX, FootprintZ    int32
	FootprintSizeX            int32
	FootprintSizeZ            int32

	// XExtent, YExtent and ZExtent are the three hull deltas of [03 §3.2]
	// step 5. They are three separate definition fields: YExtent is the height
	// decrement applied at the north sample and is neither ZExtent nor half of
	// the unit's height.
	XExtent numeric.Fixed
	YExtent numeric.Fixed
	ZExtent numeric.Fixed

	Hidden bool   // cloaked instance bit [03 §3.2] C8 step 2
	Status uint32 // runtime status bits; 0x200 underwater exemption [03 §3.2] C8
}

// Box is the feature extents query [03 §3.2] — the two-corner form used by the
// feature draw pass. Coordinates are 16.16 world units, like Target's.
type Box struct {
	MinX, MinZ numeric.Fixed
	MaxX, MaxZ numeric.Fixed
	Y          numeric.Fixed
	Owner      PlayerID
}

// pixel narrows a 16.16 world coordinate to its signed 16-bit map-pixel
// component, which is what the projection shifts [03 §3.2] C8 step 4.
//
// The narrowing is part of the contract, not a convenience: retail takes the
// high word of the 16.16 value as a signed 16-bit quantity, so a coordinate
// beyond ±32,768 map pixels wraps rather than saturating. Shifting the 16.16
// value directly is wrong by a factor of 65,536 and puts every unit outside
// the grid bounds.
func pixel(v numeric.Fixed) int32 {
	return int32(int16(int64(v) >> 16))
}

// IsVisible is the single gameplay gate [03 §3.2] C8 P0-11.
//
// Evaluation order: 1 owner bypass, 2 hidden reject, 3 below sea level with
// 0x200 exempt, 4 sample projection against the mode-selected source with a
// four-point hull [03 §3.2] [06 §3.1].
func (s *Service) IsVisible(viewer PlayerID, t Target) bool {
	if s == nil {
		return false
	}
	return s.rules().Visible(s, viewer, t)
}

// strictVisible is the retail unit predicate selected by StrictRules and the
// fallback of every community request the extension does not own.
func (s *Service) strictVisible(viewer PlayerID, t Target) bool {
	if s == nil {
		return false
	}
	return t.IsVisible(viewer, s.seaLevelWorld(), func(x, y, z numeric.Fixed) bool {
		return s.sample(viewer, x, y, z)
	})
}

// communityVisible preserves the first two retail gates, then either delegates
// the whole remaining request to retail or substitutes the nearest-cell answer
// for a qualifying aircraft [community patch engine behavior CP-ENV-1(a)].
func (s *Service) communityVisible(viewer PlayerID, t Target) bool {
	if !validPlayer(viewer) || !validPlayer(t.Owner) {
		return false
	}
	if viewer == t.Owner {
		return true
	}
	if t.Hidden {
		return false
	}
	// Only an aircraft's request reads the off-map filing below, so a ground
	// target skips the lookup (a quarter of the Modern AI observation's
	// predicate cost, docs/MODERN_AI_RESEARCH.md §5.1).
	if t.Flying && t.UnitID != 0 && s.Community.OffMap != nil {
		t.OffMap = s.Community.OffMap(t.UnitID)
	}
	if !t.Flying || (!t.OffMap && !s.originProjectionOutside(t)) || !s.footprintWithinCommunityMargin(t) {
		return s.strictVisible(viewer, t)
	}
	return s.sampleClampedOrigin(viewer, t.OriginX, t.OriginY, t.OriginZ)
}

// originPixel keeps the extension's full signed world-pixel component. This
// request is formed from a live unit origin and does not apply the retail
// predicate's signed-16-bit hull-coordinate narrowing [community patch engine
// behavior CP-ENV-1(a)].
func originPixel(v numeric.Fixed) int64 { return int64(int32(v)) >> 16 }

func (s *Service) originProjectionOutside(t Target) bool {
	if s == nil || s.W <= 0 || s.H <= 0 {
		return false
	}
	col := originPixel(t.OriginX) >> 5
	row := (originPixel(t.OriginZ) - (originPixel(t.OriginY) >> 1)) >> 5
	return col < 0 || col >= int64(s.W) || row < 0 || row >= int64(s.H)
}

// footprintWithinCommunityMargin measures Chebyshev tile distance from the
// cached whole stamp to the map rectangle. A partially overlapping stamp has
// distance zero [community patch engine behavior CP-ENV-1(a)].
func (s *Service) footprintWithinCommunityMargin(t Target) bool {
	if s == nil || s.terrain == nil || s.Community.OffMapAircraftMarginTiles <= 0 {
		return false
	}
	lastX := int64(s.terrain.CellW) - 1
	lastZ := int64(s.terrain.CellH) - 1
	x0, z0 := int64(t.FootprintX), int64(t.FootprintZ)
	x1 := x0 + int64(t.FootprintSizeX) - 1
	z1 := z0 + int64(t.FootprintSizeZ) - 1
	dx := int64(0)
	if x1 < 0 {
		dx = -x1
	} else if x0 > lastX {
		dx = x0 - lastX
	}
	dz := int64(0)
	if z1 < 0 {
		dz = -z1
	} else if z0 > lastZ {
		dz = z0 - lastZ
	}
	if dz > dx {
		dx = dz
	}
	return dx <= int64(s.Community.OffMapAircraftMarginTiles)
}

func (s *Service) sampleClampedOrigin(viewer PlayerID, x, y, z numeric.Fixed) bool {
	if s == nil || !validPlayer(viewer) || s.W <= 0 || s.H <= 0 {
		return false
	}
	col := originPixel(x) >> 5
	row := (originPixel(z) - (originPixel(y) >> 1)) >> 5
	if row < 0 || row >= int64(s.H) {
		trueRow := originPixel(z) >> 5
		if trueRow >= 0 && trueRow < int64(s.H) {
			row = trueRow
		}
	}
	if col < 0 {
		col = 0
	} else if col >= int64(s.W) {
		col = int64(s.W) - 1
	}
	if row < 0 {
		row = 0
	} else if row >= int64(s.H) {
		row = int64(s.H) - 1
	}
	idx := int(row*int64(s.W) + col)
	if s.mode&ModeCurrentEnabled != 0 {
		grid := s.byteGrids[viewer]
		return grid != nil && grid[idx] != 0
	}
	return s.wordMask[idx]&cellBit(s.local) != 0
}

// IsVisible applies the same ordered gameplay gate to live or committed hull
// inputs. The caller supplies only the mode-selected point sampler; ownership,
// hidden state, depth and the cumulative four-probe walk stay here [03 §3.2]
// [06 §3.1].
func (t Target) IsVisible(viewer PlayerID, seaLevel numeric.Fixed, sample func(x, y, z numeric.Fixed) bool) bool {
	if !validPlayer(viewer) || !validPlayer(t.Owner) {
		return false
	}
	// 1. owner identity bypass — queried record equals unit's owner ⇒ visible [C8.1] P0-11.
	// This precedes the cloak test, so a player always sees its own cloaked units.
	if viewer == t.Owner {
		return true
	}
	// 2. hidden/cloaked instance bit → false. Cloak is a predicate early-out,
	// never a mask edit; the decloak timer does not bypass this gate [03 §3.2]
	// [06 §3.1].
	if t.Hidden {
		return false
	}
	// 3. the first probe's height below sea level ⇒ not visible unless status
	// 0x200 [C8.3]. Unit callers seed the probe at min X/max Y/min Z from the
	// definition box [06 §3.1].
	// Sea level is the map header byte scaled to world units [03 §2.2] C9 —
	// not zero. Comparing against zero makes every unit between world Y 0 and
	// sea level wrongly visible on any map with a nonzero sea-level byte.
	if t.Status&underwaterExempt == 0 && t.Y < seaLevel {
		return false
	}
	// 4-5. The four hull samples ACCUMULATE: one coordinate triple is carried
	// through all four tests and each step mutates it [03 §3.2] C8 step 5.
	// That is why the last step subtracts the X extent "again". The resulting
	// figure is a rectangle in projected space, not a diamond about the base.
	x, y, z := t.X, t.Y, t.Z
	if sample(x, y, z) { // 0: centre
		return true
	}
	x += t.XExtent
	if sample(x, y, z) { // 1: east
		return true
	}
	y -= t.YExtent
	z += t.ZExtent
	if sample(x, y, z) { // 2: north, still carrying the east offset
		return true
	}
	x -= t.XExtent
	return sample(x, y, z) // 3: west, still carrying the north offset
}

// sample projects one world point and tests the mode-selected source
// [03 §3.2] C8 step 4.
func (s *Service) sample(viewer PlayerID, x, y, z numeric.Fixed) bool {
	if !validPlayer(viewer) {
		return false
	}
	// Half-height shear on the pixel components [03 §3.2] C8 step 4.
	u := int64(pixel(x) >> 5)
	v := int64((pixel(z) - (pixel(y) >> 1)) >> 5)
	// Unsigned bounds against the queried record's grid dimensions, so a
	// negative projection wraps high and fails rather than indexing backwards.
	if uint32(u) >= uint32(s.W) || uint32(v) >= uint32(s.H) {
		return false
	}
	idx := int(v*int64(s.W) + u)
	// Mode-selected source: the record's current-coverage byte grid when
	// current coverage is enabled (any nonzero count is visible), otherwise
	// the word grid at the LOCAL player's bit [03 §3.2] C8 step 4 — the
	// reader literal is 1<<localPlayer, not 1<<viewer, so a query on behalf
	// of another record still reads the local player's bit.
	//
	// Ally vision is never OR'd (C9): the writer sets only the source unit's
	// own slot bit and this reader tests only one bit, so allied coverage
	// cannot admit through either path.
	if s.mode&ModeCurrentEnabled != 0 {
		grid := s.byteGrids[viewer]
		return grid != nil && grid[idx] != 0
	}
	return s.wordMask[idx]&cellBit(s.local) != 0
}

// seaLevelWorld returns the terrain's sea level in world units, or zero when
// the service has no terrain (fixtures) [03 §2.2] C9.
func (s *Service) seaLevelWorld() numeric.Fixed {
	if s.terrain == nil {
		return 0
	}
	return s.terrain.SeaLevelWorld()
}

// VisiblePoint is the reduced one-point predicate used by projectiles
// [03 §3.2]. It has no owner, no hull and no cloak state — just the projection.
func (s *Service) VisiblePoint(viewer PlayerID, x, y, z numeric.Fixed) bool {
	if s == nil || !validPlayer(viewer) || s.W == 0 || s.H == 0 {
		return false
	}
	return s.sample(viewer, x, y, z)
}

// VisibleExtents is the two-corner feature predicate [03 §3.2]. The feature
// draw pass tests the footprint's opposite corners rather than a hull.
func (s *Service) VisibleExtents(viewer PlayerID, b Box) bool {
	if s == nil || !validPlayer(viewer) || !validPlayer(b.Owner) || s.W == 0 || s.H == 0 {
		return false
	}
	if viewer == b.Owner {
		return true
	}
	if s.sample(viewer, b.MinX, b.Y, b.MinZ) {
		return true
	}
	return s.sample(viewer, b.MaxX, b.Y, b.MaxZ)
}
