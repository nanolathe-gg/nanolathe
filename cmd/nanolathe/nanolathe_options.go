package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Nanolathe's presentation preferences extend the options family without
// changing retail controls. See DESIGN_INTERFACE_HUD_INPUT §3.4.1.
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

// Add the fifth category one authored category spacing below VISUALS. Keeping
// its size and art selector reuses the same game-data button family.
func addNanolatheOptionsCategory(window *gui.Window) {
	visual, speeds := window.GadgetIndex("VISUALS"), window.GadgetIndex("SPEEDS")
	if visual < 0 || speeds < 0 {
		return
	}
	button := window.Gadgets[visual]
	button.Rect.Y += button.Rect.Y - window.Gadgets[speeds].Rect.Y
	button.Name, button.SourceName = "NANOLATHE", "NANOLATHE_CATEGORY"
	button.Art, button.Text, button.QuickKey = "", "NANOLATHE", 0
	window.Gadgets = append(window.Gadgets, button)
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
	// the two captioned rows use a tight caption-plus-control pitch and the six
	// effect switches carry their own names in their stage text instead of
	// spending a caption line each (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
	const captionedPitch, switchPitch = 44, 24
	y := label.Rect.Y
	for _, row := range []struct {
		name, title, text string
		stages            uint8
	}{
		{"NRENDER", "Renderer", "Classic|Modern", 2},
		{"NFPS", "FPS cap (Modern)", "30|60|120", 3},
	} {
		caption, control := label, button
		caption.Name, caption.SourceName, caption.Text = row.name+"LABEL", row.name+"LABEL", row.title
		caption.Rect.Y = y
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = caption.Rect.Y + caption.Rect.H + 4
		kept = append(kept, caption, control)
		y += captionedPitch
	}
	// The Enhanced presentation switches (DESIGN_GPU_RENDERER §30). Glow keeps
	// its home in the display block; the other five are presentation values.
	// The modern executor is their only consumer — classic composes the same
	// pixels whatever they say.
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

// nanolatheEffectSwitches is the page order of the five presentation switches,
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
	optionsPanel.SetStageAt(optionsPanel.Index("NRENDER"), boolInt(g.presentation.Renderer == "modern"))
	g.syncNanolatheFPSStage()
	// The six Enhanced switches. Glow reads the display block; the other five
	// read the presentation block (DESIGN_GPU_RENDERER §30).
	optionsPanel.SetStageAt(optionsPanel.Index("NGLOW"), boolInt(g.display.Glow != 0))
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
	label := fmt.Sprintf("%d", g.presentation.FPS)
	if g.presentation.FPS == 0 {
		label = "Display"
	}
	optionsPanel.Window.Gadgets[index].Labels = []string{label, "60", "120"}
}

func (g *gameShell) activateNanolatheOption(name string) bool {
	p := g.presentation
	switch name {
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
		optionsPanel.Window.Gadgets[optionsPanel.Index("NFPS")].Labels = []string{"30", "60", "120"}
	case "NGLOW":
		// Glow lives in the display block, so it takes the same live path the
		// VISUALS controls take rather than the presentation poll (§19.4).
		g.display.Glow = g.retailOptionsStage(name, 2, boolInt(g.display.Glow != 0))
		optionsPanel.SetStageAt(optionsPanel.Index("NGLOW"), g.display.Glow)
		g.applyRetailVisualOptions(clPtr)
		return true
	default:
		// The five presentation switches. The host polls the shell's committed
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
