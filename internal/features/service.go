package features

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Instance is a live feature instance [05 "Feature instance and terrain cell"].
// Retail's 48-byte size is record identity; Go stores named fields and a plot
// reference rather than reproducing a packed layout [I13].
type Instance struct {
	Def     *content.FeatureDef // immutable catalog definition [02 "Feature record"]
	Terrain *world.Terrain      // plot reference [01 §6.1]
	CX, CZ  int                 // anchor cell coordinates

	// Health and reclaim progress. Damage value of the definition is the
	// completion threshold for reclaim [05 "Feature reclaim"].
	Health          int32
	MaxHealth       int32
	ReclaimProgress int32

	// Burning state [05 "Feature burning"].
	IsBurning        bool
	BurnCountdown    int32 // countdown to burn event, decremented each tick
	BurnTicks        int32 // elapsed animation ticks
	BurnDuration     int32 // finite lifetimes 46–282 visits [05 "Feature burning"]
	RemoteSuppressed bool  // multiplayer authority suppress flag

	// Sinking state [05 "Feature sinking and water interaction"].
	Y         numeric.Fixed // world Y
	Vy        numeric.Fixed // vertical velocity
	IsSinking bool
	Settled   bool

	// Animation/status byte bit0 clear means GAF at rest [06 §13.1].
	Status uint8

	// World position derived from anchor cell (centre)
	X, Z numeric.Fixed

	// Footprint cached from Def for removal without re-reading Def after clear.
	FootprintX, FootprintZ int32
}

// Cause selects the successor hop [05 "Removal and successor replacement"].
type Cause int

const (
	CauseDead    Cause = iota // ordinary destruction uses featuredead
	CauseReclaim              // reclaim completion uses featurereclamate
	CauseBurnt                // burning uses featureburnt
)

// BurnWeaponEvent records a burn weapon emission [05 "Feature burning"] [06 §13.1].
type BurnWeaponEvent struct {
	Weapon  string
	CX, CZ  int
	X, Y, Z numeric.Fixed
}

// Pool limits per [P1-10][P1-15]: catalog 0x100, anim slots 0x800, plot cell 0xD stride.
const (
	FeatureCatalogLimit  = 0x100  // 256 entries max [P1-10][P1-15]
	FeatureAnimSlots     = 0x800  // 2048 burning anim slots [P1-10][P1-15]
	PlotCellStride       = 0x0D   // 13 bytes per cell [P1-15]
	FeatureSuccessorNone = 0xFFFF // sentinel no successor [P1-10][P1-15]
)

// Service is the features runtime [PLAN_08 WU-08-6].
type Service struct {
	Terrain *world.Terrain
	Sim     *rng.Simulation // nil => rng.Global.Sim (I4)
	Crt     *rng.CRT        // for smoke jitter, not sim draws [05 "Feature burning"]
	Wind    *world.Wind

	cursor int // global cursor descending from W*H-1 with wrap-skip [06 §13.1]

	instances map[int]*Instance // key = cz*W+cx, deterministic iteration via sorted keys

	// LastReproIdx is the last cell visited by the reproduction walker, -1 if
	// the wrap-skip cell was the cursor (W*H-1 never scanned) [06 §13.1].
	LastReproIdx int

	// BurnWeaponsEmitted records burn weapon emissions for tests [05 "Feature burning"].
	BurnWeaponsEmitted []BurnWeaponEvent

	// BurnAnimationTicks reports how long a definition's burn animation runs.
	// A burning feature clears its cell when that animation finishes
	// [05 "Feature burning"], and the animation is a presentation asset this
	// package does not own — hence a seam rather than a constant. A nil hook
	// (or a zero result) means no length is known and the instance burns until
	// something else clears it. Shipped finite lifetimes forced non-looping 46-282 visits [P1-10][P1-15].
	BurnAnimationTicks func(*content.FeatureDef) int32
}

