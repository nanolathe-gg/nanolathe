package ai

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// HelperKind identifies which placement helper produced the result
// [08 R-AI-03 §2].
type HelperKind int

const (
	HelperNone HelperKind = iota
	HelperA               // exhaustive patch helper [08 R-AI-03 §3]
	HelperB               // statistical scatter helper [08 R-AI-03 §4]
)

// ReasonCode records why a placement attempt succeeded or failed [P0-03][RS-11].
// Deterministic: recording a reason never consumes an extra RNG draw [I4].
type ReasonCode int

const (
	ReasonNone ReasonCode = iota
	ReasonSuccess
	ReasonNoPatchData        // the battle-entry vector is empty [08 R-AI-03 §3]
	ReasonBlocked            // canonical world placement rejected the candidate
	ReasonMetalScoreExceeded // score > SurfaceMetal*footX*footZ*2 [08 R-AI-03 §4]
	ReasonOutOfBounds
	ReasonTooManyTrials // 30 trials exhausted [08 R-AI-03 §4]
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
	ReasonMissingStrategicState
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
	// Both the add/multiply and the shift are 32-bit writes; overflow wraps
	// before Nanolathe widens the stored word [08 R-AI-03 §5].
	word := footprint + 2*outputCell
	return numeric.Fixed(word << 19)
}

// worldUnitsPerCell is 16*65536 = 1<<20 [03 §2.1].
const worldUnitsPerCell = 1 << 20 // 1048576

// stepTowardCenter is the two-axis fixture adapter for the three-axis root.
// Production passes the builder and strategic-center Y through
// retailPlacementOrigin [08 R-AI-03 §2].
func stepTowardCenter(originX, originZ, centerX, centerZ, radius numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	p := retailPlacementOrigin(
		retailPlacementPoint{x: originX, z: originZ},
		retailPlacementPoint{x: centerX, z: centerZ},
		int32(radius.Int()),
	)
	return p.x, p.z
}

// extractorHelperA is retained as a fixture adapter. Production supplies the
// exact root origin directly to retailExtractorHelperA [08 R-AI-03 §3].
// The marker retired here asked for a trace of the helper's negative-row
// branch. It has one: [08 R-AI-03 §7.1] traced the blocker's entry test in
// 2026-09-02 and the answer is "reads preceding storage". The test is
// `candX > 0` on the signed column AND the packed cell word exceeding 0xffff as
// unsigned, so row 0 is rejected exactly as column 0 is, a negative row passes
// and the walk then addresses cells before the plot grid with no guard.
// Whether that storage is mapped is Unknown and undecidable from the
// executable, so §7.1 gives the implementation rule instead: reject a negative
// row as Nanolathe's deterministic choice, marked as that choice, and reject
// row 0 as retail does. validateRetailAICandidate in placement_heap.go does
// both, and says which is which.
func extractorHelperA(m *Manager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	_ = surfaceMetal
	pd, failure := resolveRetailPlacementDef(m, defKey)
	if failure.Proof != nil {
		return failure
	}
	return retailExtractorHelperA(m, pd, retailPlacementPoint{x: m.OriginX, z: m.OriginZ}, m.Strategic.Radius, terrain)
}

