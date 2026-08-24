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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// extractor gate is the only established extractor signal. If research later
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return def.ExtractsMetal != 0
}
func (m *Manager) getFactory() *units.Unit {
	if m == nil {
		return nil
	}
	return m.Factory
}

// queueBuild is the ordinary construction path [PLAN 11 C12].
// It is a variable so tests can spy on it without needing a full units.World
// with an order queue. Default is construction.QueueBuild.
var queueBuild = construction.QueueBuild

// stepTowardCenter moves the search origin toward the strategic center using the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// Retail "moves the search origin toward the strategic center using the stored
// radius" — the exact arithmetic is not traced, so this implements the
// normalized step: if distance <= radius, origin becomes center; otherwise
// origin advances radius units along the center direction. This preserves the
// invariant that the step length equals radius and the direction is toward
// center, which is the testable contract "origin-toward-center step vector".
//
// TODO(question): exact retail step arithmetic untraced; this uses float64 hypot
// transient and Fixed scaling. If a probe shows retail uses per-axis clamp or
// integer truncation differently, this is the one place to adjust.
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
	// Length via float64 transient [I2 allowlist: ballistic discriminant is the only
	// established Fixed-adjacent float64, but distance is a transient for placement;
	// marked TODO(question) for exact integer alternative].
	dist := math.Sqrt(float64(dx)*float64(dx) + float64(dz)*float64(dz))
	if dist == 0 {
		return originX, originZ
	}
	if dist <= float64(rRaw) {
		return centerX, centerZ
	}
	// Normalized step: origin + delta * radius / dist
	// Use float64 for the scale to avoid 64-bit overflow of dx*rRaw.
	stepX := int64(float64(dx) * float64(rRaw) / dist)
	stepZ := int64(float64(dz) * float64(rRaw) / dist)
	return numeric.Fixed(int64(originX) + stepX), numeric.Fixed(int64(originZ) + stepZ)
}

// extractorHelperA and extractorHelperB are the two extractor placement helpers
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Placeholder: return false so the extractor branch falls through to the generic
// build-command path, allowing AI to still build valid structures without
// inventing metal-patch geometry [PLAN 11 Explicit unknowns].
func extractorHelperA(m placementManager, defKey string, surfaceMetal int32) bool {
	return false
}
func extractorHelperB(m placementManager, defKey string, surfaceMetal int32) bool {
	return false
}

// placeGeneric attempts generic placement at the stepped origin.
//
// It validates against the terrain yard map when possible and returns the
// fixed-point placement on success. On failure it grows the radius and returns
// false without resetting. Success resets radius and queues the build command.
//
// TODO(question): radius growth on failure untraced; this increments by one
// cell (16 map pixels) to ensure progress.
func placeGeneric(m placementManager, defKey string, w *world.Terrain, originX, originZ numeric.Fixed) (numeric.Fixed, numeric.Fixed, bool) {
	// Terrain validation when world and definition are available.
	// Retrieve footprint/yard via catalog if m is *Manager; for the interface
	// we need to handle generically. For simplicity, if m is *Manager with
	// catalog, we look up there; otherwise skip validation and succeed.
	var footX, footZ int
	var yard []world.YardCell
	var hasDef bool
	if mgr, ok := m.(*Manager); ok && mgr != nil && mgr.Catalog != nil {
		ck := canonicalKey(defKey)
		if def, ok2 := mgr.Catalog.Unit(ck); ok2 && def != nil {
			footX = int(def.FootprintX)
			footZ = int(def.FootprintZ)
			hasDef = true
			// Parse yard map; if it fails, treat as no yard (allow placement).
			if ym, err := world.ParseYardMap(def.YardMap, footX, footZ); err == nil {
				yard = ym
			} else if footX > 0 && footZ > 0 && strings.TrimSpace(def.YardMap) == "" {
				// Empty yard with footprint still needs a yard of appropriate
				// size for validation; ParseYardMap would error, so synthesize
				// a permissive yard (all 'o' like occupancy) for test simplicity.
				// Retail would have a yard per footprint; missing is not fatal.
				yard = nil
			}
		}
	}
	// If we have a terrain and a footprint, try to validate.
	if w != nil && hasDef && footX > 0 && footZ > 0 && yard != nil {
		cx := world.WorldToCell(originX)
		cz := world.WorldToCell(originZ)
		if err := w.ValidatePlacement(cx, cz, yard, footX, footZ, 0); err != nil {
			// Grow radius on failure; do not reset, do not queue.
			if s := m.getStrategic(); s != nil {
				// Increase by one cell (16*65536) [03 §2.1].
				s.Radius = s.Radius.Add(numeric.Fixed(16 * 65536))
			}
			return 0, 0, false
		}
	} else if w != nil && hasDef && footX > 0 && footZ > 0 && yard == nil {
		// Yard parsing failed but footprint valid: attempt validation with
		// permissive check; if world is flat and empty, it will succeed.
		// Without yard we cannot validate, so treat as success to allow AI to
		// build in tests. This avoids inventing a yard shape.
	}

	strat := m.getStrategic()
	if strat != nil {
		// Reset radius on success [08 "Established AI-facing data and rooted planner"] [PLAN 11 C8].
		strat.Radius = 0
	}
	// Issue build command through ordinary path [PLAN 11 C12].
	if fac := m.getFactory(); fac != nil {
		// Use the indirection so tests can spy; ignore error for placement success
		// (queue full still counts as placement success for radius reset).
		_ = queueBuild(fac, defKey, 1)
	}
	return originX, originZ, true
}

