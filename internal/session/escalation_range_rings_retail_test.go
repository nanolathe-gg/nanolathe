//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestEscalationRangeRingsMatchFiringRange locks issue #199: Gold 10.2.0
// shows a selected turret's firing range on the minimap through an
// `antiweapons` unit's interceptor slot named "Range Ring", authored with
// coverage equal to the range plus retail's 512 bias [03 §3.9]. Under the
// Escalation config the published ring radius is therefore the authored
// range of every such ring, and a T1 artillery tower's ring is its cannon's
// firing range. Scope: research/extensions/taesc-engine.md "Interceptor
// coverage and range rings".
func TestEscalationRangeRingsMatchFiringRange(t *testing.T) {
	fs, cat, limits, profile := loadEscalationShields(t)
	cfg := DirectSkirmishConfig("expanded confluence")
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
	limit := 100
	sources := CommunitySources{Content: profile.GameplaySources(), Player: community.Overrides{UnitLimit: &limit}}
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{ContentLimits: limits, CommunitySources: sources})
	if err != nil {
		t.Fatal(err)
	}
	if s.Combat.Community.AntinukeCircularCoverage {
		t.Error("the Escalation config resolved circular antinuke coverage")
	}

	rings := 0
	for _, key := range cat.SortedUnitKeys() {
		def, _ := cat.Unit(key)
		if def == nil || !def.AntiWeapons {
			continue
		}
		for _, w := range []*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
			if w == nil || !w.Interceptor || w.Name != "Range Ring" {
				continue
			}
			rings++
			if got := s.Combat.InterceptorRingRadius(w); got != w.Range {
				t.Errorf("%s %s: ring radius %d, authored range %d (coverage %d)", key, w.CanonicalKey, got, w.Range, w.Coverage)
			}
		}
	}
	if rings == 0 {
		t.Fatal("no authored range ring found")
	}

	// The reported T1 artillery: both factions' towers and the Guardian's
	// Ambusher upgrade publish one dashed ring at the slot-1 cannon's range.
	for i, key := range []string{"ARMGUARD", "CORPUN", "ARMAMB"} {
		u := placeCompleteRetailUnit(t, s, key, 0, numeric.FixedFromInt(int64(800+300*i)), numeric.FixedFromInt(1000))
		cannon := u.SlotAt(0).Weapon
		if cannon == nil || cannon.Interceptor || cannon.Range <= 0 {
			t.Fatalf("%s slot 1 is not its authored cannon", key)
		}
		advanceEscalationShieldTicks(t, s, 1)
		var enabled []int32
		for _, c := range s.Snapshot.Current().Radar.Contacts {
			if c.Handle != u.Handle {
				continue
			}
			for _, r := range c.Rings {
				if r.Enabled {
					if !r.Dashed {
						t.Errorf("%s: interceptor ring is solid", key)
					}
					enabled = append(enabled, r.Range)
				}
			}
		}
		if len(enabled) != 1 || enabled[0] != cannon.Range {
			t.Fatalf("%s: published ring radii %v, want only the cannon's range %d", key, enabled, cannon.Range)
		}
	}
}
