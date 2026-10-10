package main

// The Replays screen (DESIGN_INTERFACE_HUD_INPUT "Replays", DESIGN_MULTIPLAYER
// §10): the replay directory's recordings in the authored load dialog,
// LOADGAME.GUI on the save direction's backdrop, whose Delete and name-field
// frames it uses, with Watch, Delete and Cancel. Watch composes the playback
// on a job goroutine and enters it; leaving the playback comes back here. A
// replay recorded under another installed mod mounts that mod first through
// the ordinary content reload, as an online join does. Everything here is
// host presentation and file handling; nothing reaches a tick.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const (
	// replaysButton is the main menu's REPLAYS button, beside NANOLATHE.
	replaysButton = "REPLAYS"

	replaysTemplateGUI      = "guis/loadgame.gui"
	replaysTemplateBackdrop = "bitmaps/dsavegame2.pcx"

	// The screen's own labels, added to the dialog.
	replaysTitle  = "NTITLE"
	replaysStatus = "STATUS"
	replaysEmpty  = "EMPTY"
	// replaysDeleteConfirm is the confirmation's Delete button.
	replaysDeleteConfirm = "DELETEREPLAY"

	replaysEmptyText = "No replays yet. Every skirmish and Survival battle is recorded here."

	// The status line fills the save backdrop's name-field frame, two lines
	// deep; the empty-list text sits in the list's frame.
	replaysStatusX, replaysStatusY, replaysStatusW, replaysStatusH = 108, 271, 236, 27
	replaysEmptyX, replaysEmptyY, replaysEmptyW, replaysEmptyH     = 75, 82, 222, 48
	// replaysSummaryW is the width the summary values fit to.
	replaysSummaryW = 150
)

// replaysSummaryRows are the summary panel's rows in the authored order of
// its value labels, each with the caption the screen draws in place of the
// backdrop's baked one.
var replaysSummaryRows = [...]struct{ value, caption string }{
	{"GAMETYPE", "Game type:"},
	{"SIDE", "Map:"},
	{"MISSION", "Players:"},
	{"DIFF", "Length:"},
	{"TIME", "Played:"},
}

// replaysScreen is the open Replays screen (gameShell.replays).
type replaysScreen struct {
	panel  *ui.Panel
	assets *retailPanelAssets
	dir    string

	list     []replayListing
	selected int
	// status is a notice about what the player just did; it replaces the
	// selection's own note until the selection changes.
	status string
	// load is the playback being composed for Watch, nil when none.
	load *replayLoad
	// confirm is the open delete confirmation and the file it deletes.
	confirm     *ui.Panel
	confirmPath string
}

// replayLoad is Watch's job: the playback composed on a goroutine, with its
// progress on the loading screen's bars.
type replayLoad struct {
	path     string
	progress *loadingState
	done     chan replayLoadResult
}

type replayLoadResult struct {
	playback *replayPlayback
	detail   *client.DetailArt
	err      error
}

// replayWatchResume is a Watch waiting on the content reload that mounts its
// replay's mod; the shell the reload builds resumes it (pollReplays).
type replayWatchResume struct {
	path string
	mod  session.MatchMod
}

var pendingReplayWatch *replayWatchResume

// replaysWindowTemplate loads the authored load dialog and the save
// direction's backdrop from the mounted content. Tests substitute windows of
// the same shape.
var replaysWindowTemplate = func(g *gameShell) (*gui.Window, *formats.PCX, error) {
	window, err := g.cs.loadGUI(replaysTemplateGUI)
	if err != nil {
		return nil, nil, retailFrontendAssetError(g.cs, "replays screen GUI unavailable", replaysTemplateGUI, "the authored load dialog", err)
	}
	background, err := replaysBackdrop(g.cs.fs)
	if err != nil {
		return nil, nil, retailFrontendAssetError(g.cs, "replays screen bitmap", replaysTemplateBackdrop, "the authored save dialog backdrop", err)
	}
	return window, background, nil
}

