package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
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
	saved.ScrollSpeed = 42
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	saveFullscreenSetting(true)
	saved, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Fullscreen || saved.ScrollSpeed != 42 {
		t.Fatal("fullscreen overwrote a later battle setting")
	}
}

func TestEntryZoomAppliesToTheSelectedCanvas(t *testing.T) {
	for i := 0; i < 2; i++ {
		// The entry camera has already adopted the selected canvas by the time
		// the start-up zoom factor is applied [07 "The loading screen"].
		b := &battleSession{cam: &camera.Camera{ViewW: 1024, ViewH: 768, MapW: 4096, MapH: 4096}}
		applyEntryZoom(Options{Zoom: camera.ZoomMax}, b)
		if w, h := b.cam.EffectiveView(); w != 512 || h != 384 {
			t.Fatalf("zoom viewport = %dx%d", w, h)
		}
	}
}
