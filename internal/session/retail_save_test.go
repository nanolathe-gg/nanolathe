package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/save"
)

func TestProjectRetailSessionMapsClockAndPlayers(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 77}, LocalOwner: 0, Econ: &economy.Service{}}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].Stock[economy.Energy] = 19.5
	s.Econ.Players[0].Stock[economy.Metal] = 8.25
	in := RetailSaveInputs{
		Summary:        save.Summary{Campaign: "c", Mission: "m", MapName: "map", Gametype: 1, Players: 1, IsBattle: true},
		Camera:         save.Camera{XPosition: 4, ZPosition: 5},
		PlayerFeatures: []byte{1},
		Mapping:        []byte{2},
	}
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		t.Fatalf("ProjectRetailSession: %v", err)
	}
	if p.Summary.GameTime != 77 || p.HumanPlayer != 0 || p.Scheduler[16] != 77 {
		t.Fatalf("session metadata not projected: summary=%+v human=%d scheduler=%v", p.Summary, p.HumanPlayer, p.Scheduler)
	}
	if len(p.Players) != 1 || p.Players[0].Energy != 19.5 || p.Players[0].Metal != 8.25 {
		t.Fatalf("player projection = %+v", p.Players)
	}
	if len(p.PlayerFeatures) != 1 || len(p.Mapping) != 1 {
		t.Fatal("explicit terrain state was not copied")
	}
}

func TestProjectRetailSessionContinuationDoesNotNeedClock(t *testing.T) {
	s := &Session{}
	p, err := ProjectRetailSession(s, RetailSaveInputs{Summary: save.Summary{Gametype: 1, BetweenMissions: 1}})
	if err != nil {
		t.Fatalf("continuation projection: %v", err)
	}
	data, err := p.RetailBytes()
	if err != nil {
		t.Fatalf("continuation RetailBytes: %v", err)
	}
	bank, err := save.OpenBytes(data, save.RetailTag)
	if err != nil {
		t.Fatalf("continuation OpenBytes: %v", err)
	}
	if len(bank.Accounts()) != 1 || bank.Accounts()[0].Name != save.SummaryAccount {
		t.Fatalf("continuation accounts = %v, want Summary only", bank.Accounts())
	}
}