// NewService creates a service bound to terrain.
// If sim is nil the global simulation stream is used (I4).
func NewService(terrain *world.Terrain, sim *rng.Simulation, crt *rng.CRT, wind *world.Wind) *Service {
	s := &Service{
		Terrain:      terrain,
		Sim:          sim,
		Crt:          crt,
		Wind:         wind,
		instances:    make(map[int]*Instance),
		LastReproIdx: -1,
	}
	if terrain != nil {
		total := int(terrain.CellW * terrain.CellH)
		if total > 0 {
			s.cursor = total - 1 // start at W*H-1, which is never scanned [06 §13.1]
		} else {
			s.cursor = -1
		}
	}
	return s
}

func (s *Service) sim() *rng.Simulation {
	if s.Sim != nil {
		return s.Sim
	}
	return rng.Global.Sim
}

func (s *Service) crt() *rng.CRT {
	if s.Crt != nil {
		return s.Crt
	}
	return rng.Global.Crt
}

// Tick advances the feature phase one tick [06 §13.1] [05 "Feature burning"].
// Order: reproduction walker (top of phase) [06 §13.1], then burning, then sinking.
func (s *Service) Tick(tick uint32) {
	s.reproduceTick()
	s.burnTick(tick)
	s.sinkTick()
}

// TickMotion advances only the prepass/reproduction walker which belongs in
// phase 4 (effects/feature motion) [01 §4.4][06 §13.1]. Lifecycle (burning and
// sinking) belongs in phase 6. Splitting keeps reproduction ordering before
// burning/sinking and avoids double Tick when both phases call [P0-I06].
func (s *Service) TickMotion(tick uint32) {
	_ = tick
	s.reproduceTick()
}

// TickLifecycle advances only the lifecycle part of the feature phase which
// belongs in phase 6 (feature lifecycle) [01 §4.4][05 "Feature burning"][05 "Feature sinking and water interaction"].
// It runs burning (including smoke via CRT, spread via sim, successor) and then sinking.
func (s *Service) TickLifecycle(tick uint32) {
	s.burnTick(tick)
	s.sinkTick()
}

// PlaceAt stamps a feature at anchor cell (cx,cz) via the common placement
// helper with no position/velocity override [06 §13.1][P1-10][P1-15]. It is the
// public entry for mission and corpse placement; deterministic order is the
// caller's responsibility [I1]. Returns the new instance or nil on silent pool
// failure [P1-10].
func (s *Service) PlaceAt(cx, cz int, def *content.FeatureDef) *Instance {
	return s.spawnFeatureAt(cx, cz, def)
}

// PlaceAtWorld stamps a feature at world position (x,z) using floor-corrected
// cell conversion [03 §2.1] I3 and then PlaceAt. Used for corpse placement from
// unit world coordinates.
func (s *Service) PlaceAtWorld(x, z numeric.Fixed, def *content.FeatureDef) *Instance {
	if s.Terrain == nil || def == nil {
		return nil
	}
	cx := int(world.WorldToCell(x))
	cz := int(world.WorldToCell(z))
	return s.PlaceAt(cx, cz, def)
}

// PlaceCorpse stamps a corpse feature for a dying unit at its world position
// and initiates sinking when submerged [05 "Feature sinking and water interaction"].
// fromIsFeature is the dying unit's IsFeature flag; isfeature corpses never
// descend. Chain depth is already resolved by caller (low nibble) [06 §12.1] C23;
// this helper just stamps the resolved def. Returns the corpse instance or nil.
func (s *Service) PlaceCorpse(x, z numeric.Fixed, def *content.FeatureDef, fromIsFeature bool) *Instance {
	if def == nil || s.Terrain == nil {
		return nil
	}
	inst := s.PlaceAtWorld(x, z, def)
	if inst == nil {
		return nil
	}
	// Submerged start latches vy to -11468 when terrain at or below sea level
	// and dying definition lacks isfeature flag [05 "Feature sinking and water interaction"].
	s.StartSinking(inst, fromIsFeature)
	return inst
}

