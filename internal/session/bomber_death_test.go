package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Dropped creators retain zero burst state [06 §4.3]. Every release is already
// a moving bomb, so death preserves its flight without leaving a scheduler
// that can release additional bombs [06 §12.1].
func TestBomberDeathPreservesReleasedBombsWithoutNewReleases(t *testing.T) {
	s := newLoopTestSession(t, 0)
	w := &content.WeaponDef{ID: 77, Dropped: true, Burst: 5, BurstRate: 1, WeaponTimer: 100}
	s.Catalog.Weapons = map[string]*content.WeaponDef{"bomb": w}
	def := s.Catalog.Units["armcom"]
	h, err := s.Units.Create(def, 0, numeric.FixedFromInt(32), numeric.FixedFromInt(100), numeric.FixedFromInt(32))
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.Move.Speed = numeric.FixedFromInt(1)
	origin := combat.Vec3{X: u.X, Y: u.Y, Z: u.Z}
	for range 2 {
		if _, ok := combat.TryFire(s.Combat, &combat.Slot{Weapon: w}, 0,
			combat.Target{Kind: combat.TargetPoint, X: u.X, Z: u.Z}, 1,
			combat.FirePorts{Shooter: u, Origin: origin, RNG: s.SimRNG()}); !ok {
			t.Fatal("bomb release failed")
		}
	}
	s.Units.Destroy(h, units.DeathKilled)
	s.finalizePhase2Death(h, 2)
	if s.Units.Unit(h) != nil {
		t.Fatal("bomber remained allocated after finalization")
	}
	if got := s.Combat.Count(); got != 2 {
		t.Fatalf("death left %d released bombs, want both still in flight [06 §4.3]", got)
	}
	for tick := uint32(2); tick <= 4; tick++ {
		s.phaseProjectiles(tick)
		if got := s.Combat.Count(); got != 2 {
			t.Fatalf("tick %d has %d bombs, want only the two already released", tick, got)
		}
		for i := 0; i < s.Combat.Count(); i++ {
			p := s.Combat.Records[i]
			if p.BurstRemaining != 0 || p.CreationTick != 1 || p.Pos == origin {
				t.Fatalf("tick %d bomb %d became a stationary scheduler or new release: %+v", tick, i, p)
			}
		}
	}
}
