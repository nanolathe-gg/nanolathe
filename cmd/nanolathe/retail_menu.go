package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
)

type retailScrollbarGeometry struct {
	vertical   bool
	axisStart  int
	axisEnd    int
	thumbLen   int
	travel     int
	maxTop     int
	arrowStart int
	arrowEnd   int
}

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
	if g.missionAny {
		return g.assets.missionAny
	}
	if len(g.campaigns) > 2 {
		return g.assets.missionCampaign
	}
	return g.assets.missionSmall
}

// applyRetailMissionLayout is the small but visible runtime mutation in
// the retail implementation. The same NEWGAME.GUI window is used for New Campaign and Play
// Any Game; Play Any compresses the campaign and mission list controls rather
// than loading a custom panel. These are local GUI coordinates (the panel's
// header is at the origin in this retail resource).
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
	if g.missionAny {
		setRect("Campaign", 308, 48)
		setRect("CampaignKnob", 308, 48)
		setRect("Missions", 386, 62)
		return
	}
	setRect("Campaign", 314, 142)
	setRect("CampaignKnob", 314, 136)
	setRect("Missions", 386, 78)
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
	// the retail implementation rebuilds the campaign-name array from camps/*.tdf and
	// retains only records whose HEADER campaignside matches the selected
	// side, plus the literal ALL. When the retail campaign count is <=2,
	// the retail implementation hides the list and the retail implementation selects the authored
	// "Arm Campaign"/"Core Campaign" filename directly instead.
	showCampaign := g.missionAny || len(g.campaigns) > 2
	g.campaignOptions = g.retailCampaignOptions(showCampaign)
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

	// NEWGAME's setup routine enables the lists according to the same
	// campaign-count/Play-Any branch as the retail implementation.
	p.SetActive("Campaign", showCampaign)
	p.SetActive("CampaignKnob", showCampaign)
	p.SetActive("Missions", g.missionAny)
	p.SetActive("MissionsKnob", g.missionAny)
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

func (g *gameShell) retailCampaignOptions(showCampaign bool) []mission.Campaign {
	if g == nil {
		return nil
	}
	if !showCampaign {
		name := "Arm Campaign"
		if g.missionSide&1 != 0 {
			name = "Core Campaign"
		}
		for _, c := range g.campaigns {
			if strings.EqualFold(c.Name, name) {
				return []mission.Campaign{c}
			}
		}
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

func (g *gameShell) drawRetailPanel(c *client.Client) {
	if g != nil && g.briefing != nil && g.briefing.State() == BriefingOpen {
		g.drawBriefing(c)
		return
	}
	p := g.activePanel()
	if p == nil || p.Window == nil || g.assets == nil {
		return
	}
	if g.frontend.Mode == modeMenuSkirmish {
		g.updateHoverHelp(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
	}
	// Back to front along the window chain. Modal entries are presented by
	// drawRetailModal after all saved-under screens; every entry is authored.
	entries := g.frontend.Panels.Entries()
	for i, entry := range entries {
		if entry.Modal || entry.Panel == nil || entry.Panel.Window == nil {
			continue
		}
		mode := g.panelMode(entry.Panel)
		if i == len(entries)-1 && g.frontend.Panels.Modal() == nil {
			mode = g.frontend.Mode
		}
		g.drawRetailWindow(c, mode, entry.Panel)
	}
}

func (g *gameShell) panelMode(panel *ui.Panel) shellMode {
	if g != nil && g.assets != nil && panel != nil {
		for mode := modeMenuMain; mode <= modeMenuSkirmish; mode++ {
			if asset := g.assets.panel[mode]; asset != nil && asset.window == panel.Window {
				return mode
			}
		}
	}
	return g.frontend.Mode
}

// drawRetailWindow composes one window of the chain. mode selects the resource
// set the gadget art is resolved against, so a window beneath the active one
// still draws with its own GUI GAF.
func (g *gameShell) drawRetailWindow(c *client.Client, mode shellMode, p *ui.Panel) {
	if p == nil || p.Window == nil {
		return
	}
	savedMode := g.frontend.Mode
	g.frontend.SetMode(mode)
	defer func() { g.frontend.SetMode(savedMode) }()

	background := g.panelBackground()
	if saveLoadAssets != nil && p == saveLoadPanel {
		// The save/load dialog is a child window with its own authored
		// backdrop; it must not borrow the surface it was opened over
		// [07 R-FE-01 §8].
		background = saveLoadAssets.background
	}
	if bg := background; bg != nil {
		// the retail implementation copies the window's background bitmap into the window's
		// own surface at (0,0), and that surface is the window rectangle. The
		// bitmap therefore lands at the window origin and anything past the
		// rectangle is not part of the window [07 §4].
		r := p.Window.Rect
		c.UIBlitPCXClipped(bg, int(r.X), int(r.Y), int(r.X), int(r.Y), int(r.W), int(r.H))
	} else {
		// the retail implementation fills a background-less window by tiling its art over
		// the window rectangle; the stock fallback entry is BackTile.
		g.drawPanelTile(c, p.Window.Rect)
	}
	for i, gad := range p.Window.Gadgets {
		if i == 0 || !p.ActiveOf(gad.Name) {
			continue
		}
		r := p.Window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			g.drawRetailButton(c, p, i, gad, r)
		case gui.KindListBox:
			g.drawRetailList(c, p, gad, r)
		case gui.KindScrollBar:
			g.drawRetailScrollbar(c, p, gad, r)
		case gui.KindSurface:
			g.drawRetailSurface(c, p, gad, r)
		case gui.KindLabel, gui.KindPicture:
			g.drawRetailArt(c, p, gad, r)
			g.drawRetailText(c, p, i, gad, r)
		default:
			g.drawRetailArt(c, p, gad, r)
			g.drawRetailText(c, p, i, gad, r)
		}
	}
}

// drawRetailModal draws the authored MSGBOX.GUI panel. Message text is bound
// only to an authored text control; no runtime fallback gadgets are emitted.
func (g *gameShell) drawRetailModal(c *client.Client) {
	if g == nil || g.assets == nil {
		return
	}
	m := g.frontend.Panels.Modal()
	if m == nil || m.Window == nil {
		return
	}
	g.drawPanelTile(c, m.Window.Rect)
	for i, gad := range m.Window.Gadgets {
		if i == 0 || !m.ActiveOf(gad.Name) {
			continue
		}
		r := m.Window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			pressed := false
			if c != nil && c.Input() != nil && c.Input().Mouse != nil {
				pressed = pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), r) &&
					c.Input().Mouse.Held(input.MouseButtonLeft)
			}
			if frame := g.retailButtonFrame(gad, m.StatusOf(gad.Name), pressed); frame != nil {
				blitRetailFrame(c, frame, int(r.X), int(r.Y))
			}
			g.drawRetailTextState(c, m, i, gad, r)
		case gui.KindLabel:
			g.drawRetailTextState(c, m, i, gad, r)
		}
	}
}

