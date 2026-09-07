package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func killCreditFixture(t *testing.T) (*Session, *units.Unit, *content.UnitDef) {
	t.Helper()
	s := newLoopTestSession(t, 1)
	attacker := s.Units.IterSliced()[0]
	def := &content.UnitDef{
		UnitName:  "credit-victim",
		MaxDamage: 100,
		BMCode:    1,
		Script:    fixtureCOBProgram(),
	}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	s.Catalog.Units[def.CanonicalKey] = def
	return s, attacker, def
}

func finalizeCreditVictim(t *testing.T, s *Session, def *content.UnitDef, cause combat.Cause, side uint8, killer pool.Handle, remaining float32, tick uint32) *units.Unit {
	t.Helper()
	h, err := s.Units.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	victim := s.Units.Unit(h)
	victim.Remaining = remaining
	victim.LastDamageCause = uint8(cause)
	victim.LastDamageSide = side
	s.Units.DestroyBy(h, units.DeathKilled, killer)
	s.stepUnitPhase(tick)
	if s.Units.Unit(h) != nil {
		t.Fatalf("victim slot %d stayed live after its session death phase", h)
	}
	return victim
}

func TestSessionDeathsAdvanceUnitVeterancyAtFiveSixAndTwentyFive(t *testing.T) {
	s, attacker, def := killCreditFixture(t)
	target := &units.Unit{Def: &content.UnitDef{BMCode: 1}}
	slot := &units.Slot{Flags: units.SlotFlagEnabled}
	weapon := &content.WeaponDef{WeaponVelocity: 1}

	for n := 1; n <= 25; n++ {
		finalizeCreditVictim(t, s, def, combat.CauseOrdinary, attacker.Owner, attacker.Handle, 0, uint32(n))
		if got := attacker.Kills; got != int32(n) {
			t.Fatalf("after %d finalized deaths attacker kills = %d, want %d", n, got, n)
		}
		switch n {
		case 5:
			if combat.PreFireLeadGate(attacker, target, slot, weapon) {
				t.Fatal("five credited kills enabled pre-fire lead")
			}
		case 6:
			if !combat.PreFireLeadGate(attacker, target, slot, weapon) {
				t.Fatal("six credited kills did not enable pre-fire lead")
			}
		case 25:
			// Use the normal weapon visit, rather than only its reload helper:
			// the earned kill word must reach a live shot's stored timer.
			weapon := &content.WeaponDef{
				ID: 71, LineOfSight: true, Range: 1000, ReloadTime: 100,
				Tolerance: 32767, PitchTolerance: 32767, WeaponVelocity: 1 << 16,
			}
			// This fixture's creation program has no rendered piece binding, so
			// use the line-of-sight executor's established bare-position muzzle
			// path for the live fire assertion.
			attacker.SetScript(nil)
			attacker.Y = numeric.FixedFromInt(10)
			attacker.InstallWeapon(0, weapon)
			shot := attacker.SlotAt(0)
			shot.Target = units.Target{Kind: units.TargetGround, X: attacker.X + numeric.FixedFromInt(20), Z: attacker.Z}
			if got := s.Combat.StepWeaponsForUnit(attacker, uint32(n+1), s.Units, nil, s.World, s.Econ, s.Catalog, nil, nil).Fired; got != 1 {
				t.Fatalf("twenty-five credited kills did not reach a live shot: fired %d", got)
			}
			if got := shot.Reload; got != 70 {
				t.Fatalf("twenty-five credited kills stored reload = %d, want 70", got)
			}
		}
	}
}

func TestFinalizedDeathUnitCreditGatesAndRawAttackerSlots(t *testing.T) {
	tests := []struct {
		name      string
		cause     combat.Cause
		side      uint8
		killer    func(*units.Unit) pool.Handle
		remaining float32
		wantKills int32
		prepare   func(t *testing.T, s *Session, attacker *units.Unit, def *content.UnitDef) *units.Unit
	}{
		{
			name:      "unfinished victim",
			cause:     combat.CauseOrdinary,
			killer:    func(a *units.Unit) pool.Handle { return a.Handle },
			remaining: 1,
		},
		{
			name:   "excluded self destruct cause",
			cause:  combat.CauseSelfDestruct,
			killer: func(a *units.Unit) pool.Handle { return a.Handle },
		},
		{
			name:   "null attacker",
			cause:  combat.CauseOrdinary,
			killer: func(*units.Unit) pool.Handle { return 0 },
		},
		{
			name:      "same side victim",
			cause:     combat.CauseOrdinary,
			killer:    func(a *units.Unit) pool.Handle { return a.Handle },
			wantKills: 0,
			prepare: func(t *testing.T, s *Session, attacker *units.Unit, def *content.UnitDef) *units.Unit {
				t.Helper()
				h, err := s.Units.Create(def, attacker.Owner, 0, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				return s.Units.Unit(h)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, attacker, def := killCreditFixture(t)
			attacker.Kills = 4
			if tc.prepare != nil {
				victim := tc.prepare(t, s, attacker, def)
				victim.LastDamageCause = uint8(tc.cause)
				victim.LastDamageSide = attacker.Owner
				s.Units.DestroyBy(victim.Handle, units.DeathKilled, tc.killer(attacker))
				s.stepUnitPhase(1)
			} else {
				side := tc.side
				if side == 0 {
					side = attacker.Owner
				}
				finalizeCreditVictim(t, s, def, tc.cause, side, tc.killer(attacker), tc.remaining, 1)
			}
			if got := attacker.Kills; got != 4+tc.wantKills {
				t.Fatalf("attacker kills = %d, want %d", got, 4+tc.wantKills)
			}
		})
	}

	t.Run("wrap", func(t *testing.T) {
		s, attacker, def := killCreditFixture(t)
		attacker.Kills = 0xffff
		finalizeCreditVictim(t, s, def, combat.CauseOrdinary, attacker.Owner, attacker.Handle, 0, 1)
		if attacker.Kills != 0 {
			t.Fatalf("wrapped unit kill word = %d, want 0", attacker.Kills)
		}
	})

	t.Run("empty stale attacker record", func(t *testing.T) {
		s, attacker, def := killCreditFixture(t)
		stale := attacker.Handle
		attacker.Kills = 9
		s.Units.Destroy(stale, units.DeathKilled)
		s.Units.FinalizeDeath(stale, 1)
		if s.Units.Unit(stale) != nil {
			t.Fatal("stale attacker slot remained live")
		}
		finalizeCreditVictim(t, s, def, combat.CauseOrdinary, attacker.Owner, stale, 0, 2)
		if got := s.Units.RawUnitRecord(stale).Kills; got != 10 {
			t.Fatalf("empty stale attacker kills = %d, want 10", got)
		}
	})

	t.Run("reused stale attacker slot", func(t *testing.T) {
		s, attacker, def := killCreditFixture(t)
		stale := attacker.Handle
		s.Units.Destroy(stale, units.DeathKilled)
		s.Units.FinalizeDeath(stale, 1)
		reused, err := s.Units.Create(def, attacker.Owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if reused != stale {
			t.Fatalf("reused attacker slot = %d, want stale slot %d", reused, stale)
		}
		replacement := s.Units.Unit(reused)
		finalizeCreditVictim(t, s, def, combat.CauseOrdinary, attacker.Owner, stale, 0, 2)
		if replacement.Kills != 1 {
			t.Fatalf("reused attacker kills = %d, want 1", replacement.Kills)
		}
	})
}
