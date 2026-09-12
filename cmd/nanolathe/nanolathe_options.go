package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
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
	top := label.Rect.Y
	for i, row := range []struct {
		name, title, text string
		stages            uint8
	}{
		{"NRENDER", "Renderer", "Classic|Modern", 2},
		{"NFPS", "FPS cap", "30|60|120", 3},
	} {
		caption, control := label, button
		caption.Name, caption.SourceName, caption.Text = row.name+"LABEL", row.name+"LABEL", row.title
		caption.Rect.Y = top + int32(i)*64
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = caption.Rect.Y + caption.Rect.H + 4
		kept = append(kept, caption, control)
	}
	// The cap is an upper bound for modern. Classic retains its 30 Hz cadence.
	for i, text := range []string{"Classic: 30 FPS", "Modern: capped"} {
		note := label
		note.Name, note.SourceName, note.Text = fmt.Sprintf("NNOTE%d", i), fmt.Sprintf("NNOTE%d", i), text
		note.Rect.Y = top + 140 + int32(i)*18
		kept = append(kept, note)
	}
	window.Gadgets = kept
	return nil
}

var nanolatheFPSChoices = [...]int{30, 60, 120}

func (g *gameShell) syncNanolatheOptions() {
	if optionsState == nil || optionsState.page != "nanolathe" || optionsPanel == nil {
		return
	}
	optionsPanel.SetStageAt(optionsPanel.Index("NRENDER"), boolInt(g.presentation.Renderer == "modern"))
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
	default:
		return false
	}
	g.setPresentation(p)
	g.syncNanolatheOptions()
	return true
}
