package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/triggers"
)

// TestStepOrderPathPublicationAfterMovement locks the corrected observable
// relationship: phase 2 integrates movement, then phase 5 performs path
// publication. A newly admitted move therefore can advance on the first
// tick, while the resulting route is visible at the phase-5 boundary [01
// §4.4][04 §7.3].
func TestStepOrderPathPublicationAfterMovement(t *testing.T) {
	rng.SeedGlobal(0x1234, 0x5678)
	s := newLoopTestSession(t, 1)
	s.SeedSessionRNG(0x1234, 0x5678)
	u := s.Units.Unit(1)
	if u == nil {
		t.Fatal("test unit missing")
	}
	u.Def.CanMove = true
	u.Def.MaxVelocity = 2000
	u.Def.TurnRate = 1000
	ensureMovementForAll(s)
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground lookup failed")
	}
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: 25 * 65536, GoalZ: 25 * 65536})
	startX, startZ := u.X, u.Z
	s.Clock.ScaledAnchor = 0
	s.Step(1)
	if u.X == startX && u.Z == startZ {
		t.Fatalf("unit did not move during phase-2 integration: start=(%d,%d) got=(%d,%d)", startX.Raw(), startZ.Raw(), u.X.Raw(), u.Z.Raw())
	}
	route := s.Movement.Routes[u.Handle]
	if route == nil || !route.Active || route.Count == 0 {
		t.Fatalf("phase-5 scheduler did not publish an active route after unit sweep: route=%+v", route)
	}
	s.Step(2)
	if u.X == startX && u.Z == startZ {
		t.Fatalf("published route was not consumed across the next unit sweep")
	}
}

// TestMissionTriggerPrecedence locks the type-specific poll order at the
// local-player phase-5 boundary. Campaign evaluates victory first; direct OTA
// evaluates defeat first only after the local commander marker clears, while a
// live commander suppresses defeat polling but not victory polling [08
// "Evaluation"].
func TestMissionTriggerPrecedence(t *testing.T) {
	newTriggerSession := func(typ mission.Type, localCommanderAlive bool) *Session {
		s := newLoopTestSession(t, 2)
		s.Mission.Type = typ
		// The fixture's authored side records name ARMCOM/CORCOM; make the
		// local side selection explicit so the gate resolves Catalog.Sides[0].
		s.Skirmish.Players[0].Side = 0
		s.Skirmish.Players[1].Side = 1
		s.Mission.Victory = []*triggers.Trigger{triggers.New(triggers.KindDestroyAllUnits, "")}
		s.Mission.Defeat = []*triggers.Trigger{triggers.New(triggers.KindCommanderKilled, "")}
		if !localCommanderAlive {
			for _, u := range s.Units.IterSliced() {
				if u != nil && u.Owner == s.LocalOwner {
					u.Alive = false
				}
			}
		}
		s.triggerDue = 0
		s.triggerDueValid = true
		return s
	}

	campaign := newTriggerSession(mission.TypeCampaign, false)
	campaign.pollMissionTriggers(0)
	if !campaign.VictoryDone || campaign.DefeatDone {
		t.Fatalf("campaign simultaneous completion must resolve as victory: victory=%v defeat=%v", campaign.VictoryDone, campaign.DefeatDone)
	}

	directDead := newTriggerSession(mission.TypeSkirmish, false)
	directDead.pollMissionTriggers(0)
	if directDead.VictoryDone || !directDead.DefeatDone {
		t.Fatalf("direct OTA with dead local commander must resolve as defeat: victory=%v defeat=%v", directDead.VictoryDone, directDead.DefeatDone)
	}

	directAlive := newTriggerSession(mission.TypeSkirmish, true)
	directAlive.pollMissionTriggers(0)
	if !directAlive.VictoryDone || directAlive.DefeatDone {
		t.Fatalf("direct OTA with live local commander must poll victory only: victory=%v defeat=%v", directAlive.VictoryDone, directAlive.DefeatDone)
	}
	if directAlive.Mission.Defeat[0].Completed {
		t.Fatal("direct OTA defeat trigger completed while local commander marker was set")
	}
}
