package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/client"
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

func directWindowOptions(opts Options, preferences settings.Settings) ebitenapp.RunOptions {
	options := windowRunOptions(opts)
	options.Fullscreen = startupFullscreen(opts, preferences.Fullscreen)
	options.WindowSize = func() (int, int) {
		return preferences.Display.Width, preferences.Display.Height
	}
	options.FullscreenChanged = saveFullscreenSetting
	return options
}

// Direct entry and restart have no shell adoption pass to square the camera
// with the selected logical surface [07 "The loading screen"]. Do this before
// applying view scale, so centering and clamp bounds use the battle viewport.
func fitDirectBattleViewport(cl *client.Client, b *battleSession) {
	if cl == nil || b == nil || b.cam == nil {
		return
	}
	w, h := cl.Size()
	b.cam.ViewW, b.cam.ViewH = int32(w), int32(h)
	b.cam.Clamp()
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