// retailMessageWrapWidth is the wrap width the shell's own diagnostics open the
// message box with. `MSGBOX.GUI` authors no text control at all, so the width is
// an opener argument, never an authored one; retail's own call sites use 150,
// 200, 250, 320, 400, 480 and 500, and 500 is the width all 25 of the widest
// group use — the checksum and sound warnings, the longest strings retail puts
// in the box [07 R-FE-01 §9].
const retailMessageWrapWidth = 500

// showRetailMessage opens the authored message window over a message the box
// builds its own controls for.
//
// `MSGBOX.GUI` holds exactly two gadgets: the `HEADER` panel and the `OK`
// button. There is no authored `TEXT` or `LABEL` gadget, so binding the message
// to one could never succeed and every diagnostic was swallowed behind
// "retail message box cannot be constructed". Retail does not bind: the opener
// localises the text, wraps it to the caller's pixel width, splits it at `\n`
// and **appends one centred `TEXT` label per line** to the window, then resizes
// and re-centres the window and moves `OK` into its bottom-right corner
// [07 R-FE-01 §9]. The authored file is the frame; the controls are runtime.
func (g *gameShell) showRetailMessage(message string) error {
	const logical = "guis/msgbox.gui"
	if g == nil || g.assets == nil || g.assets.message == nil || g.assets.message.window == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window")
	}
	if !g.hasRetailTextFont() {
		return g.retailMessageError(logical, "a loaded frontend text font to measure the message with")
	}
	built := g.buildRetailMessageWindow(g.assets.message.window, message)
	if built == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window with a panel record")
	}
	m := ui.NewPanel(built)
	if m == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window")
	}
	m.SetMessage(message)
	g.frontend.Panels.PushModal(m)
	return nil
}

// buildRetailMessageWindow reproduces the `MSGBOX` opener's layout
// [07 R-FE-01 §9]. The authored window is left untouched; a copy carrying the
// appended labels and the recomputed geometry is returned.
//
// Every constant below is the opener's:
//   - the first label sits at local y = 20 and each next one advances by
//     `fontHeight + 5`, where `fontHeight` is the active FNT's height byte —
//     the opener takes the line step from the FNT even where the widths come
//     from the GUI's GAF font;
//   - each label is created at local x = 0 with height 15 and the centring
//     attribute, and is then widened to the finished panel width;
//   - the panel width is `max(label text widths) + 20` (the `autoWidth`
//     argument every retail call site but one passes), the wrap width otherwise;
//   - the panel height is `lines × 25 + 40` plus the height field of the
//     window's **second gadget record** — for `MSGBOX.GUI` that is the `OK`
//     button's 42, not a title bar. §9's "titleHeight" names that field; the
//     opener reads it at a fixed offset that lands on gadget 1;
//   - the window is centred at `((W − w) / 2, (H − h) / 2)`;
//   - `OK` moves to `(w − okW − 15, h − okH − 15)` and becomes the Enter and
//     Escape default. `MSGBOX.GUI` already authors both defaults as `OK`, so
//     that write is a no-op on the stock file and is not repeated here.
func (g *gameShell) buildRetailMessageWindow(authored *gui.Window, message string) *gui.Window {
	if authored == nil || len(authored.Gadgets) == 0 {
		return nil
	}
	lines := retailMessageWrap(message, g.retailTextWidth, retailMessageWrapWidth)
	step := g.retailMessageLineStep()

	built := &gui.Window{
		Name:   authored.Name,
		Rect:   authored.Rect,
		Focus:  authored.Focus,
		Header: authored.Header,
	}
	built.Gadgets = append(built.Gadgets, authored.Gadgets...)
	for i, line := range lines {
		built.Gadgets = append(built.Gadgets, gui.Gadget{
			Kind:       gui.KindLabel,
			Name:       "TEXT",
			SourceName: fmt.Sprintf("GADGET%d", len(authored.Gadgets)+i),
			Rect:       gui.Rect{X: 0, Y: int32(20 + i*step), W: 0, H: 15},
			Attribs:    2,
			ColorF:     15,
			Active:     1,
			Text:       line,
		})
	}

	// Every retail call site but one passes `autoWidth` — the panel is sized to
	// its widest line plus 20 and the wrap width only bounds the lines
	// [07 R-FE-01 §9]. The shell's diagnostics do the same.
	widest := 0
	for _, line := range lines {
		if w := g.retailTextWidth(line); w > widest {
			widest = w
		}
	}
	width := int32(widest + 20)
	height := int32(len(lines)*25 + 40)
	if len(authored.Gadgets) > 1 {
		height += authored.Gadgets[1].Rect.H
	}
	built.Rect.W, built.Rect.H = width, height
	built.Rect.X = (retailScreenW - width) / 2
	built.Rect.Y = (retailScreenH - height) / 2
	built.OriginX, built.OriginY = built.Rect.X, built.Rect.Y
	built.Gadgets[0].Rect = built.Rect

	for i := range built.Gadgets {
		gad := &built.Gadgets[i]
		switch {
		case gad.Kind == gui.KindLabel && gad.Name == "TEXT":
			gad.Rect.W = width
		case i != 0 && strings.EqualFold(gad.Name, "OK"):
			gad.Rect.X = width - gad.Rect.W - 15
			gad.Rect.Y = height - gad.Rect.H - 15
		}
	}
	return built
}

