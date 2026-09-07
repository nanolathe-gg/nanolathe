package ai

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// refreshInterval is the strategic-state refresh period in ticks [08
// "Established AI-facing data and rooted planner"].
const refreshInterval uint32 = 30

// ClassVector holds the three signed class coefficients per type [08
// "Established AI-facing data and rooted planner"].
type ClassVector struct {
	C0 int8 // otherMix coefficient
	C1 int8 // metalMix coefficient
	C2 int8 // energyMix coefficient
}

// PlacementRegion is one immutable lattice region drawn when the strategic
// state is constructed. The scatter helper selects Land when MinWaterDepth is
// negative and Water otherwise [08 R-AI-03 §4].
type PlacementRegion struct {
	CellW   int16
	CellH   int16
	OffsetX int16
	OffsetZ int16
}

// Strategic is the fixed-size retail strategic state [08
// "Established AI-facing data and rooted planner"] per I13.
// Go uses named fields and embeds this state in Manager [PLAN_11 Public API].
type Strategic struct {
	// Strategic center, recomputed every refresh on all three axes in the
	// authoritative 16.16 fixed-point representation [08 R-AI-03 §2; I2].
	CenterX numeric.Fixed
	CenterY numeric.Fixed
	CenterZ numeric.Fixed

	// Radius is a plain count of world units, grown by 160 before each
	// placement attempt [08 R-AI-03 §2].
	Radius int32

	// MetalSpots is the battle-entry snapshot consumed only by the exhaustive
	// extractor helper. Reclaim and strategic refresh never rebuild it
	// [08 R-AI-03 §1].
	MetalSpots []MetalSpot

	// LandRegion and WaterRegion are the eight constructor-draw products used
	// by the scatter lattice [08 R-AI-03 §4].
	LandRegion  PlacementRegion
	WaterRegion PlacementRegion

	// Refresh gate state: a refresh is due when at least 30 ticks elapsed [08].
	LastRefreshTick uint32

	// Per-type completed counts, keyed by canonical definition name [08].
	Counts map[string]int32

	// BuildCapable is the owning player's count of live, completed units whose
	// definition carries a non-empty build-option list. The 30-tick refresh
	// clears it and increments it once per qualifying unit during its live-pool
	// scan; the construction task reads it and never recomputes it
	// [08 R-AI-01 §3][08 R-P0-05 §5].
	BuildCapable int32

	// Per-type refreshed class vectors [08].
	ClassVectors map[string]ClassVector

	// Initialization-only per-type vector [P0-01]. It is never overwritten by
	// refresh recomputation [08].
	InitVectors map[string]int8

	// Per-type single coefficient recomputed alongside the class triple [P0-01].
	SingleVectors map[string]int8

	// Catalog is the content catalog for definition lookups [P0-I16].
	Catalog *content.Catalog

	// unitLimit is the session's per-player unit limit — the Max Units lobby
	// option, or the mission's unit-count key — copied once into a 16-bit
	// session word when the world is built [08 R-AI-01 §13]. unitLimitBound
	// records that a session actually supplied it: an unbound fixture must not
	// read the zero value as a real cap, because (0 >> 1) sits below every
	// non-zero live count and would fire the half-capacity addend everywhere.
	unitLimit      uint16
	unitLimitBound bool

	// maxWind is the map's maximum wind word — the session's authored
	// `maxwindspeed`, with the canonical 2000 fallback a map that authors none
	// gets [05 R-PROD-01 §3]. The class routine's wind-generator zeroing branch
	// is its only reader here [08 R-P0-05 §9]. maxWindBound distinguishes an
	// unbound fixture from an authored zero: an unbound one must not zero every
	// wind generator, so the branch does not fire without a session word.
	maxWind      int32
	maxWindBound bool

	// liveUnitCount mirrors the owning player record's live unit count, the
	// 16-bit field the class routine reaches through the strategic state's
	// back-pointer. It is incremented at unit creation and decremented in unit
	// teardown, so the refresh samples it from the live pool rather than
	// deriving it from the completed counts [08 R-AI-01 §13].
	liveUnitCount uint16

	// energyEnvironment reads the live wind scalar and immutable map tidal
	// strength used by the computer player's signed net-energy query. It is a
	// session binding rather than copied strategic state so a gated 30-tick
	// recompute observes the current wind value [05 R-PROD-01 §1][08
	// R-P0-05 §5–§6].
	energyEnvironment func() (windScalar, tidalStrength float32)

	// rebuildRegistry is the seam onto the OTHER half of this one retail
	// routine: the per-side target registry's candidate lists and
	// secondary-list gate, which live in internal/combat [06 §3.1].
	//
	// "The per-side target registry and the strategic state are one object per
	// player slot, the registry rebuild and the strategic refresh are one
	// routine, and the bound-30 draw here is the one draw [08 R-AI-01 §16]
	// records" [06 §3.1]. This build keeps the two halves in two packages
	// because internal/combat may not import internal/ai and internal/ai may
	// not import internal/combat, so the session binds this callback and
	// MaybeRefresh's gate below is the single clock for both. Before WU-19-126
	// each half kept its own cadence word and the two could drift apart by up
	// to thirty ticks.
	//
	// A nil callback means no session bound one (a bare fixture): the census
	// half still refreshes on the gate, exactly as it did before the seam
	// existed.
	rebuildRegistry func(tick uint32, player uint8)

	// setupDraws retains the eight construction-time values for draw-ledger
	// verification; LandRegion and WaterRegion are their semantic products
	// [08 R-AI-03 §4].
	setupDraws      [8]uint32
	setupDrawsReady bool
	negRegionW      uint32
	negRegionH      uint32
	posRegionW      uint32
	posRegionH      uint32
}

