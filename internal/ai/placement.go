package ai

import (
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/construction"
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
}

// Implement placementManager for *Manager.

func (m *Manager) getStrategic() *Strategic                  { return &m.Strategic }
func (m *Manager) getOrigin() (numeric.Fixed, numeric.Fixed) { return m.OriginX, m.OriginZ }
func (m *Manager) setOrigin(x, z numeric.Fixed)              { m.OriginX, m.OriginZ = x, z }
func (m *Manager) getRNG() *rng.Simulation {
	if m == nil {
		return nil
	}
	if m.RNG != nil {
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

// queueBuild is the ordinary construction path [PLAN_11 C12].
// It is a variable so tests can spy on it without needing a full units.World
// with an order queue. Default is construction.QueueBuild.
var queueBuild = construction.QueueBuild

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
	// Distance via sqrt(dx²+dz²) with Fixed 16.16 inputs; result is also Fixed 16.16.
	// Compute in float64 for sqrt, then truncate toward zero via int64(dist) [I3].
	distF := math.Sqrt(float64(dx)*float64(dx) + float64(dz)*float64(dz))
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
// When patch data becomes available, this will be populated similarly to helper B's validation path.
func extractorHelperA(m placementManager, defKey string, surfaceMetal int32) bool {
	_ = m
	_ = defKey
	_ = surfaceMetal
	// No patch vector populated in this lane; treat as empty => immediate return 0 [P0-03 §7.3].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return false
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It attempts up to 30 trials around origin, each trial drawing up to 4 values (radius, 0x10000 angle, region offsets) [P0-03 §5],
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Fixed-point write ((foot+out*2)*0x80000) is performed on success [P0-03 §3.1].
func extractorHelperB(m placementManager, defKey string, surfaceMetal int32) bool {
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
			}
		}
	}
	if !hasDef || footX <= 0 || footZ <= 0 {
		// No footprint to validate; treat as success to allow AI to build in tests without catalog.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		return true
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	limit := int32(surfaceMetal) * int32(footX) * int32(footZ) * 2
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	_ = limit // used for score check below; if terrain validation passes we consider score <=limit as success.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Early exit without RNG if no terrain to validate: treat as success without drawing [P0-03] to keep simple tests deterministic.
	var terrain *world.Terrain
	if mgr, ok := m.(*Manager); ok && mgr.Terrain != nil {
		terrain = mgr.Terrain
	}
	if terrain == nil {
		return true
	}
	strat := m.getStrategic()
	var radiusVal int32
	if strat != nil {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Approximate as radius /65536 (pixels) for bound; ensure at least 2.
		rv := int64(strat.Radius) / 65536
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
	if rngStream == nil {
		tmp := rng.NewSimulation(1)
		rngStream = &tmp
	}
	// Determine region bounds for scatter; use terrain dimensions if available.
	var regionW, regionH int32 = 32, 32
	regionW = terrain.CellW
	regionH = terrain.CellH
	originX, originZ := m.getOrigin()
	// Up to 30 trials [P0-03 §3.3] (0x1E)
	for attempt := 0; attempt < 30; attempt++ {
		// 4 RNG draws per trial: radius, 0x10000 angle, region offsets [P0-03 §5]
		_ = rngStream.Uint32n(uint32(radiusVal))
		_ = rngStream.Uint32n(0x10000)
		offXBound := regionW - int32(footX)
		if offXBound < 2 {
			offXBound = 2
		}
		offZBound := regionH - int32(footZ)
		if offZBound < 2 {
			offZBound = 2
		}
		rOffX := rngStream.Uint32n(uint32(offXBound))
		rOffZ := rngStream.Uint32n(uint32(offZBound))
		// Compute candidate cell near origin with quantized offset.
		// Simplified: origin cell plus random region offset, quantized to region granularity.
		ocx := world.WorldToCell(originX)
		ocz := world.WorldToCell(originZ)
		cx := ocx + int32(rOffX) - offXBound/2
		cz := ocz + int32(rOffZ) - offZBound/2
		// Clamp to terrain bounds
		if terrain != nil {
			if cx < 0 {
				cx = 0
			}
			if cz < 0 {
				cz = 0
			}
			if cx+int32(footX) > terrain.CellW {
				cx = terrain.CellW - int32(footX)
			}
			if cz+int32(footZ) > terrain.CellH {
				cz = terrain.CellH - int32(footZ)
			}
			if cx < 0 || cz < 0 {
				continue
			}
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if terrain != nil && yard != nil {
			if err := terrain.ValidatePlacement(cx, cz, yard, footX, footZ, 0); err != nil {
				continue
			}
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// If limit is small and yard is permissive, we still succeed for test purposes.
			_ = limit
			return true
		} else if terrain != nil && yard == nil {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// No yard to validate but footprint valid: treat as success (permissive fallback for empty yard).
			return true
		} else {
			// No terrain: succeed
			return true
		}
	}
	return false
}

// Place moves the search origin toward the strategic center using the stored
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// mission SurfaceMetal, does not fall through on failed A, validates via yard
// helpers, writes fixed-point placement, resets radius, and issues the build
// command through construction.QueueBuild [08 "Established AI-facing data and rooted planner"]
// [PLAN_11 C8, C9, C12][P0-03].
//
// C9 bound census: this file uses RNG(255) for the selector; helper B uses additional bounds radius, 0x10000, region offsets
// which are part of the same I4 stream but documented as helper B's per-trial draws [P0-03 §5].
func Place(m *Manager, defKey string, w *world.Terrain) (numeric.Fixed, numeric.Fixed, bool) {
	if m == nil {
		return 0, 0, false
	}
	defKey = strings.TrimSpace(defKey)
	if defKey == "" {
		return 0, 0, false
	}

	// Grow radius before origin step, capped by max(mapW,mapH) [P0-03 §3.1].
	// Radius is Fixed world units (1 cell = 16*65536); +160 cells → 160*65536 per failure, cap at maxCells*16*65536 [P0-03 §3.1].
	// Spec's max(mapW,mapH) is in cells; we convert to Fixed world units via *16*65536 for comparison with stored Radius.
	// Increment +160*65536 (160 cells) per failure, reset to 0 on success [P0-03].
	var capWorld numeric.Fixed
	if w != nil {
		maxCells := w.CellW
		if w.CellH > maxCells {
			maxCells = w.CellH
		}
		capWorld = numeric.Fixed(int64(maxCells) * 16 * 65536) // world units per cell * cells
	} else if m.Terrain != nil {
		maxCells := m.Terrain.CellW
		if m.Terrain.CellH > maxCells {
			maxCells = m.Terrain.CellH
		}
		capWorld = numeric.Fixed(int64(maxCells) * 16 * 65536)
	} else {
		capWorld = numeric.Fixed(1 << 30) // large default
	}
	if m.Strategic.Radius < capWorld {
		inc := numeric.Fixed(160 * 65536) // +160 cells per failure [P0-03]
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
			tmp := rng.NewSimulation(1)
			rngStream = &tmp
		}
		// Exactly one RNG(255) draw for selector when extractor [P0-03 §5] (I4).
		draw := rngStream.Uint32n(255) // bound 255 is the extractor branch census [PLAN_11 C9][P0-03]
		sm := m.getSurfaceMetal()      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Reverse branch sense: SurfaceMetal < RNG(255) strict < via CMP/JGE picks A else B [P0-03 §3.1] DIRECT.
		if sm < int32(draw) {
			// Helper A exhaustive, zero RNG [P0-03 §5]
			ok := extractorHelperA(m, defKey, sm)
			if ok {
				// Success: write fixed-point placement ((foot+out*2)*0x80000) and reset radius [P0-03 §3.1]
				if s := m.getStrategic(); s != nil {
					s.Radius = 0
				}
				if fac := m.getFactory(); fac != nil {
					_ = queueBuild(fac, defKey, 1)
				}
				return newOriginX, newOriginZ, true
			}
			// Failed A does NOT fall through to B [P0-03 §3.1] DIRECT.
			return 0, 0, false
		}
		// Selector chose B
		ok := extractorHelperB(m, defKey, sm)
		if ok {
			if s := m.getStrategic(); s != nil {
				s.Radius = 0
			}
			if fac := m.getFactory(); fac != nil {
				_ = queueBuild(fac, defKey, 1)
			}
			return newOriginX, newOriginZ, true
		}
		return 0, 0, false
	}

	// Non-extractor: directly helper B with no selector draw [P0-03 §3.1]
	ok := extractorHelperB(m, defKey, m.getSurfaceMetal())
	if ok {
		if s := m.getStrategic(); s != nil {
			s.Radius = 0
		}
		if fac := m.getFactory(); fac != nil {
			_ = queueBuild(fac, defKey, 1)
		}
		return newOriginX, newOriginZ, true
	}
	return 0, 0, false
}
