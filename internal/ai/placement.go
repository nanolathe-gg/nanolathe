package ai

import (
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// HelperKind identifies which placement helper produced the result [P0-03].
type HelperKind int

const (
	HelperNone HelperKind = iota
	HelperA               // exhaustive patch helper [P0-03]
	HelperB               // statistical scatter helper [P0-03]
)

// ReasonCode records why a placement attempt succeeded or failed [P0-03][RS-11].
// Deterministic: recording a reason never consumes an extra RNG draw [I4].
type ReasonCode int

const (
	ReasonNone ReasonCode = iota
	ReasonSuccess
	ReasonNoPatchData        // helper A unavailable: no established metal patch vector [P0-03 §6][RS-11]
	ReasonBlocked            // yard/occupancy rejected by the canonical world validator [P0-03]
	ReasonMetalScoreExceeded // score > SurfaceMetal*footX*footZ*2 [P0-03 §3.3]
	ReasonOutOfBounds
	ReasonTooManyTrials // 30 trials exhausted [P0-03 §3.3]
	ReasonInvalidFootprint
	ReasonNilManager
	ReasonEmptyDef
	ReasonMissingCatalog
	ReasonMissingTerrain
	ReasonMissingDefinition
	ReasonMissingRNG
	ReasonMissingBuilder
	ReasonMissingQueue
	ReasonQueueFailed
	ReasonUnknownGeometry
)

// PlacementResult is the exact, typed placement result [RS-11][P0-03].
// It contains the validated world and cell site, footprint, score, helper
// identity and validation proof. The queue must use exactly this site
// bit-for-bit, not the strategic origin [RS-11][P0-03].
type PlacementResult struct {
	Valid  bool
	Helper HelperKind
	Reason ReasonCode
	// Exact site that passed validation [P0-03 §3.1] fixed-point 16.16 world and cell anchor.
	WorldX   numeric.Fixed
	WorldZ   numeric.Fixed
	CellX    int32
	CellZ    int32
	FootX    int
	FootZ    int
	Score    int32
	Limit    int32
	Attempts int
	// TrialReasons records reason per failed trial without changing RNG draws [RS-11][I4].
	TrialReasons []ReasonCode
	Proof        error // nil when Valid, otherwise validation error
}

func placementFailure(helper HelperKind, reason ReasonCode, detail string) PlacementResult {
	return PlacementResult{Valid: false, Helper: helper, Reason: reason, Proof: fmt.Errorf("ai placement: %s", detail)}
}

// placementWorldCoordinate converts a validated footprint anchor to the
// mobile unit's model coordinate. Validation uses the north-west anchor, while
// the order carries the footprint midpoint [04 §6.2–§6.4].
func placementWorldCoordinate(outputCell, footprint int32) numeric.Fixed {
	return numeric.Fixed((int64(footprint) + 2*int64(outputCell)) * (worldUnitsPerCell / 2))
}

// worldUnitsPerCell is 16*65536 = 1<<20 [03 §2.1].
const worldUnitsPerCell = 1 << 20 // 1048576

// stepTowardCenter moves the search origin toward the strategic center using the
// stored search radius [08 "Established AI-facing data and rooted planner"] [PLAN_11 C8][P0-03].
//
// Retail "moves the search origin toward the strategic center using the stored
// radius" via a square-root distance followed by fixed-point scaling [P0-03 §3.1].
// This implements the same arithmetic with int64 Fixed math and a math.Sqrt transient for sqrt.
// The scaling uses 64-bit multiply/divide with truncation toward zero [P0-03 §4].
// Distance zero or distance <= radius => origin becomes center, else origin advances radius units along delta.
// TODO(question): the exact square-root temporary precision is not established
// for every representable Fixed value; use an integer square root if a probe
// demonstrates a divergence [P0-03].
func stepTowardCenter(originX, originZ, centerX, centerZ, radius numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	dx := int64(centerX) - int64(originX)
	dz := int64(centerZ) - int64(originZ)
	if dx == 0 && dz == 0 {
		return originX, originZ
	}
	rRaw := int64(radius)
	if rRaw < 0 {
		rRaw = -rRaw
	}
	if rRaw == 0 {
		return originX, originZ
	}
	// Distance via sqrt(dx²+dz²) with Fixed 16.16 inputs; result is also Fixed 16.16 [P0-03 §4] [I2] sqrt transient allowed per flight brake analogy [04 §10.1].
	// Compute in float64 for sqrt, then truncate toward zero via int64(dist) [I3][I2].
	distF := math.Sqrt(float64(dx)*float64(dx) + float64(dz)*float64(dz)) // [P0-03 §4] sqrt transient [I2] allowed
	if distF == 0 {
		return originX, originZ
	}
	dist := int64(distF) // truncate toward zero [I3]
	if dist <= rRaw {
		return centerX, centerZ
	}
	// Normalized step: origin + delta * radius / dist via 64-bit multiply then divide (truncate toward zero) [P0-03 §4].
	// Use int64 to avoid overflow of dx*rRaw (both 32-bit Fixed, product 64-bit).
	stepX := dx * rRaw / dist
	stepZ := dz * rRaw / dist
	return numeric.Fixed(int64(originX) + stepX), numeric.Fixed(int64(originZ) + stepZ)
}

// extractorHelperA is the exhaustive extractor placement helper [P0-03].
// It searches the precomputed metal patch vector within (radius<<2)², filters by distance, sorts by distance,
// validates each candidate with the canonical placement and score checks, and
// stops when next distance exceeds best+160 slack.
// Failed A does NOT fall through to B [P0-03 §3.1] DIRECT.
// Zero RNG draws [P0-03 §5] NEGATIVE-BOUNDED.
// The precomputed metal-patch vector is not yet populated from the terrain in
// this lane; expose explicit unavailable result per RS-11 [P0-03 §6].
func extractorHelperA(m *Manager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	_ = defKey
	_ = surfaceMetal
	_ = terrain
	// No established metal patch vector in this build [P0-03 §6][RS-11].
	// TODO(question): wire the precomputed patch vector when terrain metal scan
	// is established; until then explicit unavailable.
	return PlacementResult{
		Valid:  false,
		Helper: HelperA,
		Reason: ReasonNoPatchData,
		Proof:  fmt.Errorf("helper A: no established metal patch vector [P0-03 §6]"),
	}
}

// extractorHelperB is the statistical scatter helper [P0-03].
// Its concrete dependencies and placement profile are resolved before the
// candidate source is consulted. The patch-vector/scatter candidate geometry
// remains Unknown, so this helper fails explicitly without consuming trial
// RNG or fabricating a map-wide target [RS-11].
func extractorHelperB(m *Manager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	if m == nil {
		return placementFailure(HelperB, ReasonNilManager, "manager unavailable")
	}
	if m.Catalog == nil {
		return placementFailure(HelperB, ReasonMissingCatalog, "unit catalog unavailable")
	}
	def, ok := m.Catalog.Unit(canonicalKey(defKey))
	if !ok || def == nil {
		return placementFailure(HelperB, ReasonMissingDefinition, fmt.Sprintf("unit definition %q unavailable", defKey))
	}
	if (def.FootprintX <= 0 || def.FootprintZ <= 0) && strings.TrimSpace(def.MovementClass) == "" {
		return placementFailure(HelperB, ReasonInvalidFootprint, fmt.Sprintf("unit %q has invalid authored footprint %dx%d", defKey, def.FootprintX, def.FootprintZ))
	}
	if def.FootprintX <= 0 || def.FootprintZ <= 0 {
		mc, ok := m.Catalog.Movement[content.CanonicalKey(def.MovementClass)]
		if !ok || mc == nil || mc.FootprintX <= 0 || mc.FootprintZ <= 0 {
			return placementFailure(HelperB, ReasonInvalidFootprint, fmt.Sprintf("unit %q has no compiled footprint", defKey))
		}
	}
	footX32, footZ32 := world.FootprintForUnit(m.Catalog, def)
	if footX32 <= 0 || footZ32 <= 0 {
		return placementFailure(HelperB, ReasonInvalidFootprint, fmt.Sprintf("unit %q has invalid footprint %dx%d", defKey, footX32, footZ32))
	}
	footX, footZ := int(footX32), int(footZ32)
	var yard []world.YardCell
	if !def.BMCode {
		if strings.TrimSpace(def.YardMap) == "" {
			return placementFailure(HelperB, ReasonMissingDefinition, fmt.Sprintf("unit %q has no placement yard", defKey))
		}
		var err error
		yard, err = world.ParseYardMap(def.YardMap, footX, footZ)
		if err != nil {
			return placementFailure(HelperB, ReasonMissingDefinition, fmt.Sprintf("unit %q yard: %v", defKey, err))
		}
	}
	if terrain == nil {
		return placementFailure(HelperB, ReasonMissingTerrain, "terrain unavailable")
	}
	if m.RNG == nil {
		return placementFailure(HelperB, ReasonMissingRNG, "simulation RNG unavailable")
	}
	rules, errRules := world.PlacementRulesForUnit(m.Catalog, def)
	if errRules != nil {
		return placementFailure(HelperB, ReasonMissingDefinition, fmt.Sprintf("unit %q placement profile: %v", defKey, errRules))
	}
	// The canonical yard and placement profile are resolved before the unknown
	// candidate source is consulted; no candidate can be validated until that
	// source is recovered [04 §6.2–§6.4][05 "Construction placement"].
	_ = yard
	_ = rules
	// Limit SurfaceMetal*footX*footZ*2 [P0-03 §2.2][P0-03 §3.3]
	limit := int32(surfaceMetal) * int32(footX) * int32(footZ) * 2
	// TODO(question): patch-vector/scatter candidate geometry is Unknown. The
	// recovered contract does not establish how the scatter helper's patch
	// vector, radius/angle draws, and map-region records become a candidate
	// cell. Static evidence or data-driven retail probes that recover those
	// records and the conversion would settle this question. Do not substitute
	// a map center, offset, clamp, or other candidate source here.
	return PlacementResult{
		Valid:  false,
		Helper: HelperB,
		Reason: ReasonUnknownGeometry,
		FootX:  footX,
		FootZ:  footZ,
		Limit:  limit,
		Proof:  fmt.Errorf("helper B: patch-vector/scatter candidate geometry is Unknown [TODO(question)]"),
	}
}

func placementMetalScore(terrain *world.Terrain, rect world.FootprintRect) (int64, error) {
	if terrain == nil {
		return 0, fmt.Errorf("terrain unavailable")
	}
	var score int64
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := terrain.PlotAt(x, z)
			if cell == nil {
				return 0, fmt.Errorf("terrain cell %d,%d unavailable", x, z)
			}
			score += int64(cell.Metal())
		}
	}
	return score, nil
}

