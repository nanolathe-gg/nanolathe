package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestFullscreenFlagOverridesOnlyWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		saved, want bool
	}{
		{nil, true, true}, {nil, false, false},
		{[]string{"--fullscreen"}, false, true},
		{[]string{"--fullscreen=false"}, true, false},
	} {
		opts, err := parseFlags(tc.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if got := startupFullscreen(opts, tc.saved); got != tc.want {
			t.Fatalf("%v saved=%v: got %v", tc.args, tc.saved, got)
		}
	}
}

func TestWindowPreferencesPreserveShellAndDirectSettings(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	prefs := settings.Defaults()
	prefs.Fullscreen = true
	prefs.Display.Width, prefs.Display.Height = 1024, 768
	if err := prefs.Save(); err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{}
	shell.applySettings(prefs)
	shell.settingsWritable = true
	options := shell.windowOptions()
	if !options.Fullscreen {
		t.Fatal("saved fullscreen lost")
	}
	if w, h := options.WindowSize(); w != 1024 || h != 768 {
		t.Fatalf("window %dx%d", w, h)
	}
	shell.display.Width, shell.display.Height = 800, 600
	if w, h := options.WindowSize(); w != 1024 || h != 768 {
		t.Fatal("pending slider edit resized the window")
	}
	options.FullscreenChanged(false)
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Fullscreen || saved.Display.Width != 1024 {
		t.Fatal("fullscreen toggle persisted the pending resolution")
	}
	shell.commitWindowSize()
	shell.saveSettings()
	if w, h := options.WindowSize(); w != 800 || h != 600 {
		t.Fatal("committed resolution did not resize the window")
	}
	saved, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Display.Width != 800 || saved.Display.Height != 600 {
		t.Fatal("committed resolution was not saved")
	}
	direct := directWindowOptions(Options{}, saved)
	saved.ScrollSpeed = 42
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	direct.FullscreenChanged(true)
	saved, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Fullscreen || saved.ScrollSpeed != 42 {
		t.Fatal("fullscreen overwrote a later battle setting")
	}
}

func TestDirectBattleViewportMatchesSelectedCanvasBeforeZoom(t *testing.T) {
	cl, err := client.New(client.Options{Width: 1024, Height: 768})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		// A fresh camera is constructed at the authored size on both startup
		// and restart. The host must adopt the selected canvas before zoom.
		b := &battleSession{cam: &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}}
		fitDirectBattleViewport(cl, b)
		if b.cam.ViewW != 1024 || b.cam.ViewH != 768 {
			t.Fatal("direct camera retained authored viewport")
		}
		applyEntryZoom(Options{Zoom: camera.ViewScaleDetail}, b)
		if w, h := b.cam.EffectiveView(); w != 512 || h != 384 {
			t.Fatalf("zoom viewport = %dx%d", w, h)
		}
	}
}