// BindEnergyEnvironment supplies the two battle values read by the signed
// per-definition net-energy query. The provider is invoked only when class
// vectors are recomputed and consumes no random numbers [05 R-PROD-01 §1]
// [08 R-P0-05 §5–§6].
func (s *Strategic) BindEnergyEnvironment(read func() (windScalar, tidalStrength float32)) {
	if s == nil {
		return
	}
	s.energyEnvironment = read
}

// BindTargetRegistryRebuild supplies the combat-side half of the one 30-tick
// routine [06 §3.1]. The session binds it; the gate in MaybeRefresh then drives
// both halves from one clock, in retail's order — the candidate lists and the
// secondary-list gate first, then the census and the centroid, then the single
// bound-30 draw [06 §3.1][08 R-AI-01 §16].
func (s *Strategic) BindTargetRegistryRebuild(rebuild func(tick uint32, player uint8)) {
	if s == nil {
		return
	}
	s.rebuildRegistry = rebuild
}

// TargetRegistryRebuildBound reports whether a session has bound the combat
// half of the routine, so a caller can bind it exactly once.
func (s *Strategic) TargetRegistryRebuildBound() bool {
	return s != nil && s.rebuildRegistry != nil
}

// SetUnitLimit supplies the session's per-player unit limit, the only global
// the class routine's half-capacity comparison reads [08 R-AI-01 §13]. It is
// one word for the whole battle, written when the world is built, so a session
// binds it once before the construction-time class computation. The value is
// held in the record's 16-bit width; a negative argument is a setup error and
// leaves the limit unbound.
func (s *Strategic) SetUnitLimit(limit int32) {
	if s == nil || limit < 0 {
		return
	}
	s.unitLimit = uint16(limit)
	s.unitLimitBound = true
}

// SetMaxWind supplies the map's maximum wind word, the second operand of the
// class routine's wind-generator zeroing branch [08 R-P0-05 §9]. It is the
// session's authored `maxwindspeed` (canonical maps that author none get 2000,
// [05 R-PROD-01 §3]); a session binds it once, before the construction-time
// class computation, because that computation already consults it. A negative
// argument is a setup error and leaves the word unbound.
func (s *Strategic) SetMaxWind(maxWind int32) {
	if s == nil || maxWind < 0 {
		return
	}
	s.maxWind, s.maxWindBound = maxWind, true
}

// MaxWind reports the bound maximum wind word and whether a session supplied
// one [08 R-P0-05 §9].
func (s *Strategic) MaxWind() (int32, bool) {
	if s == nil {
		return 0, false
	}
	return s.maxWind, s.maxWindBound
}

// windGeneratorSuppressed is the class routine's third zeroing branch: zero the
// first coefficient when the definition's `windgenerator` compares not equal to
// floating zero AND the map's maximum wind word is strictly less than the wind
// divisor divided by two — a signed integer divide of the compiled-in 5000
// [05 R-PROD-01 §3], so the threshold is 2500. The comparison is strict and
// integer [08 R-P0-05 §9]. On a map whose maximum wind is below 2500 every wind
// generator's class coefficient is zero, which suppresses the definition in the
// construction selection.
func (s *Strategic) windGeneratorSuppressed(def *content.UnitDef) bool {
	if s == nil || def == nil || !s.maxWindBound {
		return false
	}
	return def.WindGenerator != 0 && s.maxWind < windGeneratorMinimumMaxWind
}

// windGeneratorMinimumMaxWind is 5000/2 with the signed integer divide retail
// takes [08 R-P0-05 §9][05 R-PROD-01 §3].
const windGeneratorMinimumMaxWind = int32(5000 / 2)