// extractorHelperB is retained as a fixture adapter. Production supplies the
// exact root origin directly to retailExtractorHelperB [08 R-AI-03 §4].
func extractorHelperB(m *Manager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	if m == nil {
		return placementFailure(HelperB, ReasonNilManager, "manager unavailable")
	}
	if m.Catalog == nil {
		return placementFailure(HelperB, ReasonMissingCatalog, "unit catalog unavailable")
	}
	if terrain == nil {
		return placementFailure(HelperB, ReasonMissingTerrain, "terrain unavailable")
	}
	if m.RNG == nil {
		return placementFailure(HelperB, ReasonMissingRNG, "simulation RNG unavailable")
	}
	pd, failure := resolveRetailPlacementDef(m, defKey)
	if failure.Proof != nil {
		return failure
	}
	return retailExtractorHelperB(m, pd, retailPlacementPoint{x: m.OriginX, z: m.OriginZ}, m.Strategic.Radius, surfaceMetal, terrain)
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
// point 16.16 interpolation and 160-world-unit growth [08 R-AI-03 §2].
//
// The validator's mode argument is the caller's movement mode, not a placement
// policy, and its off-map acceptance is the mode-2 (active locomotion) branch.
// No computer-player path can observe it: the scatter helper, the mobile-build
// site check, the skirmish spawn scan and the remaining order-handler site
// checks all pass the literal 1, and only the mover commit step can pass 2
// [08 R-AI-03 §7.2]. Nothing here needs a mode-2 arm.
func PlaceWithResult(m *Manager, defKey string, w *world.Terrain) PlacementResult {
	res := PlaceCandidate(m, defKey, w)
	if !res.Valid {
		return res
	}
	// The submitted request carries X and Z only. Retail's construction task
	// submits a stack-residue Y that nothing reads: the order node stores the
	// triple verbatim, the MobileBuild handler copies Y into a local it never
	// uses, and immediately before the nanoframe is created it rewrites
	// `y := siteHeight(def, cell) << 16` from the same height-under-footprint
	// query the blocker's tail computes [08 R-AI-03 §7.3]. Our handler already
	// derives the height that way (internal/construction/factory.go's mobile
	// build step takes result.SiteHeight), so there is no residue to carry.
	if err := queueExactResult(m, defKey, res); err != nil {
		res.Valid = false
		res.Reason = ReasonQueueFailed
		res.Proof = err
	}
	return res
}

// PlaceCandidate is the placement root's search half: it moves the origin,
// selects and runs a helper, and returns the validated site without
// submitting anything. The root itself never issues the build order — the
// construction task applies its distance cap to the returned site and submits
// afterwards [08 R-AI-03 §5][08 R-AI-01 §3]. PlaceWithResult keeps the
// combined search-and-submit shape for callers that do not apply a cap.
func PlaceCandidate(m *Manager, defKey string, w *world.Terrain) PlacementResult {
	if m == nil {
		return placementFailure(HelperNone, ReasonNilManager, "nil manager")
	}
	defKey = strings.TrimSpace(defKey)
	if defKey == "" {
		return placementFailure(HelperNone, ReasonEmptyDef, "empty definition key")
	}
	if m.Catalog == nil {
		return placementFailure(HelperNone, ReasonMissingCatalog, "unit catalog unavailable")
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
	pd, failure := resolveRetailPlacementDef(m, defKey)
	if failure.Proof != nil {
		return failure
	}

	// The radius is a plain world-unit count. The recovered branch adds 160
	// when below the larger map extent; it does not clamp the final addition
	// [08 R-AI-03 §2].
	maxWorld := terrain.CellW * 16
	if h := terrain.CellH * 16; h > maxWorld {
		maxWorld = h
	}
	if m.Strategic.Radius < maxWorld {
		m.Strategic.Radius += placementGrowthWorld
	}
	radius := m.Strategic.Radius
	origin := retailPlacementOrigin(
		retailPlacementPoint{x: m.Factory.X, y: m.Factory.Y, z: m.Factory.Z},
		retailPlacementPoint{x: m.Strategic.CenterX, y: m.Strategic.CenterY, z: m.Strategic.CenterZ},
		radius,
	)

	var res PlacementResult
	if pd.def.ExtractsMetal != 0.0 {
		draw := retailSignedDraw(m, 255)
		if m.SurfaceMetal < draw { // strict; equality selects scatter [08 R-AI-03 §2]
			res = retailExtractorHelperA(m, pd, origin, radius, terrain)
		} else {
			res = retailExtractorHelperB(m, pd, origin, radius, m.SurfaceMetal, terrain)
		}
	} else {
		res = retailExtractorHelperB(m, pd, origin, radius, m.SurfaceMetal, terrain)
	}
	if !res.Valid {
		return res
	}

	// Root success resets before later caller gates or queue submission
	// [08 R-AI-03 §5].
	m.Strategic.Radius = 0
	return res
}

// Place moves the search origin toward the strategic center using the stored
// search radius, handles the extractor branch with one RNG(255) draw against
// mission SurfaceMetal, does not fall through on failed A, validates via yard
// helpers, writes fixed-point placement, resets radius, and issues the build
// command through the typed queue [08 "Established AI-facing data and rooted planner"]
// [PLAN_11 C8, C9, C12][P0-03][P0-07].
//
// The selector and per-trial draw census are exact in placement_heap.go
// [08 R-AI-03 §2, §4].
// Typed path preserves X/Z via BuildRequest with MobileSite [P0-07] F-P0-004.
// Exact site from PlacementResult is queued bit-for-bit [RS-11].
func Place(m *Manager, defKey string, w *world.Terrain) (numeric.Fixed, numeric.Fixed, bool) {
	res := PlaceWithResult(m, defKey, w)
	if res.Valid {
		return res.WorldX, res.WorldZ, true
	}
	return 0, 0, false
}
