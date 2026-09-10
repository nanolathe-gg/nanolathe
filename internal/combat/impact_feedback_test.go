package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"testing"
)

// Direct feedback uses the nominal amount's low unsigned word; area feedback
// uses signed nominal totals before packet narrowing and armor [06 §9.4].
func TestImpactFeedbackUsesItsOwnAmountBoundary(t *testing.T) {
	for _, tc := range []struct {
		name            string
		direct          bool
		enemy, friendly int32
		want            uint32
	}{
		{"direct low word zero", true, 65536, 0, 0x2000},
		{"direct enemy", true, 1, 0, 0x4000},
		{"area retains full nominal", false, 65536, 0, 0x4000},
		{"area strict equality", false, 100, 50, 0x2000},
		{"area before armor", false, 101, 50, 0x4000},
		{"area signed friendly", false, 0, -1, 0x4000},
		{"empty area", false, 0, 0, 0x2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, w, terrain := splashFixture(t)
			shooter := splashUnit(t, w, &content.UnitDef{UnitName: "shooter", MaxDamage: 100}, 0, 10, 0, 10)
			shooter.Pending = 4
			weapon := &content.WeaponDef{AreaOfEffect: 32, Damage: map[string]int32{"enemy": tc.enemy, "friendly": tc.friendly}, UnitsOnly: true}
			var direct pool.Handle
			for i, entry := range []struct {
				name   string
				owner  uint8
				amount int32
			}{{"enemy", 1, tc.enemy}, {"friendly", 0, tc.friendly}} {
				if entry.amount == 0 {
					continue
				}
				u := splashUnit(t, w, &content.UnitDef{UnitName: entry.name, MaxDamage: 100, FootprintX: 1, FootprintZ: 1, DamageModifier: 0}, entry.owner, 64, 0, 64)
				u.Armored = true
				if i == 0 {
					stampGroundOccupancy(t, terrain, u)
					direct = u.Handle
				} else {
					stampAirOccupancy(t, terrain, u)
				}
			}
			if tc.direct {
				weapon.AreaOfEffect = 16
			} else {
				direct = 0
			}
			p := &Projectile{Shooter: shooter.Handle, ShooterSide: 0, Pos: Vec3{X: 64 << 16, Z: 64 << 16}}
			handleProjectileImpact(svc, 0, p, weapon, w, terrain, nil, nil, nil, 100, Vec3{}, nil, direct)
			if shooter.Pending != 4|tc.want {
				t.Fatalf("shooter pending=%#x want=%#x", shooter.Pending, 4|tc.want)
			}
		})
	}
}

// The side classifier uses the projectile's saved side, and feedback targets
// the shooter rather than the damaged unit [06 §9.4].
func TestImpactFeedbackUsesProjectileSide(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	shooter := splashUnit(t, w, &content.UnitDef{UnitName: "shooter", MaxDamage: 100}, 0, 10, 0, 10)
	victim := splashUnit(t, w, &content.UnitDef{UnitName: "victim", MaxDamage: 100}, 1, 64, 0, 64)
	p := &Projectile{Shooter: shooter.Handle, ShooterSide: 1, Pos: Vec3{X: 64 << 16, Z: 64 << 16}}
	weapon := &content.WeaponDef{AreaOfEffect: 16, DamageDefault: 10}
	handleProjectileImpact(svc, 0, p, weapon, w, terrain, nil, nil, nil, 100, Vec3{}, nil, victim.Handle)
	if shooter.Pending != 0x2000 || victim.Pending&0x6000 != 0 {
		t.Fatalf("shooter/victim pending=%#x/%#x", shooter.Pending, victim.Pending)
	}
	p.Shooter = 0
	handleProjectileImpact(svc, 0, p, weapon, w, terrain, nil, nil, nil, 101, Vec3{}, nil, victim.Handle)
	if victim.Pending&0x6000 != 0 {
		t.Fatal("null shooter feedback reached victim")
	}
}

// Both accumulation and doubling retain signed 32-bit wrap [06 §9.4].
func TestImpactFeedbackWrapsTotalsBeforeComparing(t *testing.T) {
	_, w, _ := splashFixture(t)
	shooter := splashUnit(t, w, &content.UnitDef{UnitName: "shooter", MaxDamage: 100}, 0, 10, 0, 10)
	f := impactFeedback{shooter: shooter.Handle, enemy: 2147483647}
	f.add(1, false)
	f.publish(w)
	if shooter.Pending != 0x2000 {
		t.Fatalf("wrapped enemy feedback=%#x", shooter.Pending)
	}
	shooter.Pending = 0
	f = impactFeedback{shooter: shooter.Handle, friendly: 1073741824}
	f.publish(w)
	if shooter.Pending != 0x4000 {
		t.Fatalf("wrapped friendly double feedback=%#x", shooter.Pending)
	}
}

// The outer blast publishes after nested interceptor impacts [06 §9.4].
func TestImpactFeedbackFollowsNestedInterceptorImpacts(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	shooter := splashUnit(t, w, &content.UnitDef{UnitName: "shooter", MaxDamage: 100}, 0, 10, 0, 10)
	incoming := &content.WeaponDef{ID: 92, AreaOfEffect: 32, UnitsOnly: true}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"incoming": incoming}}
	cat.RebuildWeaponIndex()
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	pos := Vec3{X: 64 << 16, Z: 64 << 16}
	svc.Records[int(h)-1].WeaponID = incoming.ID
	svc.Records[int(h)-1].Pos = pos
	impacts := 0
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			impacts++
			if shooter.Pending != 0 {
				t.Errorf("feedback preceded nested impact: %#x", shooter.Pending)
			}
		}
	}
	p := &Projectile{Shooter: shooter.Handle, Pos: pos}
	handleProjectileImpact(svc, 0, p, &content.WeaponDef{AreaOfEffect: 32, Interceptor: true, UnitsOnly: true}, w, terrain, nil, nil, cat, 100, Vec3{}, nil, 0)
	if impacts != 2 || shooter.Pending != 0x2000 {
		t.Fatalf("impacts=%d final feedback=%#x", impacts, shooter.Pending)
	}
}
