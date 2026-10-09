package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Nanolathe's presentation preferences extend the options family without
// changing retail controls. See DESIGN_INTERFACE_HUD_INPUT §3.4.1.
func startupGameplay(opts Options, saved gameplay.Mode) gameplay.Mode {
	if opts.GameplaySet {
		return opts.Gameplay.Normalize()
	}
	return saved.Normalize()
}

func (g *gameShell) setGameplay(mode gameplay.Mode) {
	mode = mode.Normalize()
	changed := g.gameplay.Normalize() != mode
	if changed && g.battle != nil && g.battle.sess != nil {
		if err := g.battle.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanGameplay, Gameplay: mode}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}
	g.gameplay, g.opts.Gameplay = mode, mode
}

func startupPresentation(opts Options, saved settings.Presentation) settings.Presentation {
	if opts.RendererSet {
		saved.Renderer = opts.Renderer
		if saved.Renderer != "modern" {
			saved.Renderer = "classic"
		}
	}
	if opts.FPSSet {
		saved.FPS = opts.FPS
	}
	if opts.ArrivalSet {
		saved.Arrival = boolInt(opts.Arrival)
	}
	if opts.UIScale >= 0 {
		saved.UIScale = opts.UIScale
	}
	saved.Normalize()
	return saved
}

// Renderer-dependent flags must also be checked after the saved preference is
// resolved, because parsing alone only knows the command-line default.
func validatePresentationZoom(opts Options) error {
	if opts.ZoomText != "" && !modernRenderer(opts) {
		if _, err := camera.ParseViewScale(opts.ZoomText); err != nil {
			return fmt.Errorf("invalid value %q for flag -zoom: %w", opts.ZoomText, err)
		}
	}
	return nil
}

func (g *gameShell) setPresentation(p settings.Presentation) {
	p.Normalize()
	g.presentation = p
	g.opts.Renderer, g.opts.FPS, g.opts.Arrival = p.Renderer, p.FPS, p.Arrival != 0
	applyCommunityHUDOptions(clPtr, p)
}

// presentationEffects converts the persisted switches into the value both the
// recorder and the modern executor read (DESIGN_GPU_RENDERER §30). The
// conversion lives here so internal/settings stays a leaf the frontend converts
// to and from rather than one that knows about the draw list. It is one field
// for one field: every switch is independent. The strengths are not switches
// and either travel in Effects or reach the client on their own (applyEffectStrengths).
func presentationEffects(p settings.Presentation) drawlist.Effects {
	return drawlist.Effects{
		WaterSurface:       p.WaterSurface != 0,
		WaterMotion:        p.WaterMotion != 0,
		HovercraftLandWash: p.HovercraftLandWash != 0,
		WaterFoam:          p.WaterFoam != 0,
		WaterReflections:   p.WaterReflections != 0,
		ModelLight:         p.ModelLight != 0,
		GroundLight:        p.GroundLight != 0,
		WeaponGlowStrength: p.WeaponGlowStrength, ExplosionGlowStrength: p.ExplosionGlowStrength, NanoGlowStrength: p.NanoGlowStrength,
		ShadowSoftness: p.ShadowSoftness,
		Finish:         p.Finish != 0,
		Glint:          p.Glint != 0,
		BlastRings:     p.BlastRings != 0,
		FireShimmer:    p.FireShimmer != 0,
		WreckGlow:      p.WreckGlow != 0,
		WreckShimmer:   p.WreckShimmer != 0,
		Scorch:         p.Scorch != 0,
		SoftShadows:    p.SoftShadows != 0,
		Supersample:    p.Supersample != 0,
	}
}

