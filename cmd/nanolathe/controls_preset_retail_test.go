//go:build retail

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// presetRetailShell opens a windowed-style shell on opts with a client bound
// for captures, with its settings attached and writable.
func presetRetailShell(t *testing.T, opts Options) (*gameShell, *client.Client) {
	t.Helper()
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	opts.Root, opts.Roots = cs.root, cs.roots
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.attachSettings()
	shell.enforceModGameplayMinimum()
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = saved })
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	return shell, cl
}

func presetCapture(t *testing.T, cl *client.Client, name string) {
	t.Helper()
	dir := os.Getenv("NANOLATHE_MODS_CAPTURE")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, cl.ComposeFrame()); err != nil {
		t.Fatal(err)
	}
}

// TestProTARecommendedSettingsAreOfferedOnce installs the ProTA package as a
// metadata-less local mod, as a drop or --install-mod does. Its content
// profile supplies the preset and the gameplay minimum (§4.3, §4.5): the Mods
// & Mutators screen offers the preset when switching to it, and a start
// with --mod offers it once on the main menu, remembered in the settings.
// The loading screen names the unit limit ProTA's feature table sets.
func TestProTARecommendedSettingsAreOfferedOnce(t *testing.T) {
	prota := os.Getenv("NANOLATHE_MOD_ROOTS_PROTA")
	if prota == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset")
	}
	retail := testsupport.RetailRoot(t)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, settingsPath)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Cleanup(func() { applyRetailAudioOptions(settings.DefaultAudio()) })
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()
	if err := runInstallMod(Options{Root: retail, InstallMod: filepath.SplitList(prota)[0]}, devnull); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("NANOLATHE_MODS_CAPTURE"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Switching from the Mods & Mutators screen offers the preset.
	shell, cl := presetRetailShell(t, Options{Root: retail})
	shell.openMenu(modeMenuMain)
	shell.pollControlsOffer()
	if controlsOfferUI != nil {
		t.Fatal("the original game offered a preset")
	}
	if err := shell.openModsScreen(); err != nil {
		t.Fatal(err)
	}
	shell.setGameplay(gameplay.Strict31)
	shell.selectModsRow(1)
	if !modsPanel.ActiveOf("PRESET") {
		t.Fatal("the local ProTA package's preset is not offered on the Mods & Mutators screen")
	}
	presetCapture(t, cl, "mods-preset")
	shell.applyModsScreen()
	request := pendingContentReload
	pendingContentReload = nil
	if request == nil || request.controls != "community" || request.offered == "" || request.gameplay != gameplay.Community39 {
		t.Fatalf("the switch request = %+v, want the preset, the offer and the Community 3.9 minimum", request)
	}
	shell.closeModsScreen()

	// A start that names the mod on the command line offers it on the main
	// menu instead, once.
	shell, cl = presetRetailShell(t, Options{Root: retail, Mod: request.offered, ModSet: true})
	if shell.cs.mod == nil || shell.cs.mod.Controls != "community" || shell.cs.mod.MinimumGameplay != string(gameplay.Community39) {
		t.Fatalf("the mounted local package = %+v, gameplay %s", shell.cs.mod, shell.gameplay)
	}
	shell.openMenu(modeMenuMain)
	shell.pollControlsOffer()
	if controlsOfferUI == nil || controlsOfferUI.key != request.offered {
		t.Fatal("the main menu did not offer the running mod's preset")
	}
	presetCapture(t, cl, "offer")
	shell.activateGadget("LOAD")
	if controlsOfferUI != nil {
		t.Fatal("Apply did not close the offer")
	}
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ControlsWereOffered(stored.ControlsOffered, request.offered) || stored.SwitchAlt != 1 || stored.Presentation.CommunitySelection != 1 || stored.Audio.SoundMode != settings.SoundMode3D {
		t.Fatalf("the accepted preset was not saved: offered %q, switchAlt %d", stored.ControlsOffered, stored.SwitchAlt)
	}
	shell.openMenu(modeMenuMain)
	shell.pollControlsOffer()
	if controlsOfferUI != nil {
		t.Fatal("the preset was offered twice")
	}

	shell.loading = newLoadingState("Comet Catcher")
	shell.frontend.SetMode(modeLoading)
	if line := shell.loadingSelectionLines()[0]; !strings.Contains(line, "Unit limit 1500 (set by ") {
		t.Fatalf("loading line %q does not name the effective unit limit", line)
	}
	presetCapture(t, cl, "loading")
	shell.setGameplay(gameplay.Strict31)
	shell.loading = newLoadingState("Comet Catcher")
	if line := shell.loadingSelectionLines()[0]; strings.Contains(line, "set by") {
		t.Fatalf("Strict 3.1 ignores the feature table, but the loading line reads %q", line)
	}
}
