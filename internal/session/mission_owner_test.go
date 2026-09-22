package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// Admission precedes allocation and aborts entry; the diagnostic names the
// normalized, zero-based player [08 R-ENTRY-01 §6][08 R-TRIG-01 §9].
func TestMissionPlacementRejectsIneligiblePlayerBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		player int32
		edit   func(*Session)
		want   string
	}{
		{"negative", -1, nil, "Player number -2 invalid for unit armcom"},
		{"out of range", 11, nil, "Player number 10 invalid for unit armcom"},
		{"inactive", 3, nil, "Player number 2 invalid for unit armcom"},
		{"closed controller", 1, func(s *Session) { s.Econ.Players[0].ControllerState = 0 }, "Player number 0 invalid for unit armcom"},
		{"other controller", 1, func(s *Session) { s.Econ.Players[0].ControllerState = 4 }, "Player number 0 invalid for unit armcom"},
		{"terminal side", 1, func(s *Session) { s.Econ.Players[0].Side = 10 }, "Player number 0 invalid for unit armcom"},
		{"missing players", 1, func(s *Session) { s.Econ = nil }, "Player number 0 invalid for unit armcom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := strictNewSessionWithUnits(t, 0, 7, 11)
			// The fixture has active human/computer rows 0/1 and no row 2.
			if tc.edit != nil {
				tc.edit(s)
			}
			beforeSim, beforeCRT := *s.SimRNG(), *s.CrtRNG()
			m := &mission.Mission{Units: []mission.UnitPlacement{
				{UnitName: "armcom", Player: tc.player, HealthPercentage: 100},
				{UnitName: "armcom", Player: 2, HealthPercentage: 100},
			}}
			if err := reconstructUnits(s, m); err == nil || err.Error() != tc.want {
				t.Fatalf("reconstructUnits error = %v, want %q", err, tc.want)
			}
			if got := len(s.Units.Iter()); got != 0 {
				t.Fatalf("created %d units despite fatal player admission", got)
			}
			if *s.SimRNG() != beforeSim || *s.CrtRNG() != beforeCRT {
				t.Fatal("rejected placement advanced a random stream")
			}
		})
	}
}

func TestMissionPlacementAcceptsParticipatingPlayers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		player     int32
		owner      uint8
		controller uint8
	}{
		{"zero normalizes to human", 0, 0, 1},
		{"human", 1, 0, 1},
		{"computer", 2, 1, 2},
		{"remote", 3, 2, 3},
		{"last player", 10, 9, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := strictNewSessionWithUnits(t, 0, 7, 11)
			s.Econ.Players[tc.owner] = economy.Player{Exists: true, ControllerState: tc.controller}
			m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "armcom", Player: tc.player, HealthPercentage: 100}}}
			if err := reconstructUnits(s, m); err != nil {
				t.Fatalf("reconstructUnits: %v", err)
			}
			created := s.Units.Iter()
			if len(created) != 1 || created[0].Owner != tc.owner {
				t.Fatalf("created units = %+v, want one owned by %d", created, tc.owner)
			}
		})
	}
}

// Definition misses precede player admission; allocator refusals remain
// nonfatal and retain each later record's placement identity [08 R-ENTRY-01 §6].
func TestMissionPlacementSkipsMissingDefinitionsAndAllocatorRefusals(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	def := s.Catalog.Units["armcom"]
	def.LimitEnabled, def.Limit = true, 1
	def.BuildAngle = 4096
	before := s.SimRNG().Draws()
	m := &mission.Mission{Units: []mission.UnitPlacement{
		{UnitName: "missing", Player: 11},
		{UnitName: "armcom", Player: 1, Ident: "first", HealthPercentage: 100},
		{UnitName: "armcom", Player: 1, Ident: "refused", HealthPercentage: 100},
		{UnitName: "armcom", Player: 2, Ident: "last", HealthPercentage: 100},
	}}
	if err := reconstructUnits(s, m); err != nil {
		t.Fatalf("reconstructUnits: %v", err)
	}
	created := s.Units.Iter()
	if len(created) != 2 || created[0].PlacementIdx != 1 || created[0].PlacementIdent != "first" || created[1].PlacementIdx != 3 || created[1].PlacementIdent != "last" {
		t.Fatalf("created units = %+v, want placements 1 and 3", created)
	}
	if got := s.SimRNG().Draws() - before; got != 4 {
		t.Fatalf("allocation draws = %d, want two per successful placement", got)
	}
}