// retailMessageLineStep is the message box's line advance: the active FNT's
// height plus five [07 R-FE-01 §9].
func (g *gameShell) retailMessageLineStep() int {
	height := 0
	if g != nil && g.font != nil {
		height = int(g.font.Height)
	}
	if height <= 0 {
		height = g.retailTextHeight()
	}
	return height + 5
}

// retailMessageWrap is the message box's word wrapper [07 R-FE-01 §9]. It is
// not the label painter's wrapper: this one measures the line only when the
// **next** character is a space, a newline or a hyphen, breaks when the line has
// reached the wrap width, and rewinds to that separator, replacing it with a
// CR/LF pair. Splitting the result on newlines therefore leaves a trailing CR on
// every broken line, which is retail's own buffer content; control bytes have no
// glyph and measure zero, so the carriage return neither draws nor moves the pen
// [07 §4].
func retailMessageWrap(text string, measure func(string) int, limit int) []string {
	if measure == nil || limit <= 0 {
		return splitRetailMessageLines(text)
	}
	out := make([]byte, 0, len(text)*2+2)
	lineStart := 0
	for i := 0; i < len(text); i++ {
		out = append(out, text[i])
		if i+1 < len(text) {
			switch text[i+1] {
			case ' ', '\n', '-':
			default:
				continue
			}
		} else {
			continue
		}
		if measure(string(out[lineStart:])) < limit {
			if text[i+1] == '\n' {
				lineStart = len(out) + 1
			}
			continue
		}
		// Rewind over the word just completed, in the output and the source
		// together, until the separator that precedes it. Retail walks off the
		// front of the buffer when a single word is wider than the wrap width;
		// the line is kept whole here instead, which is the only deviation.
		back := i
		for back > 0 && text[back] != ' ' && text[back] != '-' {
			back--
		}
		if back == 0 || len(out)-(i-back) <= lineStart {
			continue
		}
		out = out[:len(out)-(i-back)]
		out[len(out)-1] = '\r'
		out = append(out, '\n')
		lineStart = len(out)
		i = back
	}
	return splitRetailMessageLines(string(out))
}

