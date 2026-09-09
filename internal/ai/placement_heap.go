package ai

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// MetalSpot is the logical eight-byte battle-entry vector record. Metal is
// copied from authored feature data and overwritten only in a helper-local
// working copy when it becomes the heap key [08 R-AI-03 §1, §3].
type MetalSpot struct {
	CellX int16
	CellZ int16
	Metal float32
}

const (
	placementGrowthWorld = int32(160) // world units [08 R-AI-03 §2]
	placementTrialCount  = 30         // strict t < 30 [08 R-AI-03 §4]
	metalFeatureLimit    = uint16(0xfffb)
)

// BuildMetalSpots scans feature anchors in row-major order. It is called by
// the battle-entry tail, not by strategic refresh [08 R-AI-03 §1].
func BuildMetalSpots(terrain *world.Terrain) []MetalSpot {
	return appendMetalSpots(nil, terrain)
}

func appendMetalSpots(spots []MetalSpot, terrain *world.Terrain) []MetalSpot {
	if terrain == nil {
		return spots
	}
	for z := int32(0); z < terrain.CellH; z++ {
		for x := int32(0); x < terrain.CellW; x++ {
			cell := terrain.PlotAt(x, z)
			if cell == nil {
				continue
			}
			feature := cell.Feature()
			if feature >= metalFeatureLimit {
				continue
			}
			def, ok := terrain.FeatureDefAt(feature)
			if !ok || def.Metal == 0 || !def.Indestructible {
				continue
			}
			spots = append(spots, MetalSpot{CellX: int16(x), CellZ: int16(z), Metal: float32(uint16(def.Metal))})
		}
	}
	return spots
}

// InitializeMetalSpots retains vector capacity while replacing the snapshot.
// Session composition calls it once for each AI state [08 R-AI-03 §1].
func (s *Strategic) InitializeMetalSpots(terrain *world.Terrain) {
	if s == nil {
		return
	}
	s.MetalSpots = s.MetalSpots[:0]
	s.MetalSpots = appendMetalSpots(s.MetalSpots, terrain)
}

type retailPlacementPoint struct {
	x numeric.Fixed
	y numeric.Fixed
	z numeric.Fixed
}

func placementIntegerSqrt(v uint64) uint64 {
	// Restoring integer sqrt is trunc(sqrt(v)) without choosing a host float
	// precision for the recovered x87 operation [08 R-AI-03 §2].
	var root uint64
	bit := uint64(1) << 62
	for bit > v {
		bit >>= 2
	}
	for bit != 0 {
		if v >= root+bit {
			v -= root + bit
			root = (root >> 1) + bit
		} else {
			root >>= 1
		}
		bit >>= 2
	}
	return root
}

// retailPlacementOrigin performs the three-axis inverted branch. Radius is a
// plain world-unit count; positions and distance are 16.16 [08 R-AI-03 §2].
func retailPlacementOrigin(builder, center retailPlacementPoint, radius int32) retailPlacementPoint {
	dx := int64(int32(center.x - builder.x))
	dy := int64(int32(center.y - builder.y))
	dz := int64(int32(center.z - builder.z))
	dist := int64(placementIntegerSqrt(uint64(dx*dx) + uint64(dy*dy) + uint64(dz*dz)))
	r := int64(radius) << 16
	if !(r < dist) {
		return center
	}
	scale := (r << 16) / dist
	step := func(base numeric.Fixed, delta int64) numeric.Fixed {
		// Retail narrows the interpolated term and performs the final add in the
		// destination's signed 32-bit 16.16 word [08 R-AI-03 §2].
		return numeric.Fixed(int32(base) + int32((delta*scale)>>16))
	}
	return retailPlacementPoint{x: step(builder.x, dx), y: step(builder.y, dy), z: step(builder.z, dz)}
}

type retailPlacementDef struct {
	def          *content.UnitDef
	extent       world.FootprintExtent
	yard         []world.YardCell
	rules        world.PlacementRules
	footX, footZ int32
}

