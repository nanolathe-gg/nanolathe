package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// healTimeFixture composes the smallest session the `healtime` act needs: a
// unit pool, one seated human row so the sweep's control-byte gate passes, a
// terrain with the two mission water words at zero (so the act above this one
// is inert and cannot be mistaken for the effect under test), and the
// construction service that owns the shared repair helper.
//
// The authored numbers are the two stock commanders' shape, scaled down: the
// heal term is `trunc(1 + (maxdamage · worker - 1) / buildtime)` and both terms
// are clamped to exactly one whenever positive [05 R-WORK-01 §3], so any
// definition whose quantum is non-zero heals one point per firing.
func healTimeFixture(t *testing.T, healTime int32) (*Session, *content.UnitDef) {
	t.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "healtest"},
		UnitName:         "healtest",
		MaxDamage:        3000,
		BuildTime:        95897,
		BuildCostEnergy:  34125,
		DamageModifier:   65536, // 1.0 [02 "Unit record"]
		HealTime:         healTime,
		Script:           fixtureCOBProgram(),
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	ter := &world.Terrain{
		CellW:    4,
		CellH:    4,
		Plot:     make([]world.PlotCell, 16),
		SeaLevel: 20,
		// Both mission words zero: water damage is inert here [04 §9.2].
	}
	w := newSessionFixtureWorld(4, cat)
	econ := &economy.Service{}
	s := &Session{
		Units:   w,
		Catalog: cat,
		World:   ter,
		Econ:    econ,
		Combat:  &combat.Service{},
	}
	s.Build = construction.NewService(ter, cat, w, econ)
	s.Build.Combat = s.Combat
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	return s, def
}

// healTimeSpawn creates one unit above sea level (so nothing else in step 9
// touches its health) and damages it to the given health.
func healTimeSpawn(t *testing.T, s *Session, def *content.UnitDef, health int32) pool.Handle {
	t.Helper()
	h, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(40), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Units.Unit(h).Health = health
	return h
}

// TestHealTimeRegainsOnePointEveryEightTicks locks the whole of `healtime`'s
// observable behavior [05 R-WORK-01 §3, "`healtime`, the only consumer"]
// [04 R-SPEC-01 §4]: the cadence is `tick & 7 == 0`, the amount is exactly one
// health point per firing (both of the helper's terms are clamped to one
// whenever positive), and one energy unit is requested with it.
//
// `healtime` 27 is what both stock commanders author: the quantum is
// `(27 × 8) / 30 = 7`, and with their build time of ~95_900 the unclamped heal
// term is 1 and the unclamped energy term is 3 — both land on 1 after the
// clamp, which is why every self-healing definition in the game heals at the
// same rate.
func TestHealTimeRegainsOnePointEveryEightTicks(t *testing.T) {
	s, def := healTimeFixture(t, 27)
	h := healTimeSpawn(t, s, def, 2990)

	if got := construction.HealQuantum(def.HealTime); got != 7 {
		t.Fatalf("heal quantum = %d, want 7 ((27 × 8) / 30) [05 R-WORK-01 §3]", got)
	}

	// Not a cadence tick: nothing moves.
	for _, tick := range []uint32{1, 2, 3, 4, 5, 6, 7} {
		s.stepUnitPhase(tick)
	}
	if got := s.Units.Unit(h).Health; got != 2990 {
		t.Fatalf("ticks 1..7 health = %d, want 2990: the cadence is tick & 7 == 0 [05 R-WORK-01 §3]", got)
	}

	s.stepUnitPhase(8)
	if got := s.Units.Unit(h).Health; got != 2991 {
		t.Fatalf("tick 8 health = %d, want 2991 (one point per firing) [05 R-WORK-01 §3]", got)
	}
	// The unit pays its own energy through the one-resource admission against
	// its OWN buckets — builder and target are the same unit here.
	buckets := s.Econ.UnitBuckets(h)
	if buckets == nil || buckets[economy.Energy].Requested != 1 {
		t.Fatalf("energy requested = %v, want exactly 1 per firing [05 R-WORK-01 §3]", buckets[economy.Energy].Requested)
	}

	// Nine more firings carry it to full health, and then it stops.
	for tick := uint32(9); tick <= 8*20; tick++ {
		s.stepUnitPhase(tick)
	}
	if got := s.Units.Unit(h).Health; got != 3000 {
		t.Fatalf("health = %d, want the definition's maxdamage 3000: self-repair stops at full health [05 R-WORK-01 §3]", got)
	}
	// It stopped at ten firings, not twenty: the gate closed at full health and
	// no further energy was requested.
	if got := buckets[economy.Energy].Requested; got != 10 {
		t.Fatalf("energy requested = %v, want 10 (one per firing, ten firings to full) [05 R-WORK-01 §3]", got)
	}
}

