package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Contracts of the autosave, quick save and quick load
// (docs/DESIGN_SESSIONS_AI_SAVE.md §5): every way out of a live battle writes
// the Autosave slot first, Survival included, and the load dialog opens on
// the newest save.

// startAutosaveBattle starts a direct battle that saves into a fresh
// directory.
func startAutosaveBattle(t *testing.T, survival bool) (*gameShell, *battleSession, *client.Client, string) {
	t.Helper()
	resetSaveLoadScreenState(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	prefs := settings.Defaults()
	prefs.Presentation.Arrival = 0
	if err := prefs.Save(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7, SaveDir: dir, Survival: survival}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	previous := clPtr
	t.Cleanup(func() { clPtr = previous })
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shell.teardownBattle(cl) })
	clPtr = cl
	for range 10 {
		autosaveStep(shell, cl)
	}
	return shell, shell.battle, cl, dir
}

// autosaveMillis drives the battles these tests step: one scaled tick per step,
// as the direct save/load test drives its battle.
var autosaveMillis = &shotMillisSource{}

// autosaveStep runs one host step of the shell with the battle's clock advanced
// by one tick.
func autosaveStep(g *gameShell, cl *client.Client) {
	if g.battle != nil {
		g.battle.millisSource = autosaveMillis
	}
	autosaveMillis.step++
	g.step(1.0/30, cl)
}

func TestLeavingALiveBattleWritesTheAutosave(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(*gameShell, *battleSession, *client.Client)
		left  func(*gameShell, *battleSession, *client.Client) bool
	}{
		{
			name:  "window close",
			leave: func(g *gameShell, _ *battleSession, cl *client.Client) { cl.NoteWindowClosing(); g.step(1.0/30, cl) },
			left:  func(_ *gameShell, _ *battleSession, cl *client.Client) bool { return cl.ExitRequested() },
		},
		{
			name: "exit game",
			leave: func(_ *gameShell, b *battleSession, cl *client.Client) {
				b.openBattleMenu()
				b.battleState().ShowExit()
				b.battleState().ShowConfirmation(false)
				b.activateBattleMenuButton("CHOICE1", cl)
			},
			left: func(_ *gameShell, _ *battleSession, cl *client.Client) bool { return cl.ExitRequested() },
		},
		{
			name: "main menu",
			leave: func(_ *gameShell, b *battleSession, cl *client.Client) {
				b.openBattleMenu()
				b.battleState().ShowExit()
				b.battleState().ShowConfirmation(true)
				b.activateBattleMenuButton("CHOICE1", cl)
			},
			left: func(_ *gameShell, b *battleSession, _ *client.Client) bool { return b.ended },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, b, cl, dir := startAutosaveBattle(t, false)
			tick := b.sess.Clock.GlobalTick
			tc.leave(g, b, cl)
			if !tc.left(g, b, cl) {
				t.Fatal("the battle was not left")
			}
			path := session.RetailSavePath(dir, autosaveName)
			summary, ok, err := save.ReadSummaryFile(path)
			if err != nil || !ok {
				t.Fatalf("no autosave bank: ok=%t err=%v", ok, err)
			}
			if summary.Description != autosaveName || summary.GameTime != int32(tick) {
				t.Fatalf("autosave %q at tick %d, want %q at %d", summary.Description, summary.GameTime, autosaveName, tick)
			}
			if _, ok, err := save.ReadSidecar(path); err != nil || !ok {
				t.Fatalf("no autosave sidecar: ok=%t err=%v", ok, err)
			}
		})
	}
}

// A Survival battle autosaves like any other, its director in the sidecar,
// and the autosave loads back as a Survival battle that returns to the
// Survival setup screen when it ends.
func TestSurvivalBattleAutosavesAndLoadsAsSurvival(t *testing.T) {
	g, b, cl, dir := startAutosaveBattle(t, true)
	if !b.sess.IsSurvival() {
		t.Fatal("not a Survival battle")
	}
	cl.NoteWindowClosing()
	g.step(1.0/30, cl)
	if !cl.ExitRequested() {
		t.Fatal("closing the window did not exit")
	}
	path := session.RetailSavePath(dir, autosaveName)
	sc, ok, err := save.ReadSidecar(path)
	if err != nil || !ok || len(sc.Survival) == 0 {
		t.Fatalf("no Survival director beside the autosave: ok=%t err=%v", ok, err)
	}
	g.lastBattleSurvival = false
	if err := g.loadRetailSavePath(path); err != nil {
		t.Fatalf("load the Survival autosave: %v", err)
	}
	if g.battle == nil || g.battle == b || !g.battle.sess.IsSurvival() || !g.lastBattleSurvival {
		t.Fatal("the autosave did not load as a Survival battle")
	}
}

