package ai

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// refreshInterval is the strategic-state refresh period in ticks [08 "Established AI-facing data and rooted planner"].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const refreshInterval uint32 = 30

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Retail stores i8[ntypes][3] in the 0x10D strategic state [strategic-ai.md §4].
type ClassVector struct {
	C0 int8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	C1 int8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	C2 int8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// Strategic is the 0x10D-byte retail strategic state [08 "Established AI-facing data and rooted planner"] per I13.
// Go uses named fields; offsets noted per field. It is embedded in Manager [PLAN_11 Public API].
type Strategic struct {
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	CenterX numeric.Fixed // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	CenterZ numeric.Fixed // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Radius numeric.Fixed // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	LastRefreshTick uint32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// LastClassRecomputeTick records the tick of the last gated class-vector recompute.
	// Updated only when RNG(30)==0 at a refresh; used to assert cadence without inventing varying coefficients.
	LastClassRecomputeTick uint32

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Counts map[string]int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ClassVectors map[string]ClassVector // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	InitVectors map[string]int8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Stores the first coefficient (weapon-budget + cost path) clamped to [-100,100].
	SingleVectors map[string]int8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// Catalog is the content catalog for def lookups [P0-I16]. Was package var AICatalog.
	Catalog *content.Catalog // [P0-I16] per-session, not package global
}

// Init initializes per-type state once at battle setup [08 "Established AI-facing data and rooted planner"].
// It clears completed counts to zero and recomputes class vectors for the supplied types plus once at init.
// The type list should be CanonicalKey values of the catalog's unit types; sorting ensures determinism (I1).
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	s.InitClassVectors()
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	s.recomputeClassVectors()
	// Init recompute does not set LastClassRecomputeTick; only gated recompute updates it, so cadence can be observed.
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		def := s.lookupDef(ck)
		c := int32(0)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// is unresolved. Do not substitute BMCode or another similarly named
		// FBI flag; until the field is mapped, this initialization addend is
		// intentionally absent [P0-01 §2.2] [R-P0-05] [I9].
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		hasBuild := s.hasBuildOptions(ck, def)
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

// Tick is an alias for MaybeRefresh for Manager embedding [PLAN_11 Public API].
// Manager.Tick delegates to Strategic.Tick per C11.
func (s *Strategic) Tick(tick uint32, r *rng.Simulation, player uint8, w *units.World) bool {
	return s.MaybeRefresh(tick, r, player, w)
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if tick-s.LastRefreshTick < refreshInterval {
		return false
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	s.refreshCountsAndCenter(player, w)
	s.LastRefreshTick = tick
	// Class vectors gated on single RNG(30)==0 draw [08 ...] (I4). Bound census: 30 here (C9).
	if r != nil {
		if r.Uint32n(refreshInterval) == 0 { // [08 "Established AI-facing data and rooted planner"] RNG(30) gate (I4)
			s.recomputeClassVectors()
			s.LastClassRecomputeTick = tick
		}
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
	var sumX, sumZ int64
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
			sumX += int64(u.X)
			sumZ += int64(u.Z)
			n++
		}
	}
	if n > 0 {
		// Average with truncation toward zero for Fixed average [I3]; world coordinates are Fixed 16.16 [I2].
		s.CenterX = numeric.Fixed(sumX / n)
		s.CenterZ = numeric.Fixed(sumZ / n)
	} else {
		s.CenterX = 0
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

// lookupDefGlobal is a helper for contexts without a Strategic receiver; retained for compatibility
// but prefers the Strategic catalog when available. Avoid package global AICatalog [P0-I16].
func lookupDef(ck string) *content.UnitDef {
	return (&Strategic{}).lookupDef(ck)
}

// hasBuildOptions reports whether def has a non-empty build-option list at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// adapter. A Builder flag without a resolved list is not a substitute for the
// runtime count [R-P0-05] [I9].
func (s *Strategic) hasBuildOptions(ck string, def *content.UnitDef) bool {
	if s != nil && s.Catalog != nil && s.Catalog.BuildMenus != nil {
		if page, ok := s.Catalog.BuildMenus[ck]; ok && page != nil && len(page.Buttons) > 0 {
			return true
		}
	}
	_ = def
	return false
}

func hasBuildOptions(ck string, def *content.UnitDef) bool {
	return (&Strategic{}).hasBuildOptions(ck, def)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// against zero by the class routine, but the helper's definition inputs and
// semantic name were not recovered. Do not proxy it with CanMove,
// MaxVelocity, or standing-order flags [P0-01 §2.2] [R-P0-05] [I9].
func classify(def *content.UnitDef) float32 {
	_ = def
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// signed result. Zero is the documented neutral placeholder; callers
	// therefore do not invent either side of the <0 comparison.
	return 0.0
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// Collect keys from ClassVectors and Counts to cover all types. Loop ascending type index [P0-01 §3] stride 0x249.
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
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		acc0 := int32(1)
		if def != nil && def.ExtractsMetal != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc0 = 11
		}
		if def != nil && def.MakesMetal != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc0 += 10
		}
		fval := classify(def)
		if fval < 0 {
			acc0 += 10
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		if def != nil && def.CanAttack { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
				// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
				wSum = wSum + dmg/0x28 + 5 + rnge/100
			}
		}
		wSum = clamp100(wSum) // also clamp to 100 via intermediate steps [P0-01 §3]
		acc0Final := wSum + t1
		acc0Final = clamp100(acc0Final)
		s.SingleVectors[ck] = int8(acc0Final)

		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		acc1 := int32(0)
		if def != nil && def.CanAttack { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 = 21
		}
		if def != nil && def.Builder && count < 3 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 += 30
		}
		if fval < 0 {
			acc1 += 50
		}
		if def != nil && def.ExtractsMetal != 0 {
			acc1 += 50
		}
		if def != nil && def.MakesMetal != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 += 25
		}
		if def != nil && def.CanFly { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 += 40
		}
		if def != nil && def.SonarDistance != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 += 15
		}
		if def != nil && def.RadarDistance != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			acc1 += 5
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		tmp := acc1
		val := tmp // ftol via FILD
		if count == 0 {
			val = val << 2 // *4
		} else if count == 1 {
			val = val * 2
		}
		if def != nil && def.MaxSlope >= 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			val = val * 3
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// stock state leaves this global comparison false [R-P0-05].
		// Zero-izing branches
		if def != nil && def.CanLoad { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			val = 0
		}
		if def != nil && def.IsFeature { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			val = 0
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// branch false, so no proxy field is consulted [R-P0-05].
		val = clamp100(val) // clamp to 100 max, negative kept [P0-01 §3]
		// store to C0
		cv := s.ClassVectors[ck]
		cv.C0 = int8(val)

		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		baseVal := int32(0)
		if def != nil && def.ExtractsMetal != 0 {
			baseVal = 100
		}
		metalAdj := int32(0)
		if def != nil && def.MakesMetal != 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
			metalAdj = -25 // -0x19 via NEG SBB [P0-01 §3]
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
