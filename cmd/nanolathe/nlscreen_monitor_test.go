package main

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
)

func monitorCardFromPages(s *nlScreen) nlCard {
	for _, page := range s.pages() {
		for _, card := range page.cards {
			if card.key == "monitor" {
				return card
			}
		}
	}
	panic("monitor card missing")
}

func TestMonitorChoicesAfterMenuWarmup(t *testing.T) {
	s := newNLScreen(func() *gameShell { return nil })
	// The main menu builds the catalogue before opening settings enumerates displays.
	monitorCardFromPages(s)
	s.monitors = []ebitenapp.DisplayMonitor{
		{ID: "0:DP-3", Label: "External"},
		{ID: "0:eDP-1", Label: "Laptop"},
	}
	card := monitorCardFromPages(s)
	if !slices.Equal(card.steps, []string{"System primary", "External", "Laptop"}) {
		t.Fatalf("opening settings retained warmup choices: %v", card.steps)
	}
	card.set(&s.draft, 1)
	if s.draft.monitor != "0:DP-3" {
		t.Fatalf("external choice selected %q", s.draft.monitor)
	}
	card.set(&s.draft, 2)
	if s.draft.monitor != "0:eDP-1" {
		t.Fatalf("laptop choice selected %q", s.draft.monitor)
	}
}

func TestMonitorChoicesRefreshAfterDisconnect(t *testing.T) {
	s := newNLScreen(func() *gameShell { return nil })
	s.monitors = []ebitenapp.DisplayMonitor{{ID: "0:DP-3", Label: "External"}, {ID: "0:eDP-1", Label: "Laptop"}}
	s.draft.monitor = "0:DP-3"
	monitorCardFromPages(s)
	// Reopening settings refreshes the snapshot; an unplugged saved choice stays visible.
	s.monitors = []ebitenapp.DisplayMonitor{{ID: "0:eDP-1", Label: "Laptop"}}
	card := monitorCardFromPages(s)
	if !slices.Equal(card.steps, []string{"System primary", "Laptop", "Disconnected: DP-3"}) {
		t.Fatalf("disconnected choice lost or stale display retained: %v", card.steps)
	}
	if card.get(&s.draft) != 2 {
		t.Fatal("disconnect silently replaced the saved selection")
	}
	card.set(&s.draft, 1)
	if s.draft.monitor != "0:eDP-1" {
		t.Fatalf("remaining display selected %q", s.draft.monitor)
	}
}
