package main

// The front-end pointer and keyboard pass: gadget activation, the escape
// route, list clicks and the skirmish setup rows [07 §5] [07 R-FE-01 §5].

import (
	"os"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func (g *gameShell) menuInput(cl *client.Client) {
	if cl == nil || cl.Input() == nil {
		return
	}
	in := cl.Input()
	if g != nil && g.briefing != nil && g.briefing.State() == BriefingOpen {
		g.briefingInput(cl)
		return
	}
	if g.frontend.Panels.Modal() != nil {
		g.modalInput(cl)
		return
	}
	p := g.activePanel()
	if p == nil || p.Window == nil {
		return
	}
	g.serviceMenuWidgets(p, in)
}

// serviceMenuWidgets is the front-end adapter around the common widget pass.
// It retains screen-specific selection commits and actions outside ui.
func (g *gameShell) serviceMenuWidgets(p *ui.Panel, in *input.State) bool {
	if g == nil || p == nil || in == nil || in.Mouse == nil || in.Kbd == nil {
		return true
	}
	// The options owner is authoritative for its read-out and painter state.
	// Copy it into the service before the pass, then Change copies a moved knob
	// back synchronously, so a later refresh cannot draw a stale position.
	for i, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindScrollBar {
			if s := g.retailOptionsSliderAt(i); s != nil {
				p.SetSliderKnobAt(i, s.knob)
			}
		}
	}
	editorIndex := p.EditorIndex()
	frame := pointerFrame(in, widgetTokens(in), g.widgetTimerAdvanced(p))
	frame.TokenMode = true
	// TODO(question): census every front-end transition which disables this
	// independently initialized navigation word; the shell's enabled state is
	// traced, but a universal lifetime is not [07 R-WGT-01 §2].
	frame.KeyNavigation = true
	if in.Kbd != nil {
		frame.ShiftHeld, frame.AltHeld = in.Kbd.HasShift(), in.Kbd.KeyHeld(input.KeyAlt)
	}
	result := p.ServiceFrame(frame, ui.WidgetHooks{
		Metric: func(int) int { return g.retailTextHeight() },
		ArtFrames: func(index int) int {
			gad, ok := g.currentGadget(index)
			if !ok {
				return 0
			}
			return g.retailButtonArtFrames(gad)
		},
		Measure: func(index int, text string) int {
			gad, ok := g.currentGadget(index)
			if !ok {
				return len(text)
			}
			measure, _ := g.retailTextMetrics(g.windowGadgetFont(p, gad))
			return measure(text)
		},
		Change: func(index int) {
			gad, ok := g.currentGadget(index)
			if !ok {
				return
			}
			if gad.Kind == gui.KindListBox {
				g.commitListSelection(gad.Name, p.ListAt(index).Selected())
			}
			if gad.Kind == gui.KindScrollBar {
				if s := g.retailOptionsSliderAt(index); s != nil {
					g.moveRetailSliderAt(index, s, p.SliderKnobAt(index))
				}
			}
		},
	})
	in.DiscardTokens(result.ConsumedTokens)
	if editorIndex >= 0 && g.saveLoadPanelActive() {
		if gad, ok := g.currentGadget(editorIndex); ok && gui.CallbackNameEqual(gad.Name, "GAMENAME") {
			saveLoadUI.SetName(p.TextAt(editorIndex))
		}
	}
	if !result.Fired {
		return true
	}
	gad, ok := g.currentGadget(result.FiredIndex)
	if !ok {
		return true
	}
	if result.FiredButton == 2 && g.frontend.Mode == modeMenuSkirmish {
		if slot, kind, ok := dynamicSlot(gad.Name); ok {
			if kind == "Metal" {
				g.setup.Players[slot].Metal = decreaseResource(g.setup.Players[slot].Metal)
			} else if kind == "Energy" {
				g.setup.Players[slot].Energy = decreaseResource(g.setup.Players[slot].Energy)
			} else if kind == "Color" {
				g.setup.Players[slot].Color = g.nextRetailPlayerColor(slot, -1)
			}
			g.refreshRetailPanel()
			return true
		}
	}
	g.activateWidgetGadget(p, result)
	return true
}

