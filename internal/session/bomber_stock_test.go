package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Stock dropped weapons fire one root at each authored reload boundary
// [06 §3.3][06 §4.2], then stop at lethal intake before the later slot-end
// finalization [06 §9.1][06 §12.1]. This probes those gates with retail scripts
// while keeping the point target fixed; it does not prescribe bombs per run.
func TestRetailBomberCadenceAndDeath(t *testing.T) {
	s := aiE2ESkirmish(t, "ashap plateau", 7)
	// This fixture locks retail launch semantics, including authored Hold Fire.
	s.SetGameplay(gameplay.Strict31)
	for _, key := range s.Catalog.SortedUnitKeys() {
		def, _ := s.Catalog.Unit(key)
		if def == nil || !def.CanFly || def.Weapon1Def == nil || !def.Weapon1Def.Dropped {
			continue
		}
		h, err := s.Units.Create(def, 0, numeric.FixedFromInt(2000), numeric.FixedFromInt(1000), numeric.FixedFromInt(2000))
		if err != nil {
			t.Fatal(err)
		}
		u := s.Units.Unit(h)
		slot := u.SlotAt(0)
		slot.Flags |= units.SlotFlagEnabled
		slot.Target = units.Target{Kind: units.TargetGround, X: u.X, Z: u.Z}
		w := slot.Weapon
		var releases []uint32
		for tick := uint32(1); tick <= 20; tick++ {
			sum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
			if sum.Fired > 0 {
				if sum.Fired != 1 {
					t.Fatalf("%s emitted %d roots in one visit", key, sum.Fired)
				}
				releases = append(releases, tick)
			}
		}
		if len(releases) < 2 {
			t.Fatalf("%s did not establish a firing cadence: %v", key, releases)
		}
		for i := 1; i < len(releases); i++ {
			if delta := releases[i] - releases[i-1]; delta != uint32(w.ReloadTime) {
				t.Fatalf("%s release interval %d differs from authored reload %d", key, delta, w.ReloadTime)
			}
		}
		res := s.Combat.AcceptDamage(s.Units, 20, combat.DamageInput{Victim: h, Nominal: 30000, Kind: 1})
		if !res.DeathLatched {
			t.Fatalf("%s did not die: %+v", key, res)
		}
		if s.Units.Unit(h) != u || !u.Dying {
			t.Fatalf("%s did not retain its death-marked record until finalization", key)
		}
		for tick := uint32(21); tick <= 40; tick++ {
			sum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
			if sum.Fired != 0 {
				t.Fatalf("%s fired after lethal intake at %d", key, tick)
			}
			if tick == 21 {
				s.finalizePhase2Death(h, tick)
				if s.Units.Unit(h) != nil {
					t.Fatalf("%s remained allocated after finalization", key)
				}
			}
		}
		t.Logf("%s weapon=%s burst=%d reload=%d releaseTicks=%v deathLatched=%v freed=%v", key, w.CanonicalKey, w.Burst, w.ReloadTime, releases, res.DeathLatched, s.Units.Unit(h) == nil)
	}
}