func splitRetailMessageLines(text string) []string {
	// The opener walks the wrapped buffer with strtok on "\n", so empty runs
	// between separators produce no label at all.
	lines := make([]string, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

func (g *gameShell) retailMessageError(logical, expected string) error {
	var providers []string
	if g != nil && g.cs != nil && g.cs.fs != nil {
		providers = providerNames(g.cs.fs)
	}
	return &missingProductError{what: "retail message box cannot be constructed", logical: logical, providers: providers, expected: expected}
}

func (g *gameShell) drawPanelTile(c *client.Client, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("BackTile")
	if !ok || len(e.Frames) == 0 || e.Frames[0].Frame == nil {
		return
	}
	f := e.Frames[0].Frame
	for y := int(r.Y); y < int(r.Y+r.H); y += int(f.Height) {
		for x := int(r.X); x < int(r.X+r.W); x += int(f.Width) {
			blitRetailFrame(c, f, x, y)
		}
	}
}

// blitRetailFrame is the GUI gadget path. GAF offsets are animation-anchor
// metadata; retail's interface renderer places GUI art by the authored
// gadget rectangle and does not apply those offsets (the common GUI frames
// deliberately carry offsets from their animation canvases) [fmt gaf].
func blitRetailFrame(c *client.Client, f *formats.GAFFrame, x, y int) {
	if f == nil {
		return
	}
	c.UIBlit(f, x, y)
}

func (g *gameShell) drawRetailArt(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	if f := g.gadgetArt(gad, p.StatusOf(gad.Name)); f != nil {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
}

func (g *gameShell) drawRetailButton(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	pressed := retailButtonPressed(c, r)
	status := p.StatusOf(gad.Name)
	art := g.gadgetButtonArt(gad, status, pressed)
	if art != nil {
		blitRetailFrame(c, art, int(r.X), int(r.Y))
	} else if frame := g.retailButtonFrame(gad, status, pressed); frame != nil {
		blitRetailFrame(c, frame, int(r.X), int(r.Y))
	}
	g.drawRetailText(c, p, index, gad, r)
}

// gadgetButtonArt applies the pressed state that the retail button pump
// applies to an owned GAF entry. Retail does not tint or replace an ordinary
// button merely because the pointer is over it; the armed frame is visible
// only while the left button is held inside the gadget [07 §3]. The
// executable's renderer uses frame 1 for an ordinary two-state entry and
// the penultimate frame for an entry with Stages set [the retail trace].
func (g *gameShell) gadgetButtonArt(gad gui.Gadget, status int, pressed bool) *formats.GAFFrame {
	art := g.gadgetArt(gad, status)
	if art == nil || !pressed {
		return art
	}
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	p := g.panelAssets()
	if p == nil {
		return art
	}
	var e *formats.GAFEntry
	if p.art != nil {
		e, _ = p.art.Find(name)
	}
	if e == nil && g.assets != nil && g.assets.logos != nil {
		e, _ = g.assets.logos.Find(name)
	}
	if e == nil || len(e.Frames) < 2 {
		return art
	}
	idx := 1
	if gad.Stages != 0 {
		idx = len(e.Frames) - 2
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(e.Frames) {
		idx = len(e.Frames) - 1
	}
	return e.Frames[idx].Frame
}

func (g *gameShell) retailButtonFrame(gad gui.Gadget, status int, pressed bool) *formats.GAFFrame {
	if g.assets == nil || g.assets.common == nil {
		return nil
	}
	// Retail staged controls keep Status at zero in the ordinary button record;
	// the selected label is the final authored frame before the pressed state.
	// Press uses the common
	// final active frame (entry-count minus two), as the retail implementation does.
	if gad.Stages > 0 {
		name := fmt.Sprintf("stagebuttn%d", gad.Stages)
		if e, ok := g.assets.common.Find(name); ok && len(e.Frames) != 0 {
			stage := clampMenuStage(status, int(gad.Stages))
			idx := stage
			if pressed && len(e.Frames) >= 2 {
				idx = len(e.Frames) - 2
			}
			if idx >= len(e.Frames) {
				idx = len(e.Frames) - 1
			}
			return e.Frames[idx].Frame
		}
	}
	e, ok := g.assets.common.Find("BUTTONS0")
	if !ok {
		return nil
	}
	bestFrame, bestScore := -1, int(^uint(0)>>1)
	for i, ref := range e.Frames {
		f := ref.Frame
		if f == nil {
			continue
		}
		score := absInt(int(f.Width)-int(gad.Rect.W)) + absInt(int(f.Height)-int(gad.Rect.H))
		if score < bestScore {
			bestFrame, bestScore = i, score
		}
	}
	if bestFrame < 0 {
		return nil
	}
	// BUTTONS0 is four frames per size: normal, active, disabled, spare.
	base := bestFrame / 4 * 4
	idx := base + clampMenuStage(status, 4)
	if pressed {
		idx = base + 1
	}
	if idx >= len(e.Frames) {
		idx = len(e.Frames) - 1
	}
	return e.Frames[idx].Frame
}

func (g *gameShell) drawRetailText(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	g.drawRetailTextState(c, p, index, gad, r)
}

// guiColor resolves a GUI file's semantic color field through the retail
// GUIPAL→PALETTE nearest-RGB table. This is a color-field operation, not an
// image-pixel conversion: GAF/PCX/TNT bytes are copied directly to the
// indexed surface and use PALETTE.PAL at presentation.
func (g *gameShell) guiColor(source byte) byte {
	if g != nil && g.assets != nil && g.assets.pal != nil {
		return g.assets.pal.GUIColor(source)
	}
	return source
}

func (g *gameShell) drawRetailTextState(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	if gad.Kind == gui.KindPicture {
		// The picture-box painter blits frame 0 and darkens the rectangle; it
		// installs no colour and draws no text at all. A caption on a picture
		// box has no retail counterpart [03 R-FONT-01 §6].
		return
	}
	text := p.TextFor(gad)
	if len(gad.Labels) != 0 {
		idx := clampMenuStage(p.StatusOf(gad.Name), len(gad.Labels))
		text = gad.Labels[idx]
	}
	if text == "" || !g.hasRetailTextFont() {
		return
	}
	width := g.retailTextWidth(text)
	pressed := retailButtonPressed(c, r)
	x := int(r.X)
	// the retail implementation tests the left-aligned attribute before the right/center
	// attributes. These are the actual authored GUI conventions: bit 0 uses a
	// three-pixel inset, bit 2 centers, and bit 4 right-aligns with a
	// three-pixel inset. A held button adds the one-pixel armed offset.
	switch {
	case gad.Attribs&1 != 0:
		x += 3
	case gad.Attribs&4 != 0:
		x = int(r.X+r.W) - width - 3
		if x < int(r.X) {
			x = int(r.X)
		}
	case gad.Attribs&2 != 0:
		x += (int(r.W)-1-width)/2 + 1
	default:
		x += 3
	}
	if pressed {
		x += boolInt(gad.Attribs&1 != 0 || gad.Attribs&2 != 0)
	}
	y := retailTextPenY(gad, r, g.retailTextHeight())
	color, shade := g.retailTextPen(p, index, gad)
	maxWidth := int(r.W)
	if maxWidth <= 0 {
		width, _ := c.Size()
		maxWidth = width - x
	}
	// the retail implementation measures the gadget against two lines of the active font
	// before it picks a renderer: it compares the rectangle's inclusive height
	// (y1-y0) with twice the capital-I frame height plus two, and sends the
	// taller case to the wrapping renderer the retail implementation and everything else to
	// the single-line the retail implementation. SELMAP.GUI authors DESCRIPTION 235x31 for
	// the wrapped case and SIZE 235x18 for the single-line one [07 §4].
	lineStep := g.retailTextHeight()
	if int(r.H)-1 > 2*lineStep {
		lines := retailWrapLines(text, g.retailTextWidth, maxWidth)
		top := int(r.Y) + (int(r.H)-1-len(lines)*lineStep)/2
		if top < int(r.Y) {
			top = int(r.Y)
		}
		for i, line := range lines {
			g.drawRetailStringLit(c, line, x, top+i*lineStep, maxWidth, color, shade)
		}
		return
	}
	g.drawRetailStringLit(c, text, x, y, maxWidth, color, shade)
}

// retailTextPen resolves the two things a gadget's text pen needs: the
// light-table row the keyed GAF blitter remaps every glyph byte through, and
// the foreground byte the FNT fallback installs ahead of its glyph mask.
//
// Neither is the authored `colorf` field read directly. `colorf` is a
// light-table row that lives on the Panel instance as the gadget's live
// flash word (`Panel.FlashRow`): the builder zeroes it for buttons and
// labels at open, and the service pass decays whatever a screen sets
// [07 R-WGT-01 §1][07 R-WGT-01 §12]. The FNT painter each kind installs
// ahead of its text call is closed by "The FNT foreground each painter
// installs" [03 R-FONT-01 §6]:
//
//   - Button (kind 1): foreground = GUIPAL map entry `row` (the live flash
//     word) when `stages == 0`, map entry 0 when `stages != 0` — the
//     authored `colorf` is never read. The GAF pen's `mode` is always 0 for
//     a button, whatever the flash row, so `shade` stays 0 here.
//   - Label (kind 5): foreground = the live flash word RAW, as a physical
//     palette index — no GUIPAL lookup. The GAF pen's `mode` is the same
//     word, so `shade` still carries it for the GAF path.
//   - Picture box (kind 12): draws no caption at all; drawRetailTextState
//     returns before reaching here, so this function is never called for one.
//
// Every other kind keeps `colorf` as a colour read through the GUIPAL map: a
// listbox draws its rows in "the window colour-table entry the gadget's
// `colorf` selects" [07 R-WGT-01 §4], and focusing a text input "sets the
// drawing colour from the gadget's `colorf`" [07 R-WGT-01 §8].
func (g *gameShell) retailTextPen(p *ui.Panel, index int, gad gui.Gadget) (color byte, shade int) {
	row := p.FlashRow(index)
	switch gad.Kind {
	case gui.KindButton:
		if gad.Stages != 0 {
			return g.guiColor(0), 0
		}
		return g.guiColor(byte(row & 0xff)), 0
	case gui.KindLabel:
		return byte(row & 0xff), int(row)
	default:
		return g.guiColor(byte(gad.ColorF & 0xff)), shade
	}
}

// retailWrapLines breaks a label at spaces so it fits maxWidth, keeping each
// line verbatim. the retail implementation wraps the authored string in place rather than
// re-joining words, so the double space the OTA missiondescription carries
// after the map size survives into the drawn line.
func retailWrapLines(text string, measure func(string) int, maxWidth int) []string {
	if text == "" || measure == nil || maxWidth <= 0 {
		return []string{text}
	}
	var lines []string
	for text != "" {
		if measure(text) <= maxWidth {
			lines = append(lines, text)
			break
		}
		cut := -1
		for i := 0; i < len(text); i++ {
			if text[i] != ' ' {
				continue
			}
			if measure(text[:i]) > maxWidth {
				break
			}
			cut = i
		}
		if cut <= 0 {
			// A single run wider than the box: emit what fits and continue,
			// which is what the renderer's per-glyph width test amounts to.
			cut = len(text)
			for cut > 1 && measure(text[:cut]) > maxWidth {
				cut--
			}
			lines = append(lines, text[:cut])
			text = text[cut:]
			continue
		}
		lines = append(lines, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return lines
}

// retailTextPenY mirrors the retail implementation. The inclusive gadget bottom makes the
// centering span H-1, and staged controls add one to the pen coordinate. The
// pressed state changes the selected art frame but does not move the text pen.
func retailTextPenY(gad gui.Gadget, r gui.Rect, textHeight int) int {
	y := int(r.Y)
	if r.H > 0 {
		y += (int(r.H)-1-textHeight)/2 + boolInt(gad.Stages != 0)
	}
	return y
}

func retailButtonPressed(c *client.Client, r gui.Rect) bool {
	if c == nil || c.Input() == nil || c.Input().Mouse == nil {
		return false
	}
	m := c.Input().Mouse
	return pointInRect(int32(m.X), int32(m.Y), r) && m.Held(input.MouseButtonLeft)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (g *gameShell) drawRetailList(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	g.drawListBox(c, r)
	items, selected, top, ok := p.ListValues(gad.Name)
	if !ok || len(items) == 0 || !g.hasRetailTextFont() {
		return
	}
	itemHeight := retailListItemHeight(gad, g.retailTextHeight())
	visible := retailVisibleListRows(r, itemHeight)
	maxTop := len(items) - visible
	if maxTop < 0 {
		maxTop = 0
	}
	p.SetListTop(gad.Name, top, maxTop)
	_, selected, top, _ = p.ListValues(gad.Name)
	for row := 0; row < visible; row++ {
		idx := top + row
		if idx >= len(items) {
			break
		}
		// the retail implementation reserves the first two pixels of a listbox before
		// calculating rows. The same origin is used by its text renderer.
		y := int(r.Y) + 2 + row*itemHeight
		color := g.guiColor(byte(gad.ColorF & 0xff))
		g.drawRetailString(c, items[idx], int(r.X)+4, y, int(r.W)-4, color)
		// The highlight runs after the row's text, as the retail implementation does: the
		// operator remaps whatever is already in the rectangle, so the glyphs
		// are lifted along with the listbox interior.
		if idx == selected {
			g.drawListSelection(c, r, y, itemHeight)
		}
	}
}

// retailVisibleListRows is the row count used by the retail implementation: two pixels of
// the authored list rectangle are reserved before dividing by itemheight.
func retailVisibleListRows(r gui.Rect, itemHeight int) int {
	if itemHeight <= 0 {
		itemHeight = 1
	}
	visible := (int(r.H) - 2) / itemHeight
	if visible < 1 {
		visible = 1
	}
	return visible
}

func (g *gameShell) drawListBox(c *client.Client, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("LISTBOX")
	if !ok || len(e.Frames) < 9 {
		return
	}
	frames := make([]*formats.GAFFrame, 9)
	for i := range frames {
		frames[i] = e.Frames[i].Frame
	}
	if frames[0] == nil || frames[4] == nil {
		return
	}
	left, top := int(r.X), int(r.Y)
	w, h := int(r.W), int(r.H)
	cornerW, cornerH := int(frames[0].Width), int(frames[0].Height)
	if cornerW <= 0 || cornerH <= 0 {
		return
	}
	for y := top; y < top+h; y += cornerH {
		for x := left; x < left+w; x += cornerW {
			col := 1
			row := 1
			if x == left {
				col = 0
			} else if x+cornerW >= left+w {
				col = 2
			}
			if y == top {
				row = 0
			} else if y+cornerH >= top+h {
				row = 2
			}
			idx := row*3 + col
			if idx >= len(frames) {
				idx = 4
			}
			if frames[idx] != nil {
				blitRetailFrame(c, frames[idx], x, y)
			}
		}
	}
}

// drawListSelection is the retail highlight. the retail implementation does not stamp art
// over the selected row: it hands the row rectangle to the retail implementation at level
// +30, and a non-negative level there indexes the 32-row PALETTE.LHT
// brightening table, so the row's own pixels are remapped one row at a time.
// That is what makes the selected entry read as a lit bar over the listbox
// interior rather than a painted block [the retail trace][03 §4.3.1].
func (g *gameShell) drawListSelection(c *client.Client, r gui.Rect, y, h int) {
	if g == nil || g.assets == nil || g.assets.pal == nil {
		return
	}
	c.UILightRect(g.assets.pal, int(r.X), y, int(r.W), h, retailListSelectionLevel)
}

// retailListSelectionLevel is the literal the retail implementation pushes for a selected
// list row, focused or not.
const retailListSelectionLevel = 30

func (g *gameShell) drawRetailScrollbar(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect) {
	if g.assets == nil || g.assets.common == nil {
		return
	}
	e, ok := g.assets.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return
	}
	vertical := r.H >= r.W
	base := 0
	if !vertical {
		base = 10
	}
	// SLIDERS is partitioned exactly as the runtime builder expects:
	// base+0..2 are track end/middle pieces, base+3..5 are the three thumb
	// pieces, and base+6..9 are the normal/pressed arrow pairs. The vertical
	// family is frames 0..9; horizontal is frames 10..19 [07 §4].
	track0 := e.Frames[base+0].Frame
	track1 := e.Frames[base+1].Frame
	track2 := e.Frames[base+2].Frame
	thumb0 := e.Frames[base+3].Frame
	thumb1 := e.Frames[base+4].Frame
	thumb2 := e.Frames[base+5].Frame
	arrow0Normal := e.Frames[base+6].Frame
	arrow1Normal := e.Frames[base+8].Frame
	if track0 == nil || track1 == nil || track2 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil || arrow0Normal == nil || arrow1Normal == nil {
		return
	}
	arrow0 := arrow0Normal
	arrow1 := arrow1Normal
	leftMouseHeld := c != nil && c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft)

	left, top, right, bottom := int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)
	if vertical {
		arrowH := int(arrow0.Height)
		if int(arrow1.Height) > arrowH {
			arrowH = int(arrow1.Height)
		}
		if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: r.W, H: int32(arrowH)}) {
			if e.Frames[base+7].Frame != nil {
				arrow0 = e.Frames[base+7].Frame
			}
		}
		if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(bottom - arrowH), W: r.W, H: int32(arrowH)}) {
			if e.Frames[base+9].Frame != nil {
				arrow1 = e.Frames[base+9].Frame
			}
		}
		trackTop := top + arrowH
		trackBottom := bottom - arrowH
		trackW := int(track0.Width)
		trackX := left + (int(r.W)-trackW)/2
		blitRetailFrame(c, arrow0, left+(int(r.W)-int(arrow0.Width))/2, top)
		blitRetailFrame(c, arrow1, left+(int(r.W)-int(arrow1.Width))/2, bottom-int(arrow1.Height))
		drawRetailScrollbarTrack(c, track0, track1, track2, trackTop, trackX, trackBottom, false)

		l := listForAssocPanel(p, gad.Assoc)
		itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
		visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
		maxTop := 0
		if l != nil {
			maxTop = l.Len() - visible
			if maxTop < 0 {
				maxTop = 0
			}
		}
		total := 0
		if l != nil {
			total = l.Len()
		}
		thumbLen := retailScrollbarKnobSize(visible, total, int(r.H))
		travel := trackBottom - trackTop - thumbLen
		if travel < 0 {
			travel = 0
		}
		pos := 0
		if l != nil && maxTop > 0 {
			pos = l.Top() * travel / maxTop
		}
		thumbX := trackX + (trackW-int(thumb0.Width))/2
		drawRetailScrollbarThumb(c, thumb0, thumb1, thumb2, thumbX, trackTop+pos, thumbLen, false)
		return
	}

	arrowW := int(arrow0.Width)
	if int(arrow1.Width) > arrowW {
		arrowW = int(arrow1.Width)
	}
	if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(left), Y: int32(top), W: int32(arrowW), H: r.H}) {
		if e.Frames[base+7].Frame != nil {
			arrow0 = e.Frames[base+7].Frame
		}
	}
	if leftMouseHeld && pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), gui.Rect{X: int32(right - arrowW), Y: int32(top), W: int32(arrowW), H: r.H}) {
		if e.Frames[base+9].Frame != nil {
			arrow1 = e.Frames[base+9].Frame
		}
	}
	trackLeft := left + arrowW
	trackRight := right - arrowW
	trackH := int(track0.Height)
	trackY := top + (int(r.H)-trackH)/2
	blitRetailFrame(c, arrow0, left, top+(int(r.H)-int(arrow0.Height))/2)
	blitRetailFrame(c, arrow1, right-int(arrow1.Width), top+(int(r.H)-int(arrow1.Height))/2)
	drawRetailScrollbarTrack(c, track0, track1, track2, trackLeft, trackY, trackRight, true)
	l := listForAssocPanel(p, gad.Assoc)
	itemHeight := retailListAssocItemHeightPanel(g, p, gad.Assoc)
	visible := retailVisibleListRows(listRectForAssocPanel(p, gad.Assoc), itemHeight)
	maxTop := 0
	if l != nil {
		maxTop = l.Len() - visible
		if maxTop < 0 {
			maxTop = 0
		}
	}
	total := 0
	if l != nil {
		total = l.Len()
	}
	thumbLen := retailScrollbarKnobSize(visible, total, int(r.W))
	travel := trackRight - trackLeft - thumbLen
	if travel < 0 {
		travel = 0
	}
	pos := 0
	if l != nil && maxTop > 0 {
		pos = l.Top() * travel / maxTop
	}
	thumbY := trackY + (trackH-int(thumb0.Height))/2
	drawRetailScrollbarThumb(c, thumb0, thumb1, thumb2, trackLeft+pos, thumbY, thumbLen, true)
}

