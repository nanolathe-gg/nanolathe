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
func (g *gameShell) applyRetailMissionLayout(window *gui.Window) {
	if window == nil {
		return
	}
	setRect := func(name string, y, h int32) {
		if i := window.GadgetIndex(name); i >= 0 {
			window.Gadgets[i].Rect.Y = y
			window.Gadgets[i].Rect.H = h
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

// installRetailWindowButtonArt writes the generic window builder's effective
// button record before ui.NewPanel copies its runtime state. The installed
// record is the authority for button art, geometry and staged state thereafter
// [07 R-WGT-01 §3].
func (g *gameShell) installRetailWindowButtonArt(window *gui.Window, own *formats.GAF) {
	if window == nil {
		return
	}
	if g == nil || !g.quickKeyPreclearDisabled {
		gui.PreclearButtonQuickKeys(window)
	}
	for i := range window.Gadgets {
		gad := &window.Gadgets[i]
		if gad.Kind == gui.KindButton {
			// Button flash is reset before either bypass gate. This is runtime
			// builder state, not the authored colour [07 R-WGT-01 §3].
			gad.ColorF = 0
		}
		// The per-gadget resource prepass precedes the kind switch. Its
		// resolved-nil result is retained. Only the button arm bypasses its
		// ordinary builder when that bit is set [07 R-WGT-01 §3][§12].
		if gad.GAFFile&1 != 0 {
			gad.ExternalArt = g.externalGadgetArt(*gad)
			gad.ExternalArtResolved = true
			if gad.Kind == gui.KindButton {
				gad.ButtonArt = gad.ExternalArt
				gad.ArtFrame = 0
				gad.ButtonArtResolved = true
				continue
			}
		}
		switch gad.Kind {
		case gui.KindLabel:
			gad.ColorF = 0
			gui.AssignLinkedLabelQuickKey(window, i)
		case gui.KindButton:
			if gad.Attribs&0x1800 != 0 {
				continue
			}
			gui.AssignButtonQuickKey(window, i)
			art := g.buildRetailButtonArt(*gad, own)
			built := art.gadget
			built.ArtFrame = int32(art.base)
			built.ButtonArt = art.entry
			built.ButtonArtResolved = true
			if art.entry != nil && art.base >= 0 && art.base < len(art.entry.Frames) {
				if frame := art.entry.Frames[art.base].Frame; frame != nil {
					built.Rect.W = int32(frame.Width)
					built.Rect.H = int32(frame.Height)
				}
			}
			window.Gadgets[i] = built
		}
	}
}

// externalGadgetArt is the odd-gaffile prepass. A failed resource or
// entry lookup remains a resolved nil result, so later drawing cannot replace
// this record with normal/common/fallback art [07 R-WGT-01 §3].
func (g *gameShell) externalGadgetArt(gad gui.Gadget) *formats.GAFEntry {
	name := gad.Name
	if cut := strings.IndexByte(name, 0); cut >= 0 {
		name = name[:cut]
	}
	if g == nil || g.cs == nil || g.cs.fs == nil || name == "" {
		return nil
	}
	bank, err := formats.LoadGAFFile(g.cs.fs, "anims/"+name+"_gadget.GAF")
	if err != nil {
		return nil
	}
	entry, _ := bank.Find(name)
	return entry
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
	p.SetStageAt(p.Index("Difficulty"), clampMenuStage(g.missionDifficultyValue, 3))
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
		p.SetStageAt(p.Index("LineOfSight"), 0)
		p.SetHelp("LineOfSight", "All mapped terrain is visible.")
	} else if g.setup.LOSType == 1 {
		p.SetStageAt(p.Index("LineOfSight"), 1)
		p.SetHelp("LineOfSight", "Terrain elevations affect a unit's view.")
	} else {
		p.SetStageAt(p.Index("LineOfSight"), 2)
		p.SetHelp("LineOfSight", "Terrain elevations do not affect a unit's view.")
	}
	p.SetStageAt(p.Index("Difficulty"), clampMenuStage(g.setup.Difficulty, 3))

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
		help = p.HelpAt(idx)
	}
	p.SetText("HELPTEXT", help)
}

// installSkirmishDynamicGadgets supplies authored lobby row values to the
// canonical UI runtime builder. The builder owns gadget construction and
// geometry; this composition root retains only skirmish configuration.
func (g *gameShell) installSkirmishDynamicGadgets(window *gui.Window) {
	if window == nil {
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
	ui.InstallSkirmishDynamicGadgets(window, slots)
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
	oldItems, oldSelected, _, exists := p.ListValues(name)
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
		p.SetList(name, items)
		// A new row source starts at its first row; an identical refresh below
		// leaves the user's manual scrollbar position untouched.
		visible := g.retailListVisibleRows(name)
		maxTop := len(items) - visible
		if maxTop < 0 {
			maxTop = 0
		}
		p.SetListTop(name, 0, maxTop)
	}
	if len(items) == 0 {
		p.SetList(name, nil)
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
		p.SetListSelection(name, selected, g.retailListVisibleRows(name))
		g.ensureRetailListVisible(name)
	}
}

func (g *gameShell) retailListVisibleRows(name string) int {
	p := g.activePanel()
	if i := p.Index(name); i >= 0 {
		gad := p.Window.Gadgets[i]
		if gad.Kind == gui.KindListBox {
			return retailVisibleListRows(p.Window.PlacedRect(i), retailListItemHeight(gad, g.retailTextHeight()))
		}
	}
	return 1
}

func (g *gameShell) ensureRetailListVisible(name string) {
	p := g.activePanel()
	g.ensureRetailListIndexVisible(p, p.Index(name))
}

func (g *gameShell) ensureRetailListIndexVisible(p *ui.Panel, index int) {
	l := p.ListAt(index)
	if l == nil || l.Len() == 0 {
		return
	}
	gad := p.Window.Gadgets[index]
	p.SetListSelectionAt(index, l.Selected(), retailVisibleListRows(p.Window.PlacedRect(index), retailListItemHeight(gad, g.retailTextHeight())))
}

func (g *gameShell) currentGadget(index int) (gui.Gadget, bool) {
	p := g.activePanel()
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) {
		return gui.Gadget{}, false
	}
	return p.Window.Gadgets[index], true
}

