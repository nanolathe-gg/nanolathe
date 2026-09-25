//go:build retail

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// TestModsScreenCapture renders the main menu, the Mods & Mutators screen and
// the Get more mods dialog to PNGs for review. A review diagnostic: it runs
// only when NANOLATHE_MODS_CAPTURE names an output directory. Set
// NANOLATHE_MODS_ZIP to install a mod zip into a scratch library first, and
// NANOLATHE_MOD_CATALOG to a local catalogue to populate the dialog.
func TestModsScreenCapture(t *testing.T) {
	out := os.Getenv("NANOLATHE_MODS_CAPTURE")
	if out == "" {
		t.Skip("NANOLATHE_MODS_CAPTURE is unset")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if zip := os.Getenv("NANOLATHE_MODS_ZIP"); zip != "" {
		devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err := runInstallMod(Options{Root: retail, InstallMod: zip}, devnull); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{Root: retail, Mod: os.Getenv("NANOLATHE_MODS_SELECT"), ModSet: os.Getenv("NANOLATHE_MODS_SELECT") != ""}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
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
	defer func() { clPtr = saved }()
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	shell.openMenu(modeMenuMain)
	capture := func(name string) {
		img := cl.ComposeFrame()
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	capture("main")
	if err := shell.openModsScreen(); err != nil {
		t.Fatal(err)
	}
	if len(modsUI.installed) > 0 {
		shell.selectModsRow(1)
	}
	// Drive the Mutators dialog the way a player would: Build speed up twice,
	// Build cost down twice, then Sight up once.
	shell.activateModsGadget("MUTEDIT")
	if mutatorsUI == nil {
		t.Fatal("Change... opened no Mutators dialog")
	}
	capture("mutators-open")
	press := func(key string, button string, times int) {
		for i, row := range mutatorsUI.rows {
			if row.key == key {
				shell.selectMutatorRow(i)
			}
		}
		for i := 0; i < times; i++ {
			shell.activateModsGadget(button)
		}
	}
	press("buildSpeed", "MUTRAISE", 2)
	press("buildCost", "MUTLOWER", 2)
	press("sight", "MUTRAISE", 1)
	capture("mutators")
	if os.Getenv("NANOLATHE_MODS_ALL") != "" {
		press("health", "MUTRAISE", 2)
		press("damage", "MUTLOWER", 1)
		press("radar", "MUTRAISE", 3)
		press("areaOfEffect", "MUTRAISE", 2)
		capture("mutators-all")
	}
	shell.activateModsGadget("LOAD")
	capture("mods")
	shell.opts.Mutators = modsUI.mutators
	if os.Getenv("NANOLATHE_MOD_CATALOG") != "" {
		shell.activateModsGadget("GETMORE")
		for i := 0; i < 100; i++ {
			shell.pollModsFetch()
			modsFetchUI.mu.Lock()
			fetched := modsFetchUI.status != "Fetching the catalogue..."
			modsFetchUI.mu.Unlock()
			if fetched {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		shell.pollModsFetch()
		shell.refreshModsFetch()
		capture("getmore")
	}
	shell.loading = newLoadingState("Comet Catcher")
	shell.frontend.SetMode(modeLoading)
	capture("loading")
}

// TestModsHotReloadSwitchesContent applies a mod from the Mods & Mutators
// screen and steps the window loop once: the host must swap in a fresh shell
// mounted on the base install plus that mod, then switch back to the original
// game the same way (docs/DESIGN_MODS_MUTATORS.md §4.4). It needs a
// ProTA package directory in NANOLATHE_MOD_ROOTS_PROTA.
func TestModsHotReloadSwitchesContent(t *testing.T) {
	prota := os.Getenv("NANOLATHE_MOD_ROOTS_PROTA")
	if prota == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset")
	}
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err := runInstallMod(Options{Root: retail, InstallMod: filepath.SplitList(prota)[0]}, devnull); err != nil {
		t.Fatal(err)
	}
	cs, err := openContent(Options{Root: retail})
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: cs.root, Roots: cs.roots, ContentProfile: cs.profile}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.attachSettings()
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	clPtr = cl
	defer func() { clPtr = saved }()
	host := &shellHost{shell: shell}
	if err := bindShellContent(shell, cl); err != nil {
		t.Fatal(err)
	}
	switchTo := func(row int) {
		t.Helper()
		before := host.shell
		if err := before.openModsScreen(); err != nil {
			t.Fatal(err)
		}
		before.selectModsRow(row)
		before.applyModsScreen()
		if pendingContentReload == nil {
			t.Fatal("applying a different mod requested no reload")
		}
		host.step(1.0/30, cl)
		if host.shell == before {
			t.Fatalf("reload kept the old shell; notice %q", before.cs.modNotice)
		}
		if modsUI != nil || modsPanel != nil {
			t.Fatal("the old shell's Mods screen survived the reload")
		}
	}
	switchTo(1)
	if host.shell.cs.mod == nil || host.shell.cs.profile != "prota" {
		t.Fatalf("after switching to ProTA: mod %v, profile %q", host.shell.cs.mod, host.shell.cs.profile)
	}
	if dir := os.Getenv("NANOLATHE_MODS_CAPTURE"); dir != "" {
		img := cl.ComposeFrame()
		f, _ := os.Create(filepath.Join(dir, "reloaded-prota.png"))
		_ = png.Encode(f, img)
		f.Close()
	}
	switchTo(0)
	if host.shell.cs.mod != nil || host.shell.cs.profile != "retail" {
		t.Fatalf("after switching back: mod %v, profile %q", host.shell.cs.mod, host.shell.cs.profile)
	}
}

// TestFailedModSwitchChangesNothing applies a switch to a mod whose cursor
// art does not parse, so the new shell builds and then fails to bind. The
// reload is transactional (docs/DESIGN_MODS_MUTATORS.md §4.4): the running
// shell keeps its gameplay, mutators, mod choice and presentation, the
// settings file is not written, and the failure is shown on the Mods &
// Mutators screen the player applied it from.
func TestFailedModSwitchChangesNothing(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, settingsPath)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installBadCursorMod(t, retail)
	cs, err := openContent(Options{Root: retail})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	shell, err := newGameShell(Options{Root: cs.root, Roots: cs.roots, ContentProfile: cs.profile}, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.attachSettings()
	shell.setGameplay(gameplay.Strict31)
	shell.saveSettings()
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	clPtr = cl
	defer func() { clPtr = saved }()
	host := &shellHost{shell: shell}
	if err := bindShellContent(shell, cl); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	presentation, modSetting := shell.presentation, shell.modSetting

	if err := shell.openModsScreen(); err != nil {
		t.Fatal(err)
	}
	defer shell.closeModsScreen()
	shell.selectModsRow(1)
	if err := modsUI.mutators.SetFactor("health", content.MutatorSteps[len(content.MutatorSteps)-1]); err != nil {
		t.Fatal(err)
	}
	modsUI.usePreset = true
	shell.applyModsScreen()
	if pendingContentReload == nil || pendingContentReload.gameplay != gameplay.Community39 || pendingContentReload.controls != "community" {
		t.Fatalf("the reload request does not carry the pending selection: %+v", pendingContentReload)
	}
	check := func(when string) {
		t.Helper()
		if after, err := os.ReadFile(settingsPath); err != nil || string(after) != string(before) {
			t.Fatalf("%s: the settings file was written", when)
		}
		if shell.gameplay != gameplay.Strict31 || !shell.opts.Mutators.IsZero() || shell.modSetting != modSetting || shell.presentation != presentation {
			t.Fatalf("%s: the running shell changed: gameplay %s, mutators %q, mod %+v", when, shell.gameplay, shell.opts.Mutators.String(), shell.modSetting)
		}
	}
	check("before the reload")
	host.step(1.0/30, cl)
	if host.shell != shell {
		t.Fatal("a failed reload swapped the shell")
	}
	check("after the failed reload")
	if modsUI == nil || !strings.HasPrefix(modsUI.notice, "Mod switch failed") {
		t.Fatal("the failure is not shown on the Mods & Mutators screen")
	}
	if _, err := shell.cs.fs.Stat("gamedata/sidedata.tdf"); err != nil {
		t.Fatalf("the running content was closed: %v", err)
	}
	if cl.Cursors() == nil {
		t.Fatal("the client's cursors were not restored")
	}
}

// installBadCursorMod installs "badcursors", a mod that passes the install
// check but whose cursor art does not parse, so a shell built on it cannot
// bind.
func installBadCursorMod(t *testing.T, retail string) {
	t.Helper()
	pack := t.TempDir()
	meta := `{"schema":1,"id":"badcursors","name":"Bad Cursors","version":"1","minimumGameplay":"community-3.9","controls":"community"}`
	for name, body := range map[string]string{modlibrary.MetadataFile: meta, client.CursorGAFPath: "not a GAF bank"} {
		path := filepath.Join(pack, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()
	if err := runInstallMod(Options{Root: retail, InstallMod: pack}, devnull); err != nil {
		t.Fatal(err)
	}
}

// TestSavedModThatFailsToBindStartsWithoutIt: start-up never fails because of
// the saved mod (docs/DESIGN_MODS_MUTATORS.md §4.3). A saved mod whose shell
// builds but cannot bind starts the game with no mod, a main-menu notice
// naming it, and the saved choice kept; the same mod named by --mod is an
// error.
func TestSavedModThatFailsToBindStartsWithoutIt(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installBadCursorMod(t, retail)
	stored := settings.Defaults()
	stored.Mod = settings.ModSelection{ID: "badcursors"}
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	defer func() { clPtr = saved }()
	start := func(launch Options) (*gameShell, error) {
		t.Helper()
		cs, err := openContent(launch)
		if err != nil {
			t.Fatal(err)
		}
		if cs.mod == nil || cs.mod.ID != "badcursors" {
			t.Fatalf("the mod was not selected: %+v", cs.mod)
		}
		opts := launch
		opts.Root, opts.Roots, opts.ContentProfile = cs.root, cs.roots, cs.profile
		shell, err := startWindowedShell(launch, opts, cs, cl)
		if err != nil {
			_ = cs.Close()
		}
		return shell, err
	}
	shell, err := start(Options{Root: retail})
	if err != nil {
		t.Fatalf("the saved mod stopped the start: %v", err)
	}
	defer shell.cs.Close()
	if shell.cs.mod != nil || !strings.Contains(shell.cs.modNotice, "Bad Cursors 1") {
		t.Fatalf("fallback mod %+v, notice %q", shell.cs.mod, shell.cs.modNotice)
	}
	if shell.modSetting.ID != "badcursors" {
		t.Fatalf("the saved choice was not kept: %+v", shell.modSetting)
	}
	if _, err := start(Options{Root: retail, Mod: "badcursors", ModSet: true}); err == nil {
		t.Fatal("--mod naming a mod that cannot bind started")
	}
}

// TestMutatorsDialogListsTheCatalogOnOpen locks the first frame of the
// Mutators dialog: opening it from the Mods & Mutators screen must fill its
// list with the grouped catalogue at once, not the mods list the screen
// underneath refreshes (docs/DESIGN_MODS_MUTATORS.md §8.2).
func TestMutatorsDialogListsTheCatalogOnOpen(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cs, err := openContent(Options{Root: retail})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	shell, err := newGameShell(Options{Root: cs.root, Roots: cs.roots}, cs)
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.openModsScreen(); err != nil {
		t.Fatal(err)
	}
	defer shell.closeModsScreen()
	shell.activateModsGadget("MUTEDIT")
	if mutatorsPanel == nil || shell.activePanel() != mutatorsPanel {
		t.Fatal("Change... did not open the Mutators dialog on top")
	}
	defer shell.closeMutatorsDialog(false)
	items, selected, _, ok := mutatorsPanel.ListValues("MAPNAMES")
	if !ok || len(items) != len(mutatorsUI.rows) {
		t.Fatalf("dialog list has %d rows, want the %d catalogue rows", len(items), len(mutatorsUI.rows))
	}
	for i, row := range mutatorsUI.rows {
		if items[i] != row.text {
			t.Fatalf("row %d = %q, want %q", i, items[i], row.text)
		}
	}
	if mutatorsUI.rows[selected].key == "" {
		t.Fatalf("the selection rests on heading row %d", selected)
	}
	for _, name := range []string{"MUTRESETALL", "MUTDEFAULT"} {
		if i := mutatorsPanel.Window.GadgetIndex(name); i < 0 || mutatorsPanel.Window.Gadgets[i].GrayedOut&1 == 0 {
			t.Fatalf("%s is live with no mutator set (index %d)", name, i)
		}
	}
	if mods, _, _, _ := modsPanel.ListValues("MAPNAMES"); len(mods) == 0 || mods[0] != "Total Annihilation" {
		t.Fatalf("the Mods screen's own list was disturbed: %q", mods)
	}
}