func resolveRetailPlacementDef(m *Manager, defKey string) (retailPlacementDef, PlacementResult) {
	def, ok := m.Catalog.Unit(canonicalKey(defKey))
	if !ok || def == nil {
		return retailPlacementDef{}, placementFailure(HelperNone, ReasonMissingDefinition, fmt.Sprintf("unit definition %q unavailable", defKey))
	}
	if (def.FootprintX <= 0 || def.FootprintZ <= 0) && strings.TrimSpace(def.MovementClass) == "" {
		return retailPlacementDef{}, placementFailure(HelperNone, ReasonInvalidFootprint, fmt.Sprintf("unit %q has invalid authored footprint", defKey))
	}
	footX, footZ := world.FootprintForUnit(m.Catalog, def)
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		return retailPlacementDef{}, placementFailure(HelperNone, ReasonInvalidFootprint, err.Error())
	}
	var yard []world.YardCell
	if def.BMCode == 0 {
		if strings.TrimSpace(def.YardMap) == "" {
			return retailPlacementDef{}, placementFailure(HelperNone, ReasonMissingDefinition, fmt.Sprintf("unit %q has no placement yard", defKey))
		}
		yard, err = world.ParseYardMap(def.YardMap, int(footX), int(footZ))
		if err != nil {
			return retailPlacementDef{}, placementFailure(HelperNone, ReasonMissingDefinition, err.Error())
		}
	}
	rules, err := world.PlacementRulesForUnit(m.Catalog, def)
	if err != nil {
		return retailPlacementDef{}, placementFailure(HelperNone, ReasonMissingDefinition, err.Error())
	}
	return retailPlacementDef{def: def, extent: extent, yard: yard, rules: rules, footX: footX, footZ: footZ}, PlacementResult{}
}

func retailOriginCell(origin numeric.Fixed, footprint int32) int16 {
	return int16((int64(int32(origin)) - (int64(footprint) << 19) + (1 << 19)) >> 20)
}

func retailPlacementScore(terrain *world.Terrain, rect world.FootprintRect) (int32, error) {
	var score int32
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := terrain.PlotAt(x, z)
			if cell == nil {
				return 0, fmt.Errorf("ai: terrain cell %d,%d unavailable", x, z)
			}
			score += int32(cell.Metal())
		}
	}
	return score, nil
}

func validateRetailAICandidate(terrain *world.Terrain, pd retailPlacementDef, x, z int32, exhaustive bool) (world.FootprintRect, int32, ReasonCode, error) {
	// The blocker's entry test is "column > 0" on the signed column word AND
	// "the packed cell word, read as an unsigned 32-bit value, exceeds 0xffff",
	// which holds exactly when the row's sixteen-bit pattern is non-zero. Row 0
	// is therefore rejected the same way column 0 is [08 R-AI-03 §7.1].
	if x+pd.footX >= terrain.CellW || z+pd.footZ >= terrain.CellH || (!exhaustive && (x < 0 || z < 0)) || (exhaustive && (x <= 0 || z == 0)) {
		return world.FootprintRect{}, 0, ReasonOutOfBounds, fmt.Errorf("ai: candidate %d,%d outside strict AI bounds", x, z)
	}
	if exhaustive && z < 0 {
		// A negative row passes retail's row test and the signed height test,
		// and the walk then addresses cells before the plot grid's first cell —
		// one row of storage per unit of negative row — with no guard. Whether
		// that storage is mapped, so the walk returns a garbage verdict rather
		// than faulting, depends on where the process allocator placed the grid
		// and is Unknown; nothing in the executable decides it [08 R-AI-03
		// §7.1]. Rejecting is Nanolathe's deterministic choice, recorded as
		// that choice and not as retail's behavior.
		return world.FootprintRect{}, 0, ReasonOutOfBounds, fmt.Errorf("ai: negative exhaustive row %d has no retail verdict; Nanolathe rejects deterministically", z)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(x, z), pd.extent)
	if err != nil {
		return world.FootprintRect{}, 0, ReasonOutOfBounds, err
	}
	// Both helpers dispatch on the same byte: a `bmcode == 0` definition (every
	// building) goes to the yard-map blocker, which reads the authored yard
	// bytes and writes the footprint accumulator; a `bmcode != 0` definition
	// (mobile) walks the footprint with the plain rule set. The scatter helper
	// is no exception — [08 R-AI-03 §4]'s two class labels were inverted when
	// first written and were corrected 2026-09-02, and [08 R-AI-03 §7.4] lists
	// the four readers that agree. Mobile=true selects the plain branch of the
	// repository's one canonical world validator.
	yard, plainFootprint := pd.yard, pd.def.BMCode != 0
	_, err = terrain.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: pd.rules, Self: 0, Mobile: plainFootprint})
	if err != nil {
		return rect, 0, ReasonBlocked, err
	}
	score, err := retailPlacementScore(terrain, rect)
	if err != nil {
		return rect, 0, ReasonBlocked, err
	}
	return rect, score, ReasonSuccess, nil
}