func drawRetailScrollbarTrack(c *client.Client, first, middle, last *formats.GAFFrame, start, cross0, cross1 int, horizontal bool) {
	if first == nil || middle == nil || last == nil {
		return
	}
	if horizontal {
		blitRetailFrame(c, first, start, cross0)
		end := cross1 - int(last.Width)
		for x := start + int(first.Width); x < end; x += int(middle.Width) {
			blitRetailFrame(c, middle, x, cross0)
		}
		blitRetailFrame(c, last, end, cross0)
		return
	}
	blitRetailFrame(c, first, cross0, start)
	end := cross1 - int(last.Height)
	for y := start + int(first.Height); y < end; y += int(middle.Height) {
		blitRetailFrame(c, middle, cross0, y)
	}
	blitRetailFrame(c, last, cross0, end)
}

// retailScrollbarKnobSize is the authored knob length used by the scrollbar.
// It divides the associated list's visible row count by its item count, scales
// that by the scrollbar's own length less three pixels, rounds, and clamps the
// result up to ten. SLIDERS carries the knob as a one-pixel cap, a repeatable
// three-pixel middle and a one-pixel cap, so the length is a computed run and
// never the sum of those frames [the retail trace].
func retailScrollbarKnobSize(visible, total, barLength int) int {
	const minimum = 10
	if total <= 0 || visible <= 0 || barLength <= 3 {
		return minimum
	}
	span := barLength - 3
	size := int(math.Round(float64(visible) / float64(total) * float64(span)))
	if size < minimum {
		size = minimum
	}
	if size > span {
		size = span
	}
	return size
}

