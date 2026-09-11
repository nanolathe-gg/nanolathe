package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A missing death weapon resolves to record zero, which still reaches central
// impact through the live death finalizer [06 §12.2][06 R-WFX-01 §2].
func TestDeathFinalizationPreservesSentinelExplosion(t *testing.T) {
	for _, cause := range []combat.Cause{combat.CauseOrdinary, combat.CauseSelfDestruct} {
		t.Run(map[combat.Cause]string{combat.CauseOrdinary: "ordinary", combat.CauseSelfDestruct: "self-destruct"}[cause], func(t *testing.T) {
			s := newLoopTestSession(t, 1)
			victim := s.Units.IterSliced()[0]
			def := *victim.Def
			sentinel := &content.WeaponDef{ID: 0}
			other := &content.WeaponDef{ID: 1, ExplosionGaf: "other", ExplosionArt: "wrong-death-weapon", SoundHit: "wrong-death-sound"}
			def.ExplodeAsDef, def.SelfDestructAsDef = sentinel, other
			if cause == combat.CauseSelfDestruct {
				def.ExplodeAsDef, def.SelfDestructAsDef = other, sentinel
			}
			victim.Def = &def
			victim.Health, victim.MaxHealth, victim.Remaining = -1, 100, 0
			victim.LastDamageCause = uint8(cause)
			var events []combat.Event
			sink := s.Combat.Events
			s.Combat.Events = func(ev combat.Event) {
				events = append(events, ev)
				if sink != nil {
					sink(ev)
				}
			}
			s.Units.DestroyBy(victim.Handle, units.DeathCauseFromKind(uint8(cause)), 0)
			s.stepUnitPhase(1)
			if s.Units.Unit(victim.Handle) != nil {
				t.Fatal("victim did not finalize")
			}
			flashes := 0
			for _, ev := range events {
				if ev.Kind == combat.EventHitSound || ev.Kind == combat.EventWaterSound {
					t.Fatalf("sentinel death emitted sound: %+v", ev)
				}
				if ev.Kind == combat.EventExplosion || ev.Kind == combat.EventWaterExplosion {
					flashes++
					if !ev.HasCalculatedFlash || ev.CalculatedTable != 0 || ev.Bank != "" || ev.Graphic != "" {
						t.Fatalf("death effect = %+v, want record-zero calculated flash [06 R-WFX-01 §2]", ev)
					}
				}
			}
			if flashes != 1 {
				t.Fatalf("death flashes = %d, want one sentinel impact [06 §12.2]", flashes)
			}
		})
	}
}