// UnitLimit reports the bound per-player unit limit and whether a session
// supplied one [08 R-AI-01 §13].
func (s *Strategic) UnitLimit() (uint16, bool) {
	if s == nil {
		return 0, false
	}
	return s.unitLimit, s.unitLimitBound
}

// LiveUnitCount reports the owning player record's live unit count as the last
// refresh sampled it [08 R-AI-01 §13].
func (s *Strategic) LiveUnitCount() uint16 {
	if s == nil {
		return 0
	}
	return s.liveUnitCount
}

// halfCapacity reports the class routine's half-capacity comparison: the
// session's per-player unit limit shifted right one, compared unsigned against
// the owning player's live unit count [08 R-AI-01 §13]. It fires for any player
// that owns more than half its unit cap, which is ordinary late-game state. An
// unbound limit is a setup gap, not a zero cap, so it never fires.
func (s *Strategic) halfCapacity() bool {
	if s == nil || !s.unitLimitBound {
		return false
	}
	return (s.unitLimit >> 1) < s.liveUnitCount
}

// InitializeRandomState consumes the strategic-constructor draws once. The
// first pair seeds the negative-slope region dimensions and the second pair
// seeds the positive-slope dimensions; those derived dimensions are the
// bounds of the following draws. A missing RNG is a setup failure [08].
func (s *Strategic) InitializeRandomState(r *rng.Simulation) bool {
	if s == nil || s.setupDrawsReady || r == nil {
		return false
	}
	// Constructor order and derived bounds are load-bearing: draw 10 derives
	// width 11..20; draw 3 derives height 11..13; draw 20 derives width 14..33;
	// draw 3 derives height 14..16 [08 RNG inventory].
	s.setupDraws[0] = r.Uint32n(10)
	s.negRegionW = 11 + s.setupDraws[0]
	s.setupDraws[1] = r.Uint32n(3)
	s.negRegionH = 11 + s.setupDraws[1]
	s.setupDraws[2] = r.Uint32n(s.negRegionW)
	s.setupDraws[3] = r.Uint32n(s.negRegionH)
	s.setupDraws[4] = r.Uint32n(20)
	s.posRegionW = 14 + s.setupDraws[4]
	s.setupDraws[5] = r.Uint32n(3)
	s.posRegionH = 14 + s.setupDraws[5]
	s.setupDraws[6] = r.Uint32n(s.posRegionW)
	s.setupDraws[7] = r.Uint32n(s.posRegionH)
	s.LandRegion = PlacementRegion{
		CellW:   int16(s.negRegionW),
		CellH:   int16(s.negRegionH),
		OffsetX: int16(s.setupDraws[2]) - int16(s.negRegionW)/2,
		OffsetZ: int16(s.setupDraws[3]) - int16(s.negRegionH)/2,
	}
	s.WaterRegion = PlacementRegion{
		CellW:   int16(s.posRegionW),
		CellH:   int16(s.posRegionH),
		OffsetX: int16(s.setupDraws[6]) - int16(s.posRegionW)/2,
		OffsetZ: int16(s.setupDraws[7]) - int16(s.posRegionH)/2,
	}
	s.setupDrawsReady = true
	return true
}

// Init initializes per-type state once at battle setup [08 "Established AI-facing data and rooted planner"].
// It clears completed counts to zero and computes the class vectors once for
// the supplied types at construction.
// The type list should be CanonicalKey values of the catalog's unit types; sorting ensures determinism (I1).
// The constructor sequence is described in [08; P0-01].
func (s *Strategic) Init(types []string) {
	if s == nil {
		return
	}
	if s.Counts == nil {
		s.Counts = make(map[string]int32)
	}
	if s.ClassVectors == nil {
		s.ClassVectors = make(map[string]ClassVector)
	}
	if s.InitVectors == nil {
		s.InitVectors = make(map[string]int8)
	}
	if s.SingleVectors == nil {
		s.SingleVectors = make(map[string]int8)
	}
	sorted := make([]string, len(types))
	copy(sorted, types)
	sort.Strings(sorted)
	for _, t := range sorted {
		ck := canonicalKey(t)
		if ck == "" {
			continue
		}
		if _, ok := s.Counts[ck]; !ok {
			s.Counts[ck] = 0
		}
		if _, ok := s.ClassVectors[ck]; !ok {
			s.ClassVectors[ck] = ClassVector{}
		}
		if _, ok := s.InitVectors[ck]; !ok {
			s.InitVectors[ck] = 0
		}
		if _, ok := s.SingleVectors[ck]; !ok {
			s.SingleVectors[ck] = 0
		}
	}
	// Write the initialization-only vector [P0-01].
	s.InitClassVectors()
	// Compute the refresh-written vectors once at construction; later writes are
	// gated by the refresh draw [P0-01].
	s.recomputeClassVectors()
}

