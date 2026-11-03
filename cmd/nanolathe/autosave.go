package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Autosave, quick save and quick load (docs/DESIGN_SESSIONS_AI_SAVE.md §5,
// "Autosave, quick save and quick load"):
//
//   - leaving a live battle — the window's close button, or Exit Game or
//     Main Menu in the battle menu — first writes it to the Autosave slot;
//   - a live battle is also written to the Autosave slot at the player's
//     autosave interval of game time, ten minutes unless the Nanolathe
//     screen's Autosave interval card chose another or Off
//     (presentation.autosaveMinutes);
//   - Ctrl+F11 writes the Quicksave slot and Ctrl+F12 loads it.
//
// Each goes through the same bank and sidecar writer as Save Game, and only
// into a save directory the host selected (--save-dir), as the installer's
// launcher selects one: without it the save directory is SAVEGAME inside the
// game folder, which nothing writes unasked. Load Game opens with the newest
// save selected.

const (
	// autosaveName and quicksaveName are the slots' file names and list
	// descriptions.
	autosaveName  = "Autosave"
	quicksaveName = "Quicksave"
	// autosaveTicksPerMinute is a minute of game time at 30 ticks a second.
	autosaveTicksPerMinute = 60 * 30
)

// autosaveIntervalTicks is the player's periodic autosave interval in ticks
// of game time, or 0 when the periodic autosave is off.
func (g *gameShell) autosaveIntervalTicks() uint32 {
	minutes := min(g.presentation.AutosaveMinutes, settings.MaxAutosaveMinutes)
	if minutes <= settings.AutosaveOff {
		return 0
	}
	return uint32(minutes) * autosaveTicksPerMinute
}

// quickAction is a quick save or load the battle's keys asked for, run at the
// next shell step after the simulation join (serviceSaveSlots).
type quickAction uint8

const (
	quickNone quickAction = iota
	quickSave
	quickLoad
)

// slotSaveDir is the host-selected save directory, or "" when there is none.
func (g *gameShell) slotSaveDir() string {
	if g == nil {
		return ""
	}
	return g.opts.SaveDir
}

// autosaveAllowed reports whether a battle is live and the host selected a
// save directory: the battle is not ended, its result has not latched, and it
// is neither an online battle, a replay's playback nor a continued online
// replay. A Survival battle saves its director in the sidecar
// (DESIGN_SURVIVAL §11).
func (g *gameShell) autosaveAllowed() bool {
	if g.slotSaveDir() == "" || g.battle == nil {
		return false
	}
	b := g.battle
	if b.sess == nil || b.ended || b.multiplayerPersistence() || b.takeoverRefusal("SAVEGAME") != "" {
		return false
	}
	if cur, ok := b.currentSnapshot(); ok && cur.Result.Ended {
		return false
	}
	return true
}

// writeSlotSave writes the live battle to slot name in the host's save
// directory.
func (g *gameShell) writeSlotSave(name string) error {
	dir := g.slotSaveDir()
	path := session.RetailSavePath(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nanolathe: %s not written: %v: logical path %s, providers searched [save directory], expected a writable save directory", name, err, path)
	}
	return replaceSlotSave(path, func(staged string) error {
		if err := g.writeSaveWithSidecar(staged, func() error { return g.writeBattleSave(staged, name) }); err != nil {
			return err
		}
		// The retail writer ignores errors after opening its bank. Before an
		// automatic slot replaces a previous checkpoint, require the staged
		// bank to decode as a complete battle (DESIGN_SESSIONS_AI_SAVE §5).
		bank, err := save.Open(staged)
		if err != nil {
			return err
		}
		if _, err := save.DecodeBattleImage(bank); err != nil {
			return err
		}
		if _, present, err := save.ReadSidecar(staged); err != nil {
			return err
		} else if !present {
			return fmt.Errorf("nanolathe: %s not written: staged sidecar missing: logical path %s, providers searched [save directory], expected a complete bank and sidecar", name, staged)
		}
		return nil
	}, os.Rename)
}

// autosaveForExit writes a live battle to the Autosave slot. A failure is
// reported and never holds up the exit.
func (g *gameShell) autosaveForExit() {
	if !g.autosaveAllowed() {
		return
	}
	if err := g.writeSlotSave(autosaveName); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// serviceWindowClose answers the window's close button once: autosave, then
// the exit the window backend is waiting for. It runs after the simulation
// join, when the session may be read.
func (g *gameShell) serviceWindowClose(cl *client.Client) bool {
	if cl == nil || !cl.WindowClosing() || cl.ExitRequested() {
		return false
	}
	g.autosaveForExit()
	cl.RequestExit()
	return true
}

// handleQuickSaveKeys reads Ctrl+F11 (quick save) and Ctrl+F12 (quick load).
// Neither key has a Ctrl binding: plain F11 and F12 are a developer switch
// and the message-log clear, and Ctrl+Shift+F11 is the debug capture.
func (b *battleSession) handleQuickSaveKeys(kbd *input.KeyboardState, ctrlHeld bool) {
	if b.shell == nil || kbd == nil || !ctrlHeld || kbd.HasShift() || b.developer.quickkeysDisabled {
		return
	}
	if kbd.KeyDown(input.KeyF11) {
		b.shell.pendingQuick = quickSave
	}
	if kbd.KeyDown(input.KeyF12) {
		b.shell.pendingQuick = quickLoad
	}
}

// serviceSaveSlots runs a pending quick save or load and the periodic
// autosave. It runs after the simulation join, when the session may be read
// and the battle replaced.
func (g *gameShell) serviceSaveSlots() {
	action := g.pendingQuick
	g.pendingQuick = quickNone
	switch action {
	case quickSave:
		g.quicksave()
	case quickLoad:
		g.quickload()
	}
	g.periodicAutosave()
}

// saveNotice shows text on the battle's message lines.
func (g *gameShell) saveNotice(text string) {
	if g.battle != nil {
		g.battle.messageRing().PostSilent(text, 0, g.battle.currentTick())
	}
}

func (g *gameShell) quicksave() {
	switch {
	case g.battle == nil:
		return
	case g.battle.takeoverRefusal("SAVEGAME") != "":
		g.saveNotice(g.battle.takeoverRefusal("SAVEGAME"))
		return
	case g.battle.multiplayerPersistence():
		g.saveNotice("Saves and loads are unavailable in multiplayer games")
		return
	case g.slotSaveDir() == "":
		g.saveNotice("Quick save needs a save folder (--save-dir)")
		return
	case !g.autosaveAllowed():
		return
	}
	if err := g.writeSlotSave(quicksaveName); err != nil {
		fmt.Fprintln(os.Stderr, err)
		g.saveNotice("Quick save failed")
		return
	}
	g.saveNotice("Game saved")
}

func (g *gameShell) quickload() {
	if g.battle == nil {
		return
	}
	if g.battle.multiplayerPersistence() {
		g.saveNotice("Saves and loads are unavailable in multiplayer games")
		return
	}
	dir := g.slotSaveDir()
	path := session.RetailSavePath(dir, quicksaveName)
	if _, err := os.Stat(path); dir == "" || err != nil {
		g.saveNotice("No quick save to load")
		return
	}
	if err := g.loadRetailSavePath(path); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: loading %s: %v\n", path, err)
		reportRetailMessageError(g.showRetailMessage(loadFailureMessage(err)))
		return
	}
	g.saveNotice("Game loaded")
}

// periodicAutosave writes the Autosave slot once the player's interval of
// game time has passed since the last periodic autosave; a battle's clock
// starts when the shell first sees it, so a load or a new battle waits a full
// interval. A paused game's clock does not run, so it does not autosave.
//
// The interval is read at every step, measured from the clock's start, so a
// changed interval keeps the time already elapsed: a shorter one already
// passed saves at once, a longer one waits out the rest. Off writes nothing
// and keeps the clock's start, so choosing an interval again counts from the
// last periodic autosave.
func (g *gameShell) periodicAutosave() {
	b := g.battle
	if b == nil || !g.autosaveAllowed() {
		return
	}
	tick := b.currentTick()
	if g.autosaveBattle != b {
		g.autosaveBattle, g.autosaveFrom = b, tick
		return
	}
	every := g.autosaveIntervalTicks()
	if every == 0 || tick-g.autosaveFrom < every {
		return
	}
	g.autosaveFrom = tick
	if err := g.writeSlotSave(autosaveName); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	g.saveNotice("Game autosaved")
}
