package combat

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestWeaponDamageFalloffRetainsLowWord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    int32
		falloff float32
		want    int32
	}{
		{"positive wrap", 1073741825, 4, 4},
		{"negative wrap", -1073741825, 4, -4},
		{"infinity", 100, float32(math.Inf(1)), 0},
		{"nan", 100, float32(math.NaN()), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			victim := &units.Unit{Def: &content.UnitDef{UnitName: "target"}}
			weapon := &content.WeaponDef{Damage: map[string]int32{"target": tc.base}}
			// The signed authored override crosses the stored-falloff boundary before
			// any recipient scale [06 §9.2][01 R-DET-01 §1].
			if got := weaponDamageNominal(weapon, victim, nil, tc.falloff); got != tc.want {
				t.Fatalf("nominal=%d want%d", got, tc.want)
			}
		})
	}
}

func TestDamageShooterPresencePrecedesArmor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present bool
		nominal int32
		packet  uint16
	}{
		{"null shooter", false, 35000000, 45720},
		{"present zero-kill shooter", true, -7949672, 22860},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReactionFixture(t)
			f.victim.Armored = true
			f.victim.Def.DamageModifier = 32768
			weapon := &content.WeaponDef{Damage: map[string]int32{f.victim.Def.UnitName: 35000000}}
			var attacker *units.Unit
			if tc.present {
				attacker = f.attacker
			}
			nominal := weaponDamageNominal(weapon, f.victim, attacker, 1)
			// 35,000,000*100 wraps to -794,967,296 before /100. A null shooter
			// skips that stage, so only the present-shooter arm enters armor [06 §9.2].
			if nominal != tc.nominal {
				t.Fatalf("nominal=%d want%d", nominal, tc.nominal)
			}
			got := f.svc.AcceptDamage(f.w, 7, DamageInput{Victim: f.victim.Handle, Nominal: nominal, Kind: KindNoReaction})
			if !got.Accepted || got.Amount != tc.packet {
				t.Fatalf("result=%+v want amount%d", got, tc.packet)
			}
		})
	}
}

func TestDamageKillReadersUseStoredWord(t *testing.T) {
	f := newReactionFixture(t)
	f.attacker.Kills = 65536
	f.victim.Kills = 65536
	weapon := &content.WeaponDef{DamageDefault: 100}
	nominal := weaponDamageNominal(weapon, f.victim, f.attacker, 1)
	if nominal != 100 {
		t.Fatalf("attacker nominal=%d want100", nominal)
	}
	got := f.svc.AcceptDamage(f.w, 7, DamageInput{Victim: f.victim.Handle, Nominal: nominal, Kind: KindNoReaction})
	if !got.Accepted || got.Amount != 100 {
		t.Fatalf("defender result=%+v want100", got)
	}
}

func TestReloadKillReaderUsesStoredWord(t *testing.T) {
	// A whole-word wrap must not grant the maximum veteran reload reduction
	// [06 §4.2]; both production and the exposed intermediate use one reader.
	if got := ComputeStoredReload(100, 100, 65536, 100); got != 100 {
		t.Fatalf("reload=%d want100", got)
	}
	if got := VeteranReloadForTest(65536, 100); got != 100 {
		t.Fatalf("intermediate reload=%d want100", got)
	}
}
