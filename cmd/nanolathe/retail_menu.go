package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// ensureRetailSkirmishControllers mirrors the state that the retail implementation and
// the retail implementation hand to SKIRMISH.GUI when the per-player controller values are
// absent: all rows are open, then row zero is made the human player. The
// numeric values are kept separate from session.SkirmishConfig because the
// retail GUI distinguishes an open row (0) from a human (1), while the
// session compatibility API uses 0 for its human controller.
func (g *gameShell) ensureRetailSkirmishControllers() {
	if g == nil || g.retailControllersSet {
		return
	}
	for i := range g.retailControllers {
		g.retailControllers[i] = 0
	}
	if g.setup.NumPlayers > 0 {
		g.retailControllers[0] = 1
		// the retail implementation installs ally group 2 when it has to create the first
		// human row from an otherwise empty controller array.
		g.setup.Players[0].AllyGroup = 2
	}
	g.retailControllersSet = true
}

// skirmishConfigForStart converts the authored retail row state into the
// existing single-player session entry point. Open rows are not players;
// human/computer rows are compacted in display order just as the retail start
// gate counts non-zero controller rows.
func (g *gameShell) skirmishConfigForStart(mapName string) session.SkirmishConfig {
	g.ensureRetailSkirmishControllers()
	cfg := g.setup
	cfg.MapName = mapName
	out := 0
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		if g.retailControllers[i] == 0 {
			continue
		}
		cfg.Players[out] = g.setup.Players[i]
		if g.retailControllers[i] == 1 {
			cfg.Players[out].Controller = session.SkirmishDefaultController
		} else {
			cfg.Players[out].Controller = 1
		}
		out++
	}
	for i := out; i < session.SkirmishMaxPlayers; i++ {
		cfg.Players[i] = session.SkirmishPlayer{}
	}
	cfg.NumPlayers = out
	cfg.ApplyDefaults()
	return cfg
}

func menuKey(s string) string { return ui.Key(s) }

type retailMapData struct {
	label       string
	description string
	size        string
	ota         *formats.OTA
	tnt         *formats.TNT
}

func (g *gameShell) mapDataFor(name string) *retailMapData {
	if g == nil || g.cs == nil || g.cs.fs == nil || name == "" {
		return nil
	}
	if g.mapData == nil {
		g.mapData = make(map[string]*retailMapData)
	}
	key := strings.ToLower(name)
	if d, ok := g.mapData[key]; ok {
		return d
	}
	d := &retailMapData{label: name}
	ota, _ := formats.LoadOTAFile(g.cs.fs, "maps/"+name+".ota")
	d.ota = ota
	if ota != nil {
		if strings.TrimSpace(ota.MissionName) != "" {
			d.label = ota.MissionName
		}
		d.description = ota.MissionDescription
		d.size = ota.Size
	}
	tnt, _ := formats.LoadTNTFile(g.cs.fs, "maps/"+name+".tnt")
	d.tnt = tnt
	g.mapData[key] = d
	return d
}

func (g *gameShell) panelAssets() *retailPanelAssets {
	if g == nil || g.assets == nil {
		return nil
	}
	return g.assets.panel[g.frontend.Mode]
}

func (g *gameShell) panelBackground() *formats.PCX {
	p := g.panelAssets()
	if p == nil {
		return nil
	}
	if g.frontend.Mode != modeMenuMission || g.assets == nil {
		return p.background
	}
	// NEWGAME.GUI's opener takes a layout flag: 0 selects a campaign-only
	// layout (background newcampaign4/newcampaign4x, mission list hidden)
	// and 1 selects the play-any layout (background playanygame4, both
	// lists shown). Every reachable call site — both `NewCamp` and `AnyMsn`
	// on SINGLE.GUI — passes 1; the flag-0 layout is authored but its only
	// caller is unreachable, so newcampaign4/newcampaign4x are never
	// installed in a real session [07 §4 "SINGLE, NEWGAME and the briefing
	// screens" (R-FE-01 §4)]. Nanolathe therefore always uses the play-any
	// background regardless of which button opened this screen.
	return g.assets.missionAny
}

