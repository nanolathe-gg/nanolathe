package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
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
	g.opts.Renderer, g.opts.FPS = p.Renderer, p.FPS
	applyCommunityHUDOptions(clPtr, p)
}

// presentationEffects converts the persisted switches into the value both the
// recorder and the modern executor read (DESIGN_GPU_RENDERER §30). The
// conversion lives here so internal/settings stays a leaf the frontend converts
// to and from rather than one that knows about the draw list.
func presentationEffects(p settings.Presentation) drawlist.Effects {
	return drawlist.Effects{
		Water:      p.Water != 0,
		Lighting:   p.Lighting != 0,
		Finish:     p.Finish != 0,
		Distortion: p.Distortion != 0,
		Marks:      p.Marks != 0,
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
// its size and art selector reuses the same game-data button family.
func addNanolatheOptionsCategory(window *gui.Window) {
	visual, speeds := window.GadgetIndex("VISUALS"), window.GadgetIndex("SPEEDS")
	if visual < 0 || speeds < 0 {
		return
	}
	button := window.Gadgets[visual]
	pitch := button.Rect.Y - window.Gadgets[speeds].Rect.Y
	for _, name := range []string{"NANOLATHE", "BUILDERS", "COMMUNITYHUD", "PLACEMENT"} {
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
	// The page has to fit the in-battle column as well as the front-end one, so
	// the two captioned rows use a tight caption-plus-control pitch and the
	// presentation switches carry their own names in their stage text instead of
	// spending a caption line each (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
	const captionedPitch, switchPitch = 40, 20
	y := label.Rect.Y
	for _, row := range []struct {
		name, title, text string
		stages            uint8
	}{
		{"NRENDER", "Renderer", "Classic|Modern", 2},
		{"NGAMEPLAY", "Gameplay", "Strict 3.1|Community 3.9|Modern", 3},
	} {
		caption, control := label, button
		caption.Name, caption.SourceName, caption.Text = row.name+"LABEL", row.name+"LABEL", row.title
		caption.Rect.Y = y
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = caption.Rect.Y + caption.Rect.H + 4
		kept = append(kept, caption, control)
		y += captionedPitch
	}
	// Compact controls leave room for every preference in the authored column.
	for _, row := range []struct {
		name, text string
		stages     uint8
	}{
		{"NFPS", "FPS: 30|FPS: 60|FPS: 120", 3},
		{"NSIDEBAR", "Sidebar: Off|Sidebar: On", 2},
	} {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = y
		kept = append(kept, control)
		y += switchPitch
	}
	// The Enhanced presentation switches (DESIGN_GPU_RENDERER §30). Glow keeps
	// its home in the display block; the others are presentation values.
	// Classic composes the same pixels whatever these switches say.
	for i, row := range []struct{ name, text string }{
		{"NGLOW", "Glow: Off|Glow: On"},
		{"NWATER", "Water: Off|Water: On"},
		{"NLIGHTS", "Lights: Off|Lights: On"},
		{"NFINISH", "Metal: Off|Metal: On"},
		{"NHEAT", "Heat: Off|Heat: On"},
		{"NMARKS", "Marks: Off|Marks: On"},
	} {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, 2
		control.Rect.Y = y + int32(i)*switchPitch
		kept = append(kept, control)
	}
	window.Gadgets = kept
	return nil
}

type nanolatheEffectSwitch struct {
	name  string
	value *int
}

// nanolatheEffectSwitches is the page order of the presentation switches,
// paired with the persisted field each one writes. `NGLOW` is not among them:
// glow stays in the display block, where the chat command and the capture route
// already read it (DESIGN_GPU_RENDERER §19.4).
func nanolatheEffectSwitches(p *settings.Presentation) [5]nanolatheEffectSwitch {
	return [5]nanolatheEffectSwitch{
		{"NWATER", &p.Water},
		{"NLIGHTS", &p.Lighting},
		{"NFINISH", &p.Finish},
		{"NHEAT", &p.Distortion},
		{"NMARKS", &p.Marks},
	}
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
	// The Enhanced switches. Glow reads the display block; the others
	// read the presentation block (DESIGN_GPU_RENDERER §30).
	optionsPanel.SetStageAt(optionsPanel.Index("NGLOW"), boolInt(g.display.Glow != 0))
	optionsPanel.SetStageAt(optionsPanel.Index("NSIDEBAR"), boolInt(g.presentation.ExpandedSidebar != 0))
	p := g.presentation
	for _, sw := range nanolatheEffectSwitches(&p) {
		optionsPanel.SetStageAt(optionsPanel.Index(sw.name), boolInt(*sw.value != 0))
	}
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
		p.ExpandedSidebar = g.retailOptionsStage(name, 2, boolInt(p.ExpandedSidebar != 0))
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
		// The presentation switches. The host polls the shell's committed
		// preference each update, so writing it here is the live preview (§30).
		for _, sw := range nanolatheEffectSwitches(&p) {
			if sw.name != name {
				continue
			}
			*sw.value = g.retailOptionsStage(name, 2, boolInt(*sw.value != 0))
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
// on the Orders page and share the ordinary options transaction.
func (g *gameShell) setNanolathePreferences(p settings.Presentation) {
	next := g.presentation
	next.Renderer, next.FPS, next.ExpandedSidebar = p.Renderer, p.FPS, p.ExpandedSidebar
	next.Water, next.Lighting, next.Finish, next.Distortion, next.Marks = p.Water, p.Lighting, p.Finish, p.Distortion, p.Marks
	g.setPresentation(next)
}