// SetBurnAnimationTicks installs a GAF-backed duration hook so shipped burns
// terminate at the GAF sequence length rather than infinite [05 "Feature burning"] [P0-I06].
// A nil hook or zero result falls back to finite 46-282 visits [P1-10].
func (s *Service) SetBurnAnimationTicks(fn func(*content.FeatureDef) int32) {
	s.BurnAnimationTicks = fn
}

// CorpseDefFor resolves the corpse feature for depth low-nibble [06 §12.1] C23.
// It mirrors combat.ResolveCorpse but lives in features so callers without
// combat import can still resolve. Depth 0 => nil; depth 1 => authored Corpse;
// larger depths follow featuredead chain depth-1 times.
func CorpseDefFor(unitDef *content.UnitDef, features map[string]*content.FeatureDef, depth uint8) *content.FeatureDef {
	depth &= 0x0F
	if depth == 0 || unitDef == nil {
		return nil
	}
	name := unitDef.Corpse
	if name == "" {
		return nil
	}
	ck := content.CanonicalKey(name)
	cur := features[ck]
	if cur == nil {
		return nil
	}
	if depth == 1 {
		return cur
	}
	for i := uint8(1); i < depth; i++ {
		if cur == nil {
			return nil
		}
		var nxt *content.FeatureDef
		if cur.FeatureDeadDef != nil {
			nxt = cur.FeatureDeadDef
		} else if cur.FeatureDead != "" {
			ck2 := content.CanonicalKey(cur.FeatureDead)
			nxt = features[ck2]
		} else {
			return nil
		}
		if nxt == nil {
			return nil
		}
		cur = nxt
	}
	return cur
}

// Reclaim performs feature reclaim. The payout is a one-time completion event
// adding the full feature pools to the builder [05 "Feature reclaim"].
// It verifies reclaimable and not indestructible, returns the metal/energy
// pools as float32, and replaces the feature with its reclaimed successor or
// removes it when none exists [05 "Feature reclaim"]. Burning features cannot
// be reclaimed until burn completes [05 "Feature burning"].
func (s *Service) Reclaim(u *units.Unit, f *Instance, tick uint32) (metal, energy float32) {
	if f == nil || f.Def == nil || s.Terrain == nil {
		return 0, 0
	}
	// Burning FILENAME-BASED features cannot be reclaimed — the block is
	// scoped to definitions that carry a filename (the shipped ignitables),
	// not to every burning instance [05 "Feature burning"].
	if f.IsBurning && f.Def.Filename != "" {
		return 0, 0
	}
	def := f.Def
	// Verify reclaimable and not indestructible [05 "Feature reclaim"].
	if !def.Reclaimable || def.Indestructible {
		return 0, 0
	}
	// Also check indestructible via def flag; if set, no reclaim.
	metal = float32(def.Metal)   // I2 allowlist: resource pools as float32 [05 "Feature reclaim"]
	energy = float32(def.Energy) // same
	// Apply special-player scaling where required TODO(T25) — no established
	// consumer for the builder's player mode in this phase; placeholder keeps
	// ordinary addition.
	// Replace with reclaimed successor or remove [05 "Removal and successor replacement"].
	s.replaceFeatureAt(f.CX, f.CZ, def.FeatureReclamateDef)
	_ = u
	_ = tick
	return metal, energy
}

// replaceFeatureAt performs removal and placement as one logical transition at
// the same world location [05 "Removal and successor replacement"]. Missing
// successor means final removal. It clears the whole stamped footprint and
// returns the plot cell to the free sentinel 0xFFFF [05 "Removal and successor replacement"].
func (s *Service) replaceFeatureAt(cx, cz int, successor *content.FeatureDef) {
	if s.Terrain == nil {
		return
	}
	// Locate current instance to derive footprint for clearing.
	idx := cz*int(s.Terrain.CellW) + cx
	var def *content.FeatureDef
	if inst, ok := s.instances[idx]; ok && inst != nil {
		def = inst.Def
	} else {
		// Fallback: try to resolve feature at cell via world.ResolveFeature with
		// signed-offset reading [SPEC_CONFLICTS SC6] [04 §6.2][02 "Terrain file"].
		if feat, ok := world.ResolveFeature(s.Terrain.Plot, int(s.Terrain.CellW), int(s.Terrain.CellH), cx, cz); ok {
			if d, ok := s.Terrain.FeatureDefAt(feat); ok {
				def = d
			}
		}
	}
	// Clear footprint derived from def or single cell.
	s.clearFootprint(cx, cz, def)
	if successor != nil {
		s.spawnFeatureAt(cx, cz, successor)
	}
}

