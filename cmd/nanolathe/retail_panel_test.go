package main

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
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

func TestShowRetailMessageReportsMissingWindow(t *testing.T) {
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{}}
	if err := shell.showRetailMessage("diagnostic"); err == nil {
		t.Fatal("missing MSGBOX window was silently accepted")
	} else {
		for _, want := range []string{"logical path guis/msgbox.gui", "providers searched [none]", "expected an authored MSGBOX window"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("diagnostic %q missing %q", err, want)
			}
		}
	}
	if shell.frontend.Panels.Modal() != nil {
		t.Fatal("failed MSGBOX construction opened a blank modal")
	}
}

// fixedWidthFont is a synthetic FNT: every printable byte advances one pixel,
// so a measured width is the character count. Fixtures are authored here, never
// copied from retail.
func fixedWidthFont(height uint8) *formats.FNT {
	fnt := &formats.FNT{Height: height}
	for i := 0x20; i < 0x7f; i++ {
		fnt.Glyphs[i] = &formats.FNTGlyph{Width: 1, Height: height}
	}
	return fnt
}

// TestShowRetailMessageBuildsRuntimeLabels locks the message box's opener
// contract [07 R-FE-01 §9]: MSGBOX.GUI authors no text control, so the box
// appends one centred TEXT label per wrapped line, resizes and re-centres the
// window, and moves OK to the bottom-right corner. Before this, the absent
// authored label made showRetailMessage fail and the diagnostic never reached
// the player at all.
func TestShowRetailMessageBuildsRuntimeLabels(t *testing.T) {
	// The stock two-gadget file: the HEADER panel and the OK button.
	authored := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "HEADER", Rect: gui.Rect{X: 116, Y: 82, W: 372, H: 272}},
		{Kind: gui.KindButton, Name: "OK", Text: "OK", Active: 1, Rect: gui.Rect{X: 264, Y: 208, W: 80, H: 42}},
	}}
	shell := &gameShell{
		frontend: ui.NewFrontend(modeMenuMain),
		font:     fixedWidthFont(10),
		assets:   &menuAssets{message: &retailPanelAssets{window: authored}},
	}
	const message = "alpha bravo\ncharlie delta\necho"
	want := []string{"alpha bravo", "charlie delta", "echo"}
	if err := shell.showRetailMessage(message); err != nil {
		t.Fatalf("showRetailMessage: %v", err)
	}
	modal := shell.frontend.Panels.Modal()
	if modal == nil || modal.Window == nil {
		t.Fatal("no modal was opened")
	}
	if len(authored.Gadgets) != 2 {
		t.Fatalf("the authored window was mutated: %d gadgets", len(authored.Gadgets))
	}
	var labels []gui.Gadget
	for i, gad := range modal.Window.Gadgets {
		if i != 0 && gad.Kind == gui.KindLabel && gad.Name == "TEXT" {
			labels = append(labels, gad)
		}
	}
	if len(labels) != len(want) {
		t.Fatalf("message produced %d labels, want %d", len(labels), len(want))
	}
	for i, l := range labels {
		if got := strings.TrimSuffix(l.Text, "\r"); got != want[i] {
			t.Fatalf("label %d text=%q, want %q", i, got, want[i])
		}
	}
	// First label at y = 20, next advancing by fontHeight + 5, each centred
	// (attribute 2) and widened to the panel.
	step := int32(10 + 5)
	for i, l := range labels {
		if want := 20 + int32(i)*step; l.Rect.Y != want {
			t.Fatalf("label %d y=%d, want %d", i, l.Rect.Y, want)
		}
		if l.Rect.X != 0 || l.Rect.H != 15 || l.Attribs != 2 {
			t.Fatalf("label %d geometry=%+v attribs=%d", i, l.Rect, l.Attribs)
		}
		if l.Rect.W != modal.Window.Rect.W {
			t.Fatalf("label %d width=%d, want the panel's %d", i, l.Rect.W, modal.Window.Rect.W)
		}
	}
	// Panel height is lines*25 + gadget 1's height + 40; the window is centred.
	if want := int32(len(labels)*25) + 42 + 40; modal.Window.Rect.H != want {
		t.Fatalf("panel height=%d, want %d", modal.Window.Rect.H, want)
	}
	if want := (int32(retailScreenW) - modal.Window.Rect.W) / 2; modal.Window.Rect.X != want {
		t.Fatalf("panel x=%d, want %d", modal.Window.Rect.X, want)
	}
	if want := (int32(retailScreenH) - modal.Window.Rect.H) / 2; modal.Window.Rect.Y != want {
		t.Fatalf("panel y=%d, want %d", modal.Window.Rect.Y, want)
	}
	ok := modal.Window.Gadgets[1]
	if ok.Rect.X != modal.Window.Rect.W-ok.Rect.W-15 || ok.Rect.Y != modal.Window.Rect.H-ok.Rect.H-15 {
		t.Fatalf("OK at %+v for panel %+v", ok.Rect, modal.Window.Rect)
	}
}