func (g *gameShell) gadgetArt(gad gui.Gadget, status int) *formats.GAFFrame {
	e := g.namedGadgetArtEntry(gad)
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

// namedGadgetArtEntry is the existing named-art search used by non-button
// painters: the current window page, then its mounted side art. Buttons add
// the common-interface and generic fallback hops in buttonArtResolution.
func (g *gameShell) namedGadgetArtEntry(gad gui.Gadget) *formats.GAFEntry {

	p := g.panelAssets()
	if p == nil {
		return nil
	}
	artName := gad.Art
	if artName == "" {
		artName = gad.Name
	}
	if p.art != nil {
		if found, ok := p.art.Find(artName); ok {
			return found
		}
		// The executable asks the skirmish runtime GAF for TEAMICONSx. In the
		// installed retail resource set that entry is named "ally icons" (the
		// same 38×20 stock strip); retain the executable name in the runtime
		// gadget and resolve the mounted resource spelling here.
		if strings.EqualFold(artName, "TEAMICONSx") {
			if found, ok := p.art.Find("ally icons"); ok {
				return found
			}
		}
	}
	if g.assets != nil && g.assets.logos != nil {
		if found, ok := g.assets.logos.Find(artName); ok {
			return found
		}
	}
	return nil
}

type retailButtonArtResolution struct {
	entry  *formats.GAFEntry
	base   int
	gadget gui.Gadget
}

// buildRetailButtonArt selects art once for a window record. Stage forcing is
// confined to the nonzero-stage fallback arm, while staged record finalization
// runs after every selected-art arm [07 R-WGT-01 §3].
func (g *gameShell) buildRetailButtonArt(gad gui.Gadget, own *formats.GAF) retailButtonArtResolution {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	base := int(gad.ArtFrame)
	var entry *formats.GAFEntry
	if own != nil {
		if e, ok := own.Find(name); ok {
			entry = e
		}
	}
	common := (*formats.GAF)(nil)
	if g != nil && g.assets != nil {
		common = g.assets.common
	}
	if entry == nil && common != nil {
		if e, ok := common.Find(name); ok {
			entry = e
		}
	}
	if entry == nil && common != nil {
		// Choose one fallback family. Its absence remains a missing entry;
		// it does not permit trying another family [07 R-WGT-01 §3].
		fallbackName := "BUTTONS0"
		switch {
		case gad.Attribs&guiAttribCheckbox != 0:
			fallbackName = "CHECKBOX"
		case gad.Stages != 0:
			fallbackName = fmt.Sprintf("stagebuttn%d", gad.Stages)
			if gad.Text == "Off|On" || gad.Stages == 1 || gad.Attribs&0x4000 != 0 {
				gad.Stages = 2
				gad.Attribs |= 0x4000
				fallbackName = "stagebuttn1"
			}
		}
		entry, _ = common.Find(fallbackName)
		base = g.fallbackButtonArtResolution(entry, gad).base
	}
	if gad.Stages != 0 {
		// Parsing translated the complete field. The common post-art tail
		// translates each terminated stage fragment again, including a single
		// fragment, before storing the stage labels [07 R-WGT-01 §3][§11].
		caption, _, _ := strings.Cut(gad.Text, "\x00")
		gad.Labels = strings.Split(caption, "|")
		if g != nil && g.cs != nil {
			for i, label := range gad.Labels {
				gad.Labels[i] = g.cs.translations.Translate(label)
			}
		}
		gad.Attribs = (gad.Attribs & 0x4000) | 1
	}
	return retailButtonArtResolution{entry: entry, base: base, gadget: gad}
}

// resolveRetailButtonArt reads the exact entry identity installed by the
// builder. A resolved nil keeps the no-art result authoritative rather than
// consulting another GAF provider [07 R-WGT-01 §3].
func (g *gameShell) resolveRetailButtonArt(gad gui.Gadget) retailButtonArtResolution {
	if gad.ButtonArtResolved {
		return retailButtonArtResolution{entry: gad.ButtonArt, base: int(gad.ArtFrame), gadget: gad}
	}
	return g.buildRetailButtonArt(gad, nil)
}

// fallbackButtonArtResolution compares only the four-frame group bases. A
// score of 1000 is not replaced on a tie or worse, matching the builder's
// bounded search [07 R-WGT-01 §3].
func (g *gameShell) fallbackButtonArtResolution(entry *formats.GAFEntry, gad gui.Gadget) retailButtonArtResolution {
	if entry == nil {
		return retailButtonArtResolution{}
	}
	bestFrame, bestScore := 0, 1000
	for i := 0; i < len(entry.Frames); i += 4 {
		f := entry.Frames[i].Frame
		if f == nil {
			continue
		}
		score := absInt(int(f.Width)-int(gad.Rect.W)) + absInt(int(f.Height)-int(gad.Rect.H))
		if score < bestScore {
			bestFrame, bestScore = i, score
		}
	}
	return retailButtonArtResolution{entry: entry, base: bestFrame, gadget: gad}
}

func (g *gameShell) retailButtonArtFrames(gad gui.Gadget) int {
	art := g.resolveRetailButtonArt(gad)
	if art.entry == nil {
		return 0
	}
	return len(art.entry.Frames)
}

// retailButtonArt applies the button painter's independent down-state and
// current-stage words. A resolved entry starts at its stored base: named and
// staged entries use zero; BUTTONS0 uses the builder's closest four-frame
// group [07 R-WGT-01 §3].
func (g *gameShell) retailButtonArt(gad gui.Gadget, down, stage int, grey bool) *formats.GAFFrame {
	art := g.resolveRetailButtonArt(gad)
	if art.entry == nil || len(art.entry.Frames) == 0 {
		return nil
	}
	gad = art.gadget
	last := len(art.entry.Frames) - 1
	idx := art.base
	switch {
	case grey && cycleButton(gad):
		idx = last
	case grey && gad.Attribs&0x1800 != 0:
		idx = art.base
	case grey && gad.Stages != 0:
		idx = stage
	case grey:
		idx = art.base + min(down+2, last)
	case gad.Stages != 0 && down != 0 && int(gad.Stages) < len(art.entry.Frames):
		idx = last - 1
	case gad.Stages != 0:
		idx = stage
	case down != 0:
		idx = art.base + down
	}
	if idx < 0 {
		idx = 0
	}
	if idx > last {
		idx = last
	}
	return art.entry.Frames[idx].Frame
}