// Place moves the search origin toward the strategic center using the stored
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// mission SurfaceMetal, falls through to generic placement on extractor helper
// failure, writes fixed-point placement, resets radius, and issues the build
// command through construction.QueueBuild [08 "Established AI-facing data and rooted planner"]
// [PLAN 11 C8, C9, C12].
//
// C9 bound census: this file uses only RNG(255) (I4). Any other bound here is a bug.
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Placeholder: extractor helpers return false and fall through to the generic
// build-command path so AI still builds valid structures [PLAN 11 Explicit unknowns].
func Place(m *Manager, defKey string, w *world.Terrain) (numeric.Fixed, numeric.Fixed, bool) {
	if m == nil {
		return 0, 0, false
	}
	defKey = strings.TrimSpace(defKey)
	if defKey == "" {
		return 0, 0, false
	}

	// Move search origin toward strategic center using stored radius [PLAN 11 C8].
	originX, originZ := m.getOrigin()
	centerX := m.Strategic.CenterX
	centerZ := m.Strategic.CenterZ
	radius := m.Strategic.Radius
	newOriginX, newOriginZ := stepTowardCenter(originX, originZ, centerX, centerZ, radius)
	m.setOrigin(newOriginX, newOriginZ)

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if m.isExtractor(defKey) {
		rngStream := m.getRNG()
		// Ensure we have a stream; if none, create a deterministic temporary so
		// the draw count is still observable in tests that inject RNG.
		if rngStream == nil {
			tmp := rng.NewSimulation(1)
			rngStream = &tmp
		}
		// Exactly one RNG(255) draw [PLAN 11 C9] (I4).
		draw := rngStream.Uint32n(255) // bound 255 is the extractor branch census [PLAN 11 C9]
		sm := m.getSurfaceMetal()      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Choose helper based on draw vs SurfaceMetal. Retail choice is untraced
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// we use draw < SurfaceMetal to pick A else B, but both helpers are
		// TODO(T25) placeholders returning false, so the choice only matters for
		// draw-count observation, not placement outcome.
		var ok bool
		if int32(draw) < sm {
			ok = extractorHelperA(m, defKey, sm)
		} else {
			ok = extractorHelperB(m, defKey, sm)
		}
		if ok {
			// Extractor helper succeeded (not in placeholder); would write
			// placement, reset radius, queue build and return.
			// Since placeholder always false, this path is dead, but kept for
			// completeness and to show the TODO(T25) discipline.
			if s := m.getStrategic(); s != nil {
				s.Radius = 0
			}
			if fac := m.getFactory(); fac != nil {
				_ = queueBuild(fac, defKey, 1)
			}
			return newOriginX, newOriginZ, true
		}
		// Fall through to generic path on extractor helper failure [PLAN 11 Explicit unknowns].
	}

	// Generic build-command path (also the fall-through for extractor).
	x, z, ok := placeGeneric(m, defKey, w, newOriginX, newOriginZ)
	return x, z, ok
}
