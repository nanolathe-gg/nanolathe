package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A Modern controller sees through its own coverage in both visibility
// modes, while the retail planner's predicate keeps retail's Permanent LOS
// read of the local viewing slot's mapping bit [03 §3.2] C8 step 4.
func TestComputerPlayerSightReadsItsOwnCoverage(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	const human, computer = visibility.PlayerID(0), visibility.PlayerID(1)
	// x=32 and z-y/2=40 map pixels both project to grid coordinate one; a
	// definition-less unit's hull is the single point at its position.
	const idx = 65
	enemy := &units.Unit{Owner: 2, X: numeric.FixedFromInt(32), Y: numeric.FixedFromInt(16), Z: numeric.FixedFromInt(48)}
	s := &Session{Econ: &economy.Service{}}
	for p := range 3 {
		s.Econ.Players[p].Exists = true
	}
	check := func(step string, wantOwn, wantRetail bool) {
		t.Helper()
		if got := s.computerPlayerSeesOwn(uint8(computer), enemy); got != wantOwn {
			t.Fatalf("%s: controller sight %t, want %t", step, got, wantOwn)
		}
		if got := s.computerPlayerSees(uint8(computer), enemy); got != wantRetail {
			t.Fatalf("%s: retail planner sight %t, want %t", step, got, wantRetail)
		}
	}

	permanent := visibility.New(terrain, visibility.ModeHistoryEnabled)
	permanent.SetLocal(human)
	s.Vis = permanent
	permanent.WordMask()[idx] = 1 << computer
	check("Permanent LOS, explored by the computer", true, false)
	permanent.WordMask()[idx] = 1 << human
	check("Permanent LOS, explored by the human", false, true)
	permanent.WordMask()[idx] = 1<<human | 1<<computer
	check("Permanent LOS, explored by both", true, true)

	current := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	current.SetLocal(human)
	s.Vis = current
	current.ByteGrid(computer)[idx] = 1
	check("line of sight, seen by the computer", true, true)
	current.ByteGrid(computer)[idx] = 0
	current.ByteGrid(human)[idx] = 1
	check("line of sight, seen by the human", false, false)

	current.ByteGrid(computer)[idx] = 1
	s.Econ.Players[computer].IsObserver = true
	check("observer slot", false, false)
}
