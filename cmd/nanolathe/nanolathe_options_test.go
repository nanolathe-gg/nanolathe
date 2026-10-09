package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/session"
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
		tc.want.Normalize()
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
	// The front end's options leave the page to the Nanolathe screen; the
	// battle's options keep it (DESIGN_INTERFACE_HUD_INPUT §3.17).
	if optionsPanel.Index("NANOLATHE") >= 0 {
		t.Fatal("front-end options still offer the Nanolathe page")
	}
	g.closeRetailOptionsScreen()
	openOptions := func() {
		t.Helper()
		if err := g.openRetailOptionsScreen(true); err != nil {
			t.Fatal(err)
		}
	}
	openOptions()
	g.activateRetailOptionsGadget("NANOLATHE")
	if optionsState.page != "nanolathe" {
		t.Fatal("new category did not open")
	}
	for _, name := range []string{"NANOLATHE", "NGAMEPLAY", "NRENDER", "NFPS", "NZOOM", "NICONS", "NRADARDOTS", "NGLOW", "NWATER", "NLIGHTS", "NFINISH", "NHEAT", "NMARKS", "NSIDEBAR"} {
		gad := optionsPanel.Window.Gadgets[optionsPanel.Index(name)]
		if gad.ButtonArt == nil {
			t.Fatalf("%s has no game-data button art", name)
		}
	}
	if optionsPanel.Index("NNANO") >= 0 {
		t.Fatal("legacy nano control remains on Nanolathe page")
	}
	if optionsPanel.StageAt(optionsPanel.Index("NZOOM")) != settings.ZoomSmooth || optionsPanel.StageAt(optionsPanel.Index("NICONS")) != settings.StrategicIconsModern {
		t.Fatal("camera controls do not show their defaults")
	}
	if dot := optionsPanel.Window.Gadgets[optionsPanel.Index("NRADARDOTS")]; dot.Stages != 3 || optionsPanel.StageAt(optionsPanel.Index("NRADARDOTS")) != settings.RadarDotsVisible || !strings.Contains(dot.Help, "Modern gameplay") || !strings.Contains(dot.Help, "Enhanced renderer") {
		t.Fatal("radar control lost its default, stages or scope help")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "nanolathe-options.png"))
	}
	host := g.windowOptions()
	g.activateRetailOptionsGadget("NGAMEPLAY")
	if g.gameplay != gameplay.Strict31 {
		t.Fatal("gameplay did not switch to strict")
	}
	g.activateRetailOptionsGadget("NGAMEPLAY")
	if g.gameplay != gameplay.Community39 {
		t.Fatal("gameplay did not advance from strict to Community 3.9")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "nanolathe-options-community.png"))
	}
	g.activateRetailOptionsGadget("NGAMEPLAY")
	if g.gameplay != gameplay.Modern {
		t.Fatal("gameplay did not advance from Community 3.9 to Modern")
	}
	g.activateRetailOptionsGadget("NGAMEPLAY")
	if g.gameplay != gameplay.Strict31 {
		t.Fatal("gameplay did not wrap from Modern to strict")
	}
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	if mode, fps := host.PresentationSettings(); mode != ebitenapp.RendererClassic || fps != 60 {
		t.Fatalf("preview %v %d", mode, fps)
	}
	before := g.presentation
	for _, name := range append([]string{"NSIDEBAR", "NZOOM", "NICONS", "NRADARDOTS"}, effectGadgets...) {
		g.activateRetailOptionsGadget(name)
	}
	if g.presentation != before || g.display.Glow != settings.DefaultGlow {
		t.Fatal("inactive Classic/Strict controls changed stored preferences")
	}
	// The same controls become editable when their consumers are selected.
	g.activateRetailOptionsGadget("NGAMEPLAY")
	g.activateRetailOptionsGadget("NGAMEPLAY")
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	// Every Enhanced switch previews through the same poll the host makes, and
	// glow previews straight onto the live client (DESIGN_GPU_RENDERER §30).
	for _, name := range effectGadgets {
		g.activateRetailOptionsGadget(name)
	}
	g.activateRetailOptionsGadget("NSIDEBAR")
	g.activateRetailOptionsGadget("NZOOM")
	g.activateRetailOptionsGadget("NICONS")
	g.activateRetailOptionsGadget("NRADARDOTS")
	if p := g.presentation; p.ZoomStyle != settings.ZoomStepped || p.StrategicIconStyle != settings.StrategicIconsCommunity {
		t.Fatalf("camera preferences did not preview: %+v", p)
	}
	if g.presentation.RadarDots != settings.RadarDotsAttackable {
		t.Fatal("radar control did not preview Attackable dots")
	}
	if b := (&battleSession{shell: g}); !b.expandedSidebarEnabled() || b.buildPageLock() != 6 || b.sidebarOrdersEnabled() {
		t.Fatal("six-item sidebar preference did not preview immediately")
	}
	// The page's buttons are family shortcuts: each wrote every switch of its
	// family off, Marks the trail strength too, while the glint, the soft
	// shadows and the supersampling, in no family, keep theirs.
	if got := host.Effects(); got != (drawlist.Effects{HovercraftLandWash: true, Glint: true, SoftShadows: true, Supersample: true, WeaponGlowStrength: 100, ExplosionGlowStrength: 100, NanoGlowStrength: 100, ShadowSoftness: 100}) || g.presentation.TrailStrength != 0 {
		t.Fatalf("effect preview %+v, trail strength %d", got, g.presentation.TrailStrength)
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
	openOptions()
	g.activateRetailOptionsGadget("NANOLATHE")
	g.activateRetailOptionsGadget("NFPS")
	g.activateRetailOptionsGadget("NWATER")
	g.activateRetailOptionsGadget("NMARKS")
	g.activateRetailOptionsGadget("NGLOW")
	g.activateRetailOptionsGadget("NSIDEBAR")
	g.activateRetailOptionsGadget("NZOOM")
	g.activateRetailOptionsGadget("NICONS")
	g.activateRetailOptionsGadget("NRADARDOTS")
	g.activateRetailOptionsGadget("NRADARDOTS")
	// Disabling the renderer retains the values for its next activation.
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Presentation != g.presentation || saved.Presentation.FPS != 120 {
		t.Fatalf("saved %+v", saved.Presentation)
	}
	if saved.Presentation.WaterSurface != 0 || saved.Presentation.WaterMotion != 0 || saved.Presentation.WaterFoam != 0 || saved.Presentation.WaterReflections != 0 ||
		saved.Presentation.Scorch != 0 || saved.Presentation.TrailStrength != 0 || saved.Presentation.ModelLight != 1 || saved.Presentation.GroundLight != 1 {
		t.Fatalf("saved effects %+v", saved.Presentation)
	}
	if saved.Display.Glow != 0 {
		t.Fatalf("saved glow %d", saved.Display.Glow)
	}
	if saved.Presentation.ExpandedSidebar != 1 || saved.Presentation.BuildMenuPageSize != 6 || saved.Presentation.SidebarOrders != 0 {
		t.Fatal("saved preference lost six items without inline Orders")
	}
	if saved.Presentation.ZoomStyle != settings.ZoomStepped || saved.Presentation.StrategicIconStyle != settings.StrategicIconsCommunity {
		t.Fatal("OK lost the camera preferences")
	}
	if saved.Presentation.RadarDots != settings.RadarDotsNone {
		t.Fatal("OK lost the explicit No dots preference")
	}
	next := &gameShell{}
	next.applySettings(saved)
	if next.presentation != g.presentation {
		t.Fatal("restart lost selection")
	}
	openOptions()
	g.activateRetailOptionsGadget("NANOLATHE")
	if optionsPanel.StageAt(optionsPanel.Index("NZOOM")) != settings.ZoomStepped || optionsPanel.StageAt(optionsPanel.Index("NICONS")) != settings.StrategicIconsCommunity {
		t.Fatal("camera controls do not show the persisted choices")
	}
	if optionsPanel.StageAt(optionsPanel.Index("NRADARDOTS")) != settings.RadarDotsNone {
		t.Fatal("radar control does not show persisted No dots")
	}
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
	g.activateRetailOptionsGadget("NRENDER")
	g.activateRetailOptionsGadget("NFPS")
	g.activateRetailOptionsGadget("NICONS")
	g.activateRetailOptionsGadget("NZOOM")
	g.activateRetailOptionsGadget("NRADARDOTS")
	host.RendererChanged(ebitenapp.RendererModern)
	after, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.Presentation.Renderer != "modern" || after.Presentation.FPS != 120 {
		t.Fatalf("F10 saved pending cap: %+v", after.Presentation)
	}
	if after.Presentation.ZoomStyle != settings.ZoomStepped || after.Presentation.StrategicIconStyle != settings.StrategicIconsCommunity {
		t.Fatal("F10 saved pending camera choices")
	}
	if after.Presentation.RadarDots != settings.RadarDotsNone {
		t.Fatal("F10 saved the pending radar choice")
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
	if g.presentation.FPS != 60 {
		t.Fatalf("Classic's inactive cap changed: %+v", g.presentation)
	}
	click("NRENDER")
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
	controls := append([]string{"NGAMEPLAY", "NRENDER", "NFPS", "NSIDEBAR", "NUISCALE", "NZOOM", "NICONS", "NRADARDOTS", "RESTORE", "UNDO"}, effectGadgets...)
	for _, name := range controls {
		index := optionsPanel.Index(name)
		if optionsPanel.Window.Gadgets[index].ButtonArt == nil {
			t.Fatalf("%s has no game-data button art", name)
		}
		r := optionsPanel.Window.PlacedRect(index)
		if r.X < 0 || r.Y < 0 || int(r.X+r.W) > canvasW || int(r.Y+r.H) > canvasH {
			t.Fatalf("%s at %+v leaves the %dx%d canvas", name, r, canvasW, canvasH)
		}
		for _, other := range controls {
			if other == name {
				continue
			}
			o := optionsPanel.Window.PlacedRect(optionsPanel.Index(other))
			if r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H {
				t.Fatalf("%s at %+v overlaps %s at %+v", name, r, other, o)
			}
		}
		if name == "NRENDER" || name == "NZOOM" || name == "NICONS" || name == "NRADARDOTS" {
			gad := optionsPanel.Window.Gadgets[index]
			measure, _ := g.retailTextMetrics(g.windowGadgetFont(optionsPanel, gad))
			for _, text := range gad.Labels {
				if width := measure(text); width > int(r.W)-6 {
					t.Errorf("%s caption %q spans %d pixels in a %d-pixel control", name, text, width, r.W)
				}
			}
		}
	}
	for _, want := range []int{settings.RadarDotsAttackable, settings.RadarDotsNone, settings.RadarDotsVisible} {
		click("NRADARDOTS")
		if g.presentation.RadarDots != want || optionsPanel.StageAt(optionsPanel.Index("NRADARDOTS")) != want {
			t.Fatalf("pointer radar stage = %d, want %d", g.presentation.RadarDots, want)
		}
	}
	click("NZOOM")
	if g.presentation.ZoomStyle != settings.ZoomStepped || b.cameraControlStyle() != battleZoomStepped || g.presentation.StrategicIconStyle != settings.StrategicIconsModern {
		t.Fatal("pointer did not select stepped zoom independently")
	}
	click("NICONS")
	if g.presentation.StrategicIconStyle != settings.StrategicIconsCommunity || b.cameraControlStyle() != battleZoomStepped {
		t.Fatal("pointer did not select community icons independently")
	}
	click("NZOOM")
	if g.presentation.ZoomStyle != settings.ZoomNone || b.cameraControlStyle() != battleZoomNone || g.presentation.StrategicIconStyle != settings.StrategicIconsCommunity {
		t.Fatal("pointer did not select No zoom independently")
	}
	click("NZOOM")
	if g.presentation.ZoomStyle != settings.ZoomSmooth || b.cameraControlStyle() != battleZoomSmooth {
		t.Fatal("pointer did not cycle back to continuous zoom")
	}
	for _, name := range effectGadgets {
		click(name)
	}
	click("NSIDEBAR")
	if !b.expandedSidebarEnabled() || b.buildPageLock() != 6 || b.sidebarOrdersEnabled() {
		t.Fatal("pointer did not select six items without inline Orders")
	}
	// The page's five buttons are family shortcuts: off, every switch of
	// every family is off, while the glint, the aircraft soft shadows and the
	// supersampling, which belong to no family, keep their own
	// (DESIGN_GPU_RENDERER §30).
	if got := presentationEffects(g.presentation); got != (drawlist.Effects{HovercraftLandWash: true, Glint: true, SoftShadows: true, Supersample: true, WeaponGlowStrength: 100, ExplosionGlowStrength: 100, NanoGlowStrength: 100, ShadowSoftness: 100}) || g.presentation.TrailStrength != 0 {
		t.Fatalf("pointer left effects at %+v, trail strength %d", got, g.presentation.TrailStrength)
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

// Undo and Restore Defaults own the camera and radar choices alongside this page's
// renderer and effects. The icon path, Strict overview and unrelated effects
// remain the preferences of their existing owners (DESIGN_INTERFACE_HUD_INPUT
// §3.4.1, DESIGN_GPU_RENDERER §16.6 and §18.7).
func TestNanolatheCameraPreferencesRestoreOnlyTheirPage(t *testing.T) {
	entry := settings.DefaultPresentation()
	entry.ZoomStyle, entry.StrategicIconStyle = settings.ZoomStepped, settings.StrategicIconsCommunity
	entry.RadarDots = settings.RadarDotsAttackable
	entry.StrategicIconConfig = "/icons/iconcfg.ini"
	entry.Overview, entry.Glint = settings.OverviewMegamap, 0
	g := &gameShell{presentation: entry}
	g.setNanolathePreferences(settings.DefaultPresentation())
	want := entry
	want.ZoomStyle, want.StrategicIconStyle = settings.ZoomSmooth, settings.StrategicIconsModern
	want.RadarDots = settings.RadarDotsVisible
	if g.presentation != want {
		t.Fatalf("restore changed preferences outside the page: %+v", g.presentation)
	}
	g.setNanolathePreferences(entry)
	if g.presentation != entry {
		t.Fatal("undo did not restore the camera choices")
	}
}

// A family shortcut is not a stored switch (DESIGN_GPU_RENDERER §30): it reads
// On while any of its switches is, and setting it writes every one. Marks owns
// the trail strength as well: off is zero, and on gives a zero strength the
// default while a chosen strength stays.
func TestEffectFamiliesShowAnyAndSetAll(t *testing.T) {
	byGadget := func(name string) effectFamily {
		for _, f := range effectFamilies {
			if f.gadget == name {
				return f
			}
		}
		t.Fatalf("no family %s", name)
		return effectFamily{}
	}
	water := byGadget("NWATER")
	p := settings.DefaultPresentation()
	p.WaterSurface, p.WaterMotion, p.WaterReflections = 0, 0, 0
	if !water.on(p) {
		t.Fatal("water with foam alone on reads Off")
	}
	water.set(&p, false)
	if water.on(p) || p.WaterFoam != 0 {
		t.Fatalf("water off left %+v", p)
	}
	water.set(&p, true)
	if p.WaterSurface != 1 || p.WaterMotion != 1 || p.WaterFoam != 1 || p.WaterReflections != 1 {
		t.Fatalf("water on left %+v", p)
	}
	marks := byGadget("NMARKS")
	p = settings.DefaultPresentation()
	p.Scorch = 0
	if !marks.on(p) {
		t.Fatal("marks with trails alone reads Off")
	}
	p.TrailStrength = 100
	marks.set(&p, true)
	if p.Scorch != 1 || p.TrailStrength != 100 {
		t.Fatalf("marks on changed a chosen trail strength: %+v", p)
	}
	marks.set(&p, false)
	if marks.on(p) || p.TrailStrength != 0 {
		t.Fatalf("marks off left %+v", p)
	}
	marks.set(&p, true)
	if p.TrailStrength != settings.DefaultTrailStrength {
		t.Fatalf("marks on gave trail strength %d", p.TrailStrength)
	}
	// Heat is both wreck switches with the rings and the fire shimmer.
	heat := byGadget("NHEAT")
	p = settings.DefaultPresentation()
	p.BlastRings, p.FireShimmer, p.WreckShimmer = 0, 0, 0
	if !heat.on(p) {
		t.Fatal("heat with the wreck glow alone reads Off")
	}
	heat.set(&p, false)
	if p.WreckGlow != 0 || p.WreckShimmer != 0 || heat.on(p) {
		t.Fatalf("heat off left %+v", p)
	}
	// Metal is the finishes alone; the glint, the soft shadows and the
	// supersampling are no family's, so no shortcut moves them, and no
	// shortcut moves the ground light or blast ring strengths, which have
	// switches of their own.
	p = settings.DefaultPresentation()
	p.GroundLightStrength, p.BlastRingStrength = 150, 40
	for _, f := range effectFamilies {
		f.set(&p, false)
	}
	if p.Glint != 1 || p.SoftShadows != 1 || p.Supersample != 1 || p.Finish != 0 || p.GroundLightStrength != 150 || p.BlastRingStrength != 40 {
		t.Fatalf("family shortcuts moved an unowned value: %+v", p)
	}
}

// The ground light and blast ring strengths reach the client with the trail
// strength, from the same presentation block (DESIGN_GPU_RENDERER §30).
func TestEffectStrengthsReachTheClient(t *testing.T) {
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cl.GroundLightStrength() != settings.DefaultEffectStrength || cl.BlastRingStrength() != settings.DefaultEffectStrength {
		t.Fatalf("new client strengths %d/%d", cl.GroundLightStrength(), cl.BlastRingStrength())
	}
	p := settings.DefaultPresentation()
	p.TrailStrength, p.GroundLightStrength, p.BlastRingStrength = 25, 0, 200
	applyEffectStrengths(cl, p)
	if cl.TrailStrength() != 25 || cl.GroundLightStrength() != 0 || cl.BlastRingStrength() != 200 {
		t.Fatalf("client strengths %d/%d/%d", cl.TrailStrength(), cl.GroundLightStrength(), cl.BlastRingStrength())
	}
	applyEffectStrengths(nil, p)
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

func TestGameplaySwitchRetainsHostSelectionOnInvalidFeatures(t *testing.T) {
	sess := &session.Session{CommunitySources: session.CommunitySources{Player: community.Overrides{Table: "unknown-table"}}}
	sess.SetGameplay(gameplay.Strict31)
	g := &gameShell{gameplay: gameplay.Strict31, opts: Options{Gameplay: gameplay.Strict31}, battle: &battleSession{sess: sess}}
	g.setGameplay(gameplay.Community39)
	if g.gameplay != gameplay.Strict31 || g.opts.Gameplay != gameplay.Strict31 || sess.Gameplay != gameplay.Strict31 || len(sess.PendingHumanCommands()) != 0 {
		t.Fatal("rejected switch changed host or session")
	}
}

// The control shows the selection the game runs and persists: a registered
// set is captioned with its own name at its base's stage, and the reserved
// sets keep their reserved captions. The arena's research set is not linked
// into the game, so it is not selectable at all, and the retired modern-ai
// selection is refused with its replacement: the Modern AI is chosen per
// computer player, never by a gameplay rule set.
func TestGameplayOptionShowsTheSelectedSet(t *testing.T) {
	reserved := []string{"Strict 3.1", "Community 3.9", "Modern"}
	for _, name := range session.RuleSetNames() {
		mode := gameplay.Mode(name)
		labels := gameplayOptionLabels(mode)
		stage := gameplayOptionStage(mode)
		for i, label := range labels {
			want := reserved[i]
			if i == stage && session.BaseModeOf(mode) != mode {
				want = name
			}
			if label != want {
				t.Fatalf("%s: stage %d caption %q, want %q", name, i, label, want)
			}
		}
	}
	for _, name := range session.RuleSetNames() {
		if name == string(gameplay.RetiredModernAI) || name == "aikit" {
			t.Fatalf("the game offers %q", name)
		}
	}
	if labels := gameplayOptionLabels(gameplay.RetiredModernAI); labels[2] != "Modern" {
		t.Fatalf("the retired modern-ai word captions %v", labels)
	}
	if _, err := parseFlags([]string{"--gameplay=aikit"}, io.Discard); err == nil {
		t.Fatal("the arena's research set is selectable in the game")
	}
	if _, err := parseFlags([]string{"--gameplay=modern-ai"}, io.Discard); err == nil || !strings.Contains(err.Error(), "--ai-player all=modern") {
		t.Fatalf("the retired modern-ai selection was not refused with its replacement: %v", err)
	}
}

// The supersampling switch (DESIGN_GPU_RENDERER §17.5), a Nanolathe
// presentation choice, maps one field for one field like every other switch:
// on by default, a stored 0 reaches the recorder and executor as off, and the
// live value is written back unchanged.
func TestSupersampleSwitchMapsBothWays(t *testing.T) {
	p := settings.DefaultPresentation()
	if !presentationEffects(p).Supersample || presentationEffects(p) != drawlist.AllEffects() {
		t.Fatalf("default presentation effects %+v", presentationEffects(p))
	}
	p.Supersample = 0
	e := presentationEffects(p)
	want := drawlist.AllEffects()
	want.Supersample = false
	if e != want {
		t.Fatalf("supersample 0 converted to %+v", e)
	}
	back := settings.DefaultPresentation()
	storeEffects(&back, e)
	if back != p {
		t.Fatalf("supersample off stored back as %+v", back)
	}
}

func TestSavedArrivalHonorsExplicitCLIOnly(t *testing.T) {
	saved := settings.DefaultPresentation()
	saved.Arrival = 0
	for _, tc := range []struct {
		args []string
		want int
	}{
		{nil, 0}, {[]string{"--arrival"}, 1}, {[]string{"--arrival=false"}, 0},
	} {
		opts, err := parseFlags(tc.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if got := startupPresentation(opts, saved).Arrival; got != tc.want {
			t.Fatalf("%v: arrival=%d", tc.args, got)
		}
	}
}
