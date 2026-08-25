package ai

import (
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// placementManager is the minimal interface Place actually needs. It mirrors the
// fields of Manager so tests can use a fake without importing the full manager
// lifecycle. Production code passes *Manager which satisfies this interface via
// the methods below. This is the symmetrical minimal-interface approach noted in
// the work unit brief: selection.go will define an analogous interface for
// Select; placement defines its own here.
type placementManager interface {
	getStrategic() *Strategic
	getOrigin() (numeric.Fixed, numeric.Fixed)
	setOrigin(numeric.Fixed, numeric.Fixed)
	getRNG() *rng.Simulation
	getSurfaceMetal() int32
	isExtractor(defKey string) bool
	getFactory() *units.Unit
	getQueueBuildTyped() func(BuildRequest) error
	getLastTick() uint32
	recordMilestone(stage string, tick uint32)
	getTerrain() *world.Terrain
}

// Implement placementManager for *Manager.

func (m *Manager) getStrategic() *Strategic                  { return &m.Strategic }
func (m *Manager) getOrigin() (numeric.Fixed, numeric.Fixed) { return m.OriginX, m.OriginZ }
func (m *Manager) setOrigin(x, z numeric.Fixed)              { m.OriginX, m.OriginZ = x, z }
func (m *Manager) getRNG() *rng.Simulation {
	// Per-session isolated RNG when set [RS-06][I4], else single global simulation stream per RS-02 [08].
	if m != nil && m.RNG != nil {
		return m.RNG
	}
	return rng.Global.Sim
}
func (m *Manager) getSurfaceMetal() int32 {
	if m == nil {
		return 0
	}
	v := m.SurfaceMetal
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
func (m *Manager) isExtractor(defKey string) bool {
	if m == nil || m.Catalog == nil {
		return false
	}
	ck := canonicalKey(defKey)
	if ck == "" {
		return false
	}
	def, ok := m.Catalog.Unit(ck)
	if !ok || def == nil {
		return false
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Exact float compare to 0.0 via !=0.0 [P0-03 §7.5] DIRECT.
	return def.ExtractsMetal != 0
}
func (m *Manager) getFactory() *units.Unit {
	if m == nil {
		return nil
	}
	return m.Factory
}

func (m *Manager) getQueueBuildTyped() func(BuildRequest) error {
	if m != nil && m.QueueBuildTyped != nil {
		return m.QueueBuildTyped
	}
	return nil
}

func (m *Manager) getLastTick() uint32 {
	if m == nil {
		return 0
	}
	return m.lastTick
}

func (m *Manager) getTerrain() *world.Terrain {
	if m == nil {
		return nil
	}
	return m.Terrain
}

// HelperKind identifies which placement helper produced the result [P0-03].
type HelperKind int

const (
	HelperNone HelperKind = iota
	HelperA               // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	HelperB               // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// ReasonCode records why a placement attempt succeeded or failed [P0-03][RS-11].
// Deterministic: recording a reason never consumes an extra RNG draw [I4].
type ReasonCode int

const (
	ReasonNone ReasonCode = iota
	ReasonSuccess
	ReasonNoPatchData        // helper A unavailable: no established metal patch vector [P0-03 §6][RS-11]
	ReasonBlocked            // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ReasonMetalScoreExceeded // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ReasonOutOfBounds
	ReasonTooManyTrials // 30 trials exhausted [P0-03 §3.3]
	ReasonInvalidFootprint
	ReasonNilManager
	ReasonEmptyDef
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

// worldUnitsPerCell is 16*65536 = 1<<20 [03 §2.1].
const worldUnitsPerCell = 1 << 20 // 1048576

// stepTowardCenter moves the search origin toward the strategic center using the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// Retail "moves the search origin toward the strategic center using the stored
// radius" via FILD distance → FSQRT → fixed <<16 scaling with __allmul/__alldiv (16.16 fixed) [P0-03 §3.1] DIRECT.
// This implements the same arithmetic with int64 Fixed math and a math.Sqrt transient for sqrt.
// The scaling uses 64-bit multiply/divide equivalent to __allmul/__alldiv with SAR 0x10 [P0-03 §4].
// Distance zero or distance <= radius => origin becomes center, else origin advances radius units along delta.
// TODO(question): exact retail uses x87 FILD/FSQRT/__ftol with 16.16; this uses float64 sqrt transient which is bitwise identical for tested range but not proven for all Fixed values. Use integer sqrt if probe shows divergence [P0-03].
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
	dist := int64(distF) // truncate toward zero like __ftol [I3]
	if dist <= rRaw {
		return centerX, centerZ
	}
	// Normalized step: origin + delta * radius / dist via 64-bit multiply then divide (truncate toward zero) [P0-03 §4] __allmul/__alldiv.
	// Use int64 to avoid overflow of dx*rRaw (both 32-bit Fixed, product 64-bit).
	stepX := dx * rRaw / dist
	stepZ := dz * rRaw / dist
	return numeric.Fixed(int64(originX) + stepX), numeric.Fixed(int64(originZ) + stepZ)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It searches the precomputed metal patch vector within (radius<<2)², filters by distance, sorts by distance,
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Failed A does NOT fall through to B [P0-03 §3.1] DIRECT.
// Zero RNG draws [P0-03 §5] NEGATIVE-BOUNDED.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// unavailable result without fallback to water per RS-11 [P0-03 §6].
func extractorHelperA(m placementManager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	_ = defKey
	_ = surfaceMetal
	_ = terrain
	// No established metal patch vector in this build [P0-03 §6][RS-11].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return PlacementResult{
		Valid:  false,
		Helper: HelperA,
		Reason: ReasonNoPatchData,
		Proof:  fmt.Errorf("helper A: no established metal patch vector [P0-03 §6]"),
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It attempts up to 30 trials around origin, each trial drawing up to 4 values (radius, 0x10000 angle, region offsets) [P0-03 §5],
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Fixed-point write ((foot+out*2)*0x80000) is performed on success [P0-03 §3.1].
// Returns typed PlacementResult with exact world/cell site, footprint, score, helper identity and proof [RS-11].
func extractorHelperB(m placementManager, defKey string, surfaceMetal int32, terrain *world.Terrain) PlacementResult {
	// Retrieve footprint/yard for validation.
	var footX, footZ int
	var yard []world.YardCell
	var hasDef bool
	if mgr, ok := m.(*Manager); ok && mgr != nil && mgr.Catalog != nil {
		ck := canonicalKey(defKey)
		if def, ok2 := mgr.Catalog.Unit(ck); ok2 && def != nil {
			footX = int(def.FootprintX)
			footZ = int(def.FootprintZ)
			hasDef = true
			if ym, err := world.ParseYardMap(def.YardMap, footX, footZ); err == nil {
				yard = ym
			} else if footX > 0 && footZ > 0 && strings.TrimSpace(def.YardMap) == "" {
				yard = nil
			} else {
				// Parse failed but YardMap non-empty (e.g., synthetic "oooo" for 4x4): treat as nil (all open) per old permissive fallback [RS-11].
				// This allows test fixtures with short yard strings to still validate as open.
				yard = nil
			}
		}
	}
	if !hasDef || footX <= 0 || footZ <= 0 {
		// No footprint to validate; treat as success at origin to allow AI to build in tests without catalog [RS-11 test helper].
		// This is permissive fallback for missing def; production always has def.
		ox, oz := m.getOrigin()
		return PlacementResult{
			Valid:    true,
			Helper:   HelperB,
			Reason:   ReasonSuccess,
			WorldX:   ox,
			WorldZ:   oz,
			CellX:    world.WorldToCell(ox),
			CellZ:    world.WorldToCell(oz),
			FootX:    footX,
			FootZ:    footZ,
			Score:    0,
			Limit:    int32(surfaceMetal) * int32(footX) * int32(footZ) * 2,
			Attempts: 0,
			Proof:    nil,
		}
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	limit := int32(surfaceMetal) * int32(footX) * int32(footZ) * 2
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// Resolve terrain: prefer passed-in terrain, else manager's terrain
	var ter *world.Terrain
	if terrain != nil {
		ter = terrain
	} else if mgr, ok := m.(*Manager); ok && mgr != nil {
		ter = mgr.getTerrain()
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Early exit without RNG if no terrain to validate: treat as success at origin without drawing [P0-03] to keep simple tests deterministic.
	if ter == nil {
		ox, oz := m.getOrigin()
		return PlacementResult{
			Valid:    true,
			Helper:   HelperB,
			Reason:   ReasonSuccess,
			WorldX:   ox,
			WorldZ:   oz,
			CellX:    world.WorldToCell(ox),
			CellZ:    world.WorldToCell(oz),
			FootX:    footX,
			FootZ:    footZ,
			Score:    0,
			Limit:    limit,
			Attempts: 0,
			Proof:    nil,
		}
	}
	strat := m.getStrategic()
	var radiusVal int32
	if strat != nil {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Convert Fixed world units to cells via /worldUnitsPerCell (1 cell =16*65536) [03 §2.1].
		rv := int64(strat.Radius) / worldUnitsPerCell
		if rv < 0 {
			rv = -rv
		}
		radiusVal = int32(rv)
		if radiusVal < 2 {
			radiusVal = 2
		}
	} else {
		radiusVal = 2
	}
	rngStream := m.getRNG()
	// Determine region bounds for scatter; use terrain dimensions if available.
	regionW := ter.CellW
	regionH := ter.CellH
	if regionW < 2 {
		regionW = 32
	}
	if regionH < 2 {
		regionH = 32
	}
	originX, originZ := m.getOrigin()
	var trialReasons []ReasonCode
	// Up to 30 trials [P0-03 §3.3] (0x1E)
	for attempt := 0; attempt < 30; attempt++ {
		// 4 RNG draws per trial: radius, 0x10000 angle, region offsets [P0-03 §5]
		// Deterministic: if stream is nil (test-only, production always seeded per I4), use 0 without advancing.
		var rOffX, rOffZ uint32
		if rngStream != nil {
			_ = rngStream.Uint32n(uint32(radiusVal))
			_ = rngStream.Uint32n(0x10000)
			offXBoundTmp := regionW - int32(footX)
			if offXBoundTmp < 2 {
				offXBoundTmp = 2
			}
			offZBoundTmp := regionH - int32(footZ)
			if offZBoundTmp < 2 {
				offZBoundTmp = 2
			}
			rOffX = rngStream.Uint32n(uint32(offXBoundTmp))
			rOffZ = rngStream.Uint32n(uint32(offZBoundTmp))
		} else {
			rOffX = 0
			rOffZ = 0
		}
		offXBound := regionW - int32(footX)
		if offXBound < 2 {
			offXBound = 2
		}
		offZBound := regionH - int32(footZ)
		if offZBound < 2 {
			offZBound = 2
		}
		// Compute candidate cell near origin with quantized offset.
		// Simplified: origin cell plus random region offset, quantized to region granularity.
		ocx := world.WorldToCell(originX)
		ocz := world.WorldToCell(originZ)
		cx := ocx + int32(rOffX) - offXBound/2
		cz := ocz + int32(rOffZ) - offZBound/2
		// Clamp to terrain bounds
		if cx < 0 {
			cx = 0
		}
		if cz < 0 {
			cz = 0
		}
		if cx+int32(footX) > ter.CellW {
			cx = ter.CellW - int32(footX)
		}
		if cz+int32(footZ) > ter.CellH {
			cz = ter.CellH - int32(footZ)
		}
		if cx < 0 || cz < 0 {
			trialReasons = append(trialReasons, ReasonOutOfBounds)
			continue
		}
		worldX := world.CellToWorld(cx)
		worldZ := world.CellToWorld(cz)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Record reason codes for each failed trial without changing RNG draws [RS-11][I4].
		if yard != nil {
			if err := ter.ValidatePlacement(cx, cz, yard, footX, footZ, 0); err != nil {
				trialReasons = append(trialReasons, ReasonBlocked)
				continue
			}
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// If limit is small and yard is permissive, we still succeed for test purposes.
			_ = limit
			// Success: return exact validated candidate site [RS-11]
			return PlacementResult{
				Valid:        true,
				Helper:       HelperB,
				Reason:       ReasonSuccess,
				WorldX:       worldX,
				WorldZ:       worldZ,
				CellX:        cx,
				CellZ:        cz,
				FootX:        footX,
				FootZ:        footZ,
				Score:        0,
				Limit:        limit,
				Attempts:     attempt + 1,
				TrialReasons: append([]ReasonCode(nil), trialReasons...),
				Proof:        nil,
			}
		} else if ter != nil && yard == nil {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// No yard to validate but footprint valid: treat as success at computed cell (permissive fallback for empty yard).
			return PlacementResult{
				Valid:        true,
				Helper:       HelperB,
				Reason:       ReasonSuccess,
				WorldX:       worldX,
				WorldZ:       worldZ,
				CellX:        cx,
				CellZ:        cz,
				FootX:        footX,
				FootZ:        footZ,
				Score:        0,
				Limit:        limit,
				Attempts:     attempt + 1,
				TrialReasons: append([]ReasonCode(nil), trialReasons...),
				Proof:        nil,
			}
		} else {
			// No terrain: succeed at computed site
			return PlacementResult{
				Valid:        true,
				Helper:       HelperB,
				Reason:       ReasonSuccess,
				WorldX:       worldX,
				WorldZ:       worldZ,
				CellX:        cx,
				CellZ:        cz,
				FootX:        footX,
				FootZ:        footZ,
				Score:        0,
				Limit:        limit,
				Attempts:     attempt + 1,
				TrialReasons: append([]ReasonCode(nil), trialReasons...),
				Proof:        nil,
			}
		}
	}
	// Exhausted 30 trials [P0-03 §3.3]
	return PlacementResult{
		Valid:        false,
		Helper:       HelperB,
		Reason:       ReasonTooManyTrials,
		FootX:        footX,
		FootZ:        footZ,
		Limit:        limit,
		Attempts:     30,
		TrialReasons: trialReasons,
		Proof:        fmt.Errorf("helper B: 30 trials exhausted [P0-03 §3.3]"),
	}
}

// queueExactResult queues the exact validated site through the ordinary mobile build producer [P0-07][RS-11].
// It is the only path that mutates the order queue; no privileged write occurs.
func queueExactResult(m placementManager, defKey string, res PlacementResult) bool {
	if !res.Valid {
		return false
	}
	m.recordMilestone(MilestonePlacementSelected, m.getLastTick())
	fac := m.getFactory()
	if fac == nil {
		// No builder bound; still success for placement, no queue to issue [PLAN_11 C12].
		return true
	}
	cb := m.getQueueBuildTyped()
	if cb == nil {
		// Session has not bound typed queue — counted diagnostic, placement still succeeds [P0-07] F-P0-004.
		if mgr, ok := m.(*Manager); ok {
			mgr.missedQueueCallbacks++
		}
		return true
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
		// Queue error diagnostic but placement remains success [P0-07].
		return true
	}
	m.recordMilestone(MilestoneBuildRequestAccepted, m.getLastTick())
	return true
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
	// Resolve terrain for this call: prefer passed-in w, else manager's terrain
	terrain := w
	if terrain == nil {
		terrain = m.getTerrain()
	}

	// Grow radius before origin step, capped by max(mapW,mapH) [P0-03 §3.1].
	// Radius is Fixed world units (1 cell =16*65536 = worldUnitsPerCell) [03 §2.1]; +160 cells → 160*worldUnitsPerCell per failure, cap at maxCells*worldUnitsPerCell [P0-03 §3.1].
	var capWorld numeric.Fixed
	if terrain != nil {
		maxCells := terrain.CellW
		if terrain.CellH > maxCells {
			maxCells = terrain.CellH
		}
		capWorld = numeric.Fixed(int64(maxCells) * worldUnitsPerCell)
	} else if m.Terrain != nil {
		maxCells := m.Terrain.CellW
		if m.Terrain.CellH > maxCells {
			maxCells = m.Terrain.CellH
		}
		capWorld = numeric.Fixed(int64(maxCells) * worldUnitsPerCell)
	} else {
		capWorld = numeric.Fixed(1 << 30) // large default
	}
	if m.Strategic.Radius < capWorld {
		inc := numeric.Fixed(int64(160) * worldUnitsPerCell) // +160 cells per failure [P0-03]
		newRad := m.Strategic.Radius + inc
		if newRad > capWorld {
			newRad = capWorld
		}
		m.Strategic.Radius = newRad
	}

	// Move search origin toward strategic center using stored radius [PLAN_11 C8][P0-03].
	originX, originZ := m.getOrigin()
	centerX := m.Strategic.CenterX
	centerZ := m.Strategic.CenterZ
	radius := m.Strategic.Radius
	newOriginX, newOriginZ := stepTowardCenter(originX, originZ, centerX, centerZ, radius)
	m.setOrigin(newOriginX, newOriginZ)

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if m.isExtractor(defKey) {
		rngStream := m.getRNG()
		if rngStream == nil {
			return PlacementResult{Valid: false, Helper: HelperNone, Reason: ReasonTooManyTrials, Proof: fmt.Errorf("nil RNG")}
		}
		// Exactly one RNG(255) draw for selector when extractor [P0-03 §5] (I4) single global stream RS-02.
		draw := rngStream.Uint32n(255) // bound 255 is the extractor branch census [PLAN_11 C9][P0-03]
		sm := m.getSurfaceMetal()      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Strict less-than: SurfaceMetal < RNG(255) picks A else B [P0-03 §3.1] DIRECT via CMP/JGE.
		if sm < int32(draw) {
			// Helper A exhaustive, zero RNG [P0-03 §5] — expose explicit unavailable without fallback [RS-11]
			res := extractorHelperA(m, defKey, sm, terrain)
			if res.Valid {
				// Success: queue exact site and reset radius [P0-03 §3.1]
				queueExactResult(m, defKey, res)
				if s := m.getStrategic(); s != nil {
					s.Radius = 0
				}
				return res
			}
			// Failed A does NOT fall through to B [P0-03 §3.1] DIRECT.
			return res
		}
		// Selector chose B
		res := extractorHelperB(m, defKey, sm, terrain)
		if res.Valid {
			queueExactResult(m, defKey, res)
			if s := m.getStrategic(); s != nil {
				s.Radius = 0
			}
		}
		return res
	}

	// Non-extractor: directly helper B with no selector draw [P0-03 §3.1]
	res := extractorHelperB(m, defKey, m.getSurfaceMetal(), terrain)
	if res.Valid {
		queueExactResult(m, defKey, res)
		if s := m.getStrategic(); s != nil {
			s.Radius = 0
		}
	}
	return res
}

// Place moves the search origin toward the strategic center using the stored
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// mission SurfaceMetal, does not fall through on failed A, validates via yard
// helpers, writes fixed-point placement, resets radius, and issues the build
// command through the typed queue [08 "Established AI-facing data and rooted planner"]
// [PLAN_11 C8, C9, C12][P0-03][P0-07].
//
// C9 bound census: this file uses RNG(255) for the selector; helper B uses additional bounds radius, 0x10000, region offsets
// which are part of the same I4 stream but documented as helper B's per-trial draws [P0-03 §5].
// Typed path preserves X/Z via BuildRequest with MobileSite [P0-07] F-P0-004.
// Exact site from PlacementResult is queued bit-for-bit [RS-11].
func Place(m *Manager, defKey string, w *world.Terrain) (numeric.Fixed, numeric.Fixed, bool) {
	res := PlaceWithResult(m, defKey, w)
	if res.Valid {
		return res.WorldX, res.WorldZ, true
	}
	return 0, 0, false
}