// activateWidgetGadget preserves an already-advanced staged selection for the
// options callback. The shared service owns pointer-stage advancement; direct
// and keyboard activation enter with no marked service record and advance in
// the callback once [07 R-WGT-01 §1][07 R-WGT-01 §3].
func (g *gameShell) activateWidgetGadget(p *ui.Panel, result ui.ServiceResult) {
	if g == nil {
		return
	}
	if optionsState != nil && p != nil && p.Window != nil && p == optionsPanel && result.StageAdvanced && result.FiredIndex >= 0 && result.FiredIndex < len(p.Window.Gadgets) && p.Window.Gadgets[result.FiredIndex].Stages != 0 {
		state := optionsState
		state.serviceStageIndex = result.FiredIndex
		state.serviceStageActive = true
		defer func() {
			state.serviceStageIndex = -1
			state.serviceStageActive = false
		}()
	}
	g.activateGadgetAt(p, result.FiredIndex)
}

// activateGadgetAt carries the service-selected record into the screen
// callback. The callback predicate must read that fired record; looking up its
// name again would replace a later duplicate with the first record [07
// R-WGT-02 §2].
func (g *gameShell) activateGadgetAt(p *ui.Panel, index int) {
	if g == nil || p == nil || p.Window == nil || index < 1 || index >= len(p.Window.Gadgets) {
		return
	}
	if state := optionsState; state != nil && p == optionsPanel {
		previous := state.callbackIndex
		state.callbackIndex = index
		defer func() { state.callbackIndex = previous }()
	}
	g.activateGadget(p.Window.Gadgets[index].Name)
}

func (g *gameShell) modalInput(cl *client.Client) {
	if g == nil || cl == nil || cl.Input() == nil {
		return
	}
	m := g.frontend.Panels.Modal()
	if m == nil || m.Window == nil {
		return
	}
	in := cl.Input()
	frame := pointerFrame(in, widgetTokens(in), false)
	frame.TokenMode = true
	// TODO(question): modal transition ownership for the navigation word is
	// not yet fully traced [07 R-WGT-01 §2].
	frame.KeyNavigation = true
	if in.Kbd != nil {
		frame.ShiftHeld, frame.AltHeld = in.Kbd.HasShift(), in.Kbd.KeyHeld(input.KeyAlt)
	}
	result := m.ServiceFrame(frame, ui.WidgetHooks{ArtFrames: func(index int) int {
		if index < 0 || index >= len(m.Window.Gadgets) {
			return 0
		}
		return g.retailButtonArtFrames(m.Window.Gadgets[index])
	}})
	in.DiscardTokens(result.ConsumedTokens)
	if result.Fired {
		if _, ok := modalGadget(m, result.FiredIndex); ok {
			g.frontend.Panels.CloseModal()
		}
	}
}

func modalGadget(p *ui.Panel, index int) (gui.Gadget, bool) {
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) {
		return gui.Gadget{}, false
	}
	return p.Window.Gadgets[index], true
}

// widgetTokens exposes the producer-owned ordered ring. Ebiten appends its
// observed navigation transitions there before this adapter runs; host polling
// cannot recover native chronology or repeat [07 R-WGT-01 §2] TODO(T25).
func widgetTokens(in *input.State) []input.Token {
	if in == nil {
		return nil
	}
	return in.PeekTokens()
}

func (g *gameShell) commitListSelection(name string, index int) {
	name = gui.CallbackName(name)
	if g.saveLoadPanelActive() && name == "GAMES" {
		// Selecting a row refreshes the summary panel and copies the entry's
		// description into GAMENAME [08 R-SAVE-02 §1].
		g.selectSaveLoadRow(index)
		return
	}
	switch {
	case g.frontend.Mode == modeMenuMap && name == "MAPNAMES":
		g.mapIdx = index
		g.refreshRetailPanel()
	case g.frontend.Mode == modeMenuMission && name == "Campaign":
		g.campaignIdx = index
		g.missionIdx = 0
		g.refreshRetailPanel()
	case g.frontend.Mode == modeMenuMission && name == "Missions":
		g.missionIdx = index
		g.refreshRetailPanel()
	}
}

func (g *gameShell) activateEscape() {
	p := g.activePanel()
	if g.frontend.Mode == modeMenuMain || p == nil {
		return
	}
	if index := p.Window.EscapeDefaultIndex(); p.ActiveAt(index) {
		g.activateGadgetAt(p, index)
	}
}

