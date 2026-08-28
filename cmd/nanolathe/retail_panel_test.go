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
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMap)}
	shell.frontend.Panels.Replace(ui.NewPanel(window))
	items := []string{"a", "b", "c", "d", "e", "f"}
	shell.setListItems(name, items, 0)
	if !shell.frontend.Panels.Top().SetListTop(name, 3, 3) {
		t.Fatal("SetListTop did not find MAPNAMES")
	}

	shell.setListItems(name, append([]string(nil), items...), 0)
	_, selected, top, ok := shell.frontend.Panels.Top().ListValues(name)
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
	}}, frontend: ui.NewFrontend(modeMenuMain)}
	shell.openMenu(modeMenuMain)
	first := shell.frontend.Panels.Top()
	if first == nil {
		t.Fatal("main panel was not opened")
	}
	shell.openMenu(modeMenuMap)
	mapPanel := shell.frontend.Panels.Top()
	if mapPanel == first || shell.frontend.Panels.SaveUnder() != first || shell.activePanel() != mapPanel {
		t.Fatal("small panel transition did not retain save-under in PanelStack")
	}
	modal := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	shell.frontend.Panels.PushModal(modal)
	if shell.activePanel() != mapPanel || shell.frontend.Panels.Modal() != modal {
		t.Fatal("modal transition did not preserve the underlying active panel")
	}
	shell.frontend.Panels.CloseModal()
	shell.openMenu(modeMenuMain)
	if shell.frontend.Panels.Len() != 1 || shell.frontend.Panels.Top() == nil {
		t.Fatalf("full-screen replacement left stale stack entries: len=%d", shell.frontend.Panels.Len())
	}
}

func TestShowRetailMessageReportsMissingAuthoredText(t *testing.T) {
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{message: &retailPanelAssets{window: &gui.Window{
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
	if shell.frontend.Panels.Modal() != nil {
		t.Fatal("failed MSGBOX construction opened a blank modal")
	}
}
