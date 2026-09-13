package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

func TestDamageActivityAfterReactionBeforeHealthAndParalyze(t *testing.T) {
	for _, kind := range []uint8{KindOrdinary, KindParalyzer, KindNoReaction, KindHeal} {
		f := newReactionFixture(t)
		calls := 0
		f.svc.Reaction.ObserverNotice = func(v *units.Unit) { v.Owner = 3 }
		f.svc.DamageActivity = func(v, a *units.Unit, tick uint32) {
			calls++
			if tick != 7 || v.LastDamageCause != kind || v.LastDamageSide != a.Owner || v.Health != 5000 {
				t.Fatalf("activity before provenance or after health: kind=%d victim=%+v", kind, v)
			}
			if kind != KindNoReaction && v.Owner != 3 {
				t.Fatal("activity ran before reaction")
			}
		}
		f.svc.AcceptDamage(f.w, 7, DamageInput{Victim: f.victim.Handle, Attacker: f.attacker.Handle, Kind: kind, Nominal: 1})
		want := 1
		if kind == KindHeal {
			want = 0
		}
		if calls != want {
			t.Fatalf("kind=%d callbacks=%d want %d", kind, calls, want)
		}
	}
}