// replaysBackdrop copies the save dialog's backdrop and paints its own plain
// texture over the baked-in title and summary captions, so the screen can
// carry its own, as the Mods & Mutators screen does with the map-select
// backdrop (modsBackdrop).
func replaysBackdrop(from vfs.FSOps) (*formats.PCX, error) {
	source, err := formats.LoadPCXFile(from, replaysTemplateBackdrop)
	if err != nil {
		return nil, err
	}
	pcx := *source
	pcx.Pixels = append([]byte(nil), source.Pixels...)
	width := int(pcx.Width)
	patch := func(fromX, fromY, toX, toY, w, h int) {
		for y := 0; y < h; y++ {
			src := (fromY+y)*width + fromX
			dst := (toY+y)*width + toX
			if src >= 0 && dst >= 0 && src+w <= len(source.Pixels) && dst+w <= len(pcx.Pixels) {
				copy(pcx.Pixels[dst:dst+w], source.Pixels[src:src+w])
			}
		}
	}
	// The title: the plain stretch right of it, twice.
	patch(236, 26, 60, 26, 90, 36)
	patch(236, 26, 146, 26, 90, 36)
	// The summary captions: the plain strip left of them, twice.
	patch(8, 310, 66, 310, 44, 88)
	patch(8, 310, 108, 310, 42, 88)
	return &pcx, nil
}

// installMainMenuReplaysButton is the REPLAYS button: a copy of the MODS
// button (installMainMenuModsButton) placed beside it.
func installMainMenuReplaysButton(window *gui.Window, x int32) {
	mods := window.GadgetIndex("MODS")
	if mods < 0 {
		return
	}
	button := window.Gadgets[mods]
	button.Name, button.SourceName, button.Text = replaysButton, replaysButton, "REPLAYS"
	button.Rect.X = x
	window.Gadgets = append(window.Gadgets, button)
}

// disableUnavailableReplays greys REPLAYS where Skirmish is greyed: a replay
// is a skirmish or Survival battle, so it needs what they need.
func (g *gameShell) disableUnavailableReplays(window *gui.Window, mode shellMode) {
	if mode != modeMenuMain || g == nil || g.cs == nil || g.cs.fs == nil || g.assets == nil {
		return
	}
	if i := window.GadgetIndex(replaysButton); i >= 0 && !g.skirmishContentAvailable() {
		window.Gadgets[i].GrayedOut |= 1
	}
}

// buildReplaysWindow shapes the load dialog into the Replays screen: the
// list, its scrollbar, the map picture, Watch (the authored LOAD), Delete and
// Cancel keep their authored places; the name field gives way to the status
// line in its frame, and the screen adds its title and the summary captions.
func buildReplaysWindow(window *gui.Window) {
	var kept []gui.Gadget
	label := gui.Gadget{Kind: gui.KindLabel, Active: 1, ColorF: 15, Attribs: 0x31}
	for _, gad := range window.Gadgets {
		switch gad.Name {
		case "SaveGame", "LoadGame", "TITLE", "GAMENAME":
			continue
		case "GAMETYPE":
			label = gad
		case "LOAD":
			gad.Text, gad.Labels = "Watch", nil
		case "DELETE":
			gad.Text, gad.Labels = "Delete", nil
		case "CANCEL":
			gad.Text, gad.Labels = "Cancel", nil
		}
		kept = append(kept, gad)
	}
	add := func(name, text string, x, y, w, h int32, attribs uint32) {
		l := label
		l.Name, l.SourceName, l.Text, l.Attribs = name, name, text, attribs
		l.Rect = gui.Rect{X: x, Y: y, W: w, H: h}
		kept = append(kept, l)
	}
	add(replaysTitle, "REPLAYS", 66, 40, 200, 16, label.Attribs)
	for _, row := range replaysSummaryRows {
		if i := window.GadgetIndex(row.value); i >= 0 {
			r := window.Gadgets[i].Rect
			add("CAP"+row.value, row.caption, 60, r.Y, 88, r.H, label.Attribs|4)
		}
	}
	add(replaysStatus, "", replaysStatusX, replaysStatusY, replaysStatusW, replaysStatusH, label.Attribs)
	add(replaysEmpty, "", replaysEmptyX, replaysEmptyY, replaysEmptyW, replaysEmptyH, label.Attribs)
	window.Header.CrDefault, window.Header.EscDefault, window.Header.DefaultFocus = "LOAD", "CANCEL", "GAMES"
	window.Gadgets = kept
}

func (g *gameShell) replaysPanelActive() bool {
	return g != nil && g.replays != nil && g.replays.panel != nil && g.activePanel() == g.replays.panel
}

// replaysPanelAssets supplies the screen's backdrop to the window painter.
func (g *gameShell) replaysPanelAssets(p *ui.Panel) *retailPanelAssets {
	if g == nil || g.replays == nil || p == nil || p != g.replays.panel {
		return nil
	}
	return g.replays.assets
}