func (g *gameShell) activateGadget(name string) {
	name = gui.CallbackName(name)
	key := frontendCallbackKey(name)
	// The save/load dialog is a child window over the screen that opened it,
	// so its controls are resolved before the underlying screen's [07 R-FE-01 §8].
	if g.activateSaveLoadGadget(name) {
		return
	}
	// The options root is likewise a child window over the screen that opened
	// it, so its controls resolve before that screen's [07 R-FE-01 §2]. It
	// plays its own rows of the same cue column — `STARTOPT`/`PREFS` are their
	// own rows of the transition table — so it sits above the frontendCue call
	// for the same reason the save/load dialog does: a click that landed on the
	// child window must not be given the cue keyed to the screen underneath it.
	if g.activateRetailOptionsGadget(name) {
		return
	}
	// The cue runs before the transition, in the screen handler that consumes
	// the fired result [07 R-FE-01 §2][07 R-WGT-01 §3]. It is read off the
	// screen the click landed on, so it must be taken before Navigate moves the
	// mode. `SINGLE`'s own `Options` button reaches this line and takes its
	// `options` cue here: the root is not open yet when that button fires, so
	// the early return above does not claim it.
	g.playMenuCue(frontendCue(g.frontend.Mode, key))
	if target, ok := g.frontend.Navigate(name); ok {
		g.openMenu(target)
		return
	}
	switch g.frontend.Mode {
	case modeMenuMain:
		switch name {
		case "EXIT":
			// Retail MAINMENU's EXIT callback enters frontend state 8 and
			// closes the process; it does not open the unrelated YESORNO
			// CD-player dialog used during frontend initialization. The
			// preferences are flushed first, since this is the process's last
			// chance to write them.
			g.saveSettings()
			os.Exit(0)
		}
	case modeMenuSingle:
		switch name {
		case "NewCamp":
			g.openMissionMenu(false)
		case "AnyMsn":
			g.openMissionMenu(true)
		case "LoadGame":
			// SINGLE is one of the three surfaces the load dialog is reached
			// from [07 R-FE-01 §8].
			g.openSaveLoadScreenReporting(loadScreenMode, saveLoadFromFrontend)
		case "Options":
			// `SINGLE` is the front end's only route to the options root; it
			// opens as a child window over `SINGLE` [07 R-FE-01 §2].
			g.openRetailOptionsScreenReporting()
		}
	case modeMenuMission:
		switch name {
		case "Start":
			// Campaign Start first opens MSNBRIEF. Its Start action later
			// emits the same shared battle request used by every entry path
			// [08 R-CAMP-01 §2].
			g.openCampaignBriefing()
		case "Difficulty":
			g.missionDifficultyValue = cycleInt(g.missionDifficultyValue, 0, 2, 1)
			if p := g.activePanel(); p != nil {
				p.SetStageAt(p.Index("Difficulty"), g.missionDifficultyValue)
			}
		case "Side0":
			g.missionSide = 0
			g.campaignIdx = 0
			g.missionIdx = 0
			g.refreshRetailPanel()
		case "Side1":
			g.missionSide = 1
			g.campaignIdx = 0
			g.missionIdx = 0
			g.refreshRetailPanel()
		}
	case modeMenuMap:
		switch name {
		case "PrevMenu":
			g.openMenu(g.mapReturn)
		case "LOAD":
			if len(g.maps) != 0 && g.mapIdx >= 0 && g.mapIdx < len(g.maps) {
				g.setup.MapName = g.maps[g.mapIdx]
				g.saveSettings()
				g.openMenu(g.mapReturn)
			}
		}
	case modeMenuSkirmish:
		g.activateSkirmishGadget(name)
	}
}

func (g *gameShell) activateSkirmishGadget(name string) {
	if name == "PrevMenu" {
		// Backing out of SKIRMISH.GUI still commits the setup, so the next
		// visit to the screen opens on the rows that were last configured.
		g.saveSettings()
		g.openMenu(modeMenuSingle)
		return
	}
	if name == "Start" {
		if message := g.retailSkirmishStartError(); message != "" {
			reportRetailMessageError(g.showRetailMessage(message))
			return
		}
		// the retail implementation's Start leaves the frontend for the loading screen,
		// which is what actually builds the session [07 §4].
		g.startBattleLoad(g.setup.MapName)
		return
	}
	if name == "SelectMap" {
		if len(g.maps) == 0 {
			reportRetailMessageError(g.showRetailMessage("There are no multiplayer maps to choose from"))
			return
		}
		g.mapReturn = modeMenuSkirmish
		g.openMenu(modeMenuMap)
		return
	}
	switch name {
	case "StartLocation":
		g.setup.Location ^= 1
	case "CommanderDeath":
		g.setup.CommanderDeath ^= 1
	case "Mapping":
		g.setup.Mapping ^= 1
	case "LineOfSight":
		g.cycleLineOfSight(1)
	case "Difficulty":
		g.setup.Difficulty = cycleInt(g.setup.Difficulty, 0, 2, 1)
	default:
		g.activateDynamicSkirmishGadget(name)
		return
	}
	g.refreshRetailPanel()
}