func metalSpotLess(a, b MetalSpot) bool { return a.Metal < b.Metal }

// siftMetalSpot reproduces the recovered make/pop-heap hole algorithm. Equal
// children select the right child, unlike a stable distance sort
// [08 R-AI-03 §3].
func siftMetalSpot(spots []MetalSpot, root, n int) {
	if root >= n {
		return
	}
	saved := spots[root]
	hole := root
	for child := 2*hole + 1; child < n; child = 2*hole + 1 {
		right := child + 1
		if right < n && !metalSpotLess(spots[right], spots[child]) {
			child = right
		}
		spots[hole] = spots[child]
		hole = child
	}
	for hole > root {
		parent := (hole - 1) / 2
		if !metalSpotLess(spots[parent], saved) {
			break
		}
		spots[hole] = spots[parent]
		hole = parent
	}
	spots[hole] = saved
}

func makeMetalSpotHeap(spots []MetalSpot) {
	for i := len(spots)/2 - 1; i >= 0; i-- {
		siftMetalSpot(spots, i, len(spots))
	}
}

func popMetalSpot(spots *[]MetalSpot) MetalSpot {
	h := *spots
	last := len(h) - 1
	h[0], h[last] = h[last], h[0]
	if last > 0 {
		siftMetalSpot(h, 0, last)
	}
	out := h[last]
	*spots = h[:last]
	return out
}

func retailExtractorHelperA(m *Manager, pd retailPlacementDef, origin retailPlacementPoint, radius int32, terrain *world.Terrain) PlacementResult {
	res := PlacementResult{Helper: HelperA, FootX: int(pd.footX), FootZ: int(pd.footZ)}
	if len(m.Strategic.MetalSpots) == 0 {
		res.Reason = ReasonNoPatchData
		res.Proof = fmt.Errorf("ai: placement: empty battle-entry metal vector [08 R-AI-03 §3]")
		return res
	}
	cx, cz := retailOriginCell(origin.x, pd.footX), retailOriginCell(origin.z, pd.footZ)
	d := radius * 4
	disc := d * d
	work := make([]MetalSpot, 0, len(m.Strategic.MetalSpots))
	for _, spot := range m.Strategic.MetalSpots {
		dx, dz := int32(spot.CellX)-int32(cx), int32(spot.CellZ)-int32(cz)
		d2 := dx*dx + dz*dz
		if d2 <= disc {
			spot.Metal = float32(-d2)
			work = append(work, spot)
		}
	}
	makeMetalSpotHeap(work)
	best, firstD2 := int32(0), int32(-1)
	var bestX, bestZ int32
	for len(work) != 0 {
		spot := popMetalSpot(&work)
		x := int32(int16(int32(spot.CellX) - (pd.footX-3)/2))
		z := int32(int16(int32(spot.CellZ) - (pd.footZ-3)/2))
		dx, dz := x-int32(cx), z-int32(cz)
		c2 := dx*dx + dz*dz
		if firstD2 >= 0 && c2 > firstD2+160 {
			break
		}
		res.Attempts++
		_, score, reason, err := validateRetailAICandidate(terrain, pd, x, z, true)
		if err != nil {
			res.TrialReasons = append(res.TrialReasons, reason)
			continue
		}
		if score > best {
			best, bestX, bestZ = score, x, z
			if firstD2 == -1 {
				firstD2 = c2
			}
		}
	}
	if best == 0 {
		res.Reason = ReasonBlocked
		res.Proof = fmt.Errorf("ai: placement: exhaustive candidates produced no positive metal score")
		return res
	}
	res.Valid, res.Reason = true, ReasonSuccess
	res.CellX, res.CellZ, res.Score = bestX, bestZ, best
	res.WorldX = placementWorldCoordinate(bestX, pd.footX)
	res.WorldZ = placementWorldCoordinate(bestZ, pd.footZ)
	return res
}

