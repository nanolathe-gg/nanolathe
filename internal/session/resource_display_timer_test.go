package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

func TestResourceDisplayDeadlinePublicationAndSaveOverlay(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 101}, Snapshot: frame.NewBuffer(), Econ: &economy.Service{}}
	for player := range 2 {
		p := &s.Econ.Players[player]
		p.Exists = true
		p.DisplayTimer = uint32(100 + player)
		p.UpdateTime, p.WinLoseTime = 200, 300
		p.Stock[economy.Energy], p.Stock[economy.Metal] = 80, 8
		p.PassProduced[economy.Energy] = 12.5
	}
	s.publishSnapshot(101)
	c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: s.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	c.BeginPresentationFrame()
	in := RetailSaveInputs{Summary: save.Summary{Gametype: 1}, Mapping: []byte{1}, DisplayTimers: c.ResourceDisplayTimers()}
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Players) != 2 || p.Players[0].DisplayTimer != 130 || p.Players[1].DisplayTimer != 101 {
		t.Fatalf("viewed/unviewed saved deadlines=%+v", p.Players)
	}
	for player := range 2 {
		live := s.Econ.Players[player]
		if live.DisplayTimer != uint32(100+player) || live.UpdateTime != 200 || live.WinLoseTime != 300 || live.Stock[economy.Energy] != 80 || live.Stock[economy.Metal] != 8 {
			t.Fatalf("save overlay changed live economy player %d", player)
		}
	}
	var loaded economy.Player
	p.Players[0].ApplyToEconomy(&loaded)
	if loaded.DisplayTimer != 130 {
		t.Fatal("saved display deadline did not restore")
	}
	in.DisplayTimers = nil
	p, err = ProjectRetailSession(s, in)
	if err != nil || p.Players[0].DisplayTimer != 100 {
		t.Fatalf("headless projection must retain unadvanced deadline: %+v, %v", p.Players, err)
	}
}