func drawRetailScrollbarThumb(c *client.Client, first, middle, last *formats.GAFFrame, x, y, length int, horizontal bool) {
	if first == nil || middle == nil || last == nil {
		return
	}
	if horizontal {
		capLen := int(first.Width) + int(last.Width)
		if length < capLen {
			length = capLen
		}
		blitRetailFrame(c, first, x, y)
		end := x + length - int(last.Width)
		for px := x + int(first.Width); px < end; px += int(middle.Width) {
			blitRetailFrame(c, middle, px, y)
		}
		blitRetailFrame(c, last, end, y)
		return
	}
	capLen := int(first.Height) + int(last.Height)
	if length < capLen {
		length = capLen
	}
	blitRetailFrame(c, first, x, y)
	end := y + length - int(last.Height)
	for py := y + int(first.Height); py < end; py += int(middle.Height) {
		blitRetailFrame(c, middle, x, py)
	}
	blitRetailFrame(c, last, x, end)
}

func (g *gameShell) listForAssoc(assoc int32) *ui.List {
	return listForAssocPanel(g.activePanel(), assoc)
}

func listForAssocPanel(p *ui.Panel, assoc int32) *ui.List {
	if p == nil || p.Window == nil {
		return nil
	}
	for _, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return p.ListFor(gad.Name)
		}
	}
	return nil
}

