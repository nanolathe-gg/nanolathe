package main

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func TestSetListItemsPreservesManualScrollOnIdenticalRefresh(t *testing.T) {
	const name = "MAPNAMES"
	window := &gui.Window{Gadgets: []gui.Gadget{{
		Kind:   gui.KindListBox,
		Name:   name,
		Active: 1,
		Rect:   gui.Rect{W: 160, H: 40},
	}}}
	shell := &gameShell{}
	shell.panels.Replace(ui.NewPanel(window))
	items := []string{"a", "b", "c", "d", "e", "f"}
	shell.setListItems(name, items, 0)
	if !shell.panels.Top().SetListTop(name, 3, 3) {
		t.Fatal("SetListTop did not find MAPNAMES")
	}

	shell.setListItems(name, append([]string(nil), items...), 0)
	_, selected, top, ok := shell.panels.Top().ListValues(name)
	if !ok || selected != 0 || top != 3 {
		t.Fatalf("identical refresh changed list state: selected=%d top=%d ok=%t", selected, top, ok)
	}
}

func TestOpenMenuStackIsPanelSourceOfTruth(t *testing.T) {
	mainWindow := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}
	mapWindow := &gui.Window{Rect: gui.Rect{X: 84, Y: 12, W: 494, H: 420}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}
	shell := &gameShell{assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{
		modeMenuMain: {window: mainWindow},
		modeMenuMap:  {window: mapWindow},
	}}}
	shell.openMenu(modeMenuMain)
	first := shell.panels.Top()
	if first == nil {
		t.Fatal("main panel was not opened")
	}
	shell.openMenu(modeMenuMap)
	mapPanel := shell.panels.Top()
	if mapPanel == first || shell.panels.SaveUnder() != first || shell.activePanel() != mapPanel {
		t.Fatal("small panel transition did not retain save-under in PanelStack")
	}
	modal := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	shell.panels.PushModal(modal)
	if shell.activePanel() != mapPanel || shell.panels.Modal() != modal {
		t.Fatal("modal transition did not preserve the underlying active panel")
	}
	shell.panels.CloseModal()
	shell.openMenu(modeMenuMain)
	if shell.panels.Len() != 1 || shell.panels.Top() == nil {
		t.Fatalf("full-screen replacement left stale stack entries: len=%d", shell.panels.Len())
	}
}

func TestShowRetailMessageReportsMissingAuthoredText(t *testing.T) {
	shell := &gameShell{assets: &menuAssets{message: &retailPanelAssets{window: &gui.Window{
		Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}},
	}}}}
	if err := shell.showRetailMessage("diagnostic"); err == nil {
		t.Fatal("missing authored message text control was silently accepted")
	} else {
		for _, want := range []string{"logical path guis/msgbox.gui", "providers searched [none]", "expected an authored MSGBOX text or label control with active state"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("diagnostic %q missing %q", err, want)
			}
		}
	}
	if shell.panels.Modal() != nil {
		t.Fatal("failed MSGBOX construction opened a blank modal")
	}
}
