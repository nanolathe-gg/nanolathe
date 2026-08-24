package ai

import (
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestRefreshCadence(t *testing.T) {
	s := &Strategic{}
	s.Init([]string{"armfav", "corfav"})
	r := rng.NewSimulation(1)
	// Seed RNG state for determinism; use SimulationFromState wrapper to control draws?
	// Use NewSimulation with fixed seed; draws will be deterministic.

	// First tick at 0 should NOT refresh because LastRefreshTick=0, tick-0=0 <30
	if s.MaybeRefresh(0, &r, 0, nil) {
		t.Fatalf("tick 0 should not refresh; LastRefreshTick 0, need 30")
	}
	if r.Draws() != 0 {
		t.Fatalf("no draw should be consumed when not due, draws %d", r.Draws())
	}
	// Tick 29 still not due
	if s.MaybeRefresh(29, &r, 0, nil) {
		t.Fatalf("tick 29 should not refresh")
	}
	if r.Draws() != 0 {
		t.Fatalf("draws should still be 0, got %d", r.Draws())
	}
	// Tick 30 should refresh
	if !s.MaybeRefresh(30, &r, 0, nil) {
		t.Fatalf("tick 30 should refresh")
	}
	if s.LastRefreshTick != 30 {
		t.Fatalf("LastRefreshTick %d want 30", s.LastRefreshTick)
	}
	if r.Draws() != 1 {
		t.Fatalf("exactly one RNG(30) draw per refresh, draws %d want 1", r.Draws())
	}
	// Tick 31 not due (31-30=1 <30)
	if s.MaybeRefresh(31, &r, 0, nil) {
		t.Fatalf("tick 31 should not refresh")
	}
	if r.Draws() != 1 {
		t.Fatalf("draws should stay 1 at tick 31, got %d", r.Draws())
	}
	// Tick 60 should refresh again
	if !s.MaybeRefresh(60, &r, 0, nil) {
		t.Fatalf("tick 60 should refresh")
	}
	if r.Draws() != 2 {
		t.Fatalf("draws 2 after second refresh, got %d", r.Draws())
	}
	// Ensure counts recomputed each refresh even when not gated (center check later)
	// No other RNG bounds used in this file: only 30. Verified by code inspection.
}

func TestClassRecomputeCadence(t *testing.T) {
	// Class vectors recomputed ONLY when RNG(30)==0 at a refresh, plus once at init — never otherwise (I4).
	// Placeholder coefficient is constant 40; cadence is asserted via LastClassRecomputeTick + draw count.
	types := []string{"armfav", "corfav", "armship"}
	s := &Strategic{}
	s.Init(types)

	// Capture initial vectors after init (one recompute at init) — should be placeholder 40.
	for _, ck := range types {
		canon := content.CanonicalKey(ck)
		v := s.ClassVectors[canon]
		if v.C0 != 40 || v.C1 != 0 || v.C2 != 0 {
			t.Fatalf("init vector %s = %+v want C0=40 C1=0 C2=0 (placeholder)", ck, v)
		}
	}
	if s.LastClassRecomputeTick != 0 {
		t.Fatalf("LastClassRecomputeTick after init %d want 0 (only gated recompute updates it)", s.LastClassRecomputeTick)
	}
	r := rng.NewSimulation(12345)
	// Drive multiple refreshes at 30-tick intervals and assert draw-count + timestamp cadence.
	var tick uint32 = 30 // first due after init (LastRefreshTick 0)
	for i := 0; i < 10; i++ {
		tick += 30
		// Copy RNG to peek expected gate without consuming real stream
		peek := r
		peekVal := peek.Uint32n(30)
		if peekVal >= 30 {
			t.Fatalf("peek RNG(30) out of range %d", peekVal)
		}
		beforeDraws := r.Draws()
		beforeTick := s.LastClassRecomputeTick
		if !s.MaybeRefresh(tick, &r, 0, nil) {
			t.Fatalf("tick %d should be due", tick)
		}
		afterDraws := r.Draws()
		if afterDraws != beforeDraws+1 {
			t.Fatalf("tick %d: draws %d -> %d want +1", tick, beforeDraws, afterDraws)
		}
		// Vectors must stay constant placeholder (no invented varying values).
		for _, ck := range types {
			canon := content.CanonicalKey(ck)
			v := s.ClassVectors[canon]
			if v.C0 != 40 || v.C1 != 0 || v.C2 != 0 {
				t.Fatalf("tick %d: vector %s = %+v want constant placeholder 40,0,0", tick, ck, v)
			}
		}
		if peekVal == 0 {
			if s.LastClassRecomputeTick != tick {
				t.Fatalf("tick %d: RNG(30)==0 expected LastClassRecomputeTick %d, got %d", tick, tick, s.LastClassRecomputeTick)
			}
		} else {
			if s.LastClassRecomputeTick != beforeTick {
				t.Fatalf("tick %d: RNG(30)==%d expected LastClassRecomputeTick unchanged %d, got %d", tick, peekVal, beforeTick, s.LastClassRecomputeTick)
			}
		}
	}
	// Ensure that between refreshes (non-due ticks) no draw and no timestamp change
	s2 := &Strategic{}
	s2.Init(types)
	r2 := rng.NewSimulation(999)
	s2.MaybeRefresh(30, &r2, 0, nil) // first due
	drawsAfterFirst := r2.Draws()
	tsAfterFirst := s2.LastClassRecomputeTick
	// Call at tick 31 (not due) — should not draw, timestamp unchanged
	if s2.MaybeRefresh(31, &r2, 0, nil) {
		t.Fatalf("tick 31 not due should return false")
	}
	if r2.Draws() != drawsAfterFirst {
		t.Fatalf("non-due tick should not consume RNG, draws %d want %d", r2.Draws(), drawsAfterFirst)
	}
	if s2.LastClassRecomputeTick != tsAfterFirst {
		t.Fatalf("non-due tick timestamp should not change, got %d want %d", s2.LastClassRecomputeTick, tsAfterFirst)
	}
	// Determinism: sorted iteration of vectors (I1) — verify keys are handled deterministically
	keys := make([]string, 0, len(s2.ClassVectors))
	for k := range s2.ClassVectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// no assertion beyond coverage of sort path
	_ = keys
}

func TestCenterComputation(t *testing.T) {
	// Verify strategic center is average of completed units' positions [08 ...] recomputed every 30 ticks.
	s := &Strategic{}
	types := []string{"armfav"}
	s.Init(types)

	// Create world with two completed units for player 1
	w := units.New(10, nil)
	defA := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfav")}, UnitName: "armfav", MaxDamage: 100}
	// Use non-zero catalog not needed; worldIter will use Def.CanonicalKey
	// Create units at fixed positions
	x1 := numeric.FixedFromInt(0)
	z1 := numeric.FixedFromInt(0)
	x2 := numeric.FixedFromInt(200) // 200 world units => 200*65536 raw
	z2 := numeric.FixedFromInt(0)
	if _, err := w.Create(defA, 1, x1, numeric.Fixed(0), z1); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := w.Create(defA, 1, x2, numeric.Fixed(0), z2); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	// Also create a nanoframe (Remaining !=0) for same player — should be ignored in center and counts
	h, err := w.Create(defA, 1, numeric.FixedFromInt(1000), numeric.Fixed(0), numeric.FixedFromInt(1000))
	if err != nil {
		t.Fatalf("create nanoframe: %v", err)
	}
	if u := w.Unit(h); u != nil {
		u.Remaining = 0.5 // under construction, not counted
	}
	// Create unit for other player — should be ignored
	if _, err := w.Create(defA, 2, numeric.FixedFromInt(9999), numeric.Fixed(0), numeric.FixedFromInt(9999)); err != nil {
		t.Fatalf("create other player: %v", err)
	}
	r := rng.NewSimulation(1)
	// Trigger refresh at tick 30
	if !s.MaybeRefresh(30, &r, 1, w) {
		t.Fatalf("should refresh at 30")
	}
	// Center should be average of the two completed units: (0+200)/2 =100
	wantX := numeric.FixedFromInt(100)
	wantZ := numeric.FixedFromInt(0)
	if s.CenterX != wantX || s.CenterZ != wantZ {
		t.Fatalf("center X %v Z %v want %v %v", s.CenterX, s.CenterZ, wantX, wantZ)
	}
	// Counts: armfav should be 2 (nanoframe not counted, other player not counted)
	if c := s.Counts[content.CanonicalKey("armfav")]; c != 2 {
		t.Fatalf("counts armfav %d want 2, map %v", c, s.Counts)
	}
	// No-unit case: center 0,0
	s2 := &Strategic{}
	s2.Init([]string{"armfav"})
	w2 := units.New(10, nil)
	r2 := rng.NewSimulation(1)
	if !s2.MaybeRefresh(30, &r2, 1, w2) {
		t.Fatalf("empty world refresh")
	}
	if s2.CenterX != 0 || s2.CenterZ != 0 {
		t.Fatalf("empty center %v %v want 0 0", s2.CenterX, s2.CenterZ)
	}
	if c := s2.Counts[content.CanonicalKey("armfav")]; c != 0 {
		t.Fatalf("empty counts %d want 0", c)
	}
	// Verify negative coordinates average with truncation toward zero (Fixed average)
	// Add unit at -100 and +100 => average 0
	s3 := &Strategic{}
	s3.Init([]string{"armfav"})
	w3 := units.New(10, nil)
	if _, err := w3.Create(defA, 1, numeric.FixedFromInt(-100), numeric.Fixed(0), numeric.FixedFromInt(-50)); err != nil {
		t.Fatalf("create neg: %v", err)
	}
	if _, err := w3.Create(defA, 1, numeric.FixedFromInt(100), numeric.Fixed(0), numeric.FixedFromInt(50)); err != nil {
		t.Fatalf("create pos: %v", err)
	}
	r3 := rng.NewSimulation(1)
	if !s3.MaybeRefresh(30, &r3, 1, w3) {
		t.Fatalf("neg/pos refresh")
	}
	if s3.CenterX != 0 || s3.CenterZ != 0 {
		t.Fatalf("neg/pos center %v %v want 0 0", s3.CenterX, s3.CenterZ)
	}
}