// queueExactResult queues the exact validated site through the ordinary mobile build producer [P0-07][RS-11].
// It is the only path that mutates the order queue; no privileged write occurs.
func queueExactResult(m *Manager, defKey string, res PlacementResult) error {
	if !res.Valid {
		return fmt.Errorf("placement result is invalid")
	}
	fac := m.Factory
	if fac == nil {
		return fmt.Errorf("builder unavailable")
	}
	cb := m.QueueBuildTyped
	if cb == nil {
		return fmt.Errorf("typed build queue unavailable")
	}
	req := BuildRequest{
		Builder: fac.Handle,
		UnitKey: defKey,
		X:       res.WorldX,
		Z:       res.WorldZ,
		Count:   1,
		Kind:    BuildKindMobileSite,
	}
	if err := cb(req); err != nil {
		return fmt.Errorf("typed build queue: %w", err)
	}
	return nil
}

// PlaceWithResult is the exact, typed placement entry [RS-11][P0-03].
// It returns a PlacementResult containing the exact world/cell site, footprint,
// score and helper path, and queues exactly that site via the ordinary
// construction queue. The origin movement and radius growth follow the fixed-
// point 16.16 interpolation and 160-cell cap [P0-03 §3.1][RS-11].
func PlaceWithResult(m *Manager, defKey string, w *world.Terrain) PlacementResult {
	if m == nil {
		return PlacementResult{Valid: false, Helper: HelperNone, Reason: ReasonNilManager, Proof: fmt.Errorf("nil manager")}
	}
	defKey = strings.TrimSpace(defKey)
	if defKey == "" {
		return PlacementResult{Valid: false, Helper: HelperNone, Reason: ReasonEmptyDef, Proof: fmt.Errorf("empty defKey")}
	}
	if m.Catalog == nil {
		return placementFailure(HelperNone, ReasonMissingCatalog, "unit catalog unavailable")
	}
	if def, ok := m.Catalog.Unit(canonicalKey(defKey)); !ok || def == nil {
		return placementFailure(HelperNone, ReasonMissingDefinition, fmt.Sprintf("unit definition %q unavailable", defKey))
	}
	if m.RNG == nil {
		return placementFailure(HelperNone, ReasonMissingRNG, "simulation RNG unavailable")
	}
	if m.Factory == nil {
		return placementFailure(HelperNone, ReasonMissingBuilder, "mobile build builder unavailable")
	}
	if m.QueueBuildTyped == nil {
		return placementFailure(HelperNone, ReasonMissingQueue, "typed build queue unavailable")
	}
	// Resolve terrain for this call: prefer passed-in w, else manager's terrain
	terrain := w
	if terrain == nil {
		terrain = m.Terrain
	}
	if terrain == nil {
		return placementFailure(HelperNone, ReasonMissingTerrain, "terrain unavailable")
	}

	// Grow radius before origin step, capped by max(mapW,mapH) [P0-03 §3.1].
	// Radius is Fixed world units (1 cell =16*65536 = worldUnitsPerCell) [03 §2.1]; +160 cells → 160*worldUnitsPerCell per failure, cap at maxCells*worldUnitsPerCell [P0-03 §3.1].
	var capWorld numeric.Fixed
	maxCells := terrain.CellW
	if terrain.CellH > maxCells {
		maxCells = terrain.CellH
	}
	capWorld = numeric.Fixed(int64(maxCells) * worldUnitsPerCell)
	if m.Strategic.Radius < capWorld {
		inc := numeric.Fixed(int64(160) * worldUnitsPerCell) // +160 cells per failure [P0-03]
		newRad := m.Strategic.Radius + inc
		if newRad > capWorld {
			newRad = capWorld
		}
		m.Strategic.Radius = newRad
	}

	// Move search origin toward strategic center using stored radius [PLAN_11 C8][P0-03].
	originX, originZ := m.OriginX, m.OriginZ
	centerX := m.Strategic.CenterX
	centerZ := m.Strategic.CenterZ
	radius := m.Strategic.Radius
	newOriginX, newOriginZ := stepTowardCenter(originX, originZ, centerX, centerZ, radius)
	m.OriginX, m.OriginZ = newOriginX, newOriginZ

	// Extractor branch: candidates with a non-zero extracts-metal value [08][P0-03].
	if def, ok := m.Catalog.Unit(canonicalKey(defKey)); ok && def.ExtractsMetal != 0 {
		rngStream := m.RNG
		// Exactly one RNG(255) draw for selector when extractor [P0-03 §5] (I4) single global stream RS-02.
		draw := rngStream.Uint32n(255) // bound 255 is the extractor branch census [PLAN_11 C9][P0-03]
		sm := m.SurfaceMetal           // mission surface metal is an unsigned byte in the runtime schema [08]
		if sm < 0 {
			sm = 0
		}
		if sm > 255 {
			sm = 255
		}
		// Strict less-than: SurfaceMetal < RNG(255) picks A else B [P0-03 §3.1].
		if sm < int32(draw) {
			// Helper A is exhaustive and consumes no further RNG [P0-03 §5].
			res := extractorHelperA(m, defKey, sm, terrain)
			if res.Valid {
				// Success: queue exact site and reset radius [P0-03 §3.1]
				if err := queueExactResult(m, defKey, res); err != nil {
					res.Valid = false
					res.Reason = ReasonQueueFailed
					res.Proof = err
					return res
				}
				m.Strategic.Radius = 0
				return res
			}
			// Failed A does NOT fall through to B [P0-03 §3.1] DIRECT.
			return res
		}
		// Selector chose B
		res := extractorHelperB(m, defKey, sm, terrain)
		if res.Valid {
			if err := queueExactResult(m, defKey, res); err != nil {
				res.Valid = false
				res.Reason = ReasonQueueFailed
				res.Proof = err
				return res
			}
			m.Strategic.Radius = 0
		}
		return res
	}

	// Non-extractor: directly helper B with no selector draw [P0-03 §3.1]
	sm := m.SurfaceMetal
	if sm < 0 {
		sm = 0
	}
	if sm > 255 {
		sm = 255
	}
	res := extractorHelperB(m, defKey, sm, terrain)
	if res.Valid {
		if err := queueExactResult(m, defKey, res); err != nil {
			res.Valid = false
			res.Reason = ReasonQueueFailed
			res.Proof = err
			return res
		}
		m.Strategic.Radius = 0
	}
	return res
}

// Place moves the search origin toward the strategic center using the stored
// search radius, handles the extractor branch with one RNG(255) draw against
// mission SurfaceMetal, does not fall through on failed A, validates via yard
// helpers, writes fixed-point placement, resets radius, and issues the build
// command through the typed queue [08 "Established AI-facing data and rooted planner"]
// [PLAN_11 C8, C9, C12][P0-03][P0-07].
//
// C9 bound census: this file uses RNG(255) for the extractor selector. Helper
// B's trial draws are not made until its unresolved candidate geometry is
// recovered [P0-03 §5].
// Typed path preserves X/Z via BuildRequest with MobileSite [P0-07] F-P0-004.
// Exact site from PlacementResult is queued bit-for-bit [RS-11].
func Place(m *Manager, defKey string, w *world.Terrain) (numeric.Fixed, numeric.Fixed, bool) {
	res := PlaceWithResult(m, defKey, w)
	if res.Valid {
		return res.WorldX, res.WorldZ, true
	}
	return 0, 0, false
}