func (g *gameShell) activateDynamicSkirmishGadget(name string) {
	g.ensureRetailSkirmishControllers()
	index, kind, ok := dynamicSlot(name)
	if !ok {
		return
	}
	p := &g.setup.Players[index]
	switch kind {
	case "Player":
		g.cycleRetailController(index)
	case "Side":
		p.Side = cycleInt(p.Side, 0, 1, 1)
	case "Allies":
		p.AllyGroup = cycleInt(p.AllyGroup, 0, 5, 1)
	case "Metal":
		p.Metal = increaseResource(p.Metal)
	case "Energy":
		p.Energy = increaseResource(p.Energy)
	case "Color":
		p.Color = g.nextRetailPlayerColor(index, 1)
	}
	g.refreshRetailPanel()
}

// nextRetailPlayerColor follows the retail implementation, including its unusual fallback:
// the first candidate is checked only against other live rows, but once that
// candidate conflicts, the fallback scans every configured row, including
// open rows, from logo zero upward.
func (g *gameShell) nextRetailPlayerColor(slot, delta int) int {
	if g == nil || slot < 0 || slot >= session.SkirmishMaxPlayers {
		return 0
	}
	g.ensureRetailSkirmishControllers()
	const logoCount = session.SkirmishMaxPlayers
	if delta != 1 && delta != -1 {
		delta = 1
	}
	candidate := (g.setup.Players[slot].Color + delta) % logoCount
	// C's signed remainder is what the retail idiv produces. The only
	// negative remainder reachable from the normal logo range is -1.
	if candidate == -1 {
		candidate = logoCount - 1
	}

	conflict := false
	for i := 0; i < g.setup.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
		if i == slot || g.retailControllers[i] == 0 {
			continue
		}
		if g.setup.Players[i].Color == candidate {
			conflict = true
			break
		}
	}
	if !conflict {
		return candidate
	}

	// the retail implementation restarts at zero and tests all row color fields, without
	// filtering on controller state. If every stock logo is present it stores
	// -1, which is also what the frontend resolves back into the GUI art.
	for candidate = 0; candidate < logoCount; candidate++ {
		used := false
		for i := 0; i < g.setup.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
			if g.setup.Players[i].Color == candidate {
				used = true
				break
			}
		}
		if !used {
			return candidate
		}
	}
	return -1
}

// cycleRetailController is the retail implementation's exact 0→2→(0|1) controller
// transition. A row with value 0 is Open, 1 is Player, and 2 is Computer.
func (g *gameShell) cycleRetailController(slot int) {
	if slot < 0 || slot >= session.SkirmishMaxPlayers {
		return
	}
	current := g.retailControllers[slot]
	switch current {
	case 0:
		g.retailControllers[slot] = 2
	case 1:
		g.retailControllers[slot] = 0
	case 2:
		human := false
		for i := 0; i < session.SkirmishMaxPlayers; i++ {
			if g.retailControllers[i] == 1 {
				human = true
				break
			}
		}
		if human {
			g.retailControllers[slot] = 0
		} else {
			g.retailControllers[slot] = 1
		}
	default:
		g.retailControllers[slot] = 0
	}
	if g.retailControllers[slot] == 1 {
		g.setup.Players[slot].Controller = session.SkirmishDefaultController
	} else {
		// Open rows are excluded by skirmishConfigForStart. Keep the
		// compatibility data computer-coded so an accidental legacy caller
		// cannot turn every open row into another human.
		g.setup.Players[slot].Controller = 1
	}
}