// storeEffects writes a live selection back into the persisted switches, the
// inverse of presentationEffects. A direct battle's write-all and its family
// commands use it, because there the client holds the live values.
func storeEffects(p *settings.Presentation, e drawlist.Effects) {
	p.WaterSurface, p.WaterMotion, p.WaterFoam, p.WaterReflections =
		boolInt(e.WaterSurface), boolInt(e.WaterMotion), boolInt(e.WaterFoam), boolInt(e.WaterReflections)
	p.HovercraftLandWash = boolInt(e.HovercraftLandWash)
	p.ModelLight, p.GroundLight = boolInt(e.ModelLight), boolInt(e.GroundLight)
	p.Finish, p.Glint = boolInt(e.Finish), boolInt(e.Glint)
	p.BlastRings, p.FireShimmer = boolInt(e.BlastRings), boolInt(e.FireShimmer)
	p.WreckGlow, p.WreckShimmer = boolInt(e.WreckGlow), boolInt(e.WreckShimmer)
	p.Scorch = boolInt(e.Scorch)
	p.SoftShadows = boolInt(e.SoftShadows)
	p.Supersample = boolInt(e.Supersample)
	p.WeaponGlowStrength, p.ExplosionGlowStrength, p.NanoGlowStrength = e.WeaponGlowStrength, e.ExplosionGlowStrength, e.NanoGlowStrength
	p.ShadowSoftness = e.ShadowSoftness
}

// applyEffectStrengths hands the client the three Enhanced strengths the
// presentation block stores beside the switches (DESIGN_GPU_RENDERER §30): the
// trail strength, which the recorder reads, and the ground light and blast ring
// strengths, which the host passes on to the executor.
func applyEffectStrengths(cl *client.Client, p settings.Presentation) {
	if cl == nil {
		return
	}
	cl.SetTrailStrength(p.TrailStrength)
	cl.SetGroundLightStrength(p.GroundLightStrength)
	cl.SetBlastRingStrength(p.BlastRingStrength)
}

// effectFamily is one of the five Enhanced shortcuts the battle options page
// and the message line share (DESIGN_INTERFACE_HUD_INPUT §3.4.1). It is not a
// stored switch: it reads On while any of its switches is on, and setting it
// writes every one of them. Marks also owns the trail strength, whose zero is
// the trail layer's off; turning Marks on gives a zero strength the default
// and leaves a chosen one as it was. Metal is the finishes alone, not the
// glint, and the glint, land wash, soft shadows and supersampling belong to no
// family.
type effectFamily struct {
	gadget, command string
	switches        func(p *settings.Presentation) []*int
	trails          bool
}

// effectFamilies is the page order of the shortcuts.
var effectFamilies = [...]effectFamily{
	{gadget: "NWATER", command: "water", switches: func(p *settings.Presentation) []*int {
		return []*int{&p.WaterSurface, &p.WaterMotion, &p.WaterFoam, &p.WaterReflections}
	}},
	{gadget: "NLIGHTS", command: "lights", switches: func(p *settings.Presentation) []*int {
		return []*int{&p.ModelLight, &p.GroundLight}
	}},
	{gadget: "NFINISH", command: "finish", switches: func(p *settings.Presentation) []*int {
		return []*int{&p.Finish}
	}},
	{gadget: "NHEAT", command: "heat", switches: func(p *settings.Presentation) []*int {
		return []*int{&p.BlastRings, &p.FireShimmer, &p.WreckGlow, &p.WreckShimmer}
	}},
	{gadget: "NMARKS", command: "marks", switches: func(p *settings.Presentation) []*int {
		return []*int{&p.Scorch}
	}, trails: true},
}

// on reports whether any of the family's switches is on.
func (f effectFamily) on(p settings.Presentation) bool {
	for _, v := range f.switches(&p) {
		if *v != 0 {
			return true
		}
	}
	return f.trails && p.TrailStrength > 0
}

// set writes every one of the family's switches.
func (f effectFamily) set(p *settings.Presentation, on bool) {
	for _, v := range f.switches(p) {
		*v = boolInt(on)
	}
	if f.trails {
		switch {
		case !on:
			p.TrailStrength = 0
		case p.TrailStrength <= 0:
			p.TrailStrength = settings.DefaultTrailStrength
		}
	}
}