// TestHealTimeLeavesAFullHealthUnitAlone locks the gate that stops the act:
// `(unsigned)health < (unsigned)maxdamage` [05 R-WORK-01 §3]. A unit at full
// health is never touched and is never charged.
func TestHealTimeLeavesAFullHealthUnitAlone(t *testing.T) {
	s, def := healTimeFixture(t, 27)
	h := healTimeSpawn(t, s, def, 3000)

	for tick := uint32(1); tick <= 240; tick++ {
		s.stepUnitPhase(tick)
	}
	if got := s.Units.Unit(h).Health; got != 3000 {
		t.Fatalf("health = %d, want 3000: a unit at full health is refused [05 R-WORK-01 §3]", got)
	}
	if buckets := s.Econ.UnitBuckets(h); buckets != nil && buckets[economy.Energy].Requested != 0 {
		t.Fatalf("energy requested = %v, want 0: a refused unit is not billed [05 R-WORK-01 §3]", buckets[economy.Energy].Requested)
	}
}

// TestHealTimeZeroAndBelowFourAreInert locks the other two arms of the gate.
// A zero `healtime` — the default, and what 276 of the 278 stock definitions
// carry — never enters the act at all. A `healtime` of 1..3 enters it but
// produces a zero quantum, hence zero terms, hence no healing and no charge
// [05 R-WORK-01 §3].
func TestHealTimeZeroAndBelowFourAreInert(t *testing.T) {
	for _, tc := range []struct {
		name     string
		healTime int32
	}{
		{"zero, the default", 0},
		{"one", 1},
		{"three, the largest zero quantum", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, def := healTimeFixture(t, tc.healTime)
			h := healTimeSpawn(t, s, def, 2990)
			for tick := uint32(1); tick <= 240; tick++ {
				s.stepUnitPhase(tick)
			}
			if got := s.Units.Unit(h).Health; got != 2990 {
				t.Fatalf("health = %d, want 2990: healtime %d heals nothing [05 R-WORK-01 §3]", got, tc.healTime)
			}
			if buckets := s.Econ.UnitBuckets(h); buckets != nil && buckets[economy.Energy].Requested != 0 {
				t.Fatalf("energy requested = %v, want 0 [05 R-WORK-01 §3]", buckets[economy.Energy].Requested)
			}
		})
	}
}

// TestHealTimeDrawsNoRandomNumber locks the determinism half of
// [05 R-WORK-01 §3, "repair's randomness"]: the helper itself and the
// `healtime` path draw nothing. A self-healing unit therefore cannot shift the
// call order of either stream.
func TestHealTimeDrawsNoRandomNumber(t *testing.T) {
	s, def := healTimeFixture(t, 27)
	healTimeSpawn(t, s, def, 2990)

	simBefore := s.SimRNG().Draws()
	crtBefore := s.CrtRNG().Draws()
	for tick := uint32(1); tick <= 80; tick++ {
		s.stepUnitPhase(tick)
	}
	if got := s.SimRNG().Draws(); got != simBefore {
		t.Fatalf("simulation draws %d -> %d, want no draw from the healtime path [05 R-WORK-01 §3]", simBefore, got)
	}
	if got := s.CrtRNG().Draws(); got != crtBefore {
		t.Fatalf("CRT draws %d -> %d, want no draw from the healtime path [05 R-WORK-01 §3]", crtBefore, got)
	}
}