// applyRetailMissionLayout compresses the campaign and mission list
// gadgets to the play-any layout's rectangles. Retail's NEWGAME.GUI opener
// only ever runs with its layout flag set to 1 in this executable — the
// flag-0 (campaign-only, mission list hidden) call site is unreachable
// [07 §4 (R-FE-01 §4)] — so both `NewCamp` and `AnyMsn` apply this same
// compressed layout; the field that used to select between them controlled
// nothing retail ever exercises.
func (g *gameShell) applyRetailMissionLayout() {
	if g == nil || g.assets == nil {
		return
	}
	p := g.assets.panel[modeMenuMission]
	if p == nil || p.window == nil {
		return
	}
	setRect := func(name string, y, h int32) {
		for i := range p.window.Gadgets {
			if strings.EqualFold(p.window.Gadgets[i].Name, name) {
				p.window.Gadgets[i].Rect.Y = y
				p.window.Gadgets[i].Rect.H = h
				return
			}
		}
	}
	setRect("Campaign", 308, 48)
	setRect("CampaignKnob", 308, 48)
	setRect("Missions", 386, 62)
}

func (g *gameShell) refreshRetailPanel() {
	p := g.activePanel()
	if p == nil {
		return
	}
	switch g.frontend.Mode {
	case modeMenuMission:
		g.refreshMissionPanel()
	case modeMenuMap:
		g.refreshMapPanel()
	case modeMenuSkirmish:
		g.refreshSkirmishPanel()
	}
}

// resolveRetailButtonGeometry is the stateful part of the retail implementation that is
// easy to miss when treating a .GUI rectangle as final layout. Retail picks
// the closest stock/owned GAF frame and then stores that frame's dimensions
// back into the runtime gadget record. Hit testing and text centering must see
// those dimensions too.
func (g *gameShell) resolveRetailButtonGeometry() {
	p := g.activePanel()
	if p == nil || p.Window == nil {
		return
	}
	for i := range p.Window.Gadgets {
		gad := p.Window.Gadgets[i]
		if i == 0 || gad.Kind != gui.KindButton {
			continue
		}
		frame := g.gadgetArt(gad, p.StatusOf(gad.Name))
		if frame == nil {
			frame = g.retailButtonFrame(gad, p.StatusOf(gad.Name), false)
		}
		if frame == nil || frame.Width == 0 || frame.Height == 0 {
			continue
		}
		p.Window.Gadgets[i].Rect.W = int32(frame.Width)
		p.Window.Gadgets[i].Rect.H = int32(frame.Height)
	}
}

func (g *gameShell) refreshMissionPanel() {
	p := g.activePanel()
	if p == nil {
		return
	}
	if g.campaigns == nil {
		campaigns, err := mission.Discover(g.cs.fs)
		if err == nil {
			g.campaigns = campaigns
		}
	}
	// The retail implementation rebuilds the campaign-name array from
	// camps/*.tdf and retains only records whose HEADER campaignside
	// matches the selected side, plus the literal ALL. The campaign-only
	// layout that would instead hide this list and select "Arm Campaign"/
	// "Core Campaign" directly is never reached by any live call site
	// [07 §4 (R-FE-01 §4)], so the campaign list is always built and shown.
	g.campaignOptions = g.retailCampaignOptions()
	if len(g.campaignOptions) != 0 {
		if g.campaignIdx < 0 {
			g.campaignIdx = 0
		}
		if g.campaignIdx >= len(g.campaignOptions) {
			g.campaignIdx = len(g.campaignOptions) - 1
		}
	} else {
		g.campaignIdx = 0
	}
	campaignItems := make([]string, len(g.campaignOptions))
	for i := range g.campaignOptions {
		campaignItems[i] = g.campaignOptions[i].Name
	}
	g.setListItems("Campaign", campaignItems, g.campaignIdx)
	var missions []string
	if len(g.campaignOptions) != 0 && g.campaignIdx < len(g.campaignOptions) {
		for _, stub := range g.campaignOptions[g.campaignIdx].Missions {
			missions = append(missions, stub.Name)
		}
	}
	if len(missions) != 0 {
		if g.missionIdx < 0 {
			g.missionIdx = 0
		}
		if g.missionIdx >= len(missions) {
			g.missionIdx = len(missions) - 1
		}
	} else {
		g.missionIdx = 0
	}
	g.setListItems("Missions", missions, g.missionIdx)

	// NEWGAME's play-any layout — the only one any reachable call site
	// installs — always shows both lists [07 §4 (R-FE-01 §4)].
	p.SetActive("Campaign", true)
	p.SetActive("CampaignKnob", true)
	p.SetActive("Missions", true)
	p.SetActive("MissionsKnob", true)
	p.SetStatus("Difficulty", clampMenuStage(g.missionDifficultyValue, 3))
	if g.missionSide&1 == 0 {
		p.SetStatus("Side0", 1)
		p.SetStatus("Side1", 0)
		p.SetText("SIDENAME", "Arm Campaign")
	} else {
		p.SetStatus("Side0", 0)
		p.SetStatus("Side1", 1)
		p.SetText("SIDENAME", "Core Campaign")
	}
}