// InitClassVectors writes the initialization-only vector [P0-01]: zero, plus
// 40 when the definition's authored `bmcode` byte is zero (the building class)
// and plus 20 when its compiled build-option list is non-empty
// [08 R-P0-05 §5][08 R-P0-05 §9]. The refresh never rewrites it.
//
// The earlier caution here — that the category flag had no recovered key and
// that BMCode must not be substituted for it — is withdrawn by [08 R-P0-05 §9]:
// it is that byte, the same one the placement validator dispatches on.
//
// These weights are live, not inert: they are the per-unit weight the strategic
// centre applies in refreshCountsAndCenter [08 R-P0-05 §10].
//
// Build-option list non-empty is checked only via the compiled catalog's
// authored BuildMenus entry [P0-01 §2.2] [R-P0-05].
func (s *Strategic) InitClassVectors() {
	if s.InitVectors == nil {
		s.InitVectors = make(map[string]int8)
	}
	if len(s.InitVectors) == 0 && len(s.ClassVectors) == 0 {
		return
	}
	// Collect keys from both maps to ensure determinism (I1). Prefer InitVectors keys, but also include ClassVectors.
	keysSet := make(map[string]struct{})
	for k := range s.InitVectors {
		keysSet[k] = struct{}{}
	}
	for k := range s.ClassVectors {
		keysSet[k] = struct{}{}
	}
	for k := range s.Counts {
		keysSet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, ck := range keys {
		c := int32(0)
		// The category flag IS `bmcode`: the initialization pass adds 40 when
		// the definition's authored `bmcode` byte is zero — the building class,
		// the same byte the placement validator dispatches on [08 R-P0-05 §9]
		// [08 R-AI-03 §7.4]. The earlier caution here against substituting
		// BMCode is withdrawn by that section. So a plain building initializes
		// to 40, a factory or construction building to 60, a mobile unit to 0
		// or 20 (a mobile builder).
		if def := s.lookupDef(ck); def != nil && !def.BMCode {
			c += 40
		}
		// A non-empty authored build menu contributes 20 [P0-01].
		hasBuild := s.hasBuildOptions(ck)
		if hasBuild {
			c += 20
		}
		if c > 127 {
			c = 127
		}
		if c < -128 {
			c = -128
		}
		s.InitVectors[ck] = int8(c)
		// Ensure ClassVectors and SingleVectors have entries for this key (zero-initialized already by Init).
		if _, ok := s.ClassVectors[ck]; !ok {
			s.ClassVectors[ck] = ClassVector{}
		}
		if _, ok := s.SingleVectors[ck]; !ok {
			s.SingleVectors[ck] = 0
		}
	}
}

// MaybeRefresh refreshes strategic state if 30 ticks have elapsed since LastRefreshTick [08 "Established AI-facing data and rooted planner"].
// Per-type completed counts and strategic center are recomputed at each refresh;
// per-type class vectors are recomputed ONLY when RNG(30)==0 at a refresh, plus once at init — never otherwise (I4).
// Exactly one RNG(30) draw is consumed per refresh; any other bound in this file is a bug per C9 (I4).
// Returns true iff a refresh was performed.
//
// This is also the per-side TARGET REGISTRY rebuild of [06 §3.1] — one routine
// in retail, two packages here — so on a due it runs, in this order: the bound
// combat-side rebuild of the candidate lists and the secondary-list gate, the
// census and the weighted centroid, then the one bound-30 draw whose zero
// outcome recomputes the class vectors [06 §3.1][08 R-AI-01 §16].
//
// The caller is the per-player phase, once per visited slot in ascending slot
// order, after that slot's manager tick and before its per-unit visits, and
// the gate is null-checked on the strategic state rather than
// controller-checked: "the local human's slot draws on the same 30-tick
// cadence as a computer slot and a one-human, one-computer game consumes two
// draws per thirty ticks" [06 §3.1 "Which slots draw"][08 R-AI-01 §16].
func (s *Strategic) MaybeRefresh(tick uint32, r *rng.Simulation, player uint8, w *units.World) bool {
	if s == nil {
		return false
	}
	// Refresh performs an authoritative random gate. Without the session-owned
	// stream, leave counts, center, and refresh tick untouched [I4].
	if r == nil {
		return false
	}
	// Gate: tick >= LastRefreshTick+30, expressed as unsigned subtraction so tick
	// wrap follows the simulation clock [08].
	if tick-s.LastRefreshTick < refreshInterval {
		return false
	}
	// This gate is the ONE gate of the one retail routine [06 §3.1]: the
	// per-side target registry rebuild and this strategic refresh are the same
	// body, and the bound-30 draw below is its single draw. The combat half —
	// the primary and secondary candidate lists and the secondary-list gate —
	// runs FIRST, before the census and the centroid, because the retail body
	// classifies each unit into the lists and the counters in one walk and the
	// draw is taken at the end of it [06 §3.1][08 R-AI-01 §16]. The two halves
	// are two walks here and one walk there; the observable difference is
	// nothing but the walk count, because neither half reads what the other
	// writes.
	if s.rebuildRegistry != nil {
		s.rebuildRegistry(tick, player)
	}
	// Counts and strategic center are rebuilt at every due refresh [08].
	s.refreshCountsAndCenter(player, w)
	s.LastRefreshTick = tick
	// Class vectors gated on single RNG(30)==0 draw [08 ...] (I4). Bound census: 30 here (C9).
	if r.Uint32n(refreshInterval) == 0 { // [08 "Established AI-facing data and rooted planner"] RNG(30) gate (I4)
		s.recomputeClassVectors()
	}
	return true
}

func (s *Strategic) refreshCountsAndCenter(player uint8, w *units.World) {
	if s.Counts == nil {
		s.Counts = make(map[string]int32)
	}
	// Preserve known type keys, zero them for recount (retail clears i16[ntypes] each refresh) [strategic-ai.md §4].
	for k := range s.Counts {
		s.Counts[k] = 0
	}
	for k := range s.ClassVectors {
		if _, ok := s.Counts[k]; !ok {
			s.Counts[k] = 0
		}
	}
	// The refresh clears the build-capable count before its live-pool scan
	// [08 R-AI-01 §3][08 R-P0-05 §5].
	s.BuildCapable = 0
	// The class routine's half-capacity comparison reads the owning player
	// record's live unit count, which counts every allocated unit and not only
	// the completed ones the loop below recounts [08 R-AI-01 §13]. The record's
	// field is 16 bits wide.
	s.liveUnitCount = 0
	if w != nil {
		s.liveUnitCount = uint16(w.LiveCountForPlayer(int(player)))
	}
	// The strategic centre is the WEIGHTED centroid of [08 R-P0-05 §10], not an
	// unweighted mean: the initialization-only per-type byte is the per-unit
	// weight, and it has no other reader in retail [08 R-AI-01 §16]. All four
	// accumulators are 32-bit floats and each coordinate term is the raw 16.16
	// word times the weight times the single-precision reciprocal of one, so a
	// term is the coordinate in whole world units times the weight.
	var weight, accX, accY, accZ float32
	if w != nil {
		// Stable iteration: units.World.Iter is pool asc; we additionally filter by player asc already handled by Iter order (I1).
		for _, u := range w.Iter() {
			// The walk visits units that are alive and NOT DYING
			// [08 R-P0-05 §10]; a latched death mark is separate from Alive,
			// which the phase-2 finalizer clears later [04 §2.3][04 §2.4].
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			if u.Owner != player {
				continue
			}
			// Completed counts only: Remaining 0 means built; nanoframes have Remaining>0 [04 §2.3].
			if u.Remaining != 0 {
				continue
			}
			if u.Def == nil {
				continue
			}
			ck := u.Def.CanonicalKey
			if ck == "" {
				ck = canonicalKey(u.Def.UnitName)
			} else {
				ck = canonicalKey(ck)
			}
			if ck == "" {
				continue
			}
			// Ensure map has entry for this type even if not pre-initialized.
			if _, ok := s.Counts[ck]; !ok {
				// keep determinism by not altering iteration during recount; just set.
				s.Counts[ck] = 0
			}
			if _, ok := s.ClassVectors[ck]; !ok {
				// Lazily ensure vector exists; will be populated on next gated recompute or init.
				if s.ClassVectors == nil {
					s.ClassVectors = make(map[string]ClassVector)
				}
				s.ClassVectors[ck] = ClassVector{}
			}
			if _, ok := s.InitVectors[ck]; !ok {
				if s.InitVectors == nil {
					s.InitVectors = make(map[string]int8)
				}
				s.InitVectors[ck] = 0
			}
			if _, ok := s.SingleVectors[ck]; !ok {
				if s.SingleVectors == nil {
					s.SingleVectors = make(map[string]int8)
				}
				s.SingleVectors[ck] = 0
			}
			s.Counts[ck]++
			if s.hasBuildOptions(ck) {
				// The build-capable count is the construction task's own
				// per-player gate input [08 R-AI-01 §3].
				s.BuildCapable++
			}
			// One map READ per unit, never an iteration: the key is already
			// canonical here, so this is not a sim-visible map range (I1).
			// The byte is 40 for a building, 60 for a building with a build
			// list, 20 for a mobile builder and 0 for every other mobile unit
			// [08 R-P0-05 §9]. There is no comparison on it, no clamp and no
			// per-definition gate: a zero weight contributes nothing, but the
			// unit is still walked and still counted above [08 R-P0-05 §10].
			cw := float32(s.InitVectors[ck])
			weight += cw
			accX += float32(u.X) * cw * invFixedOne
			accY += float32(u.Y) * cw * invFixedOne
			accZ += float32(u.Z) * cw * invFixedOne
		}
	}
	// Only when the weight sum is non-zero is each axis divided by it; the axis
	// is then scaled back to 16.16 and truncated toward zero into the centre
	// word [08 R-P0-05 §10][08 R-AI-01 §16][I3]. A zero weight sum leaves the
	// accumulators as they are — which is zero, because the initialization byte
	// is never negative — so the centre words are the truncation of zero,
	// the "centre unset" state the explore task tests [08 R-AI-01 §6]. That is
	// also what this function produced before for an empty walk, so the empty
	// case is unchanged.
	if weight != 0 {
		accX /= weight
		accY /= weight
		accZ /= weight
	}
	s.CenterX = centreWord(accX)
	s.CenterY = centreWord(accY)
	s.CenterZ = centreWord(accZ)
}

// invFixedOne is the single-precision reciprocal of one 16.16 unit that scales
// a raw coordinate word to whole world units inside the centre accumulation
// [08 R-P0-05 §10]. It is exact in binary.
const invFixedOne = 1 / float32(1<<16)

// centreWord finishes one axis of the strategic centre: retail multiplies the
// float accumulator by the double constant 65536.0 and truncates toward zero
// through the shared float-to-integer conversion [08 R-P0-05 §10][I3]. The
// scale is a power of two, so the product is exact and forming it in single
// precision yields the identical integer; no float64 is introduced for it.
func centreWord(acc float32) numeric.Fixed {
	return numeric.Fixed(int64(acc * (1 << 16)))
}

// lookupDef returns the UnitDef for canonical key ck via s.Catalog if available [P0-I16].
// Returns nil if not found. Used by class-vector computation to read economy cost fields etc [P0-01].
func (s *Strategic) lookupDef(ck string) *content.UnitDef {
	if ck == "" {
		return nil
	}
	if s != nil && s.Catalog != nil && s.Catalog.Units != nil {
		if def, ok := s.Catalog.Units[ck]; ok {
			return def
		}
	}
	return nil
}

// hasBuildOptions reports whether the compiled catalog has a non-empty
// authored build-option list for ck. The compiled build-menu catalog is the only authoritative
// adapter. A Builder flag without a resolved list is not a substitute for the
// runtime count [R-P0-05] [I9].
func (s *Strategic) hasBuildOptions(ck string) bool {
	if s != nil && s.Catalog != nil && s.Catalog.BuildMenus != nil {
		if page, ok := s.Catalog.BuildMenus[ck]; ok && page != nil && len(page.Buttons) > 0 {
			return true
		}
	}
	return false
}

// classify evaluates the established signed net-energy query used by the
// class routine. Its semantic name remains unknown; the exact branch order,
// definition inputs, signs, and live battle inputs are established [05
// R-PROD-01 §1][08 R-P0-05 §5]. Positive results consume energy and
// negative results produce it. The float32 return is the recovered helper
// boundary consumed by the class-vector arithmetic.
func classify(def *content.UnitDef, windScalar, tidalStrength float32) float32 {
	if def == nil {
		return 0
	}
	// UnitDef retains parser precision, but these retail definition fields are
	// single precision. Narrow before both predicate and arithmetic so a value
	// that becomes float32 zero cannot incorrectly block a later branch [05
	// R-PROD-01 §1][fmt fbi].
	energyUse := float32(def.EnergyUse)
	windGenerator := float32(def.WindGenerator)
	tidalGenerator := float32(def.TidalGenerator)
	if energyUse != 0 {
		return energyUse
	}
	if windGenerator > 0 {
		return -(windScalar * windGenerator)
	}
	if tidalGenerator > 0 {
		return -(tidalStrength * tidalGenerator)
	}
	return 0
}

func (s *Strategic) classify(def *content.UnitDef) float32 {
	var windScalar, tidalStrength float32
	if s != nil && s.energyEnvironment != nil {
		windScalar, tidalStrength = s.energyEnvironment()
	}
	return classify(def, windScalar, tidalStrength)
}

// ftol truncates toward zero as required by the retail conversion contract
// [01 §8; P0-01 §4; I3].
// Narrow to float32 at CALL boundaries is done by caller passing float32.
func ftol(v float32) int32 {
	return int32(v)
}

// clamp100 clamps to [-100,100] before i8 store [P0-01 §4].
func clamp100(v int32) int32 {
	if v > 100 {
		return 100
	}
	if v < -100 {
		return -100
	}
	return v
}

// recomputeClassVectors recomputes per-type class vectors [08
// "Established AI-facing data and rooted planner"]. Retail arithmetic uses
// the constants and narrowing boundaries recorded in [P0-01 §4]: zero,
// -0.01, -0.002, 30, -0.0025, 5, 100, and -0.02.
// Every float→int via __ftol trunc toward zero with narrow to float32 at each CALL (FSTP) [P0-01 §4].
// Clamps to [-100,100] before i8 store. Zero RNG inside routine [P0-01 §5].
// TODO(T23): platform residual, not a gap in this routine. The narrowing to
// float32 at every helper invocation boundary is established and reproduced
// above; what is not established is the x87 control word in force between those
// points, and doc 08 records it as exactly this class — "an unknown of platform
// residual class; the default rounding mode is assumed"
// [08 "What remains not established"][P0-01 §8]. It can only change a result if
// the retail word differs from the default; nothing here depends on the answer.
func (s *Strategic) recomputeClassVectors() {
	if s.ClassVectors == nil {
		s.ClassVectors = make(map[string]ClassVector)
		return
	}
	if s.SingleVectors == nil {
		s.SingleVectors = make(map[string]int8)
	}
	if len(s.ClassVectors) == 0 && len(s.SingleVectors) == 0 && len(s.Counts) == 0 {
		return
	}
	// Collect keys from the initialized vectors and counts, then process them in
	// ascending canonical order [P0-01 §3; I1].
	keysSet := make(map[string]struct{})
	for k := range s.ClassVectors {
		keysSet[k] = struct{}{}
	}
	for k := range s.SingleVectors {
		keysSet[k] = struct{}{}
	}
	for k := range s.Counts {
		keysSet[k] = struct{}{}
	}
	for k := range s.InitVectors {
		keysSet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys) // determinism (I1) ascending type index
	for _, ck := range keys {
		def := s.lookupDef(ck)
		// Ensure maps have entries
		if _, ok := s.ClassVectors[ck]; !ok {
			s.ClassVectors[ck] = ClassVector{}
		}
		if _, ok := s.SingleVectors[ck]; !ok {
			s.SingleVectors[ck] = 0
		}
		count := int32(0)
		if v, ok := s.Counts[ck]; ok {
			count = v
		}
		// First coefficient (single vector) [P0-01 §3].
		acc0 := int32(1)
		if def != nil && def.ExtractsMetal != 0 { // exact float zero test [P0-01 §2.2]
			acc0 = 11
		}
		if def != nil && def.MakesMetal != 0 { // [P0-01 §2.2; R-P0-05]
			acc0 += 10
		}
		fval := s.classify(def)
		if fval < 0 {
			acc0 += 10
		}
		// Costs already carry the definition single-float store [02 R-KEYS-01 §5].
		// Score operations retain their own stores [08 R-P0-05].
		costMetal := float32(0)
		costEnergy := float32(0)
		if def != nil {
			costMetal = def.BuildCostMetal
			costEnergy = def.BuildCostEnergy
		}
		t0f := float32(acc0) + costMetal*float32(-0.01)
		t0 := ftol(t0f) // narrow to float32 at CALL then ftol [P0-01 §4]; platform-residual control-word marker above
		t1f := float32(t0) + costEnergy*float32(-0.002)
		t1 := ftol(t1f)

		// weapon budget
		wBase := int32(1)
		if def != nil && def.CanAttack { // [P0-01 §2.2; R-P0-05]
			wBase = 11
		}
		wSum := wBase
		if def != nil {
			weapons := []*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def}
			for _, wp := range weapons {
				if content.IsWeaponInactive(wp) {
					// nil, or the record-0 inactive sentinel a missed link
					// resolves to — not a weapon [02 §5 R-CONTENT-02].
					continue
				}
				// "Slot active" is the record-0 sentinel test and nothing more.
				// The class routine reads one byte of the linked weapon catalog
				// record; the catalog loader's prologue stamps every record
				// with its own index before any TDF is parsed, so record 0 —
				// what an unresolved authored name links to — reads 0 and every
				// real weapon reads its non-zero index. There is no separate
				// runtime bit, and skipping nil and sentinel links (above) is
				// already exactly retail's test [08 R-P0-05 §9].
				// The class routine reads the damage word (the weapon parser's
				// DAMAGE/default key) divided by 40 and the range word (the range
				// key) divided by 100; reloadtime is stored elsewhere (scaled by
				// thirty) and is not read by this routine — an earlier "reload
				// divided by 100" reading is retracted [08 "Class routine weapon
				// reads"].
				dmg := int32(wp.DamageDefault)
				rnge := int32(wp.Range)
				wSum = wSum + dmg/40 + 5 + rnge/100
			}
		}
		wSum = clamp100(wSum) // also clamp to 100 via intermediate steps [P0-01 §3]
		acc0Final := wSum + t1
		acc0Final = clamp100(acc0Final)
		s.SingleVectors[ck] = int8(acc0Final)

		// Other-mix coefficient [P0-01 §3].
		acc1 := int32(0)
		if def != nil && def.CanAttack { // [P0-01 §2.2; R-P0-05]
			acc1 = 21
		}
		if def != nil && def.Builder && count < 3 { // [P0-01 §2.2; R-P0-05]
			acc1 += 30
		}
		if fval < 0 {
			acc1 += 50
		}
		if def != nil && def.ExtractsMetal != 0 {
			acc1 += 50
		}
		if def != nil && def.MakesMetal != 0 { // [P0-01 §2.2; R-P0-05]
			acc1 += 25
		}
		if def != nil && def.CanFly { // [P0-01 §2.2; R-P0-05]
			acc1 += 40
		}
		if def != nil && def.SonarDistance != 0 { // [P0-01 §2.2; R-P0-05]
			acc1 += 15
		}
		if def != nil && def.RadarDistance != 0 { // [P0-01 §2.2; R-P0-05]
			acc1 += 5
		}
		// The eight addends above are the whole other-mix accumulator:
		// [08 R-P0-05 §5] enumerates it exhaustively and lists the routine's
		// definition inputs, and neither carries an energy-make term. The
		// earlier "unresolved energy-make sentinel path" marker is retired.
		tmp := acc1
		val := tmp // ftol via FILD
		if count == 0 {
			val = val << 2 // *4
		} else if count == 1 {
			val = val * 2
		}
		// R-AI-03 corrected the earlier MaxSlope label: this reader is the
		// movement profile's MinWaterDepth word [08 R-P0-05 §5][08 R-AI-03 §6].
		if def != nil && def.MinWaterDepth >= 0 {
			val = val * 3
		}
		// The half-capacity addend: when the session's per-player unit limit
		// shifted right one is unsigned-less-than the owning player's live unit
		// count, half of the single coefficient — the signed byte divided by
		// two, truncating toward zero — is added [08 R-AI-01 §13][I3]. It is
		// ordinary late-game state, not an unreachable branch.
		if s.halfCapacity() {
			val += int32(int8(acc0Final)) / 2
		}
		// Zero-izing branches
		if def != nil && def.CanLoad { // [P0-01 §2.2; R-P0-05]
			val = 0
		}
		if def != nil && def.IsFeature { // [P0-01 §2.2; R-P0-05]
			val = 0
		}
		// The third zeroing branch, closed by [08 R-P0-05 §9]: the operand is
		// the map's MAXIMUM wind word, not the live wind scalar, and the test
		// is strict and integer against 2500.
		if s.windGeneratorSuppressed(def) {
			val = 0
		}
		val = clamp100(val) // clamp to 100 max, negative kept [P0-01 §3]
		// store to C0
		cv := s.ClassVectors[ck]
		cv.C0 = int8(val)

		// Energy-mix coefficient [P0-01 §3].
		fE := costEnergy * float32(-0.0025)
		g := fval * float32(5.0)
		diff := fE - g
		if diff > 100 {
			diff = 100
		}
		if diff < -100 {
			diff = -100
		}
		val2 := ftol(diff)
		val2 = clamp100(val2)
		cv.C2 = int8(val2)

		// Metal-mix coefficient [P0-01 §3].
		baseVal := int32(0)
		if def != nil && def.ExtractsMetal != 0 {
			baseVal = 100
		}
		metalAdj := int32(0)
		if def != nil && def.MakesMetal != 0 { // [P0-01 §2.2; R-P0-05]
			metalAdj = -25 // [P0-01 §3]
		}
		adjf := costMetal*float32(-0.02) + float32(metalAdj)
		sumf := float32(baseVal) + adjf
		if sumf > 100 {
			sumf = 100
		}
		if sumf < -100 {
			sumf = -100
		}
		val3 := ftol(sumf)
		val3 = clamp100(val3)
		cv.C1 = int8(val3)

		s.ClassVectors[ck] = cv
	}
}
