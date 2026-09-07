package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestDamageProvenanceReadsRawAttackerSlot(t *testing.T) {
	weapon := &content.WeaponDef{ID: 1, DamageDefault: 1}

	t.Run("freed-slot", func(t *testing.T) {
		f := newReactionFixture(t)
		f.attacker.Kills = 17
		h := f.attacker.Handle
		owner := f.attacker.Owner
		f.w.FreeImmediate(h)
		if f.w.Unit(h) != nil || f.w.RawUnitRecord(h) == nil {
			t.Fatal("fixture did not retain a raw freed slot")
		}
		applyDamageToUnit(f.svc, f.victim, &Projectile{Shooter: h, ShooterSide: 9}, weapon, 1, 0, f.w, 3)
		if f.victim.LastDamageSide != owner {
			t.Fatalf("freed attacker side = %d, want raw slot owner %d [06 R-WPN-04 §2]", f.victim.LastDamageSide, owner)
		}
	})

	t.Run("reused-slot", func(t *testing.T) {
		f := newReactionFixture(t)
		h := f.attacker.Handle
		f.w.FreeImmediate(h)
		reused, err := f.w.Create(f.attacker.Def, f.attacker.Owner, f.attacker.X, f.attacker.Y, f.attacker.Z)
		if err != nil || reused != h {
			t.Fatalf("reuse = %d, %v; want handle %d", reused, err, h)
		}
		// The raw reader observes the record presently in the reused slot. The
		// fixture changes its owner after allocation so the expected side cannot
		// be satisfied by the old retained record or the packet routing byte.
		f.w.Unit(reused).Owner = 4
		applyDamageToUnit(f.svc, f.victim, &Projectile{Shooter: h, ShooterSide: 9}, weapon, 1, 0, f.w, 4)
		if f.victim.LastDamageSide != 4 {
			t.Fatalf("reused attacker side = %d, want current raw slot owner 4 [06 R-WPN-04 §2]", f.victim.LastDamageSide)
		}
	})

	t.Run("null-paralyzer-keeps-attacker-provenance", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.LastDamageSide = 8
		f.victim.EngagementTarget = f.attacker.Handle
		before := f.victim.Health
		paralyzer := &content.WeaponDef{ID: 2, DamageDefault: 20, Paralyzer: true}
		applyDamageToUnit(f.svc, f.victim, &Projectile{ShooterSide: 3}, paralyzer, 1, 0, f.w, 5)
		if f.victim.LastDamageSide != 8 || f.victim.EngagementTarget != f.attacker.Handle {
			t.Fatalf("null paralyzer rewrote provenance: side=%d target=%d [06 R-WPN-04 §2]", f.victim.LastDamageSide, f.victim.EngagementTarget)
		}
		if Cause(f.victim.LastDamageCause) != CauseParalyzer || f.victim.Health != before {
			t.Fatalf("null paralyzer cause/health = %d/%d, want paralyzer/%d [06 §10]", f.victim.LastDamageCause, f.victim.Health, before)
		}
	})
}
