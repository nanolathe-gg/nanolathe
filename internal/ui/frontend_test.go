package ui

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

func TestFrontendOpenOwnsModeAndSaveUnder(t *testing.T) {
	base := NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	small := NewPanel(&gui.Window{Rect: gui.Rect{X: 10, Y: 10, W: 20, H: 20}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	f := NewFrontend(ModeMain)
	f.Open(ModeMain, base, false)
	f.Open(ModeMap, small, true)
	if f.Mode != ModeMap || f.ActivePanel() != small || f.Panels.SaveUnder() != base {
		t.Fatalf("frontend transition did not retain authored save-under state")
	}
	f.Open(ModeMain, base, false)
	if f.Panels.Len() != 1 || f.Panels.Top() != base {
		t.Fatalf("full-screen transition left stale panels: len=%d", f.Panels.Len())
	}
}

func TestFrontendNavigateReportsOnlyFixedEdges(t *testing.T) {
	f := NewFrontend(ModeMain)
	if got, ok := f.Navigate("SINGLE"); !ok || got != ModeSingle {
		t.Fatalf("main SINGLE edge = %v,%t", got, ok)
	}
	f.Mode = ModeMission
	if got, ok := f.Navigate("PrevMenu"); !ok || got != ModeSingle {
		t.Fatalf("mission PrevMenu edge = %v,%t", got, ok)
	}
	f.Mode = ModeMap
	if _, ok := f.Navigate("PrevMenu"); ok {
		t.Fatal("map return depends on composition state and must not be guessed by UI")
	}
}

func TestFrontendNavigateUsesExactTerminatedFiredName(t *testing.T) {
	f := NewFrontend(ModeMain)
	for _, name := range []string{"single", "SINGLE ", " SINGLE", "SINGLE          "} {
		if _, ok := f.Navigate(name); ok {
			t.Fatalf("Navigate(%q) accepted a different fired name", name)
		}
	}
	if got, ok := f.Navigate("SINGLE\x00suffix"); !ok || got != ModeSingle {
		t.Fatal("terminated SINGLE callback did not navigate")
	}
}

func TestInstallSkirmishDynamicGadgetsUsesAuthoredRows(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}
	InstallSkirmishDynamicGadgets(w, []SkirmishSlot{{Side: 2, Color: 4}, {Side: 1, Color: 7}})
	if len(w.Gadgets) != 13 {
		t.Fatalf("dynamic gadget count=%d, want base plus two six-control rows", len(w.Gadgets))
	}
	if w.Gadgets[1].Name != "Player0" || w.Gadgets[2].Name != "Side0" || w.Gadgets[2].Stages != 2 {
		t.Fatalf("row 0 controls were not installed in authored order")
	}
	if w.Gadgets[3].Name != "Color0" || w.Gadgets[3].Status != 4 || w.Gadgets[9].Status != 7 {
		t.Fatalf("row color status not carried into dynamic surface")
	}
	InstallSkirmishDynamicGadgets(w, []SkirmishSlot{{Side: 9, Color: 3}})
	if len(w.Gadgets) != 7 {
		t.Fatalf("reinstall retained stale dynamic controls: count=%d", len(w.Gadgets))
	}
}

func TestResultActionUsesAuthoredStartOnly(t *testing.T) {
	if ResultActionForControl("Start") != ResultActionContinue {
		t.Fatal("authored Start did not emit continue")
	}
	if ResultActionForControl("MainMenu") != ResultActionMainMenu {
		t.Fatal("authored MainMenu did not emit the main-menu route")
	}
	if ResultActionForControl("Continue") != ResultActionNone {
		t.Fatal("unestablished result alias became active")
	}
	if ResultActionForControl("START") != ResultActionNone {
		t.Fatal("case-distinct result callback became active")
	}
	if ResultActionForControl("Start\x00tail") != ResultActionContinue {
		t.Fatal("terminated result callback did not use Start")
	}
}