func (g *gameShell) listNameForAssoc(assoc int32) string {
	return listNameForAssocPanel(g.activePanel(), assoc)
}

func listNameForAssocPanel(p *ui.Panel, assoc int32) string {
	if p == nil || p.Window == nil {
		return ""
	}
	for _, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return gad.Name
		}
	}
	return ""
}

func (g *gameShell) listRectForAssoc(assoc int32) gui.Rect {
	return listRectForAssocPanel(g.activePanel(), assoc)
}

func listRectForAssocPanel(p *ui.Panel, assoc int32) gui.Rect {
	if p == nil || p.Window == nil {
		return gui.Rect{}
	}
	for i, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return p.Window.PlacedRect(i)
		}
	}
	return gui.Rect{}
}

func (g *gameShell) retailScrollbarGeometry(gad gui.Gadget, r gui.Rect) (retailScrollbarGeometry, bool) {
	if g == nil || g.assets == nil || g.assets.common == nil {
		return retailScrollbarGeometry{}, false
	}
	e, ok := g.assets.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return retailScrollbarGeometry{}, false
	}
	vertical := r.H >= r.W
	base := 0
	if !vertical {
		base = 10
	}
	arrow0 := e.Frames[base+6].Frame
	arrow1 := e.Frames[base+8].Frame
	thumb0 := e.Frames[base+3].Frame
	thumb1 := e.Frames[base+4].Frame
	thumb2 := e.Frames[base+5].Frame
	if arrow0 == nil || arrow1 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil {
		return retailScrollbarGeometry{}, false
	}
	listRect := g.listRectForAssoc(gad.Assoc)
	itemHeight := g.retailListAssocItemHeight(gad.Assoc)
	visible := retailVisibleListRows(listRect, itemHeight)
	maxTop := 0
	if l := g.listForAssoc(gad.Assoc); l != nil {
		maxTop = l.Len() - visible
		if maxTop < 0 {
			maxTop = 0
		}
	}
	geometry := retailScrollbarGeometry{
		vertical: vertical,
		maxTop:   maxTop,
	}
	if vertical {
		arrowExtent := int(arrow0.Height)
		if int(arrow1.Height) > arrowExtent {
			arrowExtent = int(arrow1.Height)
		}
		geometry.arrowStart = int(r.Y)
		geometry.arrowEnd = int(r.Y+r.H) - arrowExtent
		geometry.axisStart = int(r.Y) + arrowExtent
		geometry.axisEnd = int(r.Y+r.H) - arrowExtent
		geometry.thumbLen = int(thumb0.Height) + int(thumb1.Height) + int(thumb2.Height)
	} else {
		arrowExtent := int(arrow0.Width)
		if int(arrow1.Width) > arrowExtent {
			arrowExtent = int(arrow1.Width)
		}
		geometry.arrowStart = int(r.X)
		geometry.arrowEnd = int(r.X+r.W) - arrowExtent
		geometry.axisStart = int(r.X) + arrowExtent
		geometry.axisEnd = int(r.X+r.W) - arrowExtent
		geometry.thumbLen = int(thumb0.Width) + int(thumb1.Width) + int(thumb2.Width)
	}
	geometry.travel = geometry.axisEnd - geometry.axisStart - geometry.thumbLen
	if geometry.travel < 0 {
		geometry.travel = 0
	}
	return geometry, true
}

// the retail implementation raises a list's authored item height to at least one pixel
// beyond the active font height. This is the default used by the authored
// campaign and map lists when itemheight is zero.
func retailListItemHeight(gad gui.Gadget, fontHeight int) int {
	height := int(gad.ItemHeight)
	minimum := fontHeight + 1
	if height < minimum {
		height = minimum
	}
	return height
}

func (g *gameShell) retailListAssocItemHeight(assoc int32) int {
	return retailListAssocItemHeightPanel(g, g.activePanel(), assoc)
}

