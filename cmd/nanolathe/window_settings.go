package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Host window policy is independent of the authored logical canvases.
// See DESIGN_PRESENTATION_CLIENT §2.1.
func startupFullscreen(opts Options, saved bool) bool {
	if opts.FullscreenSet {
		return opts.Fullscreen
	}
	return saved
}

func (g *gameShell) windowOptions() ebitenapp.RunOptions {
	options := windowRunOptions(g.opts)
	g.fullscreen = startupFullscreen(g.opts, g.fullscreen)
	options.Fullscreen = g.fullscreen
	options.PresentationSettings = func() (ebitenapp.RendererMode, int) { return rendererMode(g.opts), g.presentation.FPS }
	// The Enhanced effect switches are polled the same way, so an options-page
	// edit previews on the next update (DESIGN_GPU_RENDERER §30).
	options.Effects = func() drawlist.Effects { return presentationEffects(g.presentation) }
	options.RendererChanged = g.rendererChanged
	g.commitWindowSize()
	options.WindowSize = func() (int, int) { return g.windowSize.W, g.windowSize.H }
	options.FullscreenChanged = func(value bool) {
		g.fullscreen = value
		if g.settingsWritable {
			saveFullscreenSetting(value)
		}
	}
	return options
}

// commitWindowSize applies the chosen resolution after the options root closes
// through OK. Keeping pending slider edits out of the adapter prevents a resize
// from moving the pointer relative to its captured widget
// (DESIGN_PRESENTATION_CLIENT §2.1).
func (g *gameShell) commitWindowSize() {
	g.windowSize = retailDisplayMode{g.display.Width, g.display.Height}
}

// A host fullscreen toggle is independent of the options transaction. Read the
// saved block so it cannot persist pending resolution or other page edits.
func saveFullscreenSetting(value bool) {
	current, err := settings.Load()
	if err == nil {
		current.Fullscreen = value
		err = current.Save()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}