// clearFootprint clears the whole stamped footprint and releases live state,
// returning cells to the free sentinel [05 "Removal and successor replacement"].
// Sentinel for free cells is 0xFFFF [GAP T14][02 "Terrain file"].
func (s *Service) clearFootprint(cx, cz int, def *content.FeatureDef) {
	if s.Terrain == nil {
		return
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	fx, fz := 1, 1
	if def != nil {
		if def.FootprintX > 0 {
			fx = int(def.FootprintX)
		}
		if def.FootprintZ > 0 {
			fz = int(def.FootprintZ)
		}
	} else {
		// Try to infer from existing instance's cached footprint.
		idx := cz*w + cx
		if inst, ok := s.instances[idx]; ok && inst != nil {
			if inst.FootprintX > 0 {
				fx = int(inst.FootprintX)
			}
			if inst.FootprintZ > 0 {
				fz = int(inst.FootprintZ)
			}
		}
	}
	for dz := 0; dz < fz; dz++ {
		for dx := 0; dx < fx; dx++ {
			px := cx + dx
			pz := cz + dz
			if px < 0 || px >= w || pz < 0 || pz >= h {
				continue
			}
			idx := pz*w + px
			// Return to free sentinel 0xFFFF [GAP T14].
			s.Terrain.Plot[idx].SetFeature(world.PlotFeatureNone)
			// Clear occupied and anchor bytes.
			s.Terrain.Plot[idx].SetFlagByte(0)
			s.Terrain.Plot[idx].SetAnchor(0, 0)
			// Release instance if anchor.
			if px == cx && pz == cz {
				delete(s.instances, idx)
			} else {
				// Fringe cells: also delete any stray instance mapping if present.
				delete(s.instances, idx)
			}
		}
	}
}

// spawnFeatureAt stamps a feature through the common placement helper with no
// position/velocity override and neutral side [06 §13.1] [P1-10][P1-15].
// Pools 0x100 catalog / 0x800 anim slots / WH*0xD grid silent fail, successors 0xFFFF [P1-10][P1-15].
// Malformed/custom: zero/negative footprints are normalized to 1x1, nil canonical keys handled, and unknown successors are sentinel 0xFFFF [P1-I05][02 "Feature record"].
func (s *Service) spawnFeatureAt(cx, cz int, def *content.FeatureDef) *Instance {
	if s.Terrain == nil || def == nil {
		return nil
	}
	// Normalize malformed for placement without mutating catalog [P1-I05]
	if IsMalformed(def) {
		def = NormalizeDef(def)
		if def == nil {
			return nil
		}
	}
	// Pools 0x100/0x800 silent fail [P1-10][P1-15]: catalog 256, anim slots 2048.
	if len(s.Terrain.FeatureDefs) >= FeatureCatalogLimit && s.featureIndexForDef(def) == world.PlotFeatureNone {
		return nil // catalog pool 0x100 silent fail [P1-10][P1-15]
	}
	if len(s.instances) >= FeatureAnimSlots {
		return nil // anim pool 0x800 silent fail [P1-10][P1-15]
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if cx < 0 || cx >= w || cz < 0 || cz >= h {
		return nil
	}
	idx := cz*w + cx
	// Target must be in-bounds and free [06 §13.1]. Caller already checked, but double-check.
	if !s.Terrain.Plot[idx].IsEmpty() {
		return nil
	}
	featIdx := s.featureIndexForDef(def)
	if featIdx == world.PlotFeatureNone {
		// Def not in terrain's FeatureDefs; for tests with synthetic terrain
		// we may have appended def, so try to find by canonical key.
		// If still not found, synthesize index 0 as placeholder for single-cell
		// tests that don't rely on FeatureDefs mapping via resolver beyond sentinel check.
		// This keeps tests deterministic even when terrain was built without a catalog.
		// We will assign 0 if terrain has at least one entry, else treat as error.
		if len(s.Terrain.FeatureDefs) > 0 {
			if len(s.Terrain.FeatureDefs) >= FeatureCatalogLimit {
				return nil // catalog pool 0x100 silent fail [P1-10][P1-15]
			}
			// Try to append def to list for future resolves.
			s.Terrain.FeatureDefs = append(s.Terrain.FeatureDefs, def)
			featIdx = uint16(len(s.Terrain.FeatureDefs) - 1)
			// Also need to ensure FeatureNames length matches if present? Not needed for logic.
		} else {
			featIdx = 0
			s.Terrain.FeatureDefs = []*content.FeatureDef{def}
		}
	}
	// Plot grid WH*0xD already allocated; footprint clipping ensures no overflow [P1-15].
	s.Terrain.Plot[idx].SetFeature(featIdx)
	s.Terrain.Plot[idx].SetFlagByte(0)
	// Create instance.
	footX := def.FootprintX
	footZ := def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	inst := &Instance{
		Def:        def,
		Terrain:    s.Terrain,
		CX:         cx,
		CZ:         cz,
		MaxHealth:  def.Damage,
		Health:     def.Damage,
		Status:     0,
		FootprintX: footX,
		FootprintZ: footZ,
	}
	// Initial Y at sampled floor (average of derived pair) [05 "Feature sinking and water interaction"].
	inst.Y = s.Terrain.CoarseHeightAt(int32(cx), int32(cz))
	// X/Z world centre of footprint.
	inst.X = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
	inst.Z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
	s.instances[idx] = inst
	// Stamp fringe cells with sentinel 0xFFFE and signed offsets to anchor
	// [SPEC_CONFLICTS SC6] signed-offset reading [02 "Terrain file"].
	for dz := 0; dz < int(footZ); dz++ {
		for dx := 0; dx < int(footX); dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			px := cx + dx
			pz := cz + dz
			if px < 0 || px >= w || pz < 0 || pz >= h {
				continue
			}
			fIdx := pz*w + px
			s.Terrain.Plot[fIdx].SetFeature(world.PlotFeatureFringe)
			s.Terrain.Plot[fIdx].SetAnchorSigned(int8(cx-px), int8(cz-pz))
			s.Terrain.Plot[fIdx].SetFlagByte(0)
		}
	}
	return inst
}

func (s *Service) featureIndexForDef(def *content.FeatureDef) uint16 {
	if s.Terrain == nil || def == nil {
		return world.PlotFeatureNone
	}
	for i, d := range s.Terrain.FeatureDefs {
		if d == def {
			return uint16(i)
		}
	}
	for i, d := range s.Terrain.FeatureDefs {
		if d != nil && d.CanonicalKey == def.CanonicalKey {
			return uint16(i)
		}
	}
	return world.PlotFeatureNone
}

// sortedInstanceKeys returns deterministic iteration order (I1).
func (s *Service) sortedInstanceKeys() []int {
	keys := make([]int, 0, len(s.instances))
	for k := range s.instances {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// RemoveFeatureAt removes a feature at anchor cell with cause-specific successor
// [05 "Removal and successor replacement"]. It is the explicit successor
// replacement entry used by damage, reclaim, and burn paths.
func (s *Service) RemoveFeatureAt(cx, cz int, cause Cause) {
	if s.Terrain == nil {
		return
	}
	idx := cz*int(s.Terrain.CellW) + cx
	var def *content.FeatureDef
	if inst, ok := s.instances[idx]; ok && inst != nil {
		def = inst.Def
	} else {
		if feat, ok := world.ResolveFeature(s.Terrain.Plot, int(s.Terrain.CellW), int(s.Terrain.CellH), cx, cz); ok {
			if d, ok := s.Terrain.FeatureDefAt(feat); ok {
				def = d
			}
		}
	}
	var succ *content.FeatureDef
	if def != nil {
		switch cause {
		case CauseDead:
			succ = def.FeatureDeadDef // featuredead successor [05 ...]
		case CauseReclaim:
			succ = def.FeatureReclamateDef // featurereclamate [05 ...]
		case CauseBurnt:
			succ = def.FeatureBurntDef // featureburnt [05 ...]
		}
	}
	s.replaceFeatureAt(cx, cz, succ)
}

// InstanceAt returns the live instance at anchor cell or nil.
func (s *Service) InstanceAt(cx, cz int) *Instance {
	if s.Terrain == nil {
		return nil
	}
	idx := cz*int(s.Terrain.CellW) + cx
	return s.instances[idx]
}

// Instances returns all live instances in deterministic order.
func (s *Service) Instances() []*Instance {
	keys := s.sortedInstanceKeys()
	out := make([]*Instance, 0, len(keys))
	for _, k := range keys {
		if inst, ok := s.instances[k]; ok && inst != nil {
			out = append(out, inst)
		}
	}
	return out
}

// PopulateFromTerrain scans the terrain plot and creates live instances for every
// anchor cell that already holds a real feature index. It is the bridge between
// world.Load's Plot expansion (which stamps 0xFFFF/0xFFFE/0xFFFD and fringe
// anchors) and the Features Service's authoritative instance map. Without this
// pass the map-authored forest on Great Divide would remain as plot sentinels
// for placement blocking but never appear in snapshot Features, so the renderer
// would draw an empty forest [05 "Feature instance and terrain cell"] [03 §5.1].
// It is idempotent: anchors already present in the map are skipped, fringe
// cells are never instanced, and void/empty sentinels are ignored. Presentation-only
// after battle entry (I6) — it does not advance RNG or sim state.
func (s *Service) PopulateFromTerrain() int {
	if s == nil || s.Terrain == nil || s.Terrain.Plot == nil {
		return 0
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if w <= 0 || h <= 0 {
		return 0
	}
	if len(s.Terrain.Plot) < w*h {
		return 0
	}
	n := 0
	for cz := 0; cz < h; cz++ {
		for cx := 0; cx < w; cx++ {
			idx := cz*w + cx
			if idx < 0 || idx >= len(s.Terrain.Plot) {
				continue
			}
			// Only anchors carry real indices; fringe (0xFFFE) and voids are skipped
			// via IsEmpty/IsVoid checks, and ResolveFeature would follow fringe to
			// its anchor which we already handle.
			cell := s.Terrain.Plot[idx]
			if cell.IsFringe() || cell.IsVoid() || cell.IsEmpty() {
				continue
			}
			if !cell.IsRealFeature() {
				continue
			}
			if _, ok := s.instances[idx]; ok {
				continue // already instanced (e.g. mission feature placed earlier)
			}
			def, ok := s.Terrain.FeatureDefAt(cell.Feature())
			if !ok || def == nil {
				continue // unbound name or out-of-range index: blocking sentinel but no visual
			}
			footX := def.FootprintX
			footZ := def.FootprintZ
			if footX <= 0 {
				footX = 1
			}
			if footZ <= 0 {
				footZ = 1
			}
			// Respect pool caps silently [P1-10][P1-15]
			if len(s.instances) >= FeatureAnimSlots {
				return n
			}
			inst := &Instance{
				Def:        def,
				Terrain:    s.Terrain,
				CX:         cx,
				CZ:         cz,
				MaxHealth:  def.Damage,
				Health:     def.Damage,
				FootprintX: footX,
				FootprintZ: footZ,
			}
			inst.Y = s.Terrain.CoarseHeightAt(int32(cx), int32(cz))
			inst.X = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
			inst.Z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
			s.instances[idx] = inst
			n++
		}
	}
	return n
}

// Cursor returns the current global cursor value for tests [06 §13.1].
func (s *Service) Cursor() int { return s.cursor }

// SetCursor sets the cursor for tests.
func (s *Service) SetCursor(c int) { s.cursor = c }