func dynamicSlot(name string) (int, string, bool) {
	name = gui.CallbackName(name)
	for _, prefix := range []string{"Player", "Side", "Allies", "Metal", "Energy", "Color"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		value := strings.TrimPrefix(name, prefix)
		i, err := strconv.Atoi(value)
		if err != nil || i < 0 || i >= session.SkirmishMaxPlayers || name != prefix+strconv.Itoa(i) {
			return 0, "", false
		}
		return i, prefix, true
	}
	return 0, "", false
}

// frontendCallbackKey maps only established authored control literals to the
// cue table's internal keys. It is deliberately not a name normalization:
// fired-name comparisons remain full, case-sensitive byte equality [07
// R-WGT-02 §2].
func frontendCallbackKey(name string) string {
	name = gui.CallbackName(name)
	switch name {
	case "SINGLE":
		return "single"
	case "INTRO":
		return "intro"
	case "Credits":
		return "credits"
	case "NewCamp":
		return "newcamp"
	case "AnyMsn":
		return "anymsn"
	case "Skirmish":
		return "skirmish"
	case "Options":
		return "options"
	case "PrevMenu":
		return "prevmenu"
	case "LoadGame":
		return "loadgame"
	case "Start":
		return "start"
	case "Difficulty":
		return "difficulty"
	case "Side0":
		return "side0"
	case "Side1":
		return "side1"
	}
	return ""
}

func (g *gameShell) openMissionMenu(any bool) {
	g.missionAny = any
	g.campaignIdx = 0
	g.missionIdx = 0
	g.campaigns = nil
	g.campaignOptions = nil
	// NEWGAME starts a distinct campaign lifetime. Retail's 25 mission marks
	// begin Unknown here; continuation enters briefing directly and therefore
	// preserves the copied result bank instead of taking this reset path.
	g.campaignProgress = session.BankProgress{}
	for i := range g.campaignProgress.Thumbs {
		g.campaignProgress.Thumbs[i] = 'U'
	}
	g.campaignProgressSet = true
	g.openMenu(modeMenuMission)
}

// retailSkirmishStartError is the exact the retail implementation validation order and
// wording. The caller shows the result through MSGBOX.GUI, just as retail
// does, and starts a session only after every gate passes.
func (g *gameShell) retailSkirmishStartError() string {
	if g == nil || strings.TrimSpace(g.setup.MapName) == "" {
		return "The terrain for the selected map does not exist."
	}
	d := g.mapDataFor(g.setup.MapName)
	if d == nil || d.ota == nil || d.tnt == nil {
		return "The terrain for the selected map does not exist."
	}
	g.ensureRetailSkirmishControllers()
	n := g.setup.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > session.SkirmishMaxPlayers {
		n = session.SkirmishMaxPlayers
	}
	humans, computers := 0, 0
	for i := 0; i < n; i++ {
		switch g.retailControllers[i] {
		case 1:
			humans++
		case 2:
			computers++
		}
	}
	if humans == 0 || computers == 0 {
		return "There must be at least one player and one computer opponent"
	}
	playersForStart := computers + 1
	schema, err := mission.SelectNetworkSchema(d.ota, playersForStart)
	if err != nil || schema.StartPositions < playersForStart {
		return "There are too many players enabled for this map"
	}
	if g.retailAllPlayersSameAlliedGroup(n) {
		return "All players may not be in the same allied group."
	}
	return ""
}

// retailAllPlayersSameAlliedGroup mirrors the retail implementation. Allied group 5 is
// treated as the sentinel/unassigned group: if every live row is in group 5
// (or there are no live rows), retail does not report the same-group error.
// Once a non-5 live group is found, open rows are ignored and every other
// live row must match it.
func (g *gameShell) retailAllPlayersSameAlliedGroup(numPlayers int) bool {
	if g == nil {
		return false
	}
	if numPlayers < 0 {
		numPlayers = 0
	}
	if numPlayers > session.SkirmishMaxPlayers {
		numPlayers = session.SkirmishMaxPlayers
	}
	first := -1
	for i := 0; i < numPlayers; i++ {
		if g.retailControllers[i] != 0 && g.setup.Players[i].AllyGroup != 5 {
			first = g.setup.Players[i].AllyGroup
			break
		}
	}
	if first == -1 {
		return false
	}
	for i := 0; i < numPlayers; i++ {
		if g.retailControllers[i] == 0 || g.setup.Players[i].AllyGroup == first {
			continue
		}
		return false
	}
	return true
}
