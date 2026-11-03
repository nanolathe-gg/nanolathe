package main

import (
	"fmt"
	"os"
	"time"

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
	return windowOptionsFor(func() *gameShell { return g })
}

// windowOptions is the menu path's adapter: every callback reaches the shell
// the host holds when it runs, so after an in-process content reload the
// window polls the new shell rather than the start-up one
// (docs/DESIGN_MODS_MUTATORS.md §4.4).
func (h *shellHost) windowOptions() ebitenapp.RunOptions {
	return windowOptionsFor(func() *gameShell { return h.shell })
}

// windowOptionsFor builds the platform adapter's options from the shell
// current returns, reading it again inside each callback.
func windowOptionsFor(current func() *gameShell) ebitenapp.RunOptions {
	g := current()
	options := windowRunOptions(g.opts)
	g.fullscreen = startupFullscreen(g.opts, g.fullscreen)
	options.Fullscreen = g.fullscreen
	options.Monitor = g.monitor
	options.IdleAllowed = func() bool {
		g := current()
		return g.frontend != nil && g.frontend.Mode == modeBattle && g.battle != nil && g.battle.postBattle == nil
	}
	options.IdleService = func(focused bool) time.Duration {
		pumpAudio(time.Now())
		if owner := current().audioOwner; focused && owner != nil {
			serviceMusic(owner)
			if delay := owner.MusicServiceDelay(); delay > 0 {
				return min(time.Second, delay)
			}
		}
		// Reap completed device buffers without stepping or redrawing battle.
		return time.Second
	}
	options.PresentationSettings = func() (ebitenapp.RendererMode, int) {
		g := current()
		return rendererMode(g.opts), g.presentation.FPS
	}
	// The Enhanced effect switches are polled the same way, so an options-page
	// edit previews on the next update (DESIGN_GPU_RENDERER §30).
	options.Effects = func() drawlist.Effects {
		g := current()
		applyEffectStrengths(clPtr, g.presentation)
		return presentationEffects(g.presentation)
	}
	options.ShowFPS = func() bool {
		g := current()
		return g.battle != nil && g.battle.fpsShown() && g.battle.postBattle == nil
	}
	options.RendererChanged = func(mode ebitenapp.RendererMode) { current().rendererChanged(mode) }
	g.commitWindowSize()
	options.WindowSize = func() (int, int) {
		g := current()
		return g.windowSize.W, g.windowSize.H
	}
	options.FullscreenChanged = func(value bool) {
		g := current()
		g.fullscreen = value
		if !value {
			// The host can restore the pre-fullscreen position on another
			// display; keep the selected screen when returning to a window.
			ebitenapp.SelectMonitor(g.monitor)
		}
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