func TestLoadDialogOpensOnTheNewestSave(t *testing.T) {
	g, _, _, dir := startAutosaveBattle(t, false)
	older := session.RetailSavePath(dir, "older")
	if err := g.writeSaveWithSidecar(older, func() error { return g.writeBattleSave(older, "older") }); err != nil {
		t.Fatal(err)
	}
	g.autosaveForExit()
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	if err := g.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil {
		t.Fatal(err)
	}
	defer g.closeSaveLoadScreen()
	entry, ok := saveLoadUI.SelectedEntry()
	if !ok || entry.Description != autosaveName || saveLoadUI.Selected() != len(saveLoadUI.Entries())-1 {
		t.Fatalf("load dialog selected %d %q of %d", saveLoadUI.Selected(), entry.Description, len(saveLoadUI.Entries()))
	}
}

// Ctrl+F11 asks for a quick save and Ctrl+F12 for a quick load; with Shift
// held, Ctrl+F11 stays the debug capture's.
func TestQuickSaveKeys(t *testing.T) {
	_, b, _, _ := startAutosaveBattle(t, false)
	for _, tc := range []struct {
		keys []input.Key
		want quickAction
	}{
		{[]input.Key{input.KeyCtrl, input.KeyF11}, quickSave},
		{[]input.Key{input.KeyCtrl, input.KeyF12}, quickLoad},
		{[]input.Key{input.KeyCtrl, input.KeyShift, input.KeyF11}, quickNone},
		{[]input.Key{input.KeyF11}, quickNone},
	} {
		b.shell.pendingQuick = quickNone
		pressKeys(b, tc.keys...)
		if b.shell.pendingQuick != tc.want {
			t.Errorf("keys %v asked for %d, want %d", tc.keys, b.shell.pendingQuick, tc.want)
		}
	}
}

// A quick save writes the Quicksave slot; a quick load later puts the battle
// back at the saved tick.
func TestQuickSaveThenQuickLoad(t *testing.T) {
	g, b, cl, dir := startAutosaveBattle(t, false)
	if b.sim != nil {
		t.Skip("the battle simulates asynchronously; the quick actions are driven between steps here")
	}
	g.pendingQuick = quickSave
	g.serviceSaveSlots()
	saved := b.sess.Clock.GlobalTick
	summary, ok, err := save.ReadSummaryFile(session.RetailSavePath(dir, quicksaveName))
	if err != nil || !ok || summary.Description != quicksaveName || summary.GameTime != int32(saved) {
		t.Fatalf("quick save %q at %d (ok=%t err=%v), want %q at %d", summary.Description, summary.GameTime, ok, err, quicksaveName, saved)
	}
	for range 60 {
		autosaveStep(g, cl)
	}
	if b.sess.Clock.GlobalTick <= saved {
		t.Fatal("the battle did not move on after the quick save")
	}
	g.pendingQuick = quickLoad
	g.serviceSaveSlots()
	if g.battle == nil || g.battle == b {
		t.Fatal("the quick load did not replace the battle")
	}
	if got := g.battle.sess.Clock.GlobalTick; got != saved {
		t.Fatalf("quick load resumed at tick %d, want the saved %d", got, saved)
	}
}

// The periodic autosave writes the Autosave slot once its interval of game
// time has passed, and not before.
func TestPeriodicAutosave(t *testing.T) {
	g, b, cl, dir := startAutosaveBattle(t, false)
	path := session.RetailSavePath(dir, autosaveName)
	autosaveStep(g, cl) // the clock starts when the shell first sees the battle
	if g.autosaveBattle != b {
		t.Fatal("the autosave clock did not start on the battle")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("autosaved before the interval passed")
	}
	if got := g.autosaveIntervalTicks(); got != 10*60*30 {
		t.Fatalf("default interval is %d ticks, want ten minutes", got)
	}
	startAutosaveClock(g, b, g.autosaveIntervalTicks()-1)
	for range 3 {
		autosaveStep(g, cl)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no autosave after the interval: %v", err)
	}
	if elapsed := b.currentTick() - g.autosaveFrom; elapsed > 3 {
		t.Fatalf("the clock was not restarted by the autosave: %d ticks elapsed", elapsed)
	}
}

// startAutosaveClock puts the periodic autosave's clock elapsed ticks into
// the battle.
func startAutosaveClock(g *gameShell, b *battleSession, elapsed uint32) {
	g.autosaveBattle, g.autosaveFrom = b, b.currentTick()-elapsed
}