// TestLoadScreenEmptyListMessageBoxMatchesRetailCapture locks the "no saved
// games" message box's own geometry against a retail screen-capture
// measurement, not just the synthetic-font contract above. MSGBOX.GUI's `OK`
// button (authored 80x42) has no name match in any GAF, so the generic
// window builder falls it back to BUTTONS0's best-fit frame — 80x20, not the
// authored 80x42 — before the MSGBOX opener's own height and OK-position
// math runs [07 R-WGT-01 §3][07 R-FE-01 §9]. Before this, Nanolathe built the
// box from the unresolved 42, leaving it visibly taller than retail's with
// OK stranded above the 15px bottom margin instead of flush with it — the
// "wrong background, misaligned OK" a retail capture of this exact dialog
// (OTA_Menu_Skirmish, ~00:01:12) shows as a gap of bare BackTile plate below
// the button.
func TestLoadScreenEmptyListMessageBoxMatchesRetailCapture(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
		t.Fatalf("open load screen: %v", err)
	}
	modal := shell.frontend.Panels.Modal()
	if modal == nil || modal.Window == nil || len(modal.Window.Gadgets) < 2 {
		t.Fatal("no modal (or no OK gadget) was opened for the empty save list")
	}
	ok := modal.Window.Gadgets[1]
	if !strings.EqualFold(ok.Name, "OK") {
		t.Fatalf("gadget 1 is %q, want OK", ok.Name)
	}
	// BUTTONS0's best fit for the authored 80x42 OK rectangle is the 80x20
	// frame family (asset census via the retail install), not the authored
	// height.
	if ok.Rect.W != 80 || ok.Rect.H != 20 {
		t.Fatalf("OK resolved size = %dx%d, want 80x20 (BUTTONS0's best fit, not the authored 80x42)", ok.Rect.W, ok.Rect.H)
	}
	// OK sits flush 15px from the resolved panel's right and bottom edges
	// [07 R-FE-01 §9], not 15px short of a box sized for the unresolved 42.
	if want := modal.Window.Rect.W - ok.Rect.W - 15; ok.Rect.X != want {
		t.Fatalf("OK x=%d, want %d (15px from the right edge)", ok.Rect.X, want)
	}
	if want := modal.Window.Rect.H - ok.Rect.H - 15; ok.Rect.Y != want {
		t.Fatalf("OK y=%d, want %d (15px from the bottom edge)", ok.Rect.Y, want)
	}
	// Panel height is lines*25 + 40 + the *resolved* gadget-1 height (20),
	// not the authored 42 [07 R-FE-01 §9].
	if want := int32(1*25+40) + ok.Rect.H; modal.Window.Rect.H != want {
		t.Fatalf("panel height=%d, want %d (built from the resolved OK height)", modal.Window.Rect.H, want)
	}
}

// TestRetailMessageWrapBreaksAtSeparators locks the message box's wrapper
// [07 R-FE-01 §9]: it measures a line only when the next character is a space,
// a newline or a hyphen, breaks when the line has reached the wrap width, and
// rewinds to that separator, replacing it with CR/LF. Each broken line
// therefore keeps a trailing CR, which is retail's own buffer content.
func TestRetailMessageWrapBreaksAtSeparators(t *testing.T) {
	measure := func(s string) int { return len(s) }
	lines := retailMessageWrap("alpha bravo charlie delta", measure, 12)
	if len(lines) < 2 {
		t.Fatalf("no break at width 12: %q", lines)
	}
	for i, line := range lines[:len(lines)-1] {
		if !strings.HasSuffix(line, "\r") {
			t.Fatalf("broken line %d %q has no trailing CR", i, line)
		}
		if measure(strings.TrimSuffix(line, "\r")) >= 12 {
			t.Fatalf("line %d %q is not under the wrap width", i, line)
		}
	}
	joined := ""
	for _, line := range lines {
		joined += strings.TrimSuffix(line, "\r") + " "
	}
	if strings.TrimSpace(joined) != "alpha bravo charlie delta" {
		t.Fatalf("wrapped text lost content: %q", joined)
	}
	// An explicit newline starts a new label without a CR.
	if got := retailMessageWrap("one\ntwo", measure, 500); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("explicit newline split=%q", got)
	}
}
