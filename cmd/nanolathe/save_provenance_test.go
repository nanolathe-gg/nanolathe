package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
)

// TestSessionSummaryCarriesProvenance locks part of P0-019/P1-009. The save
// writer used to emit save.Summary{MapName: mapName} and a hardcoded empty
// alliance row, so a saved file could not say what it was a save of: no player
// count, no side, no selected rules, and no alliances regardless of what the
// battle actually had [08 "Summary"][08 "Player records"].
func TestSessionSummaryCarriesProvenance(t *testing.T) {
	cfg := session.SkirmishConfig{MapName: "Ashap Plateau", NumPlayers: 3, Location: 2}
	cfg.Players[0] = session.SkirmishPlayer{Controller: session.SkirmishControllerHuman, Side: 1, AllyGroup: 1}
	cfg.Players[1] = session.SkirmishPlayer{Controller: session.SkirmishControllerComputer, Side: 0, AllyGroup: 1}
	cfg.Players[2] = session.SkirmishPlayer{Controller: session.SkirmishControllerComputer, Side: 0, AllyGroup: 2}
	cfg.CommanderDeath = 1
	cfg.Difficulty = 2
	sess := &session.Session{Skirmish: cfg, LocalOwner: 0}

	sum := sessionSummary(sess, "")
	if sum.MapName != "Ashap Plateau" {
		t.Errorf("map name lost: %q", sum.MapName)
	}
	if sum.Players != 3 {
		t.Errorf("player count: got %d want 3", sum.Players)
	}
	if sum.Side != 1 {
		t.Errorf("local side: got %d want 1", sum.Side)
	}
	if sum.CommanderDeath != 1 || sum.Location != 2 || sum.Difficulty != 2 {
		t.Errorf("selected rules lost: commanderDeath=%d location=%d difficulty=%d",
			sum.CommanderDeath, sum.Location, sum.Difficulty)
	}

	// Slot 1 shares the local player's ally group; slot 2 does not.
	self, row := sessionAlliances(sess)
	if self != 0 {
		t.Errorf("self slot: got %d want 0", self)
	}
	if row[1] != 1 {
		t.Errorf("allied slot 1 not recorded: row=%v", row)
	}
	if row[2] != 0 {
		t.Errorf("hostile slot 2 recorded as allied: row=%v", row)
	}
}
