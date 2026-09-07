package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// These exercise the session binding and the actual order/sweep producers,
// including the defender-only fixed packet and pre-rewrite reaction [06 §9.1–§9.2].
func TestRuntimeSelfDamageProducersUseSharedIntake(t *testing.T) {
	for _, producer := range []string{"countdown", "commander sweep"} {
		for _, state := range []string{"veteran", "lethal", "remote", "dead latch"} {
			t.Run(producer+"/"+state, func(t *testing.T) {
				s, def := waterDamageFixture(t, 0, 0)
				def.SelfDestructCountdownPresent = true
				def.SelfDestructCountdown = "0"
				h, err := s.Units.Create(def, 0, 0, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				u := s.Units.Unit(h)
				u.Health, u.MaxHealth = 29000, 29000
				u.Armored, def.DamageModifier = true, 32768
				u.LastDamageCause, u.LastDamageSide = 5, 8
				u.EngagementTarget = 3
				switch state {
				case "veteran":
					u.Kills = 25
				case "remote":
					// The owner sweep's remote row intentionally takes its packetless arm.
					if producer == "commander sweep" {
						t.Skip("remote sweep uses the separate silent teardown contract")
					}
					s.Econ.Players[0].ControllerState = combat.ControlByteRemote
				case "dead latch":
					u.Dying = true
				}
				flashes, observations := 0, 0
				s.Combat.Events = func(ev combat.Event) {
					if ev.Kind == combat.EventDamageFlash {
						flashes++
						if ev.Tick != 17 || ev.Source != h || ev.Target != h {
							t.Errorf("flash = %+v", ev)
						}
					}
				}
				s.Combat.Reaction = &combat.ReactionSeams{ObserverNotice: func(v *units.Unit) {
					observations++
					if uint8(v.BlinkSuppress) != 240 || v.LastDamageCause != 5 || v.LastDamageSide != 8 || v.EngagementTarget != 3 {
						t.Errorf("reaction must see flash and prior provenance: %+v", v)
					}
				}}
				if producer == "countdown" {
					q := orders.BindQueueBinding(u, s.newOrderBinding())
					q.Push(orders.Lookup("SelfDestructFG"), orders.Node{Owner: h})
					q.Pump(u, 17)
				} else {
					s.sweepOwnerAfterCommanderDeath(0, 17)
				}
				if state == "dead latch" {
					if u.Health != 29000 || flashes != 0 || observations != 0 || u.LastDamageCause != 5 {
						t.Fatal("rejected damage changed dead-latched unit")
					}
					return
				}
				if flashes != 1 || observations != 1 || u.LastDamageCause != uint8(combat.CauseSelfDestruct) || u.LastDamageSide != 0 || u.EngagementTarget != h {
					t.Fatalf("intake effects: flashes=%d observations=%d kind=%d side=%d attacker=%d", flashes, observations, u.LastDamageCause, u.LastDamageSide, u.EngagementTarget)
				}
				wantHealth, wantDeath := int32(-1000), true
				if state == "veteran" {
					wantHealth, wantDeath = 5000, false
				}
				if state == "remote" {
					wantHealth, wantDeath = 0, false
				}
				if u.Health != wantHealth || u.Dying != wantDeath {
					t.Fatalf("health/death = %d/%v, want %d/%v", u.Health, u.Dying, wantHealth, wantDeath)
				}
			})
		}
	}
}

func TestWaterVisitFlashesWithoutReactionAndPreservesPriorAttacker(t *testing.T) {
	s, def := waterDamageFixture(t, 1, 10)
	h, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(5), 0)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.LastDamageCause, u.LastDamageSide, u.EngagementTarget = 5, 8, 3
	u.Armored, def.DamageModifier, u.Kills = true, 32768, 25
	s.Combat.Reaction = &combat.ReactionSeams{ObserverNotice: func(*units.Unit) { t.Error("kind 11 ran reaction") }}
	flashes := 0
	s.Combat.Events = func(ev combat.Event) {
		if ev.Kind == combat.EventDamageFlash {
			flashes++
			if ev.Tick != 30 {
				t.Error("wrong water tick")
			}
		}
	}
	s.stepWaterDamage(u, 30)
	if u.Health != 96 || uint8(u.BlinkSuppress) != 240 || flashes != 1 || u.LastDamageCause != combat.KindNoReaction || u.LastDamageSide != 8 || u.EngagementTarget != 3 {
		t.Fatalf("water intake = health %d flash %d/%d provenance %d/%d/%d", u.Health, uint8(u.BlinkSuppress), flashes, u.LastDamageCause, u.LastDamageSide, u.EngagementTarget)
	}
	u.Dying = true
	s.stepWaterDamage(u, 60)
	if u.Health != 96 || flashes != 1 {
		t.Fatal("water accepted a dead-latched victim")
	}
}