// restore copies the family's switches, and Marks' trail strength, from src.
func (f effectFamily) restore(dst *settings.Presentation, src settings.Presentation) {
	from := f.switches(&src)
	for i, v := range f.switches(dst) {
		*v = *from[i]
	}
	if f.trails {
		dst.TrailStrength = src.TrailStrength
	}
}

// An F10 swap is its own save point. Read the saved block so it cannot commit
// pending edits on other options pages, as with the host fullscreen toggle.
func (g *gameShell) rendererChanged(mode ebitenapp.RendererMode) {
	p := g.presentation
	p.Renderer = "classic"
	if mode == ebitenapp.RendererModern {
		p.Renderer = "modern"
	}
	g.setPresentation(p)
	if g.retailOptionsActive() {
		optionsState.snapshot.presentation.Renderer = p.Renderer
		g.syncNanolatheOptions()
		g.syncBuilderOptions()
	}
	if !g.settingsWritable {
		return
	}
	current, err := settings.Load()
	if err == nil {
		current.Presentation.Renderer = p.Renderer
		err = current.Save()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

// Extend the authored category column with Nanolathe's host pages. Keeping
// its size and art selector reuses the same game-data button family. The
// front end's options leave the Nanolathe page to the Nanolathe screen on the
// main menu (DESIGN_INTERFACE_HUD_INPUT §3.17); the battle's options keep it,
// since that screen does not open over a battle.
func addNanolatheOptionsCategory(window *gui.Window, inBattle bool) {
	visual, speeds := window.GadgetIndex("VISUALS"), window.GadgetIndex("SPEEDS")
	if visual < 0 || speeds < 0 {
		return
	}
	button := window.Gadgets[visual]
	pitch := button.Rect.Y - window.Gadgets[speeds].Rect.Y
	pages := []string{"NANOLATHE", "BUILDERS", "COMMUNITYHUD", "PLACEMENT"}
	if !inBattle {
		pages = pages[1:]
	}
	for _, name := range pages {
		button.Rect.Y += pitch
		button.Name, button.SourceName = name, name+"_CATEGORY"
		button.Art, button.Text, button.QuickKey = "", name, 0
		if name == "COMMUNITYHUD" {
			button.Text = "HUD"
		}
		if name == "BUILDERS" {
			button.Text = "ORDERS"
		}
		if name == "PLACEMENT" {
			button.Text = "PLACEMENT"
		}
		window.Gadgets = append(window.Gadgets, button)
	}
	// The battle root puts OK beneath this same column. Compress category
	// spacing only there; the front-end actions occupy another column.
	first, last := window.GadgetIndex("SOUND"), window.GadgetIndex("PREV")
	if first >= 0 && last >= 0 {
		start, footer := window.Gadgets[first].Rect, window.Gadgets[last].Rect
		if start.X < footer.X+footer.W && footer.X < start.X+start.W {
			names := [...]string{"SOUND", "MUSIC", "SPEEDS", "VISUALS", "NANOLATHE", "BUILDERS", "COMMUNITYHUD", "PLACEMENT"}
			pitch := (footer.Y - 4 - start.Y - start.H) / int32(len(names)-1)
			for i, name := range names {
				if index := window.GadgetIndex(name); index >= 0 {
					window.Gadgets[index].Rect.Y = start.Y + int32(i)*pitch
				}
			}
		}
	}
}

// The authored VISUALS page supplies the canvas, label style, control dimensions
// and spacing references. The battle window supplies its tiled background.
func nanolatheOptionsPage(window *gui.Window) error {
	index := window.GadgetIndex("SHADING")
	if index < 0 {
		return fmt.Errorf("missing SHADING control template")
	}
	button := window.Gadgets[index]
	var label gui.Gadget
	first := true
	for _, gad := range window.Gadgets[1:] {
		if gad.Kind == gui.KindLabel && (first || gad.Rect.Y < label.Rect.Y) {
			label = gad
			first = false
		}
	}
	if first {
		return fmt.Errorf("missing label template")
	}
	kept := []gui.Gadget{window.Gadgets[0]}
	for _, gad := range window.Gadgets[1:] {
		if gad.Name == "RESTORE" || gad.Name == "UNDO" {
			kept = append(kept, gad)
		}
	}
	button.Art, button.ArtFrame, button.Attribs, button.QuickKey = "", 0, 1, 0
	button.Status = 0
	label.Link = ""
	label.Rect.X, label.Rect.W = button.Rect.X, button.Rect.W
	// Compact Renderer leaves room for camera and radar preferences in the
	// shorter battle column. Gameplay keeps its caption because
	// a registered rule set may replace one of its stage labels
	// (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
	const captionGap = 4
	switchPitch := button.Rect.H
	captionedPitch := label.Rect.H + captionGap + button.Rect.H
	y := label.Rect.Y
	renderer := button
	renderer.Name, renderer.SourceName, renderer.Text, renderer.Stages = "NRENDER", "NRENDER", "Render: Classic|Render: Modern", 2
	renderer.Rect.Y = y
	kept = append(kept, renderer)
	y += switchPitch
	for _, row := range []struct {
		name, title, text string
		stages            uint8
	}{
		{"NGAMEPLAY", "Gameplay", "Strict 3.1|Community 3.9|Modern", 3},
	} {
		caption, control := label, button
		caption.Name, caption.SourceName, caption.Text = row.name+"LABEL", row.name+"LABEL", row.title
		caption.Rect.Y = y
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = caption.Rect.Y + caption.Rect.H + captionGap
		kept = append(kept, caption, control)
		y += captionedPitch
	}
	// Compact controls leave room for every preference in the authored column.
	for _, row := range []struct {
		name, text string
		stages     uint8
	}{
		{"NFPS", "FPS: 30|FPS: 60|FPS: 120", 3},
		{"NSIDEBAR", "Sidebar: 6|Sidebar: Flow", 2},
		{"NUISCALE", chromeScaleLabels("UI scale: "), settings.MaxChromeScale + 1},
		{"NZOOM", "Zoom: Smooth|Zoom: Steps|Zoom: Off", 3},
		{"NICONS", "Icons: Modern|Icons: Comm 3.9", 2},
		{"NRADARDOTS", "No dots|Visible dots|Attackable dots", 3},
	} {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = y
		control.Help = nanolatheConfigurationHelp(row.name)
		kept = append(kept, control)
		y += switchPitch
	}
	// The Enhanced presentation switches (DESIGN_GPU_RENDERER §30). Glow keeps
	// its home in the display block; the others are presentation values.
	// Classic composes the same pixels whatever these switches say.
	effectRows := []struct{ name, text string }{
		{"NGLOW", "Glow: Off|Glow: On"},
		{"NWATER", "Water: Off|Water: On"},
		{"NLIGHTS", "Lights: Off|Lights: On"},
		{"NFINISH", "Metal: Off|Metal: On"},
		{"NHEAT", "Heat: Off|Heat: On"},
		{"NMARKS", "Marks: Off|Marks: On"},
	}
	for i, row := range effectRows {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, 2
		control.Rect.Y = y + int32(i)*switchPitch
		kept = append(kept, control)
	}
	// Use the authored control height throughout, then spend the footer's
	// spare gap on the additional row. Restore and Undo keep their own art and
	// hit rectangles, with a clear gap after the preferences and each other.
	const footerGap = 2
	nextY := y + int32(len(effectRows))*switchPitch + footerGap
	for _, name := range []string{"RESTORE", "UNDO"} {
		for i := range kept {
			if kept[i].Name == name {
				kept[i].Rect.Y = max(kept[i].Rect.Y, nextY)
				nextY = kept[i].Rect.Y + kept[i].Rect.H + footerGap
				break
			}
		}
	}
	window.Gadgets = kept
	return nil
}

var nanolatheFPSChoices = [...]int{30, 60, 120}

func (g *gameShell) syncNanolatheOptions() {
	if optionsState == nil || optionsState.page != "nanolathe" || optionsPanel == nil {
		return
	}
	// A third-party rule set sits at the stage of the reserved layer it
	// derives from, captioned with its own name; selecting a set by name is a
	// command-line or settings-file choice (docs/DESIGN_GAMEPLAY_RULES.md §8).
	if index := optionsPanel.Index("NGAMEPLAY"); index >= 0 {
		optionsPanel.Window.Gadgets[index].Labels = gameplayOptionLabels(g.gameplay)
		optionsPanel.SetStageAt(index, gameplayOptionStage(g.gameplay))
	}
	optionsPanel.SetStageAt(optionsPanel.Index("NRENDER"), boolInt(g.presentation.Renderer == "modern"))
	g.syncNanolatheFPSStage()
	g.syncNanolatheZoomStage()
	optionsPanel.SetStageAt(optionsPanel.Index("NICONS"), g.presentation.StrategicIconStyle)
	optionsPanel.SetStageAt(optionsPanel.Index("NRADARDOTS"), g.presentation.RadarDots)
	optionsPanel.SetStageAt(optionsPanel.Index("NUISCALE"), g.presentation.UIScale)
	// The Enhanced switches. Glow reads the display block; the others
	// read the presentation block (DESIGN_GPU_RENDERER §30).
	optionsPanel.SetStageAt(optionsPanel.Index("NGLOW"), boolInt(g.display.Glow != 0))
	optionsPanel.SetStageAt(optionsPanel.Index("NSIDEBAR"), boolInt(g.presentation.BuildMenuPageSize != 6 || g.presentation.SidebarOrders != 0))
	// The five family shortcuts show On while any of their switches is on.
	// `NGLOW` is not among them: glow stays in the display block, where the
	// chat command and the capture route already read it (§19.4).
	for _, f := range effectFamilies {
		optionsPanel.SetStageAt(optionsPanel.Index(f.gadget), boolInt(f.on(g.presentation)))
	}
	g.syncNanolatheAvailability()
}

func nanolatheConfigurationKey(name string) string {
	switch name {
	case "NFPS":
		return "fps"
	case "NSIDEBAR":
		return "sidebar"
	case "NUISCALE":
		return "uiscale"
	case "NZOOM":
		return "zoomstyle"
	case "NICONS":
		return "iconstyle"
	case "NRADARDOTS":
		return "radardots"
	case "NGLOW":
		return "glow"
	case "NWATER":
		return "water"
	case "NLIGHTS":
		return "lights"
	case "NFINISH":
		return "finish"
	case "NHEAT":
		return "heat"
	case "NMARKS":
		return "marks"
	}
	return ""
}

func nanolatheConfigurationHelp(name string) string {
	switch name {
	case "NZOOM":
		return "Camera zoom: continuous, stepped, or off at 1x. Classic offers native 1x/2x or Off. Free zoom requires the Enhanced renderer. Community uses camera zoom with Tab: Options."
	case "NICONS":
		return "Modern strategic icons: generated symbols or the running content's Community 3.9 art. Missing art keeps generated symbols."
	case "NUISCALE":
		return "Magnifies the battle sidebar, minimap and top and bottom bars in the Enhanced renderer. Auto uses 2x from 1440 rows. 2x always applies when chosen; below 960 rows the command page may not fit."
	case "NRADARDOTS":
		return "Radar dots in the main view require Modern gameplay and the Enhanced renderer: hidden, display only, or attack hostile contacts without unit details. Minimap contacts are unchanged."
	}
	return ""
}

func (g *gameShell) syncNanolatheAvailability() {
	if optionsState == nil || optionsState.page != "nanolathe" || optionsPanel == nil {
		return
	}
	mode := g.configurationMode()
	for _, name := range []string{"NFPS", "NSIDEBAR", "NUISCALE", "NZOOM", "NICONS", "NRADARDOTS", "NGLOW", "NWATER", "NLIGHTS", "NFINISH", "NHEAT", "NMARKS"} {
		reason := configurationUnavailable(nanolatheConfigurationKey(name), mode, g.presentation)
		syncConfigurationOption(optionsPanel, name, reason, nanolatheConfigurationHelp(name))
	}
}

func (g *gameShell) syncNanolatheZoomStage() {
	index := optionsPanel.Index("NZOOM")
	if index < 0 {
		return
	}
	labels := []string{"Zoom: Smooth", "Zoom: Steps", "Zoom: Off"}
	if g.presentation.Renderer == "classic" {
		// Keep a saved Steps preference intact, but describe the native
		// behavior both non-Off values select with this renderer.
		labels[0], labels[1] = "Zoom: Classic", "Zoom: Classic"
	}
	optionsPanel.Window.Gadgets[index].Labels = labels
	optionsPanel.SetStageAt(index, g.presentation.ZoomStyle)
}

func (g *gameShell) syncNanolatheFPSStage() {
	index := optionsPanel.Index("NFPS")
	for stage, fps := range nanolatheFPSChoices {
		if g.presentation.FPS == fps {
			optionsPanel.SetStageAt(index, stage)
			return
		}
	}
	// CLI/file values outside the menu presets remain visible until changed.
	optionsPanel.SetStageAt(index, 0)
	label := fmt.Sprintf("FPS: %d", g.presentation.FPS)
	if g.presentation.FPS == 0 {
		label = "FPS: Display"
	}
	optionsPanel.Window.Gadgets[index].Labels = []string{label, "FPS: 60", "FPS: 120"}
}

func (g *gameShell) activateNanolatheOption(name string) bool {
	p := g.presentation
	if key := nanolatheConfigurationKey(name); key != "" && configurationUnavailable(key, g.configurationMode(), p) != "" {
		g.syncNanolatheOptions()
		return true
	}
	switch name {
	case "NGAMEPLAY":
		// Cycling selects one of the three reserved sets, so it also replaces a
		// third-party selection with the reserved set at the chosen layer.
		stage := g.retailOptionsStage(name, len(gameplayOptionModes), gameplayOptionStage(g.gameplay))
		// A running mod's minimum skips the layers below it
		// (docs/DESIGN_MODS_MUTATORS.md §4.3).
		if g.cs != nil {
			if minimum, ok := modMinimumGameplay(g.cs.mod); ok && stage < gameplayOptionStage(minimum) {
				stage = gameplayOptionStage(minimum)
			}
		}
		g.setGameplay(gameplayOptionModes[stage])
		g.syncNanolatheOptions()
		return true
	case "NSIDEBAR":
		p.ExpandedSidebar = 1
		if g.retailOptionsStage(name, 2, boolInt(p.BuildMenuPageSize != 6 || p.SidebarOrders != 0)) == 0 {
			p.BuildMenuPageSize, p.SidebarOrders = 6, 0
		} else {
			p.BuildMenuPageSize, p.SidebarOrders = 0, 1
		}
	case "NZOOM":
		p.ZoomStyle = g.retailOptionsStage(name, 3, p.ZoomStyle)
		if configurationValueUnavailable("zoomstyle", p.ZoomStyle, g.configurationMode(), p) != "" {
			p.ZoomStyle = (p.ZoomStyle + 1) % 3
		}
	case "NICONS":
		p.StrategicIconStyle = g.retailOptionsStage(name, 2, p.StrategicIconStyle)
	case "NRADARDOTS":
		p.RadarDots = g.retailOptionsStage(name, 3, p.RadarDots)
	case "NUISCALE":
		p.UIScale = g.retailOptionsStage(name, settings.MaxChromeScale+1, p.UIScale)
	case "NRENDER":
		stage := g.retailOptionsStage(name, 2, boolInt(p.Renderer == "modern"))
		p.Renderer = "classic"
		if stage == 1 {
			p.Renderer = "modern"
		}
	case "NFPS":
		current := 0
		for i, fps := range nanolatheFPSChoices {
			if p.FPS == fps {
				current = i
			}
		}
		stage := g.retailOptionsStage(name, len(nanolatheFPSChoices), current)
		p.FPS = nanolatheFPSChoices[stage]
		optionsPanel.Window.Gadgets[optionsPanel.Index("NFPS")].Labels = []string{"FPS: 30", "FPS: 60", "FPS: 120"}
	case "NGLOW":
		// Glow lives in the display block, so it takes the same live path the
		// VISUALS controls take rather than the presentation poll (§19.4).
		g.display.Glow = g.retailOptionsStage(name, 2, boolInt(g.display.Glow != 0))
		optionsPanel.SetStageAt(optionsPanel.Index("NGLOW"), g.display.Glow)
		g.applyRetailVisualOptions(clPtr)
		return true
	default:
		// The family shortcuts: pressing one writes every switch of its family
		// to the new stage. The host polls the shell's committed preference
		// each update, so writing it here is the live preview (§30).
		for _, f := range effectFamilies {
			if f.gadget != name {
				continue
			}
			f.set(&p, g.retailOptionsStage(name, 2, boolInt(f.on(p))) != 0)
			g.setPresentation(p)
			g.syncNanolatheOptions()
			return true
		}
		return false
	}
	g.setPresentation(p)
	g.syncNanolatheOptions()
	return true
}

var gameplayOptionModes = [...]gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern}

// gameplayOptionLabels are the gameplay control's three stage captions. The
// control selects only the reserved sets, but the selection it shows may be a
// registered set chosen by name on the command line or in the settings file,
// and that choice is persisted like any other. Such a set is captioned with
// its own name at the stage of the reserved set it derives from, so the page
// never reads "Modern" while `example` is what the game runs and saves.
// Cycling selects a reserved set, and the next sync restores its caption. The
// control chooses rules only: each computer player's AI is its lobby row's.
func gameplayOptionLabels(mode gameplay.Mode) []string {
	labels := []string{"Strict 3.1", "Community 3.9", "Modern"}
	if selected := mode.Normalize(); selected != session.BaseModeOf(selected) {
		labels[gameplayOptionStage(selected)] = string(selected)
	}
	return labels
}

func gameplayOptionStage(mode gameplay.Mode) int {
	base := session.BaseModeOf(mode)
	for stage, candidate := range gameplayOptionModes {
		if base == candidate {
			return stage
		}
	}
	return len(gameplayOptionModes) - 1
}

// Each options page restores only fields it owns; host input preferences live
// on the Orders page and share the ordinary options transaction. This page's
// camera and radar choices and family shortcuts are its fields; Marks owns the trail
// strength too, so Undo and Restore Defaults take all of those back;
// the glint and the soft shadows are no shortcut's and stay as they are.
func (g *gameShell) setNanolathePreferences(p settings.Presentation) {
	next := g.presentation
	next.Renderer, next.FPS, next.ExpandedSidebar = p.Renderer, p.FPS, p.ExpandedSidebar
	next.SidebarOrders, next.BuildMenuPageSize = p.SidebarOrders, p.BuildMenuPageSize
	next.ZoomStyle, next.StrategicIconStyle = p.ZoomStyle, p.StrategicIconStyle
	next.RadarDots, next.UIScale = p.RadarDots, p.UIScale
	for _, f := range effectFamilies {
		f.restore(&next, p)
	}
	g.setPresentation(next)
}

// chromeScaleLabels lists Auto and every fixed scale for a stage control.
func chromeScaleLabels(prefix string) string {
	labels := []string{prefix + "Auto"}
	for _, step := range chromeScaleSteps()[1:] {
		labels = append(labels, prefix+step)
	}
	return strings.Join(labels, "|")
}

// chromeScaleSteps names the scale preference's values in stored order.
func chromeScaleSteps() []string {
	steps := []string{"Auto"}
	for k := 1; k <= settings.MaxChromeScale; k++ {
		steps = append(steps, fmt.Sprintf("%dx", k))
	}
	return steps
}
