package ai

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
	return PlacementResult{Valid: false, Helper: helper, Reason: reason, Proof: fmt.Errorf("ai: placement: %s", detail)}
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

// queueExactResult queues the exact validated site through the ordinary mobile build producer [P0-07][RS-11].
// It is the only path that mutates the order queue; no privileged write occurs.
func queueExactResult(m *Manager, defKey string, res PlacementResult) error {
	if !res.Valid {
		return fmt.Errorf("ai: placement result is invalid")
	}
	fac := m.Factory
	if fac == nil {
		return fmt.Errorf("ai: builder unavailable")
	}
	cb := m.QueueBuildTyped
	if cb == nil {
		return fmt.Errorf("ai: typed build queue unavailable")
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
		return fmt.Errorf("ai: typed build queue: %w", err)
	}
	return nil
}

// PlaceCandidate is the placement root's search half: it moves the origin,
// selects and runs a helper, and returns the validated site without
// submitting anything. The root itself never issues the build order — the
// construction task applies its distance cap to the returned site and submits
// afterwards [08 R-AI-03 §5][08 R-AI-01 §3].
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
		return placementFailure(HelperNone, ReasonMissingQueue, "ai: typed build queue unavailable")
	}
	// Resolve terrain for this call: prefer passed-in w, else manager's terrain
	terrain := w
	if terrain == nil {
		terrain = m.Terrain
	}
	if terrain == nil {
		return placementFailure(HelperNone, ReasonMissingTerrain, "ai: terrain unavailable")
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