func retailCampaignSide(c mission.Campaign) string {
	if c.Document == nil || c.Document.Root == nil {
		return ""
	}
	header := c.Document.Root.Section("HEADER")
	if header == nil {
		return ""
	}
	side, _ := header.StringValue("campaignside", "")
	return strings.ToUpper(strings.TrimSpace(side))
}

// retailCampaignOptions rebuilds the campaign list for the selected side,
// admitting only records whose HEADER campaignside matches the selected
// side or the literal ALL [08 "Enumeration of campaigns"]. The campaign-only
// layout that instead selects "Arm Campaign"/"Core Campaign" directly and
// hides this list has no reachable caller [07 §4 (R-FE-01 §4)], so this is
// unconditional.
func (g *gameShell) retailCampaignOptions() []mission.Campaign {
	if g == nil {
		return nil
	}
	want := "ARM"
	if g.missionSide&1 != 0 {
		want = "CORE"
	}
	options := make([]mission.Campaign, 0, len(g.campaigns))
	for _, c := range g.campaigns {
		side := retailCampaignSide(c)
		if side == want || side == "ALL" {
			options = append(options, c)
		}
	}
	return options
}

func (g *gameShell) refreshMapPanel() {
	p := g.activePanel()
	if p == nil {
		return
	}
	if g.mapIdx < 0 {
		g.mapIdx = 0
	}
	if g.mapIdx >= len(g.maps) && len(g.maps) != 0 {
		g.mapIdx = len(g.maps) - 1
	}
	// The MAPNAMES items are the OTA file stems the retail implementation collected, in the
	// order the retail implementation sorted them. Retail opens no map file to build the
	// list — only the selected map's data is read, by the retail implementation — so this
	// must not touch mapDataFor per row.
	items := make([]string, len(g.maps))
	copy(items, g.mapLabels)
	for i := range items {
		if items[i] == "" {
			items[i] = g.maps[i]
		}
	}
	g.setListItems("MAPNAMES", items, g.mapIdx)
	if len(g.maps) == 0 {
		p.SetText("DESCRIPTION", "")
		p.SetText("SIZE", "")
		return
	}
	if d := g.mapDataFor(g.maps[g.mapIdx]); d != nil {
		// the retail implementation puts the OTA missiondescription in DESCRIPTION
		// verbatim — the authored string already carries the "16 X 17 " size
		// prefix — and formats OTA memory, the localized "Players" label, and
		// OTA numplayers through "%s  %s: %s" into SIZE. Both pairs of spaces
		// are part of the retail format string.
		p.SetText("DESCRIPTION", d.description)
		size := ""
		if d.ota != nil {
			memory := strings.TrimSpace(d.ota.Memory)
			numPlayers := strings.TrimSpace(d.ota.NumPlayers)
			if memory != "" || numPlayers != "" {
				size = fmt.Sprintf("%s  %s: %s", memory, "Players", numPlayers)
			}
		}
		p.SetText("SIZE", size)
	}
}