// The player's interval decides when the periodic autosave writes
// (DESIGN_SESSIONS_AI_SAVE §5): Off never writes, another interval writes
// once that much game time has passed, and a changed interval keeps the time
// already elapsed.
func TestPeriodicAutosaveFollowsTheInterval(t *testing.T) {
	g, b, cl, dir := startAutosaveBattle(t, false)
	path := session.RetailSavePath(dir, autosaveName)
	const minute = autosaveTicksPerMinute
	autosaved := func() bool {
		t.Helper()
		_, err := os.Stat(path)
		if err == nil {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(save.SidecarPath(path))
		}
		return err == nil
	}
	steps := func(n int) {
		for range n {
			autosaveStep(g, cl)
		}
	}
	for _, tc := range []struct {
		name    string
		minutes int
		elapsed uint32 // game time on the clock before three steps
		want    bool
	}{
		{"off writes nothing however long", settings.AutosaveOff, 600 * minute, false},
		{"five minutes not yet", 5, 5*minute - 100, false},
		{"five minutes passed", 5, 5*minute - 1, true},
		{"ten minutes at five", 10, 5 * minute, false},
		{"thirty minutes not yet", 30, 29 * minute, false},
		{"thirty minutes passed", 30, 30 * minute, true},
	} {
		g.presentation.AutosaveMinutes = tc.minutes
		startAutosaveClock(g, b, tc.elapsed)
		from := g.autosaveFrom
		steps(3)
		if got := autosaved(); got != tc.want {
			t.Fatalf("%s: autosaved %t, want %t", tc.name, got, tc.want)
		}
		if !tc.want && g.autosaveFrom != from {
			t.Fatalf("%s: the clock moved without an autosave", tc.name)
		}
	}

	// Changing the interval mid-battle keeps the elapsed time: seven minutes
	// into a ten-minute interval, choosing five saves at once, and choosing
	// thirty after that waits a full thirty from that save.
	g.presentation.AutosaveMinutes = 10
	startAutosaveClock(g, b, 7*minute)
	steps(3)
	if autosaved() {
		t.Fatal("autosaved seven minutes into ten")
	}
	g.presentation.AutosaveMinutes = 5
	steps(3)
	if !autosaved() {
		t.Fatal("a shorter interval already passed did not autosave")
	}
	g.presentation.AutosaveMinutes = 30
	startAutosaveClock(g, b, 7*minute)
	steps(3)
	if autosaved() {
		t.Fatal("a longer interval autosaved early")
	}
	// Off and back keeps the clock too.
	g.presentation.AutosaveMinutes = settings.AutosaveOff
	startAutosaveClock(g, b, 40*minute)
	steps(3)
	if autosaved() {
		t.Fatal("autosaved while off")
	}
	g.presentation.AutosaveMinutes = 30
	steps(3)
	if !autosaved() {
		t.Fatal("turning the autosave back on did not count the elapsed time")
	}
}

// Without a host-selected save directory nothing is written implicitly: the
// retail default would be SAVEGAME inside the game folder.
func TestSaveSlotsNeedAHostSaveDirectory(t *testing.T) {
	g, b, cl, _ := startAutosaveBattle(t, false)
	root := t.TempDir()
	g.opts.SaveDir, g.opts.Root = "", root
	g.pendingQuick = quickSave
	g.step(1.0/30, cl)
	startAutosaveClock(g, b, g.autosaveIntervalTicks())
	g.step(1.0/30, cl)
	cl.NoteWindowClosing()
	g.step(1.0/30, cl)
	if !cl.ExitRequested() {
		t.Fatal("closing the window did not exit")
	}
	if entries, _ := os.ReadDir(retailSaveDir(root)); len(entries) != 0 {
		t.Fatalf("saves written without a host save directory: %d files", len(entries))
	}
}

// A dot in the host save directory — a Windows user name such as
// "first.last", or ~/.local on Linux — must not truncate the save slots:
// their banks stay inside --save-dir, where the load dialog lists them.
func TestSaveSlotsStayInADottedSaveDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "first.last", ".local", "saves")
	g := &gameShell{opts: Options{SaveDir: dir}}
	for _, path := range []string{session.RetailSavePath(g.slotSaveDir(), autosaveName), session.RetailSavePath(g.slotSaveDir(), quicksaveName)} {
		if filepath.Dir(path) != dir {
			t.Fatalf("slot bank %q is outside the save directory %q", path, dir)
		}
	}
}

// Directory-level replacement can succeed even when the old bank is read-only.
// Hosts that refuse it must keep both previous files instead.
func TestAutomaticSlotPreservesAReadOnlyBankAndItsSidecar(t *testing.T) {
	g, _, _, dir := startAutosaveBattle(t, false)
	path := session.RetailSavePath(dir, autosaveName)
	if err := g.writeSlotSave(autosaveName); err != nil {
		t.Fatal(err)
	}
	previousBank, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previousSidecar, err := os.ReadFile(save.SidecarPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o644)
	if err := g.writeSlotSave(autosaveName); err != nil {
		checkSlotTestFile(t, path, string(previousBank))
		checkSlotTestFile(t, save.SidecarPath(path), string(previousSidecar))
	}
	if _, ok, err := save.ReadSidecar(path); err != nil || !ok {
		t.Fatalf("replacement lost its sidecar: present=%t err=%v", ok, err)
	}
	if bank, err := save.Open(path); err != nil {
		t.Fatal(err)
	} else if _, err := save.DecodeBattleImage(bank); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("completed publication retained temporary files: %v, %v", entries, err)
	}
}