func (g *gameShell) openReplaysScreenReporting() {
	if err := g.openReplaysScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

// openReplaysScreen opens the screen over the main menu on the replay
// directory, newest replay selected.
func (g *gameShell) openReplaysScreen() error {
	if g == nil || g.cs == nil || g.cs.fs == nil || g.frontend == nil {
		return fmt.Errorf("nanolathe: replays screen: logical path %s, providers searched [], expected mounted content", replaysTemplateGUI)
	}
	if g.replays != nil {
		g.closeReplaysScreen()
	}
	window, background, err := replaysWindowTemplate(g)
	if err != nil {
		return err
	}
	buildReplaysWindow(window)
	g.installRetailWindowButtonArt(window, nil)
	g.installRetailListScrollbars(window, nil)
	g.initializeRetailLabels(window)
	panel := ui.NewPanel(window)
	if panel == nil {
		return fmt.Errorf("nanolathe: replays screen: logical path %s, providers searched [], expected a panel for the authored load dialog", replaysTemplateGUI)
	}
	g.replays = &replaysScreen{panel: panel, assets: &retailPanelAssets{window: window, background: background}, dir: g.opts.ReplayDir}
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	g.refreshReplaysList("")
	return nil
}

// closeReplaysScreen leaves the screen. A Watch still composing is
// abandoned; its goroutine finishes and its playback is dropped.
func (g *gameShell) closeReplaysScreen() {
	s := g.replays
	if s == nil {
		return
	}
	if g.frontend != nil {
		if s.confirm != nil && g.frontend.Panels.Modal() == s.confirm {
			g.frontend.Panels.CloseModal()
		}
		if g.frontend.Panels.Top() == s.panel {
			g.frontend.Panels.Pop()
		}
	}
	g.replays = nil
	flushWindowTokens(clPtr)
}

// refreshReplaysList re-reads the directory and selects keep, or keeps the
// selected row's place.
func (g *gameShell) refreshReplaysList(keep string) {
	s := g.replays
	if s == nil {
		return
	}
	list, err := listReplays(s.dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		s.status = "The replay folder cannot be read: " + noticeReason(err)
	}
	s.list = list
	if keep != "" {
		for i, l := range list {
			if l.Path == keep {
				s.selected = i
			}
		}
	}
	s.selected = max(0, min(s.selected, len(list)-1))
	g.refreshReplaysPanel()
}

// selection is the selected replay.
func (s *replaysScreen) selection() (replayListing, bool) {
	if s == nil || s.selected < 0 || s.selected >= len(s.list) {
		return replayListing{}, false
	}
	return s.list[s.selected], true
}

// selectReplayRow commits a list selection.
func (g *gameShell) selectReplayRow(index int) {
	s := g.replays
	if s == nil || index < 0 || index >= len(s.list) || index == s.selected {
		return
	}
	s.selected, s.status = index, ""
	g.refreshReplaysPanel()
}

// refreshReplaysPanel writes the list, the summary, the status line and what
// can be pressed.
func (g *gameShell) refreshReplaysPanel() {
	s := g.replays
	if s == nil || s.panel == nil {
		return
	}
	p := s.panel
	rows := make([]string, len(s.list))
	for i, l := range s.list {
		rows[i] = replayRowText(l, g.replayMapLabel(l.Map()))
	}
	if g.activePanel() == p {
		g.setListItems("GAMES", rows, s.selected)
	}
	l, ok := s.selection()
	p.SetActive(replaysEmpty, len(s.list) == 0)
	p.SetText(replaysEmpty, g.fitDetail(replaysEmptyText, replaysEmptyW, 3))
	for name, value := range replaySummaryFields(l, ok, g.replayMapLabel(l.Map())) {
		p.SetText(name, g.fitDetail(value, replaysSummaryW, 1))
	}
	if ok && l.Err == nil {
		// The picture is read when the selection changes, never while
		// painting.
		g.mapDataFor(l.Map())
	}
	p.SetText(replaysStatus, g.fitDetail(g.replaysStatusText(), replaysStatusW, 2))
	busy := s.load != nil
	retailGreyGadget(p.Window, "LOAD", busy || !ok || l.Err != nil)
	retailGreyGadget(p.Window, "DELETE", busy || !ok || l.Recording)
}

// replaysStatusText is the status line: a Watch's progress, the latest
// notice, or the selection's own note.
func (g *gameShell) replaysStatusText() string {
	s := g.replays
	if s.load != nil {
		text := fmt.Sprintf("Loading the replay... %d%%", s.load.percent())
		if s.status != "" {
			text = s.status + " " + text
		}
		return text
	}
	if s.status != "" {
		return s.status
	}
	l, ok := s.selection()
	if !ok {
		return ""
	}
	return g.replayNote(l)
}

// percent is the load's progress, the mean of the loading screen's bars.
func (j *replayLoad) percent() int {
	total := 0
	for i := range j.progress.percent {
		total += int(max(0, min(100, j.progress.percent[i].Load())))
	}
	return total / len(j.progress.percent)
}

// ---------------------------------------------------------------------------
// What the list and the summary say.

// replayMapLabel is a recorded map's name as the map list spells it.
func (g *gameShell) replayMapLabel(name string) string {
	for _, m := range g.maps {
		if strings.EqualFold(m, name) {
			return m
		}
	}
	return name
}

// replayRowText is a list row: when the battle started and its map, named
// mapName.
func replayRowText(l replayListing, mapName string) string {
	name := mapName
	if l.Err != nil {
		name = "(unreadable file)"
	}
	return l.Started.Format("Jan 2 15:04") + "  " + name
}

// replayKindText names what a replay records.
func replayKindText(k replay.Kind) string {
	switch k {
	case replay.KindSkirmish:
		return "Skirmish"
	case replay.KindSurvival:
		return "Survival"
	case replay.KindOnlineSkirmish:
		return "Online skirmish"
	case replay.KindOnlineSurvival:
		return "Online Survival"
	}
	return "Unknown"
}

// replayLengthText is the recorded game time, with the recording's state.
func replayLengthText(l replayListing) string {
	text := replayGameTime(l.FinalTick)
	switch {
	case l.Recording:
		text += ", recording now"
	case l.Incomplete:
		text += ", incomplete"
	}
	return text
}

// replayPlayersText names the recorded players, human and computer, in slot
// order; an unnamed one by its place.
func replayPlayersText(l replayListing) string {
	var names []string
	for i, seat := range l.Players() {
		name := strings.TrimSpace(seat.Name)
		switch {
		case name != "":
		case seat.Role == session.MatchRoleHuman:
			name = fmt.Sprintf("Player %d", i+1)
		default:
			name = fmt.Sprintf("Computer %d", i+1)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// replaySummaryFields fills the summary rows for the selected replay, its map
// named mapName; an unreadable file shows only when it was written.
func replaySummaryFields(l replayListing, selected bool, mapName string) map[string]string {
	fields := map[string]string{}
	for _, row := range replaysSummaryRows {
		fields[row.value] = ""
	}
	if !selected {
		return fields
	}
	fields["TIME"] = l.Started.Format("2 Jan 2006 15:04")
	if l.Err != nil {
		fields["GAMETYPE"] = "Unreadable"
		return fields
	}
	fields["GAMETYPE"] = replayKindText(l.Kind())
	fields["SIDE"] = mapName
	fields["MISSION"] = replayPlayersText(l)
	fields["DIFF"] = replayLengthText(l)
	return fields
}

// replayNote is what the status line says of the selected replay by
// itself: why it cannot play, its recording's state, or the mod it needs.
func (g *gameShell) replayNote(l replayListing) string {
	if l.Err != nil {
		return "Cannot play: " + replayFileProblem(l.Err)
	}
	if want, err := replayModOf(l); err == nil && want != matchModOf(g.cs.mod) {
		_, name, refusal := g.replayModPlan(want)
		if refusal != "" {
			return refusal
		}
		return "Recorded with " + name + ". Watch switches to it first."
	}
	switch {
	case l.Recording:
		return "This battle is being recorded now, so its replay cannot be deleted."
	case l.Incomplete:
		return "The recording stopped early. It plays to where it stops."
	}
	return ""
}

// replayFileProblem says in plain words why a file is not a replay this
// build can read.
func replayFileProblem(err error) string {
	switch {
	case errors.Is(err, replay.ErrUnsupportedVersion):
		return "a newer version of Nanolathe recorded it."
	case errors.Is(err, replay.ErrCorrupt), errors.Is(err, replay.ErrTruncated):
		return "the file is damaged."
	case errors.Is(err, fs.ErrNotExist):
		return "the file is no longer there."
	}
	return "the file cannot be read."
}

// replayOpenProblem says in plain words why a replay did not compose.
func replayOpenProblem(err error) string {
	switch {
	case errors.Is(err, session.ErrMatchMapMismatch):
		return "your copy of the map differs from the recording's."
	case errors.Is(err, session.ErrMatchContentMismatch):
		return "your game files differ from the recording's."
	case errors.Is(err, session.ErrMatchModMismatch):
		return "it needs a different copy of its mod."
	case errors.Is(err, session.ErrMatchRulesMismatch):
		return "this version plays its rules differently."
	case errors.Is(err, session.ErrMatchProtocolMismatch):
		return "another version of Nanolathe recorded it."
	case errors.Is(err, replay.ErrIncompatible):
		return "this version cannot set up its battle."
	}
	return replayFileProblem(err)
}

// replayModOf is the mod a replay was recorded with.
func replayModOf(l replayListing) (session.MatchMod, error) {
	config, err := session.DecodeMatchConfig(l.Header.Config)
	if err != nil {
		return session.MatchMod{}, err
	}
	return config.Request().Mod, nil
}

// replayModPlan is the content reload that mounts want, the mod's name, or
// why it cannot be mounted (onlineModPlan's checks, in a replay's words).
func (g *gameShell) replayModPlan(want session.MatchMod) (selector, name, refusal string) {
	name = onlineModName(want)
	if onlineContentRefusal(g.cs) != nil {
		return "", name, "Recorded with " + name + ". Start without extra --root or --mod-config content to watch it."
	}
	if want.ID == "" {
		return "none", name, ""
	}
	lib, err := openModLibrary()
	if err != nil {
		return "", name, "Recorded with the mod " + name + ", but the mod library is unavailable."
	}
	mod, ok, err := lib.Lookup(want.ID, want.Version)
	if err != nil || !ok {
		return "", name, "Recorded with the mod " + name + ", which is not installed. Install it from NANOLATHE, Mods."
	}
	name = mod.Name + " " + mod.Version
	if sameMod(&mod, g.cs.mod) || want.Archive != ([32]byte{}) && matchModOf(&mod).Archive != want.Archive {
		return "", name, "Recorded with a different copy of " + name + " than the one installed."
	}
	if missing := unmetModRequirements(g.cs.baseRoots, mod); len(missing) > 0 {
		return "", name, "Recorded with " + name + ", which needs " + missing[0] + " from the base install."
	}
	return modSelectorOf(mod.ID, mod.Version), name, ""
}

// ---------------------------------------------------------------------------
// The controls.

// activateReplaysGadget routes a control of the screen.
func (g *gameShell) activateReplaysGadget(name string) bool {
	if !g.replaysPanelActive() {
		return false
	}
	s := g.replays
	switch name {
	case "LOAD", "GAMES":
		g.watchSelectedReplay()
	case "DELETE":
		g.playMenuCue(cueDeleteSaveSlot)
		g.confirmReplayDelete()
	case "CANCEL":
		g.playMenuCue(cuePreviousScreen)
		if s.load != nil {
			// Cancel stops a Watch still composing; otherwise it leaves.
			s.load, s.status = nil, ""
			g.refreshReplaysPanel()
			return true
		}
		g.closeReplaysScreen()
	}
	return true
}

// watchSelectedReplay plays the selection: its mod first if another is
// mounted, then its battle composed on a job goroutine (pollReplays enters
// it).
func (g *gameShell) watchSelectedReplay() {
	s := g.replays
	l, ok := s.selection()
	if !ok || s.load != nil {
		return
	}
	g.playMenuCue(cueSaveLoadCommit)
	fail := func(text string) {
		s.status = text
		g.refreshReplaysPanel()
	}
	if l.Err != nil {
		fail("Cannot play: " + replayFileProblem(l.Err))
		return
	}
	want, err := replayModOf(l)
	if err != nil {
		fail("Cannot play: this version cannot read its settings.")
		return
	}
	if want != matchModOf(g.cs.mod) {
		selector, name, refusal := g.replayModPlan(want)
		if refusal != "" {
			fail(refusal)
			return
		}
		s.status = "Loading " + name + " for this replay..."
		g.refreshReplaysPanel()
		pendingContentReload = &contentReloadRequest{
			selector: selector,
			mod:      settings.ModSelection{ID: want.ID, Version: want.Version},
			mutators: g.opts.Mutators,
		}
		pendingReplayWatch = &replayWatchResume{path: l.Path, mod: want}
		return
	}
	if !g.hasSkirmishMap(l.Map()) {
		fail("Cannot play: the map " + l.Map() + " is not installed.")
		return
	}
	g.startReplayLoad(l)
}

// startReplayLoad composes the selection's playback on a job goroutine with
// the optional load-time art, as the loading screen's goroutine prepares a
// fresh battle. A content reload waits for it (onlineWork).
func (g *gameShell) startReplayLoad(l replayListing) {
	s := g.replays
	job := &replayLoad{path: l.Path, progress: newLoadingState(l.Map()), done: make(chan replayLoadResult, 1)}
	s.load = job
	cs, opts := g.cs, g.opts
	onlineWork.Add(1)
	go func() {
		defer onlineWork.Done()
		p, err := prepareReplayPlayback(l.Path, cs, job.progress.report)
		var detail *client.DetailArt
		if err == nil {
			detail = detailArtFor(opts, cs, p.sess.World, job.progress.report)
		}
		job.done <- replayLoadResult{playback: p, detail: detail, err: err}
	}()
	g.refreshReplaysPanel()
}

// pollReplays resumes a Watch a mod switch was made for, takes a finished
// Watch, and keeps the progress current. It runs every shell step; nothing
// in it waits.
func (g *gameShell) pollReplays() {
	if r := pendingReplayWatch; r != nil && pendingContentReload == nil {
		pendingReplayWatch = nil
		g.resumeReplayWatch(*r)
	}
	s := g.replays
	if s == nil || s.load == nil {
		return
	}
	select {
	case res := <-s.load.done:
		job := s.load
		s.load = nil
		g.finishReplayLoad(job, res)
	default:
		if s.panel != nil {
			s.panel.SetText(replaysStatus, g.fitDetail(g.replaysStatusText(), replaysStatusW, 2))
		}
	}
}

// resumeReplayWatch continues a Watch on the shell a content reload built
// for its replay's mod, or says the switch failed.
func (g *gameShell) resumeReplayWatch(r replayWatchResume) {
	if g.replays == nil {
		if err := g.openReplaysScreen(); err != nil {
			reportRetailMessageError(g.showRetailMessage(err.Error()))
			return
		}
	}
	s := g.replays
	g.refreshReplaysList(r.path)
	if matchModOf(g.cs.mod) != r.mod {
		s.status = "The replay's mod " + onlineModName(r.mod) + " could not be loaded."
		if g.cs.modNotice != "" {
			s.status = g.cs.modNotice
		}
		g.refreshReplaysPanel()
		return
	}
	l, ok := s.selection()
	if !ok || l.Path != r.path {
		s.status = "The replay is no longer there."
		g.refreshReplaysPanel()
		return
	}
	mod := "Total Annihilation"
	if g.cs.mod != nil {
		mod = g.cs.mod.Name + " " + g.cs.mod.Version
	}
	s.status = "Switched to " + mod + "."
	if !g.hasSkirmishMap(l.Map()) {
		s.status = "Cannot play: the map " + l.Map() + " is not installed."
		g.refreshReplaysPanel()
		return
	}
	g.startReplayLoad(l)
}

// finishReplayLoad enters a composed playback, or says why it did not
// compose. Leaving the playback returns here.
func (g *gameShell) finishReplayLoad(job *replayLoad, res replayLoadResult) {
	s := g.replays
	if res.err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: replay %s: %v\n", job.path, res.err)
		s.status = "Cannot play: " + replayOpenProblem(res.err)
		g.refreshReplaysPanel()
		return
	}
	g.closeReplaysScreen()
	g.pendingDetail = res.detail
	if err := g.enterReplayPlayback(res.playback); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: replay %s: %v\n", job.path, err)
		g.pendingDetail = nil
		g.applyDisplaySize(clPtr, retailScreenW, retailScreenH)
		g.returnToReplays(job.path, "Cannot play: "+noticeReason(err))
		return
	}
	g.battle.returnToMenu = g.returnFromReplayPlayback
	g.battle.returnToSkirmish = g.returnFromReplayPlayback
	res.playback.overlay.mapName = g.replayMapLabel(res.playback.header.MapName)
}

// returnFromReplayPlayback leaves a playback, or the battle it shows, for
// the Replays screen with the watched replay selected.
func (g *gameShell) returnFromReplayPlayback(cl *client.Client) {
	path := ""
	if b := g.battle; b != nil && b.playback != nil {
		path = b.playback.Path()
	}
	g.returnFromBattle(cl)
	g.returnToReplays(path, "")
}

// returnToReplays opens the screen with path selected and a notice.
func (g *gameShell) returnToReplays(path, status string) {
	if err := g.openReplaysScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	g.refreshReplaysList(path)
	g.replays.status = status
	g.refreshReplaysPanel()
}

// leaveReplayPlayback takes the playback overlay's Exit at the shell step,
// before the battle's own step runs.
func (g *gameShell) leaveReplayPlayback(cl *client.Client) bool {
	b := g.battle
	if b == nil || b.playback == nil || !b.playback.overlay.exit || b.returnToMenu == nil {
		return false
	}
	b.playback.overlay.exit = false
	b.ended = true
	b.returnToMenu(cl)
	return true
}

// confirmReplayDelete asks, in the message window, whether to delete the
// selected replay. Enter and Escape keep it; only Delete deletes.
func (g *gameShell) confirmReplayDelete() {
	s := g.replays
	l, ok := s.selection()
	if !ok || s.load != nil {
		return
	}
	if l.Recording {
		s.status = "This battle is being recorded now, so its replay cannot be deleted."
		g.refreshReplaysPanel()
		return
	}
	what := "the replay of " + g.replayMapLabel(l.Map())
	if l.Err != nil {
		what = "this unreadable replay file"
	}
	if err := g.showRetailMessage("Delete " + what + " from " + l.Started.Format("Jan 2 15:04") + "?"); err != nil {
		reportRetailMessageError(err)
		return
	}
	m := g.frontend.Panels.Modal()
	if m == nil || m.Window == nil {
		return
	}
	w := gui.CloneWindow(m.Window)
	index := w.GadgetIndex("OK")
	if index < 0 {
		return
	}
	cancel := &w.Gadgets[index]
	cancel.Text, cancel.Labels = "Cancel", nil
	width := max(w.Rect.W, 2*cancel.Rect.W+45)
	w.Rect.W, w.Rect.X = width, (retailScreenW-width)/2
	w.OriginX = w.Rect.X
	w.Gadgets[0].Rect = w.Rect
	for i := range w.Gadgets {
		if w.Gadgets[i].Kind == gui.KindLabel {
			w.Gadgets[i].Rect.W = width
		}
	}
	cancel.Rect.X = width - cancel.Rect.W - 15
	remove := *cancel
	remove.Name, remove.SourceName, remove.Text, remove.QuickKey = replaysDeleteConfirm, replaysDeleteConfirm, "Delete", 0
	remove.Rect.X = 15
	w.Gadgets = append(w.Gadgets, remove)
	w.Header.CrDefault, w.Header.EscDefault = "OK", "OK"
	p := ui.NewPanel(w)
	g.frontend.Panels.CloseModal()
	g.frontend.Panels.PushModal(p)
	s.confirm, s.confirmPath = p, l.Path
}

// finishReplayDeleteConfirmation deletes the confirmed replay. It reports
// whether p was the confirmation.
func (g *gameShell) finishReplayDeleteConfirmation(p *ui.Panel, action string) bool {
	s := g.replays
	if s == nil || s.confirm == nil || s.confirm != p {
		return false
	}
	path := s.confirmPath
	s.confirm, s.confirmPath = nil, ""
	if action != replaysDeleteConfirm {
		return true
	}
	if err := deleteReplay(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		s.status = "The replay could not be deleted: " + noticeReason(err)
		if replayActive(path) {
			s.status = "This battle is being recorded now, so its replay cannot be deleted."
		}
	} else {
		s.status = ""
	}
	g.refreshReplaysList("")
	return true
}

// drawReplaysPreview paints the selected replay's map, as the map selector
// pictures it, into the dialog's picture frame.
func (g *gameShell) drawReplaysPreview(c *client.Client, r gui.Rect) {
	l, ok := g.replays.selection()
	if !ok || l.Err != nil || r.W < 2 || r.H < 2 {
		return
	}
	if d := g.mapDataFor(l.Map()); d != nil && d.tnt != nil {
		pixels := d.previewPixels(int(r.W), int(r.H))
		c.UIBlitIndexed(pixels, int(r.W)-1, int(r.H)-1, int(r.X), int(r.Y), int(r.W)-1, int(r.H)-1)
	}
}
