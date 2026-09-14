package main

import (
	"fmt"
	"os"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The save and load dialogs live on the frontend panel stack as one authored
// window over whichever surface opened them [07 R-FE-01 §8]. The screen state
// is a process singleton for the same reason the client pointer is: there is
// exactly one shell, and the shell struct is owned by another unit's file.
var (
	saveLoadUI     *saveLoadScreen
	saveLoadPanel  *ui.Panel
	saveLoadAssets *retailPanelAssets
)

const (
	// The two verbatim diagnostics the load screen raises, at the authored
	// width 320 [08 R-SAVE-02 §2] [07 R-FE-01 §9].
	retailNoSavedGamesMessage = "There are no saved games to choose from"
	retailInvalidSaveMessage  = "Invalid savegame file"
)

// retailSaveLoadGUI is the one authored file both directions are built from
// [08 R-SAVE-02 §1].
const retailSaveLoadGUI = "guis/loadgame.gui"

// saveLoadDir is the directory this shell's screens address.
func (g *gameShell) saveLoadDir() string {
	if g == nil {
		return retailSaveDir("")
	}
	return retailSaveDir(g.opts.Root)
}

// openSaveLoadScreen builds the authored window in the requested direction and
// pushes it over the current surface. The save direction creates `SAVEGAME\`
// first; the load direction refuses an empty list with the authored message
// and opens nothing [08 R-SAVE-02 §1] [08 R-SAVE-02 §2] [07 R-FE-01 §8].
func (g *gameShell) openSaveLoadScreen(mode saveLoadMode, source saveLoadSource) error {
	if g == nil || g.cs == nil {
		return fmt.Errorf("nanolathe: save/load screen: no mounted content: logical path %s, providers searched [], expected the authored save dialog", retailSaveLoadGUI)
	}
	dir := g.saveLoadDir()
	if mode == saveScreenMode {
		// Retail creates the directory when the save screen opens
		// [07 R-FE-01 §8]. A failure here is reported through the ordinary
		// message box rather than silently producing an empty list.
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	screen := newSaveLoadScreen(mode, dir, source)
	if mode == loadScreenMode && len(screen.Entries()) == 0 {
		// The load screen is closed again and the message shown; the save
		// screen shows no message for an empty list [08 R-SAVE-02 §2].
		return g.showRetailMessage(retailNoSavedGamesMessage)
	}
	// Reuse the side compiler without constructing a battle catalog. The
	// dialog owns its display-name copy until it closes [08 R-SAVE-02 §3].
	sides, err := content.CompileSides(g.cs.fs)
	if err != nil {
		return retailFrontendAssetError(g.cs, "save dialog side table", "gamedata/sidedata.tdf", "the compiled side definitions", err)
	}
	panel, err := g.loadSaveLoadPanel(mode)
	if err != nil {
		return err
	}
	screen.sideNames = retailSideDisplayNames(sides)
	saveLoadUI = screen
	saveLoadPanel = panel
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	g.refreshSaveLoadPanel()
	g.focusSaveLoadNameEditor()
	return nil
}

// focusSaveLoadNameEditor performs the save dialog's authored initial focus.
// The load direction has GAMENAME hidden and therefore owns no text capture
// [07 R-FE-01 §8][07 R-WGT-01 §6].
func (g *gameShell) focusSaveLoadNameEditor() {
	if !g.saveLoadPanelActive() || saveLoadUI.Mode() != saveScreenMode || saveLoadPanel.Window == nil {
		return
	}
	if i := saveLoadPanel.Window.GadgetIndex("GAMENAME"); i >= 0 && saveLoadPanel.Window.Gadgets[i].Kind == gui.KindTextBox {
		saveLoadPanel.FocusEditor(i)
	}
}

// openSaveLoadScreenReporting is the caller-facing form: a construction
// failure is surfaced through the ordinary message box rather than dropped.
func (g *gameShell) openSaveLoadScreenReporting(mode saveLoadMode, source saveLoadSource) {
	if err := g.openSaveLoadScreen(mode, source); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

// loadSaveLoadPanel parses `LOADGAME.GUI` once and re-reads only the backdrop
// when the direction changes [08 R-SAVE-02 §1].
func (g *gameShell) loadSaveLoadPanel(mode saveLoadMode) (*ui.Panel, error) {
	window, err := g.cs.loadGUI(retailSaveLoadGUI)
	if err != nil {
		return nil, retailFrontendAssetError(g.cs, "retail save dialog GUI unavailable", retailSaveLoadGUI, "the authored save/load window", err)
	}
	background, err := formats.LoadPCXFile(g.cs.fs, mode.backdrop())
	if err != nil {
		return nil, retailFrontendAssetError(g.cs, "retail save dialog bitmap", mode.backdrop(), "the authored save/load backdrop", err)
	}
	saveLoadAssets = &retailPanelAssets{window: window, background: background}
	// LOADGAME is a fresh authored open. Build before the panel captures its
	// runtime state [07 R-WGT-01 §3].
	g.installRetailWindowButtonArt(window, nil)
	panel := ui.NewPanel(window)
	if panel == nil {
		return nil, retailFrontendAssetError(g.cs, "retail save dialog GUI unavailable", retailSaveLoadGUI, "the authored save/load window", nil)
	}
	return panel, nil
}

// closeSaveLoadScreen pops the dialog and frees the name, description, side
// name and radar buffers, which is what `CANCEL` does [08 R-SAVE-02 §1].
func (g *gameShell) closeSaveLoadScreen() {
	if g != nil && saveLoadPanel != nil && g.frontend.Panels.Top() == saveLoadPanel {
		g.frontend.Panels.Pop()
	}
	saveLoadUI = nil
	saveLoadPanel = nil
	saveLoadAssets = nil
}

// saveLoadPanelActive reports whether the authored dialog is the active panel.
func (g *gameShell) saveLoadPanelActive() bool {
	return g != nil && saveLoadUI != nil && saveLoadPanel != nil && g.activePanel() == saveLoadPanel
}

// refreshSaveLoadPanel applies the hidden-gadget set, the slot list and the
// summary panel to the authored window [08 R-SAVE-02 §1] [08 R-SAVE-02 §3].
func (g *gameShell) refreshSaveLoadPanel() {
	if saveLoadUI == nil || saveLoadPanel == nil {
		return
	}
	panel := saveLoadPanel
	for _, name := range []string{"GAMES", "LOAD", "CANCEL", "SLIDER", "GAMENAME", "DELETE", "SaveGame", "LoadGame", "TITLE"} {
		panel.SetActive(name, true)
	}
	for _, name := range saveLoadUI.HiddenControls() {
		panel.SetActive(name, false)
	}
	// The save screen's title is the literal `Save Game`; the stock authored
	// file carries no `TITLE` gadget, so the write is inert there
	// [08 R-SAVE-02 §1].
	if saveLoadUI.Mode() == saveScreenMode {
		panel.SetText("TITLE", "Save Game")
	}
	g.setListItems("GAMES", saveLoadUI.Descriptions(), saveLoadUI.Selected())
	// Filling a nonempty list selects row zero [07 R-FE-02 §5]. Keep the
	// dialog's file selection aligned with that widget selection: clicking
	// the already-highlighted first row emits no change callback.
	if list := panel.ListAt(panel.Index("GAMES")); list != nil && len(saveLoadUI.Entries()) != 0 && saveLoadUI.Selected() != list.Selected() {
		saveLoadUI.Select(list.Selected())
	}
	panel.SetText("GAMENAME", saveLoadUI.Name())
	summary, ok := save.Summary{}, false
	if entry, has := saveLoadUI.SelectedEntry(); has {
		summary, ok = entry.Summary, true
	}
	for name, value := range retailSummaryPanelFields(summary, ok, g.retailSideNames()) {
		panel.SetText(name, value)
	}
}

// retailSideNames returns the dialog-owned display table [08 R-SAVE-02 §3].
func (g *gameShell) retailSideNames() []string {
	if saveLoadUI == nil {
		return nil
	}
	return saveLoadUI.sideNames
}

// retailSideDisplayNames preserves side ordinal and the first byte of each
// name, then adds 32 to each remaining byte with byte-width wrapping. This
// is not Unicode or conditional ASCII lowercasing [08 R-SAVE-02 §3].
// TODO(question): establish the packed-table index outcome when a suffix byte
// wraps to a terminator; the current host string retains that zero byte.
func retailSideDisplayNames(sides []*content.SideDef) []string {
	if len(sides) == 0 {
		return nil
	}
	names := make([]string, len(sides))
	for i, side := range sides {
		if side == nil || side.Name == "" {
			// TODO(question): empty compiled names make the retail packed-name
			// walk cross string boundaries. Establish the complete malformed
			// table outcome before replacing this host-policy empty entry.
			continue
		}
		name := []byte(side.Name)
		for j := 1; j < len(name); j++ {
			name[j] += 32
		}
		names[i] = string(name)
	}
	return names
}

// retailSummaryPanelFields renders the summary panel from the selected file's
// `Summary` account and nothing else. Every field defaults to the empty string
// when no entry is selected [08 R-SAVE-02 §3].
func retailSummaryPanelFields(summary save.Summary, selected bool, sideNames []string) map[string]string {
	fields := map[string]string{"GAMETYPE": "", "CAMPAIGN": "", "CAMPTEXT": "", "MISSION": "", "TIME": "", "SIDE": "", "DIFF": ""}
	if !selected {
		return fields
	}
	switch {
	case summary.Players == 0:
		fields["GAMETYPE"] = "???"
	case summary.Gametype == 1:
		fields["GAMETYPE"] = "Single"
	default:
		fields["GAMETYPE"] = fmt.Sprintf("Skirmish (%d players)", summary.Players)
	}
	if summary.Gametype == 1 {
		fields["CAMPAIGN"] = summary.Campaign
		fields["CAMPTEXT"] = summary.Campaign
		fields["MISSION"] = summary.Mission
	} else {
		fields["MISSION"] = summary.MapName
	}
	fields["TIME"] = retailSummaryTime(summary.GameTime)
	if index := int(summary.Side); index >= 0 && index < len(sideNames) {
		fields["SIDE"] = sideNames[index]
	} else {
		fields["SIDE"] = "???"
	}
	// Retail has no fourth row and no bounds check: the three labels are three
	// consecutive stack slots the summary writer fills immediately before
	// indexing them with the raw `Difficulty` value, so a value outside 0..2
	// formats whatever the adjacent stack holds. There is nothing to reproduce
	// — rendering no label is the deliberate divergence [08 R-SAVE-02 §3].
	switch summary.Difficulty {
	case 0:
		fields["DIFF"] = "Easy"
	case 1:
		fields["DIFF"] = "Medium"
	case 2:
		fields["DIFF"] = "Hard"
	}
	return fields
}

// retailSummaryTime formats the `Game Time` tick count as `%02d:%02d:%02d`
// with hours t/108000, minutes (t/1800) mod 60 and seconds (t/30) mod 60 —
// signed divisions truncating toward zero [08 R-SAVE-02 §3] [I3].
func retailSummaryTime(t int32) string {
	return fmt.Sprintf("%02d:%02d:%02d", t/108000, (t/1800)%60, (t/30)%60)
}

// activateSaveLoadGadget applies one authored control of the dialog. It
// reports whether the dialog consumed the name, so the shell's own screen
// dispatch does not also see it.
//
// The dialog's cue column, from the two direction callbacks [08 R-SAVE-02 §1]:
// `CANCEL` plays `Previous`, `DELETE` plays `SmallButton`, and the commit arm
// of either direction plays the small-button alias — the save callback spells
// it `smlbutton` and the load callback `SMLBUTTON`, which resolve to the same
// authored alias because lookup is case-insensitive [02 "Sound aliases"]. The
// two hidden direction toggles have no arm and so no cue.
func (g *gameShell) activateSaveLoadGadget(name string) bool {
	if !g.saveLoadPanelActive() {
		return false
	}
	switch saveLoadUI.Activate(name) {
	case saveLoadCommit:
		g.commitSaveLoadScreen()
	case saveLoadDelete:
		g.playMenuCue(cueDeleteSaveSlot)
		saveLoadUI.deleteSelected()
		g.refreshSaveLoadPanel()
	case saveLoadCancel:
		g.playMenuCue(cuePreviousScreen)
		g.closeSaveLoadScreen()
	case saveLoadToSave:
		saveLoadUI.SetMode(saveScreenMode)
		g.reopenSaveLoadPanel()
	case saveLoadToLoad:
		saveLoadUI.SetMode(loadScreenMode)
		g.reopenSaveLoadPanel()
	}
	return true
}

// reopenSaveLoadPanel re-reads the backdrop for the other direction and
// re-applies the control set to the same window [08 R-SAVE-02 §1].
func (g *gameShell) reopenSaveLoadPanel() {
	if saveLoadUI == nil || saveLoadAssets == nil {
		return
	}
	if background, err := formats.LoadPCXFile(g.cs.fs, saveLoadUI.Mode().backdrop()); err == nil {
		saveLoadAssets.background = background
	}
	g.refreshSaveLoadPanel()
	g.focusSaveLoadNameEditor()
}

// selectSaveLoadRow commits a `GAMES` list selection.
func (g *gameShell) selectSaveLoadRow(index int) {
	if !g.saveLoadPanelActive() {
		return
	}
	saveLoadUI.Select(index)
	g.refreshSaveLoadPanel()
}

// commitSaveLoadScreen runs the action button in the current direction.
func (g *gameShell) commitSaveLoadScreen() {
	if saveLoadUI == nil {
		return
	}
	if saveLoadUI.Mode() == loadScreenMode {
		g.commitSaveLoadLoad()
		return
	}
	g.commitSaveLoadWrite()
}

// commitSaveLoadLoad applies the selected bank. Preflight failures are the
// verbatim `Invalid savegame file` box and a return to the load screen —
// nothing is aborted mid-restore, because no battle state has been touched
// [08 R-SAVE-02 §2]. The CD gates have no backend in this build and are
// skipped, as the post-battle machine already skips them.
func (g *gameShell) commitSaveLoadLoad() {
	entry, ok := saveLoadUI.SelectedEntry()
	if !ok {
		return
	}
	// The load arm's cue follows its disc gates, which this build has no
	// backend for, and precedes the restore [08 R-SAVE-02 §1].
	g.playMenuCue(cueSaveLoadCommit)
	// Keep the selected row and dialog buffers through detached preparation:
	// a preflight refusal returns to this load screen [08 R-SAVE-02 §2].
	if err := g.loadRetailSavePath(entry.Path); err != nil {
		reportRetailMessageError(g.showRetailMessage(retailInvalidSaveMessage))
		return
	}
	// Successful routing may already have replaced the frontend stack. Close
	// any surviving dialog and release its buffers only after that commit.
	g.closeSaveLoadScreen()
}

// commitSaveLoadWrite writes the bank the current surface owns: a
// between-missions continuation from the results panel, a live-battle bank
// from inside a battle [08 R-CAMP-01 §8] [08 "Summary"].
//
// Successful saves close the dialog, a requested host UI policy documented in
// DESIGN_SESSIONS_AI_SAVE §5. Empty names and failed writes keep the editor so
// the player can correct them without reopening the dialog.
func (g *gameShell) commitSaveLoadWrite() {
	// The cue precedes the empty-name test, so a click on the action button
	// with a blank name box is audible and does nothing else
	// [08 R-SAVE-02 §1].
	g.playMenuCue(cueSaveLoadCommit)
	name := saveLoadUI.Name()
	path := saveLoadUI.CommitPath()
	if path == "" {
		// An empty name does nothing: no file, no message [08 R-SAVE-02 §1].
		return
	}
	var err error
	switch saveLoadUI.Source() {
	case saveLoadFromResults:
		err = g.writeBetweenMissionsSave(path, name)
	case saveLoadFromBattle:
		err = g.writeBattleSave(path, name)
	default:
		// The front end has no game to save; retail only reaches the save
		// direction from `ARMOPT` and `ENDMSN` [07 R-FE-01 §8].
		return
	}
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	g.closeSaveLoadScreen()
}

// retailSaveGameID is the `Game ID` string: the C-library wall-clock time at
// the moment of saving, seconds since the epoch [08 R-SAVE-02 §1]. It is read
// here, in the presentation layer, and never inside a session [I6].
func retailSaveGameID() string {
	return fmt.Sprintf("%d", time.Now().Unix())
}

// writeBetweenMissionsSave writes the continuation bank the results screen's
// `SaveGame` produces. The writer calls Advance before writing `Mission`/`Map`
// and `BetweenMissions = 1`, so the bank names the **next** mission whenever
// one exists — win or loss — and the played mission only when it was the last
// [08 R-CAMP-01 §8 "Between-missions save quirk"].
func (g *gameShell) writeBetweenMissionsSave(path, description string) error {
	if g == nil || g.battle == nil || g.battle.sess == nil {
		return fmt.Errorf("nanolathe: between-missions save: no results session: logical path save/Summary, providers searched [shell], expected the frozen campaign result")
	}
	if g.battle.postBattle == nil {
		return fmt.Errorf("nanolathe: between-missions save: no results controller: logical path save/Summary, providers searched [shell], expected the installed post-battle controller")
	}
	projection := g.battle.postBattle.Summary()
	if !projection.BetweenMissions {
		return fmt.Errorf("nanolathe: between-missions save: result is not a campaign continuation: logical path save/Summary, providers searched [post-battle controller], expected a campaign result")
	}
	return session.WriteRetailContinuationSave(path, session.ContinuationSummary(projection, g.betweenMissionsMetadata(description)))
}

// betweenMissionsMetadata is the caller-owned half of the continuation save.
// The campaign identity itself — including Advance's successor name — is the
// post-battle controller's frozen projection [08 R-CAMP-01 §8].
func (g *gameShell) betweenMissionsMetadata(description string) session.ContinuationSaveMetadata {
	meta := session.ContinuationSaveMetadata{
		Description: description,
		GameID:      retailSaveGameID(),
		// The `maxunits` item is the configured unit-limit word on every save
		// the writer emits, continuation included; this shell's configured word
		// is the setup record's [08 "Summary"] [08 R-SESS-01 §9].
		MaxUnits: int32(g.setup.UnitLimit),
	}
	if sess := g.battle.sess; sess != nil {
		meta.Players = session.RetailPlayerCount(sess)
		if sess.Clock != nil {
			meta.GameTime = int32(sess.Clock.GlobalTick)
		}
	}
	return meta
}

// writeBattleSave writes the live-battle bank through the existing projection
// and writer. The bulk families are whatever that writer already supports; no
// box is extended here [08 "Save-file organization"].
func (g *gameShell) writeBattleSave(path, description string) error {
	if g == nil || g.battle == nil || g.battle.sess == nil {
		return fmt.Errorf("nanolathe: battle save: no live battle: logical path save, providers searched [shell], expected a composed battle")
	}
	sess := g.battle.sess
	// The Summary's `maxunits` is the configured unit-limit word, not the
	// battle's session limit — a campaign's session word comes from the
	// mission's OTA and is deliberately not what a save records
	// [08 R-SESS-01 §9] [08 R-SKIR-01 §6]. This shell's configured word is the
	// setup record's, the same word that sizes a fresh or restored battle.
	summary := session.RetailBattleSummary(sess, description, retailSaveGameID(), g.setup.UnitLimit)
	camera := save.Camera{}
	if g.cam != nil {
		camera.XPosition = g.cam.X
		camera.ZPosition = g.cam.Z
	}
	in, err := sess.RetailBattleSaveInputs(summary, camera)
	if err != nil {
		return err
	}
	in.DisplayTimers = g.battle.cl.ResourceDisplayTimers()
	return sess.WriteRetailSave(path, in)
}

// resultControlName mirrors the authored ENDMSN release-inside gesture but
// yields the control's name rather than a typed route. The two dialog buttons
// of the ENDMSN control set — `SaveGame` and `LoadGame` — are not routes in
// the ResultAction vocabulary, so the name is what the post-battle owner needs
// [08 R-CAMP-01 §8] [07 §3] [07 §11].
func (h *retailBattleHUD) resultControlName(in *input.State) (string, bool) {
	if h == nil || h.resultPanel == nil || h.resultPanel.Window == nil || in == nil {
		return "", false
	}
	result := h.serviceBattleResultPanel(in)
	if !result.Fired || result.FiredIndex < 0 || result.FiredIndex >= len(h.resultPanel.Window.Gadgets) {
		return "", false
	}
	return h.resultPanel.Window.Gadgets[result.FiredIndex].Name, true
}
