//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestEscalationWeaponCharging exercises the unupgraded Gold 10.2.0 Sentinel's
// authored Detect, AimPrimary and FirePrimary through ordinary ground attack.
// It neither starts callbacks nor edits their statics, deadlines or port results.
// Scope: research/extensions/escalation-weapon-charging.md, "Session acceptance".
func TestEscalationWeaponCharging(t *testing.T) {
	fs, cat, limits, profile := loadEscalationShields(t)
	for _, key := range []string{"ARMHLT", "ARMFIELD"} {
		if _, ok := cat.Unit(key); !ok {
			t.Fatalf("missing authored %s", key)
		}
	}
	for _, path := range []string{"units/armhlt.fbi", "units/armfield.fbi", "weapons/taesc.tdf"} {
		info, err := fs.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("source %s: %s %s", path, info.Source.ProviderID(), info.Source.OriginalPath)
	}
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
	// Keep the starting commanders away from this isolated fixture. The
	// recipient and fields still use the ordinary session and script scheduler.
	for _, a := range s.AI {
		if a != nil {
			for j := range a.Deadlines {
				a.Deadlines[j] = ^uint32(0)
			}
		}
	}
	place := func(key string, x, z int32) *units.Unit {
		return placeCompleteRetailUnit(t, s, key, 0, numeric.FixedFromInt(int64(x)), numeric.FixedFromInt(int64(z)))
	}
	tower := place("ARMHLT", 1000, 1000)
	weapon := tower.SlotAt(0).Weapon
	if weapon == nil || weapon.CanonicalKey != "laser_heavy" || weapon.ReloadTime != 6 {
		t.Fatal("Sentinel does not have the authored LASER_HEAVY with six-tick nominal reload")
	}
	var fires []uint32
	// An actual FirePrimary start follows projectile admission [06 §4.4].
	// The sink only records the session tick; it never calls the bridge or VM.
	tower.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Name == "FirePrimary" && e.Phase == "start" {
			fires = append(fires, s.Clock.GlobalTick)
		}
	})
	targetX, targetZ := numeric.FixedFromInt(1000), numeric.FixedFromInt(1300)
	if err = s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{tower.Handle}, Code: int(input.LatchAttack),
		Position: orders.ResolvePos{X: targetX, Y: s.World.HeightAt(targetX, targetZ), Z: targetZ},
	}}); err != nil {
		t.Fatal(err)
	}
	step := func(n int) {
		for i := 0; i < n; i++ {
			// Keep authored per-shot and field upkeep costs funded so resource
			// admission cannot masquerade as the script's readiness delay.
			s.Econ.Players[0].Capacity[economy.Energy] = 1000000
			s.Econ.Players[0].Stock[economy.Energy] = 1000000
			advanceEscalationShieldTicks(t, s, 1)
		}
	}
	sample := func(label string, wantInterval uint32) {
		t.Helper()
		fires = nil
		step(400)
		if len(fires) < 8 {
			t.Fatalf("%s: insufficient repeated fire: %v; target=%+v diagnostics=%v", label, fires, tower.SlotAt(0).Target, tower.Script.Diagnostics())
		}
		for i := 1; i < len(fires); i++ {
			if got := fires[i] - fires[i-1]; got != wantInterval {
				t.Fatalf("%s: interval=%d, want %d ticks; fires=%v", label, got, wantInterval, fires)
			}
		}
		if !tower.Alive || tower.SlotAt(0).Target.Kind != units.TargetGround || tower.SlotAt(0).Weapon != weapon || weapon.ReloadTime != 6 {
			t.Fatalf("%s: recipient, ground attack or nominal weapon changed", label)
		}
		if diags := tower.Script.Diagnostics(); len(diags) != 0 {
			t.Fatalf("%s: recipient script diagnostics: %v", label, diags)
		}
		t.Logf("%s: repeated shot interval %d ticks", label, wantInterval)
	}
	step(200)
	sample("isolated", 36)
	field := place("ARMFIELD", 800, 1000)
	// An already isolated detector can be sleeping for 225 seconds. Let the
	// authored scan wake naturally; no forced scan or script restart occurs.
	step(7000)
	sample("one completed field", 18)
	second := place("ARMFIELD", 1000, 800)
	// With a nearby completed field, the authored longest scan sleep is
	// 13.5 seconds. This also clears a shot straddling a state transition.
	step(450)
	sample("two completed fields", 18)
	for i, provider := range []*units.Unit{field, second} {
		if diags := provider.Script.Diagnostics(); len(diags) != 0 {
			t.Fatalf("field script diagnostics: %v", diags)
		}
		// Ordinary reclaim damage avoids the field's nuclear death explosion
		// obscuring removal. The detector observes the normal unit lifecycle.
		r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: provider.Handle, Nominal: 30000, Kind: 5})
		if !r.DeathLatched {
			t.Fatal("reclaim damage did not remove the provider")
		}
		step(450)
		if provider.Alive {
			t.Fatal("provider was not finalized")
		}
		if i == 0 {
			sample("one overlapping field removed", 18)
		} else {
			sample("last field removed", 36)
		}
	}
}
