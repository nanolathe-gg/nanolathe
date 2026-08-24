package ai

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): AI resource-score expressions (energyRaw/metalRaw) are float32 temporaries per I2
// with exact x87 spill retention unknown; this file uses only integer counts and Fixed where noted.

// refreshInterval is the strategic-state refresh period in ticks [08 "Established AI-facing data and rooted planner"].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const refreshInterval uint32 = 30

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Retail stores i8[ntypes][3] in the 0x10D strategic state [strategic-ai.md §4].
type ClassVector struct {
	C0 int8 // otherMix coefficient [08 ...]
	C1 int8 // metalMix coefficient
	C2 int8 // energyMix coefficient
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
	}
	// Once at init recompute [08 ...] — never otherwise except gated refresh.
	s.recomputeClassVectors()
	// Init recompute does not set LastClassRecomputeTick; only gated recompute updates it, so cadence can be observed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): [08 "Established AI-facing data and rooted planner"] establishes the recompute CADENCE and the vector shape but not the coefficient arithmetic; placeholder value, not retail.
// Retail defaults are class[0]=40 +20 with build options [strategic-ai.md §7]; full retail recompute beyond that
// involves float constants and weapon tables outside the closed contract. This placeholder writes constant 40.
func (s *Strategic) recomputeClassVectors() {
	if s.ClassVectors == nil {
		s.ClassVectors = make(map[string]ClassVector)
		return
	}
	if len(s.ClassVectors) == 0 {
		return
	}
	keys := make([]string, 0, len(s.ClassVectors))
	for k := range s.ClassVectors {
		keys = append(keys, k)
	}
	sort.Strings(keys) // determinism (I1)
	for _, k := range keys {
		// TODO(question): [08 "Established AI-facing data and rooted planner"] establishes the recompute CADENCE and the vector shape but not the coefficient arithmetic; placeholder value, not retail.
		s.ClassVectors[k] = ClassVector{C0: 40, C1: 0, C2: 0}
	}
}
