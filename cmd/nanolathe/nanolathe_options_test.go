package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestPresentationStartupOverrides(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want settings.Presentation
	}{
		{nil, settings.Presentation{Renderer: "classic", FPS: 120}},
		{[]string{"--renderer=modern"}, settings.Presentation{Renderer: "modern", FPS: 120}},
		{[]string{"--fps=0"}, settings.Presentation{Renderer: "classic", FPS: 0}},
	} {
		opts, err := parseFlags(tc.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if got := startupPresentation(opts, settings.Presentation{Renderer: "classic", FPS: 120}); got != tc.want {
			t.Fatalf("args %v: got %+v, want %+v", tc.args, got, tc.want)
		}
	}
	opts, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Renderer != "modern" || opts.FPS != 60 {
		t.Fatalf("defaults %+v", opts)
	}
}

// These are Nanolathe's options transactions, not retail behavioral claims.
func TestNanolatheOptionsPreviewCancelAndPersistence(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g.attachSettings()
	t.Cleanup(g.closeRetailOptionsScreen)
	g.openMenu(modeMenuSingle)
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("NANOLATHE")
	if optionsState.page != "nanolathe" {
		t.Fatal("new category did not open")
	}
	for _, name := range []string{"NANOLATHE", "NRENDER", "NFPS"} {
		gad := optionsPanel.Window.Gadgets[optionsPanel.Index(name)]
		if gad.ButtonArt == nil {
			t.Fatalf("%s has no game-data button art", name)
		}
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "nanolathe-options.png"))
	}
	host := g.windowOptions()
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	if mode, fps := host.PresentationSettings(); mode != ebitenapp.RendererClassic || fps != 120 {
		t.Fatalf("preview %v %d", mode, fps)
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation != settings.DefaultPresentation() {
		t.Fatalf("cancel %+v", g.presentation)
	}
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("NANOLATHE")
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Presentation != g.presentation || saved.Presentation.FPS != 120 {
		t.Fatalf("saved %+v", saved.Presentation)
	}
	next := &gameShell{}
	next.applySettings(saved)
	if next.presentation != g.presentation {
		t.Fatal("restart lost selection")
	}
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("NANOLATHE")
	g.activateRetailOptionsGadget("RESTORE")
	if g.presentation != settings.DefaultPresentation() {
		t.Fatal("defaults not restored")
	}
	g.activateRetailOptionsGadget("UNDO")
	if g.presentation != saved.Presentation {
		t.Fatal("undo lost entry selection")
	}
	// F10 is independently persisted and must not save this pending cap.
	g.activateRetailOptionsGadget("NFPS")
	host.RendererChanged(ebitenapp.RendererModern)
	after, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.Presentation.Renderer != "modern" || after.Presentation.FPS != 120 {
		t.Fatalf("F10 saved pending cap: %+v", after.Presentation)
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation != after.Presentation {
		t.Fatal("cancel undid independently saved renderer")
	}
}

func TestBattleNanolatheOptionsPointerAndLayout(t *testing.T) {
	g, b, cl := retailBattleOptionsShell(t)
	t.Cleanup(g.closeRetailOptionsScreen)
	b.openBattleMenu()
	b.activateBattleMenuButton("PREFS", cl)
	click := func(name string) {
		t.Helper()
		r := optionsPanel.Window.PlacedRect(optionsPanel.Index(name))
		mouse := cl.Input().Mouse
		mouse.ResetEdges()
		mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
		mouse.SetButton(input.MouseButtonLeft, true)
		b.handleBattleMenuInput(cl.Input(), cl)
		mouse.ResetEdges()
		mouse.SetButton(input.MouseButtonLeft, false)
		b.handleBattleMenuInput(cl.Input(), cl)
	}
	click("NANOLATHE")
	if optionsState.page != "nanolathe" {
		t.Fatal("pointer did not open category")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "battle-nanolathe-options.png"))
	}
	click("NRENDER")
	if g.presentation.Renderer != "classic" {
		t.Fatalf("pointer cycled renderer incorrectly: %+v", g.presentation)
	}
	click("NFPS")
	if g.presentation.FPS != 120 {
		t.Fatalf("pointer cycled cap incorrectly: %+v", g.presentation)
	}
	click("NFPS")
	if g.presentation.FPS != 30 {
		t.Fatalf("pointer did not wrap cap: %+v", g.presentation)
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation != settings.DefaultPresentation() {
		t.Fatalf("battle cancel %+v", g.presentation)
	}
}

func TestSavedRendererControlsZoomValidation(t *testing.T) {
	opts, err := parseFlags([]string{"--zoom=0.5"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	g := &gameShell{opts: opts}
	prefs := settings.Defaults()
	prefs.Presentation.Renderer = "classic"
	g.applySettings(prefs)
	if validatePresentationZoom(g.opts) == nil {
		t.Fatal("saved classic accepted free zoom")
	}
	opts.RendererSet = true
	g.opts = opts
	g.applySettings(prefs)
	if err := validatePresentationZoom(g.opts); err != nil {
		t.Fatalf("explicit modern did not override saved classic: %v", err)
	}
}
