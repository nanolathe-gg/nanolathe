package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
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
	// The page's glow row reaches the live client through the shared visual
	// pointer, exactly as the VISUALS rows do (DESIGN_GPU_RENDERER §19.4).
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g.attachSettings()
	t.Cleanup(g.closeRetailOptionsScreen)
	g.openMenu(modeMenuSingle)
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("NANOLATHE")
	if optionsState.page != "nanolathe" {
		t.Fatal("new category did not open")
	}
	for _, name := range []string{"NANOLATHE", "NGAMEPLAY", "NRENDER", "NFPS", "NGLOW", "NWATER", "NLIGHTS", "NFINISH", "NHEAT", "NMARKS", "NSIDEBAR"} {
		gad := optionsPanel.Window.Gadgets[optionsPanel.Index(name)]
		if gad.ButtonArt == nil {
			t.Fatalf("%s has no game-data button art", name)
		}
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "nanolathe-options.png"))
	}
	host := g.windowOptions()
	g.activateRetailOptionsGadget("NGAMEPLAY")
	if g.gameplay != gameplay.Strict31 {
		t.Fatal("gameplay did not switch to strict")
	}
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	if mode, fps := host.PresentationSettings(); mode != ebitenapp.RendererClassic || fps != 120 {
		t.Fatalf("preview %v %d", mode, fps)
	}
	// Every Enhanced switch previews through the same poll the host makes, and
	// glow previews straight onto the live client (DESIGN_GPU_RENDERER §30).
	for _, name := range effectGadgets {
		g.activateRetailOptionsGadget(name)
	}
	g.activateRetailOptionsGadget("NSIDEBAR")
	if (&battleSession{shell: g}).expandedSidebarEnabled() {
		t.Fatal("sidebar preference did not preview immediately")
	}
	if got := host.Effects(); got != (drawlist.Effects{}) {
		t.Fatalf("effect preview %+v", got)
	}
	if g.display.Glow != 0 || cl.Glow() {
		t.Fatalf("glow preview: stored %d live %v", g.display.Glow, cl.Glow())
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.gameplay != gameplay.Modern {
		t.Fatal("cancel did not restore modern gameplay")
	}
	if g.presentation != settings.DefaultPresentation() {
		t.Fatalf("cancel %+v", g.presentation)
	}
	if g.display.Glow != settings.DefaultGlow || !cl.Glow() {
		t.Fatalf("cancel left glow at %d (live %v)", g.display.Glow, cl.Glow())
	}
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("NANOLATHE")
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	g.activateRetailOptionsGadget("NWATER")
	g.activateRetailOptionsGadget("NMARKS")
	g.activateRetailOptionsGadget("NGLOW")
	g.activateRetailOptionsGadget("NSIDEBAR")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Presentation != g.presentation || saved.Presentation.FPS != 120 {
		t.Fatalf("saved %+v", saved.Presentation)
	}
	if saved.Presentation.Water != 0 || saved.Presentation.Marks != 0 || saved.Presentation.Lighting != 1 {
		t.Fatalf("saved effects %+v", saved.Presentation)
	}
	if saved.Display.Glow != 0 {
		t.Fatalf("saved glow %d", saved.Display.Glow)
	}
	if saved.Presentation.ExpandedSidebar != 0 {
		t.Fatal("saved preference lost the disabled sidebar")
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
	if g.display.Glow != settings.DefaultGlow || !cl.Glow() {
		t.Fatalf("restore left glow at %d (live %v)", g.display.Glow, cl.Glow())
	}
	g.activateRetailOptionsGadget("UNDO")
	if g.presentation != saved.Presentation {
		t.Fatal("undo lost entry selection")
	}
	// UNDO takes this page's glow bit back from the entry snapshot too.
	if g.display.Glow != 0 || cl.Glow() {
		t.Fatalf("undo left glow at %d (live %v)", g.display.Glow, cl.Glow())
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

// effectGadgets is the page order of the six Enhanced switches.
var effectGadgets = []string{"NGLOW", "NWATER", "NLIGHTS", "NFINISH", "NHEAT", "NMARKS"}

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
	// Every switch has button art and a hit rectangle inside the battle column,
	// and one click cycles it to Off (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
	canvasW, canvasH := cl.Size()
	for _, name := range append([]string{"NGAMEPLAY", "NRENDER", "NFPS", "NSIDEBAR"}, effectGadgets...) {
		index := optionsPanel.Index(name)
		if optionsPanel.Window.Gadgets[index].ButtonArt == nil {
			t.Fatalf("%s has no game-data button art", name)
		}
		r := optionsPanel.Window.PlacedRect(index)
		if r.X < 0 || r.Y < 0 || int(r.X+r.W) > canvasW || int(r.Y+r.H) > canvasH {
			t.Fatalf("%s at %+v leaves the %dx%d canvas", name, r, canvasW, canvasH)
		}
		for _, other := range []string{"RESTORE", "UNDO"} {
			o := optionsPanel.Window.PlacedRect(optionsPanel.Index(other))
			if r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H {
				t.Fatalf("%s at %+v overlaps %s at %+v", name, r, other, o)
			}
		}
	}
	for _, name := range effectGadgets {
		click(name)
	}
	click("NSIDEBAR")
	if b.expandedSidebarEnabled() {
		t.Fatal("pointer did not disable expanded sidebar")
	}
	if got := presentationEffects(g.presentation); got != (drawlist.Effects{}) {
		t.Fatalf("pointer left effects at %+v", got)
	}
	if g.display.Glow != 0 {
		t.Fatalf("pointer left glow at %d", g.display.Glow)
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.gameplay != gameplay.Modern {
		t.Fatal("cancel did not restore modern gameplay")
	}
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

func TestGameplayStartupOverrides(t *testing.T) {
	opts, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := startupGameplay(opts, gameplay.Strict31); got != gameplay.Strict31 {
		t.Fatal("saved strict mode ignored")
	}
	opts, err = parseFlags([]string{"--gameplay=modern"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if startupGameplay(opts, gameplay.Strict31) != gameplay.Modern {
		t.Fatal("explicit override ignored")
	}
	if _, err = parseFlags([]string{"--gameplay=guess"}, io.Discard); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
