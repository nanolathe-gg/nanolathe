//go:build retail

package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// addRetailComputers is the host adding, through its lobby rows, a Modern AI
// on team 1 and a Classic AI on Hard on team 2 after the room's humans.
func addRetailComputers(t *testing.T) func(shells []*gameShell) {
	return func(shells []*gameShell) {
		t.Helper()
		host := shells[0]
		first, second := strconv.Itoa(len(shells)), strconv.Itoa(len(shells)+1)
		for _, name := range []string{
			"Player" + first, "Allies" + first, // Modern AI, team 1
			"Player" + second, "Player" + second, "Allies" + second, "Allies" + second, // Classic AI, team 2
		} {
			host.activateGadget(name)
		}
		r := &host.online.room
		for r.settings.computers[1].Difficulty != 2 {
			host.activateGadget("Energy" + second)
		}
		got := r.settings.computers
		if len(got) != 2 || got[0].Kind != ai.ControllerModern || got[0].Team != 1 || got[1].Kind != ai.ControllerClassic || got[1].Team != 2 || r.notice != "" {
			t.Fatalf("the host's computers %+v: %q", got, r.notice)
		}
	}
}

// awaitRelayAgreement pumps every battle until the relay reports checksums
// compared across every playing seat through tick (§16.5.2).
func awaitRelayAgreement(t *testing.T, shells []*gameShell, tick uint32) {
	t.Helper()
	end := time.Now().Add(60 * time.Second)
	for {
		agreed := true
		for _, g := range shells {
			g.battle.pumpLocalMultiplayer(nil)
			mp := g.battle.multiplayer
			if mp == nil || mp.failure != nil {
				t.Fatalf("multiplayer stopped: %v", mp.failure)
			}
			reporter, ok := mp.net.Client.(onlineProgressReporter)
			if !ok {
				t.Fatal("the hosted connection reports no match progress")
			}
			progress, reported := reporter.Progress()
			agreed = agreed && reported && progress.Agreed >= tick
		}
		if agreed {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("checksums never agreed through tick %d", tick)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Two players on teams 1 and 2 and the host's two computers, a Modern AI on
// team 1 and a Classic AI on team 2, through a real local relay: both clients
// compose the same configuration and rehearsal (onlineRetailRoom), seat the
// computers after the humans with their own kind, team and difficulty, and
// play on with the relay agreeing on every checksum while the computers build
// (DESIGN_MULTIPLAYER §6.6, §16.6).
func TestOnlineLobbyComputersRetail(t *testing.T) {
	shells := onlineRetailRoom(t, 2, false, []uint8{1, 2}, addRetailComputers(t))
	for i, g := range shells {
		sk := g.battle.sess.Skirmish
		modern, classic := sk.Players[2], sk.Players[3]
		if sk.NumPlayers != 4 || modern.Controller == session.SkirmishDefaultController || modern.AI != ai.ControllerModern || modern.AllyGroup != 0 ||
			classic.Controller == session.SkirmishDefaultController || classic.AI != ai.ControllerClassic || classic.AllyGroup != 1 || classic.Difficulty != 2 {
			t.Fatalf("player %d composed %+v", i+1, sk.Players[:sk.NumPlayers])
		}
	}
	const ticks = 600
	pumpOnlineRetail(t, shells, ticks, nil)
	awaitRelayAgreement(t, shells, ticks-60)
	// A computer's first build changes the checksums the relay compared, so
	// one that built proves its AI ran alike on both clients. The Modern AI
	// builds within a few hundred ticks; the Classic AI's first build varies
	// with the room's seeds, so it is logged, not required.
	var units [4]int
	for _, u := range shells[0].battle.sess.Units.IterSliced() {
		if u.Alive && int(u.Owner) < len(units) {
			units[u.Owner]++
		}
	}
	t.Logf("units by player at tick %d: %v", shells[0].battle.sess.Clock.GlobalTick, units)
	if units[2]+units[3] <= 2 {
		t.Fatalf("the computers built nothing by tick %d: %v", ticks, units)
	}
}

// Two Survival survivors and the host's one computer survivor play together
// through a real local relay (DESIGN_SURVIVAL, DESIGN_MULTIPLAYER §16.6).
func TestOnlineLobbySurvivalComputerRetail(t *testing.T) {
	shells := onlineRetailRoom(t, 2, true, []uint8{0, 0}, func(shells []*gameShell) {
		host := shells[0]
		host.activateGadget("Player2")
		if r := &host.online.room; len(r.settings.computers) != 1 || host.online.room.panel.ActiveOf("Player3") {
			t.Fatalf("Survival computers %+v", r.settings.computers)
		}
	})
	for i, g := range shells {
		sk := g.battle.sess.Skirmish
		if sk.NumPlayers != 4 || sk.Players[2].AI != ai.ControllerModern || sk.Players[2].AllyGroup != sk.Players[0].AllyGroup {
			t.Fatalf("player %d composed %+v", i+1, sk.Players[:sk.NumPlayers])
		}
	}
	pumpOnlineRetail(t, shells, 300, nil)
	awaitRelayAgreement(t, shells, 240)
}
