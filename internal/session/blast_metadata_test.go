package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Death selection uses the actual cause's weapon [06 §12.2]; its scalar
// presentation profile remains detached all the way into the committed frame.
func TestDeathBlastProfileReachesCommittedFrame(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cause        combat.Cause
		area, damage int32
	}{
		{"ordinary", combat.CauseOrdinary, 129, 73}, {"self-destruct", combat.CauseSelfDestruct, 257, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestSession(t, 1)
			victim := s.Units.IterSliced()[0]
			def := *victim.Def
			selected := &content.WeaponDef{ID: 21, AreaOfEffect: tc.area, DamageDefault: tc.damage, ExplosionGaf: "shared", ExplosionArt: "shared"}
			other := &content.WeaponDef{ID: 22, AreaOfEffect: 16, DamageDefault: 999, ExplosionGaf: "shared", ExplosionArt: "shared"}
			def.ExplodeAsDef, def.SelfDestructAsDef = selected, other
			if tc.cause == combat.CauseSelfDestruct {
				def.ExplodeAsDef, def.SelfDestructAsDef = other, selected
			}
			victim.Def = &def
			victim.Health, victim.MaxHealth, victim.Remaining = -1, 100, 0
			victim.LastDamageCause = uint8(tc.cause)
			s.Units.DestroyBy(victim.Handle, units.DeathCauseFromKind(uint8(tc.cause)), 0)
			s.stepUnitPhase(1)
			if s.Units.Unit(victim.Handle) != nil {
				t.Fatal("death did not finalize")
			}
			batch := s.publication.events.Snapshot()
			found := false
			for _, e := range batch.Events {
				if e.Kind == frame.KindExplosion || e.Kind == frame.KindWaterImpact {
					found = true
					if !e.HasBlastProfile || e.BlastAreaOfEffect != tc.area || e.BlastDamage != tc.damage {
						t.Fatalf("event batch profile = %+v", e)
					}
				}
			}
			if !found {
				t.Fatal("death explosion missing from event batch")
			}
			selected.AreaOfEffect, selected.DamageDefault = 0, 0
			s.publication.effects.Advance(1, s.publication.events.StagingEvents())
			s.publishSnapshot(1)
			published := s.Snapshot.Current()
			found = false
			for _, e := range published.Effects {
				if e.Kind == frame.KindExplosion.String() || e.Kind == frame.KindWaterImpact.String() {
					found = true
					if !e.HasBlastProfile || e.BlastAreaOfEffect != tc.area || e.BlastDamage != tc.damage {
						t.Fatalf("committed profile = %+v", e)
					}
				}
			}
			if !found {
				t.Fatal("death explosion missing from committed effects")
			}
		})
	}
}

func TestCOBExplosionLeavesBlastProfileAbsent(t *testing.T) {
	s, _ := bitmapExplosionFixture(t, bitmapOnlyFlag|0x100, numeric.FixedFromInt(11))
	views := s.publication.effects.Snapshot()
	if len(views) == 0 {
		t.Fatal("scripted explosion missing")
	}
	for _, e := range views {
		if e.HasBlastProfile || e.BlastAreaOfEffect != 0 || e.BlastDamage != 0 {
			t.Fatalf("scripted art gained weapon metadata: %+v", e)
		}
	}
}