func retailListAssocItemHeightPanel(g *gameShell, p *ui.Panel, assoc int32) int {
	if p == nil || p.Window == nil {
		return g.retailTextHeight() + 1
	}
	for _, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return retailListItemHeight(gad, g.retailTextHeight())
		}
	}
	return g.retailTextHeight() + 1
}

func (g *gameShell) drawRetailSurface(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect) {
	if strings.EqualFold(gad.Name, "MAPPIC") && len(g.maps) != 0 && g.mapIdx >= 0 && g.mapIdx < len(g.maps) {
		if d := g.mapDataFor(g.maps[g.mapIdx]); d != nil && d.tnt != nil {
			// the retail implementation writes the selected RADARPIC into the authored
			// MAPPIC canvas using the map's aspect, leaving the surrounding
			// canvas intact. The TNT minimap is the same indexed source for
			// this frontend path; preserve that retail letterbox instead of
			// stretching rectangular maps into the 125×125 square.
			// The source passed to the retail implementation is RADARPIC. Its aspect is
			// the minimap raster, not the terrain cell dimensions in TNT's
			// header.
			previewW, previewH := int(d.tnt.MinimapWidth), int(d.tnt.MinimapHeight)
			if previewW <= 0 || previewH <= 0 {
				return
			}
			drawW, drawH := int(r.W), int(r.H)
			if previewW < previewH {
				drawW = drawH * previewW / previewH
			} else {
				drawH = drawW * previewH / previewW
			}
			if drawW < 1 {
				drawW = 1
			}
			if drawH < 1 {
				drawH = 1
			}
			x := int(r.X) + (int(r.W)-drawW)/2
			y := int(r.Y) + (int(r.H)-drawH)/2
			c.UIBlitIndexed(d.tnt.Minimap, int(d.tnt.MinimapWidth), int(d.tnt.MinimapHeight), x, y, drawW, drawH)
		}
		return
	}
	// the retail implementation is the retail surface renderer, and it is not the ordinary
	// gadget-art blit. An RLE frame (Compressed != 0) is stamped at the gadget
	// origin; a raw frame is texture-mapped across the whole gadget rectangle.
	// SKIRMISH's Color%d surface is the visible case: textures/logos.gaf holds
	// authored 32x32 frames that retail resamples into the authored 20x20 record,
	// while anims/skirmish.gaf's RLE ally icons are stamped 1:1 [07 §4].
	if p == nil {
		return
	}
	if f := g.gadgetArt(gad, p.StatusOf(gad.Name)); f != nil {
		if f.Compressed == 0 {
			c.UIBlitFrameScaled(f, int(r.X), int(r.Y), int(r.W), int(r.H))
			return
		}
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
}

func pointInRect(x, y int32, r gui.Rect) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (g *gameShell) adjustRetailScrollbar(gad gui.Gadget, delta int) {
	if g == nil || delta == 0 {
		return
	}
	l := g.listForAssoc(gad.Assoc)
	if l == nil || l.Len() == 0 {
		return
	}
	r := g.listRectForAssoc(gad.Assoc)
	visible := retailVisibleListRows(r, g.retailListAssocItemHeight(gad.Assoc))
	maxTop := l.Len() - visible
	if maxTop < 0 {
		maxTop = 0
	}
	if p := g.activePanel(); p != nil {
		_ = p.SetListTop(g.listNameForAssoc(gad.Assoc), l.Top()+delta, maxTop)
	}
}

func (g *gameShell) clickRetailScrollbar(index int, gad gui.Gadget, r gui.Rect, x, y int32) {
	geometry, ok := g.retailScrollbarGeometry(gad, r)
	if !ok {
		return
	}
	coordinate := int(x)
	if geometry.vertical {
		coordinate = int(y)
	}
	// The runtime builder appends two ordinary button gadgets for the arrow
	// frames. Their callbacks move the associated list one row at a time.
	if coordinate < geometry.axisStart {
		// Keep the arrow armed until the left button is released. The ordinary
		// child button callback is not run on the down edge.
		return
	}
	if coordinate >= geometry.axisEnd {
		// Keep the arrow armed until the left button is released. The ordinary
		// child button callback is not run on the down edge.
		return
	}
	l := g.listForAssoc(gad.Assoc)
	if l == nil {
		return
	}
	thumbPos := geometry.axisStart
	if geometry.maxTop > 0 {
		thumbPos += l.Top() * geometry.travel / geometry.maxTop
	}
	if coordinate < thumbPos || coordinate >= thumbPos+geometry.thumbLen {
		// the retail implementation starts capture only when the click is in the
		// calculated knob rectangle; clicking the track beside it does not
		// invent page-step behavior.
		return
	}
	if p := g.activePanel(); p != nil {
		_ = p.BeginScrollDrag(index, geometry.vertical, int32(coordinate), geometry.maxTop, geometry.travel)
	}
}

func (g *gameShell) updateRetailScrollbarDrag(mouse *input.MouseState) {
	p := g.activePanel()
	if p == nil || mouse == nil || !p.ScrollDragging() {
		return
	}
	// The panel owns pointer capture and the integer thumb mapping. Geometry
	// remains in this file because it comes from authored frame dimensions.
	_ = p.UpdateScrollDrag(int32(mouse.X), int32(mouse.Y), mouse.Held(input.MouseButtonLeft))
}

func (g *gameShell) releaseRetailScrollbar(gad gui.Gadget, r gui.Rect, x, y int32) {
	geometry, ok := g.retailScrollbarGeometry(gad, r)
	if !ok {
		return
	}
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
	m := g.frontend.Panels.Modal()
	if g == nil || m == nil || m.Window == nil || cl == nil || cl.Input() == nil {
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
		if g.missionAny {
			name = "Missions"
		} else {
			name = "Campaign"
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
	// The cue runs before the transition, in the screen handler that consumes
	// the fired result [07 R-FE-01 §2][07 R-WGT-01 §3]. It is read off the
	// screen the click landed on, so it must be taken before Navigate moves the
	// mode.
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
