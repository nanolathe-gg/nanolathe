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

	// energyEnvironment reads the live wind scalar and immutable map tidal
	// strength used by the computer player's signed net-energy query. It is a
	// session binding rather than copied strategic state so a gated 30-tick
	// recompute observes the current wind value [05 R-PROD-01 §1][08
	// R-P0-05 §5–§6].
	energyEnvironment func() (windScalar, tidalStrength float32)

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

// InitClassVectors writes the initialization-only vector [P0-01]. The
// unresolved category test is deliberately not substituted; an authored build
// menu contributes 20.
// The category flag has no recovered FBI key or semantic name. It is
// intentionally left as an explicit unknown; BMCode is not a substitute
// [P0-01 §2.2] [R-P0-05] [I9].
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
		// TODO(question): the authored/runtime field that populates the category
		// flag
		// is unresolved. Do not substitute BMCode or another similarly named
		// FBI flag; until the field is mapped, this initialization addend is
		// intentionally absent [P0-01 §2.2] [R-P0-05] [I9].
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
	var sumX, sumY, sumZ int64
	var n int64
	if w != nil {
		// Stable iteration: units.World.Iter is pool asc; we additionally filter by player asc already handled by Iter order (I1).
		for _, u := range w.Iter() {
			if u == nil || !u.Alive {
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
			sumX += int64(u.X)
			sumY += int64(u.Y)
			sumZ += int64(u.Z)
			n++
		}
	}
	if n > 0 {
		// Average with truncation toward zero for Fixed average [I3]; world coordinates are Fixed 16.16 [I2].
		s.CenterX = numeric.Fixed(sumX / n)
		s.CenterY = numeric.Fixed(sumY / n)
		s.CenterZ = numeric.Fixed(sumZ / n)
	} else {
		s.CenterX = 0
		s.CenterY = 0
		s.CenterZ = 0
	}
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
// TODO(T23): x87 control-word beyond default narrow points is TODO(T23) only if word differs [P0-01 §8].
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
		// Apply the cost terms with float32 narrowing at each conversion [P0-01
		// §3–§4; R-P0-05].
		costMetal := float32(0)
		costEnergy := float32(0)
		if def != nil {
			costMetal = float32(def.BuildCostMetal)
			costEnergy = float32(def.BuildCostEnergy)
		}
		t0f := float32(acc0) + costMetal*float32(-0.01) // FC9BC
		t0 := ftol(t0f)                                 // narrow to float32 at CALL then ftol [P0-01 §4] TODO(T23) control-word
		t1f := float32(t0) + costEnergy*float32(-0.002) // FC9C0
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
				// TODO(question): the runtime weapon-slot active bit is
				// not represented by the immutable weapon definition. A resolved
				// weapon link is the only available slot identity here; do not
				// infer activity from unrelated unit flags [R-P0-05].
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
		// Unimplemented: [08 R-AI-01 §13] establishes the half-capacity addend
		// — when the session's per-player unit limit shifted right one is
		// unsigned-less-than the owning player's live unit count, half the
		// single coefficient (signed byte, truncating) is added here. It is
		// ordinary late-game state, not an unreachable branch: the previous
		// "stock state leaves this false" reading is retracted there. Strategic
		// carries neither counter — see PLAN 19 §2.4.
		// Zero-izing branches
		if def != nil && def.CanLoad { // [P0-01 §2.2; R-P0-05]
			val = 0
		}
		if def != nil && def.IsFeature { // [P0-01 §2.2; R-P0-05]
			val = 0
		}
		// [08 R-P0-05 §5] establishes that a third zeroing branch exists —
		// "the wind-generator/global-wind comparison is true" — and that the
		// definition's `windgenerator` word is one of the routine's recovered
		// inputs [05 R-PROD-01 §1]. What it is compared against
		// and in which direction is not written down.
		// TODO(question): what is the class routine's wind-generator zeroing
		// test — which global (the session's current wind scalar, or a wind
		// min/max word) and which comparison? Decider: a static trace of that
		// branch, recorded in [08 R-P0-05 §5]. Until then no proxy field is
		// consulted and the branch is not taken.
		val = clamp100(val) // clamp to 100 max, negative kept [P0-01 §3]
		// store to C0
		cv := s.ClassVectors[ck]
		cv.C0 = int8(val)

		// Energy-mix coefficient [P0-01 §3].
		fE := costEnergy * float32(-0.0025) // FC9C8
		g := fval * float32(5.0)            // FC9CC
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
		adjf := costMetal*float32(-0.02) + float32(metalAdj) // FC9D4
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
