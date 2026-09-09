package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	// The ground follower gains speed only by adding +Acceleration and sheds it
	// by subtracting -BrakeRate; both gates of [04 R-MOV-01 §4] are also
	// divisions by TurnRate and BrakeRate. A definition that authors neither
	// stays at speed zero for ever, so a fixture asserting movement must author
	// them — the earlier fixture set only MaxVelocity and TurnRate and could
	// never move, whatever the phase order did.
	u.Def.Acceleration = 1000
	u.Def.BrakeRate = 1000
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

// TestMissionTriggerPrecedence locks the kind-specific queue boundary.
// Kind 1 evaluates victory first and does not poll defeat after a true victory;
// kind 2 never polls either queue [08 R-TRIG-01 §1, §6].
func TestMissionTriggerPrecedence(t *testing.T) {
	newTriggerSession := func(typ mission.Type) *Session {
		s := newLoopTestSession(t, 2)
		s.Mission.Type = typ
		s.Mission.Units = []mission.UnitPlacement{{}}
		s.Mission.Victory = []*triggers.Trigger{triggers.New(triggers.KindDestroyAllUnits, "")}
		s.Mission.Defeat = []*triggers.Trigger{triggers.NewTimer(triggers.KindDeathTimerRunsOut, 0)}
		for _, u := range s.Units.IterSliced() {
			if u != nil && u.Owner == 1 {
				s.Units.Destroy(u.Handle, units.DeathKilled)
				s.Units.FinalizeDeath(u.Handle, 0)
			}
		}
		s.LocalOwner = 9 // stale adapter state must not select the local slot.
		return s
	}

	campaign := newTriggerSession(mission.TypeCampaign)
	campaign.pollMissionTriggers(0)
	if !campaign.VictoryDone || campaign.DefeatDone {
		t.Fatalf("campaign simultaneous completion must resolve as victory: victory=%v defeat=%v", campaign.VictoryDone, campaign.DefeatDone)
	}
	// The poll resolves the local slot from the authoritative player table, and
	// advances no deadline of its own: it is reached from that slot's
	// settlement due, and WinLoseTime is persisted-only [08 R-TRIG-01 §6].
	if campaign.LocalOwner != 0 || campaign.Econ.Players[0].WinLoseTime != 0 {
		t.Fatalf("campaign poll owner/deadline wrong: owner=%d WinLoseTime=%d", campaign.LocalOwner, campaign.Econ.Players[0].WinLoseTime)
	}

	direct := newTriggerSession(mission.TypeSkirmish)
	direct.pollMissionTriggers(0)
	if direct.VictoryDone || direct.DefeatDone || direct.Econ.Players[0].WinLoseTime != 0 {
		t.Fatalf("kind 2 polled mission queues: victory=%v defeat=%v WinLoseTime=%d", direct.VictoryDone, direct.DefeatDone, direct.Econ.Players[0].WinLoseTime)
	}
}