func (g *gameShell) refreshSkirmishPanel() {
	p := g.activePanel()
	if p == nil {
		return
	}
	g.ensureRetailSkirmishControllers()
	// the retail implementation writes the selected map's list entry — the OTA file stem —
	// into the MapName gadget. It does not reopen the map to read a title.
	p.SetText("MapName", g.setup.MapName)
	if g.setup.Location == 0 {
		p.SetStatus("StartLocation", 1)
		p.SetHelp("StartLocation", "Commanders are randomly placed on the battle field.")
	} else {
		p.SetStatus("StartLocation", 0)
		p.SetHelp("StartLocation", "Commanders are placed at pre-determined locations.")
	}
	if g.setup.CommanderDeath == 0 {
		p.SetStatus("CommanderDeath", 1)
		p.SetHelp("CommanderDeath", "Game continues after Commander is destroyed.")
	} else {
		p.SetStatus("CommanderDeath", 0)
		p.SetHelp("CommanderDeath", "Game ends when commander is destroyed.")
	}
	if g.setup.Mapping == 0 {
		p.SetStatus("Mapping", 1)
		p.SetHelp("Mapping", "Terrain is visible.")
	} else {
		p.SetStatus("Mapping", 0)
		p.SetHelp("Mapping", "Terrain is blacked out until explored.")
	}
	if g.setup.LineOfSight == 0 {
		p.SetStatus("LineOfSight", 0)
		p.SetHelp("LineOfSight", "All mapped terrain is visible.")
	} else if g.setup.LOSType == 1 {
		p.SetStatus("LineOfSight", 1)
		p.SetHelp("LineOfSight", "Terrain elevations affect a unit's view.")
	} else {
		p.SetStatus("LineOfSight", 2)
		p.SetHelp("LineOfSight", "Terrain elevations do not affect a unit's view.")
	}
	p.SetStatus("Difficulty", clampMenuStage(g.setup.Difficulty, 3))

	// Player%d/Side%d/etc. are appended at runtime by the retail implementation. They are
	// not present in SKIRMISH.GUI on disk, but are still retail gadgets with
	// fixed coordinates and stock GAF art, so refresh their authored records.
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		rowActive := i < g.setup.NumPlayers
		player := &g.setup.Players[i]
		prefix := strconv.Itoa(i)
		controller := g.retailControllers[i]
		p.SetActive("Player"+prefix, rowActive)
		// the retail implementation hides every dependent control for an open row. The
		// Player%d button remains the hit target that turns it into a
		// computer row.
		configured := rowActive && controller != 0
		p.SetActive("Side"+prefix, configured)
		p.SetActive("Allies"+prefix, configured)
		p.SetActive("Metal"+prefix, configured)
		p.SetActive("Energy"+prefix, configured)
		p.SetActive("Color"+prefix, configured)
		if rowActive {
			switch controller {
			case 1:
				p.SetText("Player"+prefix, "Player")
			case 2:
				p.SetText("Player"+prefix, "Computer")
			default:
				p.SetText("Player"+prefix, "Open")
			}
			p.SetText("Metal"+prefix, strconv.Itoa(player.Metal))
			p.SetText("Energy"+prefix, strconv.Itoa(player.Energy))
			// Controller is text written by the retail implementation; the skirmname art
			// retains its ordinary/hover frame state.
			p.SetStatus("Player"+prefix, 0)
			p.SetStatus("Side"+prefix, player.Side)
			p.SetStatus("Allies"+prefix, g.retailAllyIconFrame(i))
			p.SetStatus("Color"+prefix, player.Color)
		}
	}
	p.SetHelp("Allies0", "Click to select an allegiance symbol.")
	p.SetHelp("Metal0", "Left click to increase metal. Right click to decrease metal.")
	p.SetHelp("Energy0", "Left click to increase energy. Right click to decrease energy.")
	for i := 1; i < session.SkirmishMaxPlayers; i++ {
		prefix := strconv.Itoa(i)
		p.SetHelp("Allies"+prefix, "Click to select an allegiance symbol.")
		p.SetHelp("Metal"+prefix, "Left click to increase metal. Right click to decrease metal.")
		p.SetHelp("Energy"+prefix, "Left click to increase energy. Right click to decrease energy.")
	}
}

// retailAllyIconFrame is the retail implementation, which runs after every change to a row
// and rewrites each Allies%d surface's frame index. For one row it counts the
// configured rows — controller not Open — whose alliance number equals that
// row's, then picks the TEAMICONSx frame: none gives 10, exactly one gives
// group*2+1, and two or more give group*2. The entry's twelve frames are six
// symbols in that order, the odd one split in half and the even one whole, so
// a row alone in its alliance shows the broken symbol and a row sharing it
// shows the joined one. Alliance 5 lands on frames 10 and 11, which are both
// blank, which is what makes it read as "no allegiance" [07 §4].
func (g *gameShell) retailAllyIconFrame(slot int) int {
	const blankAllyFrame = 10
	if g == nil || slot < 0 || slot >= session.SkirmishMaxPlayers {
		return blankAllyFrame
	}
	group := g.setup.Players[slot].AllyGroup
	shared := 0
	for i := 0; i < g.setup.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
		if g.retailControllers[i] != 0 && g.setup.Players[i].AllyGroup == group {
			shared++
		}
	}
	switch shared {
	case 0:
		return blankAllyFrame
	case 1:
		return group*2 + 1
	default:
		return group * 2
	}
}

func (g *gameShell) updateHoverHelp(x, y int32) {
	p := g.activePanel()
	if p == nil {
		return
	}
	help := ""
	// The hover test skips only hidden gadgets, so a greyed button still feeds
	// HELPTEXT its help line [07 R-WGT-01 §1 step 6][07 R-WGT-01 §13].
	if idx := p.HitTest(x, y); idx >= 0 {
		if gad, ok := g.currentGadget(idx); ok {
			help = p.HelpOf(gad.Name)
		}
	}
	p.SetText("HELPTEXT", help)
}