func retailSignedDraw(m *Manager, bound int32) int32 {
	if bound < 2 {
		return 0 // no stream advance [01 §7.1]
	}
	return int32(m.RNG.Uint32n(uint32(bound)))
}

func retailScatterCell(q int32, cell int16, offset int16, draw int32) int32 {
	// The lattice expression is narrowed to a signed word before it reaches
	// validation and output [08 R-AI-03 §4]. Division truncates toward zero.
	return int32(int16((q/int32(cell))*int32(cell) + int32(offset) + draw))
}

func retailExtractorHelperB(m *Manager, pd retailPlacementDef, origin retailPlacementPoint, radius, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	res := PlacementResult{Helper: HelperB, FootX: int(pd.footX), FootZ: int(pd.footZ)}
	if !m.Strategic.setupDrawsReady {
		res.Reason = ReasonMissingStrategicState
		res.Proof = fmt.Errorf("ai: placement: strategic region words were not initialized")
		return res
	}
	region, margin := m.Strategic.LandRegion, int32(3)
	if pd.rules.MinWaterDepth >= 0 {
		region, margin = m.Strategic.WaterRegion, 6
	}
	limit := surfaceMetal
	limit *= pd.footZ
	limit *= pd.footX
	limit *= 2
	res.Limit = limit
	for trial := 0; trial < placementTrialCount; trial++ {
		r := retailSignedDraw(m, radius)
		a := retailSignedDraw(m, 65536)
		wx := origin.x - numeric.Fixed(numeric.MulRound(numeric.Sin(numeric.Angle(uint16(a))), r<<16))
		wz := origin.z - numeric.Fixed(numeric.MulRound(numeric.Cos(numeric.Angle(uint16(a))), r<<16))
		qx, qz := int32(retailOriginCell(wx, pd.footX)), int32(retailOriginCell(wz, pd.footZ))
		cellW, cellH := int32(region.CellW), int32(region.CellH)
		ox := retailSignedDraw(m, cellW-margin-pd.footX)
		gx := retailScatterCell(qx, region.CellW, region.OffsetX, ox)
		oz := retailSignedDraw(m, cellH-margin-pd.footZ)
		gz := retailScatterCell(qz, region.CellH, region.OffsetZ, oz)
		res.Attempts = trial + 1
		_, score, reason, err := validateRetailAICandidate(terrain, pd, gx, gz, false)
		if err != nil {
			res.TrialReasons = append(res.TrialReasons, reason)
			continue
		}
		// The trial footprint's own metal-byte sum IS retail's test. The
		// building branch runs the yard-map blocker, which clears the
		// accumulator on entry and adds every footprint cell's metal byte, so a
		// trial that passes leaves exactly this sum there; a rejected trial is
		// skipped before the comparison. The earlier note here — that retail
		// read a stale process-global accumulator and that comparing the
		// trial's own sum was a sanctioned divergence — followed from the
		// inverted class labels and is withdrawn [08 R-AI-03 §4 correction].
		if score > limit { // inclusive acceptance: accumulator <= limit
			res.TrialReasons = append(res.TrialReasons, ReasonMetalScoreExceeded)
			continue
		}
		res.Valid, res.Reason = true, ReasonSuccess
		res.CellX, res.CellZ, res.Score = gx, gz, score
		res.WorldX = placementWorldCoordinate(gx, pd.footX)
		res.WorldZ = placementWorldCoordinate(gz, pd.footZ)
		return res
	}
	res.Reason = ReasonTooManyTrials
	res.Proof = fmt.Errorf("ai: placement: scatter exhausted %d trials", placementTrialCount)
	return res
}
