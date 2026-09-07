package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestPublishAfterEachSubTick verifies C6: 0..5 sub-ticks publishing after phase 12
// of each completed sub-tick. A five-tick burst therefore advances the buffer five
// times and retains the final two committed states.
func TestPublishAfterEachSubTick(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &frame.Buffer{},
		Units:    units.NewSliced(10, nil),
	}
	s.RegisterAll()
	s.State = StateBattle // P0-I10: Step ticks only in battle
	// Ensure initial snapshot is empty; Step with scaledNow that yields 3 ticks.
	// clock at nominal speed: delta 3 => 3 ticks.
	s.Clock.ScaledAnchor = 0
	s.Step(3)
	// After 3 ticks, clock GlobalTick should be 3, snapshot should reflect 3 publishes.
	if s.Clock.GlobalTick != 3 {
		t.Fatalf("GlobalTick = %d, want 3", s.Clock.GlobalTick)
	}
	cur := s.Snapshot.Current()
	if cur == nil {
		t.Fatalf("snapshot not published")
	}
	if cur.Tick != 3 {
		t.Fatalf("snapshot cur tick = %d, want 3", cur.Tick)
	}

	// Test 0 ticks case: no publish, buffer unchanged.
	s2 := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 10},
		Snapshot: &frame.Buffer{},
	}
	s2.RegisterAll()
	s2.State = StateBattle // P0-I10: Step ticks only in battle
	// ScaledNow equals anchor => 0 ticks
	s2.Step(10)
	cur2 := s2.Snapshot.Current()
	if cur2 != nil {
		// If 0 ticks, buffer should remain empty (no publish). But RegisterAll does not publish initial.
		// So ok should be false or ticks 0 => no publish. Our Step does 0 SubTicks, so no publish.
		t.Fatalf("0-tick Step should not publish: got %v", cur2)
	}
}

// TestPauseUnpauseBurstCap verifies C7 SP pause path: budget short-circuits,
// anchor stalls, unpause yields one capped burst ≤5 [01 §4.3].
func TestPauseUnpauseBurstCap(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0, Paused: true},
		Snapshot: &frame.Buffer{},
	}
	s.RegisterAll()
	s.State = StateBattle // P0-I10: Step ticks only in battle
	// While paused, scaledNow advances but clock should not.
	s.Step(3000)
	if s.Clock.GlobalTick != 0 {
		t.Fatalf("paused SP should run 0 ticks, got %d", s.Clock.GlobalTick)
	}
	if s.Clock.ScaledAnchor != 0 {
		t.Fatalf("SP pause must stall scaled-time anchor [01 §4.3] C7, got %d", s.Clock.ScaledAnchor)
	}
	// Unpause should yield one capped burst ≤5 even though wall clock jumped 3000.
	s.Clock.Paused = false
	// Snapshot helper to count publishes
	s.Step(3000)
	if s.Clock.GlobalTick > 5 {
		t.Fatalf("unpause burst must be capped ≤5 [01 §4.3] C7, got %d", s.Clock.GlobalTick)
	}
	if s.Clock.GlobalTick == 0 {
		t.Fatalf("unpause should yield burst >0")
	}
	// Second call with tiny delta should continue normally capped.
	prevTick := s.Clock.GlobalTick
	s.Step(3001)
	if s.Clock.GlobalTick != prevTick+1 {
		t.Fatalf("after burst, next delta 1 should give 1 tick, got %d -> %d", prevTick, s.Clock.GlobalTick)
	}
	// Verify burst never exceeds 5 even with huge delta
	s2 := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &frame.Buffer{},
	}
	s2.RegisterAll()
	s2.State = StateBattle // P0-I10
	s2.Step(100000)        // huge delta
	if s2.Clock.GlobalTick != 5 {
		t.Fatalf("huge delta must clamp to 5, got %d [01 §4.2] C1", s2.Clock.GlobalTick)
	}
}

// Real manager maintenance runs before settlement, for eligible slots only
// [05 "Authoritative settlement order"][06 §3.2].
func TestAICallbackInsideTickPlayer(t *testing.T) {
	econ := &economy.Service{}
	p := &econ.Players[0]
	p.Exists = true
	p.ControllerState = 1
	p.EndGameCountdown = -1
	p.UpdateTime = 10
	calls := 0
	mgr := &ai.Manager{Player: 0, WeaponMaintenance: func(uint8) {
		calls++
		if p.UpdateTime != 10 {
			t.Fatal("manager ran after deadline advance")
		}
		p.Mirror[economy.Metal].Production = 5
	}}
	s := &Session{Econ: econ, Units: units.NewSliced(10, nil), AI: [10]*ai.Manager{0: mgr}}
	s.tickPlayers(10)
	if calls != 1 || p.PassProduced[economy.Metal] != 5 || p.UpdateTime != 40 {
		t.Fatalf("phase ordering calls=%d deadline=%d produced=%v", calls, p.UpdateTime, p.PassProduced[economy.Metal])
	}
	p.IsObserver = true
	s.tickPlayers(40)
	if calls != 1 || p.UpdateTime != 40 {
		t.Fatal("observer ran player work")
	}
}

func TestCoordinatorIteratesPlayersAscending(t *testing.T) {
	econ := &economy.Service{}
	var order []uint8
	var managers [10]*ai.Manager
	for i := range managers {
		p := &econ.Players[i]
		p.Exists = true
		p.ControllerState = 1
		p.EndGameCountdown = -1
		p.UpdateTime = 100
		managers[i] = &ai.Manager{Player: uint8(i), WeaponMaintenance: func(player uint8) { order = append(order, player) }}
	}
	s := &Session{Econ: econ, Units: units.NewSliced(10, nil), AI: managers}
	s.tickPlayers(99) // future settlement deadlines still run the real manager work
	if len(order) != 10 {
		t.Fatalf("visits %v", order)
	}
	for i, p := range order {
		if int(p) != i {
			t.Fatalf("player order %v", order)
		}
	}
}
