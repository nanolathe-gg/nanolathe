package main

// The front-end pointer and keyboard pass: gadget activation, the escape
// route, list clicks and the skirmish setup rows [07 §5] [07 R-FE-01 §5].

import (
	"os"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func (g *gameShell) menuInput(cl *client.Client) {
	if cl == nil || cl.Input() == nil {
		return
	}
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
	in := cl.Input()
	mouse := in.Mouse
	kbd := in.Kbd
	leftPressed := mouse.Pressed(input.MouseButtonLeft)
	leftReleased := mouse.Released(input.MouseButtonLeft)
	rightPressed := mouse.Pressed(input.MouseButtonRight)
	rightReleased := mouse.Released(input.MouseButtonRight)
	wasScrollDrag := p.ScrollDragging()
	g.updateRetailScrollbarDrag(mouse)
	if mouse.Held(input.MouseButtonLeft) && !leftPressed && !p.ScrollDragging() && p.PressedIndex() >= 0 {
		x, y := int32(mouse.X), int32(mouse.Y)
		// The captured gadget keeps receiving the pass while a button is held,
		// so this is the press-time predicate, not the hover one
		// [07 R-WGT-01 §1 "Capture"].
		if idx := p.PressTest(x, y); idx >= 0 {
			if idx == p.PressedIndex() {
				gad, ok := g.currentGadget(idx)
				if !ok || gad.Kind != gui.KindScrollBar {
					goto noRetailArrowRepeat
				}
				geometry, geometryOK := g.retailScrollbarGeometry(gad, p.Window.PlacedRect(idx))
				if geometryOK {
					coordinate := int(x)
					if geometry.vertical {
						coordinate = int(y)
					}
					if coordinate < geometry.axisStart {
						g.adjustRetailScrollbar(gad, -1)
					} else if coordinate >= geometry.axisEnd {
						g.adjustRetailScrollbar(gad, 1)
					}
				}
			}
		}
	}

noRetailArrowRepeat:
	if mouse.Scrolled() {
		g.scrollAt(int32(mouse.X), int32(mouse.Y), mouse.ScrollY)
	}
	if leftPressed {
		p.SetPressed(-1)
		x, y := int32(mouse.X), int32(mouse.Y)
		// A press takes the capture; a greyed gadget returns before its own hit
		// test and never captures [07 R-WGT-01 §13].
		idx := p.PressTest(x, y)
		if idx >= 0 {
			// Retail's GUI pump gives the clicked gadget focus before running
			// its callback, so a following Return/Space activates that same
			// control rather than the previous default.
			p.SetFocus(idx)
			gad, _ := g.currentGadget(idx)
			p.SetPressed(idx)
			if gad.Kind == gui.KindScrollBar {
				g.clickRetailScrollbar(idx, gad, p.Window.PlacedRect(idx), x, y)
			}
		}
	}
	if leftReleased {
		pending := p.PressedIndex()
		x, y := int32(mouse.X), int32(mouse.Y)
		action := p.ReleaseAction(x, y)
		if !wasScrollDrag && pending >= 0 && action.Kind == ui.ActionActivate && action.Index == pending {
			if gad, ok := g.currentGadget(action.Index); ok {
				switch gad.Kind {
				case gui.KindListBox:
					g.clickList(gad, p.Window.PlacedRect(action.Index), x, y)
				case gui.KindScrollBar:
					g.releaseRetailScrollbar(gad, p.Window.PlacedRect(action.Index), x, y)
				default:
					// Retail's pump returns to the window loop as soon as
					// a callback has run, and re-reads the panel on the
					// next pass. A callback is free to close the window it
					// was invoked from — Start leaves for the loading
					// screen — so nothing after this may touch the active panel.
					g.activateGadget(gad.Name)
					return
				}
			}
		}
	}
	if rightPressed {
		p.SetRightPressed(-1)
		if g.frontend.Mode == modeMenuSkirmish {
			x, y := int32(mouse.X), int32(mouse.Y)
			if idx := p.PressTest(x, y); idx >= 0 {
				if gad, ok := g.currentGadget(idx); ok {
					key := menuKey(gad.Name)
					if strings.HasPrefix(key, "metal") || strings.HasPrefix(key, "energy") || strings.HasPrefix(key, "color") {
						p.SetRightPressed(idx)
					}
				}
			}
		}
	}
	if rightReleased {
		pending := p.RightPressedIndex()
		p.SetRightPressed(-1)
		if g.frontend.Mode == modeMenuSkirmish && pending >= 0 {
			x, y := int32(mouse.X), int32(mouse.Y)
			if idx := p.PressTest(x, y); idx == pending {
				if gad, ok := g.currentGadget(idx); ok {
					key := menuKey(gad.Name)
					if strings.HasPrefix(key, "metal") || strings.HasPrefix(key, "energy") || strings.HasPrefix(key, "color") {
						if slot, ok := dynamicSlot(key); ok {
							if strings.HasPrefix(key, "metal") {
								g.setup.Players[slot].Metal = decreaseResource(g.setup.Players[slot].Metal)
							} else if strings.HasPrefix(key, "energy") {
								g.setup.Players[slot].Energy = decreaseResource(g.setup.Players[slot].Energy)
							} else {
								g.setup.Players[slot].Color = g.nextRetailPlayerColor(slot, -1)
							}
							g.refreshRetailPanel()
						}
					}
				}
			}
		}
	}
	if p == nil || p.Window == nil {
		// A pointer callback above closed the panel.
		return
	}
	if kbd.KeyDown(input.KeyEscape) {
		g.activateEscape()
		return
	}
	if kbd.KeyDown(input.KeyEnter) || kbd.KeyDown(input.KeySpace) {
		name := ""
		if idx := p.Focused(); idx >= 0 {
			if gad, ok := g.currentGadget(idx); ok && p.ActiveOf(gad.Name) {
				if action := p.Activate(idx); action.Kind == ui.ActionActivate {
					name = action.Gadget
				}
			}
		}
		if name == "" {
			name = p.Window.Header.CrDefault
		}
		if name != "" && g.hasActiveGadget(name) {
			g.activateGadget(name)
			return
		}
	}
	for i, gad := range p.Window.Gadgets {
		if i == 0 || gad.QuickKey == 0 || !p.ActiveOf(gad.Name) {
			continue
		}
		if quickKeyDown(kbd, gad.QuickKey) {
			g.activateGadget(gad.Name)
			return
		}
	}
	if kbd.KeyDown(input.KeyUp) || kbd.KeyDown(input.KeyDown) {
		g.adjustFocusedList(kbd.KeyDown(input.KeyUp))
	}
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
	if in.Mouse.Pressed(input.MouseButtonLeft) {
		m.Press(int32(in.Mouse.X), int32(in.Mouse.Y))
	}
	if in.Mouse.Released(input.MouseButtonLeft) {
		action := m.ReleaseAction(int32(in.Mouse.X), int32(in.Mouse.Y))
		if action.Kind == ui.ActionActivate && strings.EqualFold(action.Gadget, "OK") {
			g.frontend.Panels.CloseModal()
		}
	}
	if in.Kbd.KeyDown(input.KeyEscape) || in.Kbd.KeyDown(input.KeyEnter) || in.Kbd.KeyDown(input.KeySpace) {
		g.frontend.Panels.CloseModal()
	}
}

func quickKeyDown(kbd *input.KeyboardState, quick byte) bool {
	if kbd == nil {
		return false
	}
	if quick >= 'a' && quick <= 'z' {
		quick -= 'a' - 'A'
	}
	if quick >= 'A' && quick <= 'Z' {
		return kbd.KeyDown(input.KeyA + input.Key(quick-'A'))
	}
	if quick >= '0' && quick <= '9' {
		return kbd.KeyDown(input.Key0 + input.Key(quick-'0'))
	}
	return false
}

func (g *gameShell) adjustFocusedList(up bool) {
	p := g.activePanel()
	if p == nil {
		return
	}
	var name string
	switch g.frontend.Mode {
	case modeMenuMap:
		name = "MAPNAMES"
	case modeMenuMission:
		// Both lists are always live in the play-any layout [07 §4
		// (R-FE-01 §4)], so prefer whichever list the player actually
		// focused (by clicking into it); retail's stated initial focus for
		// this layout is "Missions", which is also the default here.
		name = "Missions"
		if gad, ok := g.currentGadget(p.Focused()); ok {
			if strings.EqualFold(gad.Name, "Campaign") {
				name = "Campaign"
			}
		}
	}
	l := p.ListFor(name)
	if l == nil || l.Len() == 0 {
		return
	}
	selected := l.Selected()
	if up {
		selected = cycleInt(selected, 0, l.Len()-1, -1)
	} else {
		selected = cycleInt(selected, 0, l.Len()-1, 1)
	}
	l.SetSelected(selected)
	g.ensureRetailListVisible(name)
	g.commitListSelection(name, selected)
}

func (g *gameShell) scrollAt(x, y int32, amount float32) {
	p := g.activePanel()
	if p == nil || p.Window == nil {
		return
	}
	for i, gad := range p.Window.Gadgets {
		if gad.Kind != gui.KindListBox || !p.ActiveOf(gad.Name) {
			continue
		}
		r := p.Window.PlacedRect(i)
		if !pointInRect(x, y, r) {
			continue
		}
		l := p.ListFor(gad.Name)
		if l == nil || l.Len() == 0 {
			return
		}
		// Ebitengine reports a positive wheel delta for motion toward the
		// top, matching the retail wheel token's sign.
		itemHeight := retailListItemHeight(gad, g.retailTextHeight())
		visible := retailVisibleListRows(r, itemHeight)
		delta := 0
		if amount > 0 {
			delta = -1
		} else if amount < 0 {
			delta = 1
		}
		p.ScrollList(gad.Name, delta, visible)
		return
	}
}

func (g *gameShell) clickList(gad gui.Gadget, r gui.Rect, x, y int32) {
	p := g.activePanel()
	if p == nil {
		return
	}
	l := p.ListFor(gad.Name)
	if l == nil || l.Len() == 0 || !g.hasRetailTextFont() {
		return
	}
	h := retailListItemHeight(gad, g.retailTextHeight())
	visible := retailVisibleListRows(r, h)
	localY := int(y-r.Y) - 2
	if localY < 0 {
		return
	}
	row := localY / h
	if row >= visible {
		return
	}
	idx := l.Top() + row
	if idx < 0 || idx >= l.Len() {
		return
	}
	l.SetSelected(idx)
	g.ensureRetailListVisible(gad.Name)
	g.commitListSelection(gad.Name, idx)
}

func (g *gameShell) commitListSelection(name string, index int) {
	if g.saveLoadPanelActive() && strings.EqualFold(name, "GAMES") {
		// Selecting a row refreshes the summary panel and copies the entry's
		// description into GAMENAME [08 R-SAVE-02 §1].
		g.selectSaveLoadRow(index)
		return
	}
	switch {
	case g.frontend.Mode == modeMenuMap && strings.EqualFold(name, "MAPNAMES"):
		g.mapIdx = index
		g.refreshRetailPanel()
	case g.frontend.Mode == modeMenuMission && strings.EqualFold(name, "Campaign"):
		g.campaignIdx = index
		g.missionIdx = 0
		g.refreshRetailPanel()
	case g.frontend.Mode == modeMenuMission && strings.EqualFold(name, "Missions"):
		g.missionIdx = index
		g.refreshRetailPanel()
	}
}

func (g *gameShell) activateEscape() {
	p := g.activePanel()
	if g.frontend.Mode == modeMenuMain {
		return
	}
	if p != nil && p.Window != nil && p.Window.Header.EscDefault != "" &&
		g.hasActiveGadget(p.Window.Header.EscDefault) {
		g.activateGadget(p.Window.Header.EscDefault)
		return
	}
	for _, name := range []string{"PrevMenu", "PREVMENU"} {
		if g.hasActiveGadget(name) {
			g.activateGadget(name)
			return
		}
	}
	// A window that authors an empty `escdefault` binds Escape to the first
	// button whose name begins `PREV` or `Cancel`, case-insensitively
	// [07 R-FE-01 §12]. `STARTOPT.GUI` authors none and names its OK button
	// `PREV`, so this is what closes the options root.
	if p == nil || p.Window == nil {
		return
	}
	for _, gad := range p.Window.Gadgets {
		if gad.Kind != gui.KindButton {
			continue
		}
		key := menuKey(gad.Name)
		if !strings.HasPrefix(key, "prev") && !strings.HasPrefix(key, "cancel") {
			continue
		}
		if !p.ActiveOf(gad.Name) {
			continue
		}
		g.activateGadget(gad.Name)
		return
	}
}

func (g *gameShell) hasActiveGadget(name string) bool {
	p := g.activePanel()
	if p == nil || p.Window == nil || !p.ActiveOf(name) {
		return false
	}
	for _, gad := range p.Window.Gadgets {
		if strings.EqualFold(gad.Name, name) {
			return true
		}
	}
	return false
}

func (g *gameShell) activateGadget(name string) {
	key := menuKey(name)
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
		switch key {
		case "exit":
			// Retail MAINMENU's EXIT callback enters frontend state 8 and
			// closes the process; it does not open the unrelated YESORNO
			// CD-player dialog used during frontend initialization. The
			// preferences are flushed first, since this is the process's last
			// chance to write them.
			g.saveSettings()
			os.Exit(0)
		}
	case modeMenuSingle:
		switch key {
		case "newcamp":
			g.openMissionMenu(false)
		case "anymsn":
			g.openMissionMenu(true)
		case "loadgame":
			// SINGLE is one of the three surfaces the load dialog is reached
			// from [07 R-FE-01 §8].
			g.openSaveLoadScreenReporting(loadScreenMode, saveLoadFromFrontend)
		case "options":
			// `SINGLE` is the front end's only route to the options root; it
			// opens as a child window over `SINGLE` [07 R-FE-01 §2].
			g.openRetailOptionsScreenReporting()
		}
	case modeMenuMission:
		switch key {
		case "start":
			// Campaign Start first opens MSNBRIEF. Its Start action later
			// emits the same shared battle request used by every entry path
			// [08 R-CAMP-01 §2].
			g.openCampaignBriefing()
		case "difficulty":
			g.missionDifficultyValue = cycleInt(g.missionDifficultyValue, 0, 2, 1)
			if p := g.activePanel(); p != nil {
				p.SetStatus("Difficulty", g.missionDifficultyValue)
			}
		case "side0":
			g.missionSide = 0
			g.campaignIdx = 0
			g.missionIdx = 0
			g.refreshRetailPanel()
		case "side1":
			g.missionSide = 1
			g.campaignIdx = 0
			g.missionIdx = 0
			g.refreshRetailPanel()
		}
	case modeMenuMap:
		switch key {
		case "prevmenu":
			g.openMenu(g.mapReturn)
		case "load":
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
	key := menuKey(name)
	if key == "prevmenu" {
		// Backing out of SKIRMISH.GUI still commits the setup, so the next
		// visit to the screen opens on the rows that were last configured.
		g.saveSettings()
		g.openMenu(modeMenuSingle)
		return
	}
	if key == "start" {
		if message := g.retailSkirmishStartError(); message != "" {
			reportRetailMessageError(g.showRetailMessage(message))
			return
		}
		// the retail implementation's Start leaves the frontend for the loading screen,
		// which is what actually builds the session [07 §4].
		g.startBattleLoad(g.setup.MapName)
		return
	}
	if key == "selectmap" {
		if len(g.maps) == 0 {
			reportRetailMessageError(g.showRetailMessage("There are no multiplayer maps to choose from"))
			return
		}
		g.mapReturn = modeMenuSkirmish
		g.openMenu(modeMenuMap)
		return
	}
	switch key {
	case "startlocation":
		g.setup.Location ^= 1
	case "commanderdeath":
		g.setup.CommanderDeath ^= 1
	case "mapping":
		g.setup.Mapping ^= 1
	case "lineofsight":
		g.cycleLineOfSight(1)
	case "difficulty":
		g.setup.Difficulty = cycleInt(g.setup.Difficulty, 0, 2, 1)
	default:
		g.activateDynamicSkirmishGadget(key)
		return
	}
	g.refreshRetailPanel()
}

func (g *gameShell) activateDynamicSkirmishGadget(key string) {
	g.ensureRetailSkirmishControllers()
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		suffix := strconv.Itoa(i)
		p := &g.setup.Players[i]
		switch {
		case key == "player"+suffix:
			g.cycleRetailController(i)
		case key == "side"+suffix:
			p.Side = cycleInt(p.Side, 0, 1, 1)
		case key == "allies"+suffix:
			p.AllyGroup = cycleInt(p.AllyGroup, 0, 5, 1)
		case key == "metal"+suffix:
			p.Metal = increaseResource(p.Metal)
		case key == "energy"+suffix:
			p.Energy = increaseResource(p.Energy)
		case key == "color"+suffix:
			p.Color = g.nextRetailPlayerColor(i, 1)
		default:
			continue
		}
		g.refreshRetailPanel()
		return
	}
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

func dynamicSlot(key string) (int, bool) {
	for _, prefix := range []string{"player", "side", "allies", "metal", "energy", "color"} {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		value := strings.TrimPrefix(key, prefix)
		i, err := strconv.Atoi(value)
		if err != nil || i < 0 || i >= session.SkirmishMaxPlayers {
			return 0, false
		}
		return i, true
	}
	return 0, false
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
