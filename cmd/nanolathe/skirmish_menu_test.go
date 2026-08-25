package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
)

func TestSkirmishMenuDefaultsAndOpponentCount(t *testing.T) {
	cfg := newSkirmishMenuConfig("small")
	if cfg.MapName != "small" || cfg.NumPlayers != session.SkirmishDefaultPlayers {
		t.Fatalf("menu defaults map/players = %q/%d", cfg.MapName, cfg.NumPlayers)
	}
	if cfg.Difficulty != session.SkirmishDefaultDifficulty ||
		cfg.Location != session.SkirmishDefaultLocation ||
		cfg.CommanderDeath != session.SkirmishDefaultCommanderDeath ||
		cfg.Mapping != session.SkirmishDefaultMapping ||
		cfg.LineOfSight != session.SkirmishDefaultLineOfSight ||
		cfg.LOSType != session.SkirmishDefaultLOSType {
		t.Fatalf("menu scalar defaults = %+v", cfg)
	}
	if cfg.Players[0].Controller != session.SkirmishDefaultController {
		t.Fatalf("slot 0 controller = %d", cfg.Players[0].Controller)
	}
	for i := 1; i < cfg.NumPlayers; i++ {
		if cfg.Players[i].Controller == session.SkirmishDefaultController {
			t.Fatalf("active opponent slot %d is not computer-controlled", i)
		}
	}

	g := &gameShell{setup: cfg, selectedSlot: 3}
	g.setOpponentCount(1)
	if g.setup.NumPlayers != 2 || g.selectedSlot != 1 {
		t.Fatalf("one-opponent setup players/selection = %d/%d", g.setup.NumPlayers, g.selectedSlot)
	}
	if g.setup.Players[0].Controller != 0 || g.setup.Players[1].Controller == 0 || g.setup.Players[2].Controller != 0 {
		t.Fatalf("one-opponent controllers = %d/%d/%d", g.setup.Players[0].Controller, g.setup.Players[1].Controller, g.setup.Players[2].Controller)
	}
	g.setOpponentCount(9)
	if g.setup.NumPlayers != session.SkirmishMaxPlayers {
		t.Fatalf("maximum-opponent setup players = %d", g.setup.NumPlayers)
	}
}

func TestSkirmishMenuRetailResourceButtons(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"decrement floor", decreaseResource(200), 200},
		{"decrement", decreaseResource(1000), 500},
		{"increment from floor quirk", increaseResource(200), 500},
		{"increment", increaseResource(500), 1000},
		{"increment cap", increaseResource(9500), 10000},
		{"increment at cap", increaseResource(10000), 10000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestSkirmishMenuRetailLineOfSightCycle(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("small")}
	if got := lineOfSightName(g.setup.LineOfSight, g.setup.LOSType); got != "ELEVATION" {
		t.Fatalf("default LOS label = %q", got)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 1 || g.setup.LOSType != 0 || lineOfSightName(g.setup.LineOfSight, g.setup.LOSType) != "FLAT" {
		t.Fatalf("second LOS state = %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 0 || g.setup.LOSType != 1 || lineOfSightName(g.setup.LineOfSight, g.setup.LOSType) != "MAPPED" {
		t.Fatalf("third LOS state = %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 1 || g.setup.LOSType != 1 {
		t.Fatalf("LOS cycle did not wrap: %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
}
