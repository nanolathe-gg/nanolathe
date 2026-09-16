package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Cargo delivery keeps the common receiver's guards, signed health and raw
// attacker semantics; its caller still detaches survivors [06 §9.1][06 §12.1].
func TestCargoCascadeCommonIntake(t *testing.T) {
	for _, state := range []string{"veteran", "lethal", "zero health", "remote", "dead latch", "null attacker", "freed attacker", "reused attacker"} {
		t.Run(state, func(t *testing.T) {
			w, system, carrier, cargo, killer := newCarrierWithCargo(t)
			service := bindCargoDamageFixture(system, w)
			cargo.Health, cargo.MaxHealth = 29000, 29000
			cargo.Armored, cargo.Def.DamageModifier = true, 32768
			cargo.LastDamageCause, cargo.LastDamageSide, cargo.EngagementTarget = 5, 8, carrier.Handle
			killer.Kills = 25 // a fixed packet must not apply this attacker's tier.
			attacker := killer.Handle
			wantSide := killer.Owner
			wantHealth, wantDeath := int32(5000), false
			cargo.Kills = 25
			switch state {
			case "lethal":
				cargo.Kills, wantHealth, wantDeath = 0, -1000, true
			case "zero health":
				cargo.Health, cargo.Kills, wantHealth, wantDeath = 0, 0, -30000, true
			case "remote":
				cargo.Kills, wantHealth = 0, 0
				service.ControlByte = func(uint8) uint8 { return combat.ControlByteRemote }
			case "dead latch":
				cargo.Dying, wantHealth, wantDeath = true, 29000, true
			case "null attacker":
				attacker, wantSide = 0, 8
			case "freed attacker", "reused attacker":
				w.FreeImmediate(killer.Handle)
				if state == "reused attacker" {
					h, err := w.Create(killer.Def, killer.Owner, killer.X, killer.Y, killer.Z)
					if err != nil {
						t.Fatal(err)
					}
					if h != attacker {
						t.Fatalf("fixture reused %d, want raw slot %d", h, attacker)
					}
					// Raw slot owner, rather than the old live object, supplies provenance.
					w.Unit(h).Owner = 7
					wantSide = 7
				}
			}
			flashes, observations := 0, 0
			service.Events = func(ev combat.Event) {
				if ev.Kind == combat.EventDamageFlash {
					flashes++
					if ev.Tick != 37 || ev.Source != attacker {
						t.Errorf("flash = %+v", ev)
					}
				}
			}
			service.Reaction = &combat.ReactionSeams{ObserverNotice: func(v *units.Unit) {
				observations++
				if uint8(v.BlinkSuppress) != 240 || v.LastDamageCause != 5 || v.LastDamageSide != 8 || v.EngagementTarget != carrier.Handle {
					t.Error("reaction lost prior provenance or flash")
				}
			}}
			system.HandleDeath(w, carrier.Handle, attacker, 37)
			if cargo.Health != wantHealth || cargo.Dying != wantDeath || isCarried(w, cargo.Handle) {
				t.Fatalf("health/death/carried = %d/%v/%v", cargo.Health, cargo.Dying, isCarried(w, cargo.Handle))
			}
			if state == "dead latch" {
				if flashes != 0 || observations != 0 || cargo.LastDamageCause != 5 {
					t.Fatal("rejected cargo changed packet state")
				}
				return
			}
			wantAttacker := attacker
			if attacker == 0 {
				wantAttacker = carrier.Handle
			}
			if flashes != 1 || observations != 1 || cargo.LastDamageCause != uint8(combat.CauseCargo) || cargo.LastDamageSide != wantSide || cargo.EngagementTarget != wantAttacker {
				t.Fatalf("effects = %d/%d kind/side/attacker = %d/%d/%d", flashes, observations, cargo.LastDamageCause, cargo.LastDamageSide, cargo.EngagementTarget)
			}
		})
	}
}