// installSkirmishDynamicGadgets supplies authored lobby row values to the
// canonical UI runtime builder. The builder owns gadget construction and
// geometry; this composition root retains only skirmish configuration.
func (g *gameShell) installSkirmishDynamicGadgets() {
	if g.assets == nil || g.assets.panel[modeMenuSkirmish] == nil || g.assets.panel[modeMenuSkirmish].window == nil {
		return
	}
	n := g.setup.NumPlayers
	if n < 1 {
		n = 1
	}
	if n > session.SkirmishMaxPlayers {
		n = session.SkirmishMaxPlayers
	}
	slots := make([]ui.SkirmishSlot, n)
	for i := range slots {
		slots[i] = ui.SkirmishSlot{Side: g.setup.Players[i].Side, Color: g.setup.Players[i].Color}
	}
	ui.InstallSkirmishDynamicGadgets(g.assets.panel[modeMenuSkirmish].window, slots)
}

func clampMenuStage(value, stages int) int {
	if value < 0 {
		return 0
	}
	if value >= stages {
		return stages - 1
	}
	return value
}

func (g *gameShell) setListItems(name string, items []string, selected int) {
	p := g.activePanel()
	if p == nil {
		return
	}
	key := menuKey(name)
	oldItems, oldSelected, _, exists := p.ListValues(key)
	if !exists {
		return
	}
	changed := len(oldItems) != len(items)
	if !changed {
		for i := range items {
			if oldItems[i] != items[i] {
				changed = true
				break
			}
		}
	}
	if changed {
		p.SetList(key, items)
		// A new row source starts at its first row; an identical refresh below
		// leaves the user's manual scrollbar position untouched.
		visible := g.retailListVisibleRows(key)
		maxTop := len(items) - visible
		if maxTop < 0 {
			maxTop = 0
		}
		p.SetListTop(key, 0, maxTop)
	}
	if len(items) == 0 {
		p.SetList(key, nil)
		return
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(items) {
		selected = len(items) - 1
	}
	selectionChanged := oldSelected != selected
	if changed || selectionChanged {
		p.SetListSelection(key, selected, g.retailListVisibleRows(key))
		g.ensureRetailListVisible(key)
	}
}

func (g *gameShell) retailListVisibleRows(name string) int {
	p := g.activePanel()
	if p == nil || p.Window == nil {
		return 1
	}
	for i, gad := range p.Window.Gadgets {
		if gad.Kind != gui.KindListBox || !strings.EqualFold(gad.Name, name) {
			continue
		}
		return retailVisibleListRows(p.Window.PlacedRect(i), retailListItemHeight(gad, g.retailTextHeight()))
	}
	return 1
}

func (g *gameShell) ensureRetailListVisible(name string) {
	p := g.activePanel()
	if p == nil || p.Window == nil {
		return
	}
	items, selected, _, ok := p.ListValues(menuKey(name))
	if !ok || len(items) == 0 {
		return
	}
	for i, gad := range p.Window.Gadgets {
		if gad.Kind != gui.KindListBox || !strings.EqualFold(gad.Name, name) {
			continue
		}
		p.SetListSelection(name, selected, retailVisibleListRows(p.Window.PlacedRect(i), retailListItemHeight(gad, g.retailTextHeight())))
		return
	}
}

func (g *gameShell) currentGadget(index int) (gui.Gadget, bool) {
	p := g.activePanel()
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) {
		return gui.Gadget{}, false
	}
	return p.Window.Gadgets[index], true
}

func (g *gameShell) gadgetArt(gad gui.Gadget, status int) *formats.GAFFrame {
	p := g.panelAssets()
	if p == nil {
		return nil
	}
	artName := gad.Art
	if artName == "" {
		artName = gad.Name
	}
	var e *formats.GAFEntry
	if p.art != nil {
		if found, ok := p.art.Find(artName); ok {
			e = found
		}
	}
	// The executable asks the skirmish runtime GAF for TEAMICONSx. In the
	// installed retail resource set that entry is named "ally icons" (the
	// same 38×20 stock strip); retain the executable name in the runtime
	// gadget and resolve the mounted resource spelling here.
	if e == nil && strings.EqualFold(artName, "TEAMICONSx") && p.art != nil {
		if found, ok := p.art.Find("ally icons"); ok {
			e = found
		}
	}
	if e == nil && g.assets != nil && g.assets.logos != nil {
		if found, ok := g.assets.logos.Find(artName); ok {
			e = found
		}
	}
	if e == nil || len(e.Frames) == 0 {
		return nil
	}
	idx := status
	if idx < 0 {
		idx = 0
	}
	if idx >= len(e.Frames) {
		idx = len(e.Frames) - 1
	}
	return e.Frames[idx].Frame
}
