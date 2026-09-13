package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The profile is authored modern presentation metadata, copied at the existing
// central-impact event without changing its order [06 §13.2].
func TestCentralImpactCopiesBlastProfile(t *testing.T) {
	for _, tc := range []struct {
		name         string
		area, damage int32
		water        bool
	}{
		{"land", 129, 731, false}, {"water", 257, 19, true}, {"known-zero", 64, 0, false}, {"signed-values", -7, -11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weapon := &content.WeaponDef{AreaOfEffect: tc.area, DamageDefault: tc.damage, SoundHit: "hit", SoundWater: "water", ShakeMagnitude: 1}
			var terrain *world.Terrain
			if tc.water {
				terrain = &world.Terrain{CellW: 1, CellH: 1, SeaLevel: 1, Plot: make([]world.PlotCell, 1)}
			}
			var events []Event
			svc := &Service{Events: func(e Event) { events = append(events, e) }}
			handleProjectileImpact(svc, 0, &Projectile{}, weapon, nil, terrain, nil, nil, nil, 7, Vec3{}, nil, 0)
			sound, explosion := EventHitSound, EventExplosion
			if tc.water {
				sound, explosion = EventWaterSound, EventWaterExplosion
			}
			want := []EventKind{EventShake, sound, explosion, EventProjectileImpact}
			if len(events) != len(want) {
				t.Fatalf("events = %+v", events)
			}
			weapon.AreaOfEffect, weapon.DamageDefault = 0, 0
			for i, e := range events {
				if e.Kind != want[i] {
					t.Fatalf("event %d kind = %v, want %v", i, e.Kind, want[i])
				}
				if i == 2 {
					if !e.HasBlastProfile || e.BlastAreaOfEffect != tc.area || e.BlastDamage != tc.damage {
						t.Fatalf("profile = %+v", e)
					}
				} else if e.HasBlastProfile {
					t.Fatalf("non-explosion gained a profile: %+v", e)
				}
			}
		})
	}
}
