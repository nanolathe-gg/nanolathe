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
)

type retailListState struct {
	items    []string
	selected int
	top      int
}

type retailScrollDrag struct {
	active     bool
	gadget     int
	vertical   bool
	startCoord int32
	startTop   int
	maxTop     int
	travel     int
}

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

type retailPanelState struct {
	window *gui.Window
	text   map[string]string
	help   map[string]string
	active map[string]bool
	status map[string]int
	lists  map[string]*retailListState
	// owner records which gadget a name belongs to. Retail resolves a gadget
	// by name with a forward scan that stops at the first match
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// SKIRMISH.GUI has five separate TEXT labels — every by-name set reaches
	// only the first, and the rest keep the text in their own record.
	owner map[string]string
}

func newRetailPanelState(window *gui.Window) *retailPanelState {
	p := &retailPanelState{
		window: window,
		text:   make(map[string]string),
		help:   make(map[string]string),
		active: make(map[string]bool),
		status: make(map[string]int),
		lists:  make(map[string]*retailListState),
		owner:  make(map[string]string),
	}
	if window == nil {
		return p
	}
	for _, gad := range window.Gadgets {
		key := menuKey(gad.Name)
		if key == "" {
			continue
		}
		if _, taken := p.owner[key]; taken {
			continue
		}
		p.owner[key] = gad.SourceName
		p.text[key] = gad.Text
		p.help[key] = gad.Help
		p.active[key] = gad.Active != 0
		p.status[key] = int(gad.Status)
		if gad.Kind == gui.KindListBox {
			p.lists[key] = &retailListState{}
		}
	}
	return p
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func menuKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (p *retailPanelState) activeOf(name string) bool {
	if p == nil {
		return false
	}
	return p.active[menuKey(name)]
}

func (p *retailPanelState) setActive(name string, active bool) {
	if p != nil {
		p.active[menuKey(name)] = active
	}
}

func (p *retailPanelState) statusOf(name string) int {
	if p == nil {
		return 0
	}
	return p.status[menuKey(name)]
}

func (p *retailPanelState) setStatus(name string, status int) {
	if p != nil {
		p.status[menuKey(name)] = status
	}
}

func (p *retailPanelState) setText(name, text string) {
	if p != nil {
		p.text[menuKey(name)] = text
	}
}

// textFor is textOf for one specific gadget: a gadget that does not own its
// name is never the target of a by-name set, so it draws its authored record.
func (p *retailPanelState) textFor(gad gui.Gadget) string {
	if p == nil {
		return ""
	}
	key := menuKey(gad.Name)
	if owner, ok := p.owner[key]; ok && owner != gad.SourceName {
		return gad.Text
	}
	return p.text[key]
}

func (p *retailPanelState) textOf(name string) string {
	if p == nil {
		return ""
	}
	return p.text[menuKey(name)]
}

func (p *retailPanelState) setHelp(name, help string) {
	if p != nil {
		p.help[menuKey(name)] = help
	}
}

func (p *retailPanelState) helpOf(name string) string {
	if p == nil {
		return ""
	}
	return p.help[menuKey(name)]
}

func (p *retailPanelState) hitTest(x, y int32) int {
	if p == nil || p.window == nil {
		return -1
	}
	for i, gad := range p.window.Gadgets {
		if gad.Kind == gui.KindPanel || !p.activeOf(gad.Name) || gad.GrayedOut != 0 {
			continue
		}
		r := p.window.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 || x < r.X || y < r.Y || x >= r.X+r.W || y >= r.Y+r.H {
			continue
		}
		return i
	}
	return -1
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
	return g.assets.panel[g.mode]
}

func (g *gameShell) panelBackground() *formats.PCX {
	p := g.panelAssets()
	if p == nil {
		return nil
	}
	if g.mode != modeMenuMission || g.assets == nil {
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	p := g.panel
	if p == nil {
		return
	}
	switch g.mode {
	case modeMenuMission:
		g.refreshMissionPanel()
	case modeMenuMap:
		g.refreshMapPanel()
	case modeMenuSkirmish:
		g.refreshSkirmishPanel()
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// easy to miss when treating a .GUI rectangle as final layout. Retail picks
// the closest stock/owned GAF frame and then stores that frame's dimensions
// back into the runtime gadget record. Hit testing and text centering must see
// those dimensions too.
func (g *gameShell) resolveRetailButtonGeometry() {
	if g == nil || g.panel == nil || g.panel.window == nil {
		return
	}
	for i := range g.panel.window.Gadgets {
		gad := g.panel.window.Gadgets[i]
		if i == 0 || gad.Kind != gui.KindButton {
			continue
		}
		frame := g.gadgetArt(gad, g.panel.statusOf(gad.Name))
		if frame == nil {
			frame = g.retailButtonFrame(gad, g.panel.statusOf(gad.Name), false)
		}
		if frame == nil || frame.Width == 0 || frame.Height == 0 {
			continue
		}
		g.panel.window.Gadgets[i].Rect.W = int32(frame.Width)
		g.panel.window.Gadgets[i].Rect.H = int32(frame.Height)
	}
}

func (g *gameShell) refreshMissionPanel() {
	p := g.panel
	if p == nil {
		return
	}
	if g.campaigns == nil {
		campaigns, err := mission.Discover(g.cs.fs)
		if err == nil {
			g.campaigns = campaigns
		}
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// retains only records whose HEADER campaignside matches the selected
	// side, plus the literal ALL. When the retail campaign count is <=2,
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	p.setActive("Campaign", showCampaign)
	p.setActive("CampaignKnob", showCampaign)
	p.setActive("Missions", g.missionAny)
	p.setActive("MissionsKnob", g.missionAny)
	p.setStatus("Difficulty", clampMenuStage(g.missionDifficultyValue, 3))
	if g.missionSide&1 == 0 {
		p.setStatus("Side0", 1)
		p.setStatus("Side1", 0)
		p.setText("SIDENAME", "Arm Campaign")
	} else {
		p.setStatus("Side0", 0)
		p.setStatus("Side1", 1)
		p.setText("SIDENAME", "Core Campaign")
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
	if g.panel == nil {
		return
	}
	if g.mapIdx < 0 {
		g.mapIdx = 0
	}
	if g.mapIdx >= len(g.maps) && len(g.maps) != 0 {
		g.mapIdx = len(g.maps) - 1
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		g.panel.setText("DESCRIPTION", "")
		g.panel.setText("SIZE", "")
		return
	}
	if d := g.mapDataFor(g.maps[g.mapIdx]); d != nil {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// verbatim — the authored string already carries the "16 X 17 " size
		// prefix — and formats OTA memory, the localized "Players" label, and
		// OTA numplayers through "%s  %s: %s" into SIZE. Both pairs of spaces
		// are part of the retail format string.
		g.panel.setText("DESCRIPTION", d.description)
		size := ""
		if d.ota != nil {
			memory := strings.TrimSpace(d.ota.Memory)
			numPlayers := strings.TrimSpace(d.ota.NumPlayers)
			if memory != "" || numPlayers != "" {
				size = fmt.Sprintf("%s  %s: %s", memory, "Players", numPlayers)
			}
		}
		g.panel.setText("SIZE", size)
	}
}

func (g *gameShell) refreshSkirmishPanel() {
	p := g.panel
	if p == nil {
		return
	}
	g.ensureRetailSkirmishControllers()
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// into the MapName gadget. It does not reopen the map to read a title.
	p.setText("MapName", g.setup.MapName)
	if g.setup.Location == 0 {
		p.setStatus("StartLocation", 1)
		p.setHelp("StartLocation", "Commanders are randomly placed on the battle field.")
	} else {
		p.setStatus("StartLocation", 0)
		p.setHelp("StartLocation", "Commanders are placed at pre-determined locations.")
	}
	if g.setup.CommanderDeath == 0 {
		p.setStatus("CommanderDeath", 1)
		p.setHelp("CommanderDeath", "Game continues after Commander is destroyed.")
	} else {
		p.setStatus("CommanderDeath", 0)
		p.setHelp("CommanderDeath", "Game ends when commander is destroyed.")
	}
	if g.setup.Mapping == 0 {
		p.setStatus("Mapping", 1)
		p.setHelp("Mapping", "Terrain is visible.")
	} else {
		p.setStatus("Mapping", 0)
		p.setHelp("Mapping", "Terrain is blacked out until explored.")
	}
	if g.setup.LineOfSight == 0 {
		p.setStatus("LineOfSight", 0)
		p.setHelp("LineOfSight", "All mapped terrain is visible.")
	} else if g.setup.LOSType == 1 {
		p.setStatus("LineOfSight", 1)
		p.setHelp("LineOfSight", "Terrain elevations affect a unit's view.")
	} else {
		p.setStatus("LineOfSight", 2)
		p.setHelp("LineOfSight", "Terrain elevations do not affect a unit's view.")
	}
	p.setStatus("Difficulty", clampMenuStage(g.setup.Difficulty, 3))

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// not present in SKIRMISH.GUI on disk, but are still retail gadgets with
	// fixed coordinates and stock GAF art, so refresh their authored records.
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		rowActive := i < g.setup.NumPlayers
		player := &g.setup.Players[i]
		prefix := strconv.Itoa(i)
		controller := g.retailControllers[i]
		g.panel.setActive("Player"+prefix, rowActive)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Player%d button remains the hit target that turns it into a
		// computer row.
		configured := rowActive && controller != 0
		g.panel.setActive("Side"+prefix, configured)
		g.panel.setActive("Allies"+prefix, configured)
		g.panel.setActive("Metal"+prefix, configured)
		g.panel.setActive("Energy"+prefix, configured)
		g.panel.setActive("Color"+prefix, configured)
		if rowActive {
			switch controller {
			case 1:
				p.setText("Player"+prefix, "Player")
			case 2:
				p.setText("Player"+prefix, "Computer")
			default:
				p.setText("Player"+prefix, "Open")
			}
			p.setText("Metal"+prefix, strconv.Itoa(player.Metal))
			p.setText("Energy"+prefix, strconv.Itoa(player.Energy))
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// retains its ordinary/hover frame state.
			p.setStatus("Player"+prefix, 0)
			p.setStatus("Side"+prefix, player.Side)
			p.setStatus("Allies"+prefix, g.retailAllyIconFrame(i))
			p.setStatus("Color"+prefix, player.Color)
		}
	}
	p.setHelp("Allies0", "Click to select an allegiance symbol.")
	p.setHelp("Metal0", "Left click to increase metal. Right click to decrease metal.")
	p.setHelp("Energy0", "Left click to increase energy. Right click to decrease energy.")
	for i := 1; i < session.SkirmishMaxPlayers; i++ {
		prefix := strconv.Itoa(i)
		p.setHelp("Allies"+prefix, "Click to select an allegiance symbol.")
		p.setHelp("Metal"+prefix, "Left click to increase metal. Right click to decrease metal.")
		p.setHelp("Energy"+prefix, "Left click to increase energy. Right click to decrease energy.")
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	if g.panel == nil {
		return
	}
	help := ""
	if idx := g.panel.hitTest(x, y); idx >= 0 {
		if gad, ok := g.currentGadget(idx); ok {
			help = g.panel.helpOf(gad.Name)
		}
	}
	g.panel.setText("HELPTEXT", help)
}

// installSkirmishDynamicGadgets is the direct frontend equivalent of
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// row spacing and stock GAF names below are taken from that runtime builder,
// not from a Nanolathe layout.
func (g *gameShell) installSkirmishDynamicGadgets() {
	if g.assets == nil || g.assets.panel[modeMenuSkirmish] == nil || g.assets.panel[modeMenuSkirmish].window == nil {
		return
	}
	w := g.assets.panel[modeMenuSkirmish].window
	base := make([]gui.Gadget, 0, len(w.Gadgets)+session.SkirmishMaxPlayers*6)
	for _, gad := range w.Gadgets {
		if gad.SourceName == retailDynamicSkirmishSource {
			continue
		}
		base = append(base, gad)
	}
	w.Gadgets = base
	n := g.setup.NumPlayers
	if n < 1 {
		n = 1
	}
	if n > session.SkirmishMaxPlayers {
		n = session.SkirmishMaxPlayers
	}
	step := 200 / n
	rowY := (180-(n-1)*step)/2 + 79
	for i := 0; i < n; i++ {
		suffix := strconv.Itoa(i)
		player := g.setup.Players[i]
		side := retailDynamicButton("Side"+suffix, 163, rowY, 45, 20, "SIDEx", player.Side)
		// The runtime builder writes Stages=2 into the Side record. For a
		// staged GAF control the retail renderer uses the entry's penultimate
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		side.Stages = 2
		w.Gadgets = append(w.Gadgets,
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// skirmname frame is the ordinary/hover button state, not the
			// Open/Player/Computer text state.
			retailDynamicButton("Player"+suffix, 45, rowY, 112, 20, "skirmname", 0),
			side,
			retailDynamicSurface("Color"+suffix, 214, rowY, 20, 20, "32xlogos", player.Color),
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// row's computed symbol; the alliance number is never the
			// frame index.
			retailDynamicSurface("Allies"+suffix, 241, rowY, 40, 20, "TEAMICONSx", 10),
			retailDynamicButton("Metal"+suffix, 286, rowY, 45, 20, "skirmmet", 0),
			retailDynamicButton("Energy"+suffix, 337, rowY, 45, 20, "skirmmet", 0),
		)
		rowY += step
	}
}

const retailDynamicSkirmishSource = "RETAIL_DYNAMIC_SKIRMISH"

func retailDynamicButton(name string, x, y, w, h int, art string, status int) gui.Gadget {
	attribs := uint32(2)
	if art == "skirmmet" {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		attribs |= 0x10000
	}
	return gui.Gadget{
		Kind:       gui.KindButton,
		Name:       name,
		Rect:       gui.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Attribs:    attribs,
		Active:     1,
		Status:     int16(status),
		Art:        art,
		SourceName: retailDynamicSkirmishSource,
	}
}

func retailDynamicSurface(name string, x, y, w, h int, art string, status int) gui.Gadget {
	return gui.Gadget{
		Kind:       gui.KindSurface,
		Name:       name,
		Rect:       gui.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Active:     1,
		Status:     int16(status),
		Art:        art,
		SourceName: retailDynamicSkirmishSource,
	}
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
	if g.panel == nil {
		return
	}
	key := menuKey(name)
	l := g.panel.lists[key]
	if l == nil {
		l = &retailListState{}
		g.panel.lists[key] = l
	}
	changed := len(l.items) != len(items)
	if !changed {
		for i := range items {
			if l.items[i] != items[i] {
				changed = true
				break
			}
		}
	}
	if changed {
		l.items = append(l.items[:0], items...)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// subsequent refreshes preserve manual scrollbar movement.
		l.top = 0
	}
	if len(l.items) == 0 {
		l.selected, l.top = 0, 0
		return
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(l.items) {
		selected = len(l.items) - 1
	}
	selectionChanged := l.selected != selected
	l.selected = selected
	if changed || selectionChanged {
		g.ensureRetailListVisible(key)
	}
}

func (g *gameShell) ensureRetailListVisible(name string) {
	if g == nil || g.panel == nil || g.panel.window == nil {
		return
	}
	l := g.panel.lists[menuKey(name)]
	if l == nil || len(l.items) == 0 {
		return
	}
	for i, gad := range g.panel.window.Gadgets {
		if gad.Kind != gui.KindListBox || !strings.EqualFold(gad.Name, name) {
			continue
		}
		visible := retailVisibleListRows(g.panel.window.PlacedRect(i), retailListItemHeight(gad, g.retailTextHeight()))
		maxTop := len(l.items) - visible
		if maxTop < 0 {
			maxTop = 0
		}
		if l.selected < l.top {
			l.top = l.selected
		} else if l.selected >= l.top+visible {
			l.top = l.selected - visible + 1
		}
		if l.top < 0 {
			l.top = 0
		}
		if l.top > maxTop {
			l.top = maxTop
		}
		return
	}
}

func (g *gameShell) currentGadget(index int) (gui.Gadget, bool) {
	if g.panel == nil || g.panel.window == nil || index < 0 || index >= len(g.panel.window.Gadgets) {
		return gui.Gadget{}, false
	}
	return g.panel.window.Gadgets[index], true
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
	if g.panel == nil || g.panel.window == nil || g.assets == nil {
		return
	}
	if g.mode == modeMenuSkirmish {
		g.updateHoverHelp(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
	}
	// Back to front along the window chain: the screen beneath first, then the
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if g.under != nil && g.under.window != nil {
		g.drawRetailWindow(c, g.underMode, g.under)
	}
	g.drawRetailWindow(c, g.mode, g.panel)
}

// drawRetailWindow composes one window of the chain. mode selects the resource
// set the gadget art is resolved against, so a window beneath the active one
// still draws with its own GUI GAF.
func (g *gameShell) drawRetailWindow(c *client.Client, mode shellMode, p *retailPanelState) {
	if p == nil || p.window == nil {
		return
	}
	savedMode, savedPanel := g.mode, g.panel
	g.mode, g.panel = mode, p
	defer func() { g.mode, g.panel = savedMode, savedPanel }()

	if bg := g.panelBackground(); bg != nil {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// own surface at (0,0), and that surface is the window rectangle. The
		// bitmap therefore lands at the window origin and anything past the
		// rectangle is not part of the window [07 §4].
		r := p.window.Rect
		c.UIBlitPCXClipped(bg, int(r.X), int(r.Y), int(r.X), int(r.Y), int(r.W), int(r.H))
	} else {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// the window rectangle; the stock fallback entry is BackTile.
		g.drawPanelTile(c, p.window.Rect)
	}
	for i, gad := range p.window.Gadgets {
		if i == 0 || !p.activeOf(gad.Name) {
			continue
		}
		r := p.window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			g.drawRetailButton(c, gad, r)
		case gui.KindListBox:
			g.drawRetailList(c, gad, r)
		case gui.KindScrollBar:
			g.drawRetailScrollbar(c, gad, r)
		case gui.KindSurface:
			g.drawRetailSurface(c, gad, r)
		case gui.KindLabel, gui.KindText, gui.KindPicture:
			g.drawRetailArt(c, gad, r)
			g.drawRetailText(c, gad, r)
		default:
			g.drawRetailArt(c, gad, r)
			g.drawRetailText(c, gad, r)
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// still uses the mounted BackTile and common button art; only its TEXT gadgets
// are created at runtime, as retail does for wrapped diagnostic strings.
func (g *gameShell) drawRetailModal(c *client.Client) {
	if g == nil || g.modal == nil || g.modal.window == nil || g.assets == nil {
		return
	}
	m := g.modal
	g.drawPanelTile(c, m.window.Rect)
	for i, gad := range m.window.Gadgets {
		if i == 0 || !m.activeOf(gad.Name) {
			continue
		}
		r := m.window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			pressed := false
			if c != nil && c.Input() != nil && c.Input().Mouse != nil {
				pressed = pointInRect(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y), r) &&
					c.Input().Mouse.Held(input.MouseButtonLeft)
			}
			if frame := g.retailButtonFrame(gad, m.statusOf(gad.Name), pressed); frame != nil {
				blitRetailFrame(c, frame, int(r.X), int(r.Y))
			}
			g.drawRetailTextState(c, m, gad, r)
		case gui.KindLabel, gui.KindText:
			g.drawRetailTextState(c, m, gad, r)
		}
	}
}

func cloneRetailWindow(src *gui.Window) *gui.Window {
	if src == nil {
		return nil
	}
	w := *src
	w.Gadgets = append([]gui.Gadget(nil), src.Gadgets...)
	return &w
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// at local y=20, each line advances by font height plus five pixels, and the
// box is centered after its content and close-button margins are known.
func (g *gameShell) showRetailMessage(message string) {
	if g == nil || g.assets == nil || g.assets.message == nil || g.assets.message.window == nil {
		return
	}
	if len(message) > 255 {
		message = message[:255]
	}
	lines := retailMessageLines(message, g.retailTextWidth, 480)
	if len(lines) == 0 {
		return
	}
	width := 0
	for _, line := range lines {
		lineWidth := g.retailTextWidth(line)
		if lineWidth > width {
			width = lineWidth
		}
	}
	// The retail close control is moved to the lower-right edge with a
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	buttonW, buttonH := 80, 20
	if frame := g.retailButtonFrame(gui.Gadget{Kind: gui.KindButton, Rect: gui.Rect{W: 80, H: 42}}, 0, false); frame != nil {
		buttonW, buttonH = int(frame.Width), int(frame.Height)
	}
	boxW := width + 20
	if boxW < buttonW+30 {
		boxW = buttonW + 30
	}
	boxH := len(lines)*25 + buttonH + 40
	if boxH < buttonH+40 {
		boxH = buttonH + 40
	}
	originX := (640 - boxW) / 2
	originY := (480 - boxH) / 2
	w := cloneRetailWindow(g.assets.message.window)
	w.Rect = gui.Rect{X: int32(originX), Y: int32(originY), W: int32(boxW), H: int32(boxH), RawX: int32(originX), RawY: int32(originY)}
	w.OriginX, w.OriginY = int32(originX), int32(originY)
	if len(w.Gadgets) != 0 {
		w.Gadgets[0].Rect = w.Rect
	}
	for i := range w.Gadgets {
		if !strings.EqualFold(w.Gadgets[i].Name, "OK") {
			continue
		}
		w.Gadgets[i].Rect.W = int32(buttonW)
		w.Gadgets[i].Rect.H = int32(buttonH)
		w.Gadgets[i].Rect.X = int32(boxW - buttonW - 15)
		w.Gadgets[i].Rect.Y = int32(boxH - buttonH - 15)
	}
	for i, line := range lines {
		w.Gadgets = append(w.Gadgets, gui.Gadget{
			Kind:       gui.KindLabel,
			Name:       fmt.Sprintf("TEXT%d", i),
			Rect:       gui.Rect{X: 0, Y: int32(20 + i*(g.retailTextHeight()+5)), W: int32(boxW), H: int32(g.retailTextHeight())},
			Attribs:    2,
			Active:     1,
			Text:       line,
			SourceName: "RETAIL_DYNAMIC_MSGBOX",
		})
	}
	g.modal = newRetailPanelState(w)
	g.modalPressed = false
}

func retailMessageLines(message string, measure func(string) int, maxWidth int) []string {
	var lines []string
	for _, paragraph := range strings.Split(message, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			continue
		}
		line := ""
		for _, word := range words {
			candidate := word
			if line != "" {
				candidate = line + " " + word
			}
			if maxWidth > 0 && measure != nil && line != "" && measure(candidate) > maxWidth {
				lines = append(lines, line)
				line = word
			} else {
				line = candidate
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
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

func (g *gameShell) drawRetailArt(c *client.Client, gad gui.Gadget, r gui.Rect) {
	if f := g.gadgetArt(gad, g.panel.statusOf(gad.Name)); f != nil {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
}

func (g *gameShell) drawRetailButton(c *client.Client, gad gui.Gadget, r gui.Rect) {
	pressed := retailButtonPressed(c, r)
	status := g.panel.statusOf(gad.Name)
	art := g.gadgetButtonArt(gad, status, pressed)
	if art != nil {
		blitRetailFrame(c, art, int(r.X), int(r.Y))
	} else if frame := g.retailButtonFrame(gad, status, pressed); frame != nil {
		blitRetailFrame(c, frame, int(r.X), int(r.Y))
	}
	g.drawRetailText(c, gad, r)
}

// gadgetButtonArt applies the pressed state that the retail button pump
// applies to an owned GAF entry. Retail does not tint or replace an ordinary
// button merely because the pointer is over it; the armed frame is visible
// only while the left button is held inside the gadget [07 §3]. The
// executable's renderer uses frame 1 for an ordinary two-state entry and
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func (g *gameShell) drawRetailText(c *client.Client, gad gui.Gadget, r gui.Rect) {
	g.drawRetailTextState(c, g.panel, gad, r)
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

func (g *gameShell) drawRetailTextState(c *client.Client, p *retailPanelState, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	text := p.textFor(gad)
	if len(gad.Labels) != 0 {
		idx := clampMenuStage(p.statusOf(gad.Name), len(gad.Labels))
		text = gad.Labels[idx]
	}
	if text == "" || !g.hasRetailTextFont() {
		return
	}
	width := g.retailTextWidth(text)
	pressed := retailButtonPressed(c, r)
	x := int(r.X)
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	color := g.guiColor(byte(gad.ColorF & 0xff))
	maxWidth := int(r.W)
	if maxWidth <= 0 {
		width, _ := c.Size()
		maxWidth = width - x
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// before it picks a renderer: it compares the rectangle's inclusive height
	// (y1-y0) with twice the capital-I frame height plus two, and sends the
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// the wrapped case and SIZE 235x18 for the single-line one [07 §4].
	lineStep := g.retailTextHeight()
	if int(r.H)-1 > 2*lineStep {
		lines := retailWrapLines(text, g.retailTextWidth, maxWidth)
		top := int(r.Y) + (int(r.H)-1-len(lines)*lineStep)/2
		if top < int(r.Y) {
			top = int(r.Y)
		}
		for i, line := range lines {
			g.drawRetailString(c, line, x, top+i*lineStep, maxWidth, color)
		}
		return
	}
	g.drawRetailString(c, text, x, y, maxWidth, color)
}

// retailWrapLines breaks a label at spaces so it fits maxWidth, keeping each
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func (g *gameShell) drawRetailList(c *client.Client, gad gui.Gadget, r gui.Rect) {
	g.drawListBox(c, r)
	l := g.panel.lists[menuKey(gad.Name)]
	if l == nil || len(l.items) == 0 || !g.hasRetailTextFont() {
		return
	}
	itemHeight := retailListItemHeight(gad, g.retailTextHeight())
	visible := retailVisibleListRows(r, itemHeight)
	maxTop := len(l.items) - visible
	if maxTop < 0 {
		maxTop = 0
	}
	if l.top < 0 {
		l.top = 0
	}
	if l.top > maxTop {
		l.top = maxTop
	}
	for row := 0; row < visible; row++ {
		idx := l.top + row
		if idx >= len(l.items) {
			break
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// calculating rows. The same origin is used by its text renderer.
		y := int(r.Y) + 2 + row*itemHeight
		color := g.guiColor(byte(gad.ColorF & 0xff))
		g.drawRetailString(c, l.items[idx], int(r.X)+4, y, int(r.W)-4, color)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// operator remaps whatever is already in the rectangle, so the glyphs
		// are lifted along with the listbox interior.
		if idx == l.selected {
			g.drawListSelection(c, r, y, itemHeight)
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// +30, and a non-negative level there indexes the 32-row PALETTE.LHT
// brightening table, so the row's own pixels are remapped one row at a time.
// That is what makes the selected entry read as a lit bar over the listbox
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (g *gameShell) drawListSelection(c *client.Client, r gui.Rect, y, h int) {
	if g == nil || g.assets == nil || g.assets.pal == nil {
		return
	}
	c.UILightRect(g.assets.pal, int(r.X), y, int(r.W), h, retailListSelectionLevel)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// list row, focused or not.
const retailListSelectionLevel = 30

func (g *gameShell) drawRetailScrollbar(c *client.Client, gad gui.Gadget, r gui.Rect) {
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

		l := g.listForAssoc(gad.Assoc)
		itemHeight := g.retailListAssocItemHeight(gad.Assoc)
		visible := retailVisibleListRows(g.listRectForAssoc(gad.Assoc), itemHeight)
		maxTop := 0
		if l != nil {
			maxTop = len(l.items) - visible
			if maxTop < 0 {
				maxTop = 0
			}
		}
		total := 0
		if l != nil {
			total = len(l.items)
		}
		thumbLen := retailScrollbarKnobSize(visible, total, int(r.H))
		travel := trackBottom - trackTop - thumbLen
		if travel < 0 {
			travel = 0
		}
		pos := 0
		if l != nil && maxTop > 0 {
			pos = l.top * travel / maxTop
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
	l := g.listForAssoc(gad.Assoc)
	itemHeight := g.retailListAssocItemHeight(gad.Assoc)
	visible := retailVisibleListRows(g.listRectForAssoc(gad.Assoc), itemHeight)
	maxTop := 0
	if l != nil {
		maxTop = len(l.items) - visible
		if maxTop < 0 {
			maxTop = 0
		}
	}
	total := 0
	if l != nil {
		total = len(l.items)
	}
	thumbLen := retailScrollbarKnobSize(visible, total, int(r.W))
	travel := trackRight - trackLeft - thumbLen
	if travel < 0 {
		travel = 0
	}
	pos := 0
	if l != nil && maxTop > 0 {
		pos = l.top * travel / maxTop
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It divides the associated list's visible row count by its item count, scales
// that by the scrollbar's own length less three pixels, rounds, and clamps the
// result up to ten. SLIDERS carries the knob as a one-pixel cap, a repeatable
// three-pixel middle and a one-pixel cap, so the length is a computed run and
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func (g *gameShell) listForAssoc(assoc int32) *retailListState {
	if g.panel == nil || g.panel.window == nil {
		return nil
	}
	for _, gad := range g.panel.window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return g.panel.lists[menuKey(gad.Name)]
		}
	}
	return nil
}

func (g *gameShell) listRectForAssoc(assoc int32) gui.Rect {
	if g == nil || g.panel == nil || g.panel.window == nil {
		return gui.Rect{}
	}
	for i, gad := range g.panel.window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return g.panel.window.PlacedRect(i)
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
		maxTop = len(l.items) - visible
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	if g == nil || g.panel == nil || g.panel.window == nil {
		return g.retailTextHeight() + 1
	}
	for _, gad := range g.panel.window.Gadgets {
		if gad.Kind == gui.KindListBox && gad.Assoc == assoc {
			return retailListItemHeight(gad, g.retailTextHeight())
		}
	}
	return g.retailTextHeight() + 1
}

func (g *gameShell) drawRetailSurface(c *client.Client, gad gui.Gadget, r gui.Rect) {
	if strings.EqualFold(gad.Name, "MAPPIC") && len(g.maps) != 0 && g.mapIdx >= 0 && g.mapIdx < len(g.maps) {
		if d := g.mapDataFor(g.maps[g.mapIdx]); d != nil && d.tnt != nil {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// MAPPIC canvas using the map's aspect, leaving the surrounding
			// canvas intact. The TNT minimap is the same indexed source for
			// this frontend path; preserve that retail letterbox instead of
			// stretching rectangular maps into the 125×125 square.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// gadget-art blit. An RLE frame (Compressed != 0) is stamped at the gadget
	// origin; a raw frame is texture-mapped across the whole gadget rectangle.
	// SKIRMISH's Color%d surface is the visible case: textures/logos.gaf holds
	// raw 32x32 frames that retail resamples into the authored 20x20 record,
	// while anims/skirmish.gaf's RLE ally icons are stamped 1:1 [07 §4].
	if f := g.gadgetArt(gad, g.panel.statusOf(gad.Name)); f != nil {
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
	if l == nil || len(l.items) == 0 {
		return
	}
	r := g.listRectForAssoc(gad.Assoc)
	maxTop := len(l.items) - retailVisibleListRows(r, g.retailListAssocItemHeight(gad.Assoc))
	if maxTop < 0 {
		maxTop = 0
	}
	l.top += delta
	if l.top < 0 {
		l.top = 0
	}
	if l.top > maxTop {
		l.top = maxTop
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
		thumbPos += l.top * geometry.travel / geometry.maxTop
	}
	if coordinate < thumbPos || coordinate >= thumbPos+geometry.thumbLen {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// calculated knob rectangle; clicking the track beside it does not
		// invent page-step behavior.
		return
	}
	g.scrollDrag = retailScrollDrag{
		active:     true,
		gadget:     index,
		vertical:   geometry.vertical,
		startCoord: int32(coordinate),
		startTop:   l.top,
		maxTop:     geometry.maxTop,
		travel:     geometry.travel,
	}
}

func (g *gameShell) updateRetailScrollbarDrag(mouse *client.MouseState) {
	if g == nil || mouse == nil || !g.scrollDrag.active {
		return
	}
	if !mouse.Held(input.MouseButtonLeft) {
		g.scrollDrag = retailScrollDrag{}
		return
	}
	if g.panel == nil || g.panel.window == nil || g.scrollDrag.gadget < 0 || g.scrollDrag.gadget >= len(g.panel.window.Gadgets) {
		g.scrollDrag = retailScrollDrag{}
		return
	}
	gad := g.panel.window.Gadgets[g.scrollDrag.gadget]
	l := g.listForAssoc(gad.Assoc)
	if l == nil || g.scrollDrag.travel <= 0 || g.scrollDrag.maxTop <= 0 {
		return
	}
	coordinate := int32(mouse.X)
	if g.scrollDrag.vertical {
		coordinate = int32(mouse.Y)
	}
	delta := int(coordinate - g.scrollDrag.startCoord)
	// The retail path converts the pointer displacement through the knob
	// travel/range and truncates the integer result toward zero.
	l.top = g.scrollDrag.startTop + delta*g.scrollDrag.maxTop/g.scrollDrag.travel
	if l.top < 0 {
		l.top = 0
	}
	if l.top > g.scrollDrag.maxTop {
		l.top = g.scrollDrag.maxTop
	}
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
	if g.modal != nil {
		g.modalInput(cl)
		return
	}
	if g.panel == nil || g.panel.window == nil {
		return
	}
	in := cl.Input()
	mouse := in.Mouse
	kbd := in.Kbd
	leftPressed := mouse.Pressed(input.MouseButtonLeft)
	leftReleased := mouse.Released(input.MouseButtonLeft)
	rightPressed := mouse.Pressed(input.MouseButtonRight)
	rightReleased := mouse.Released(input.MouseButtonRight)
	wasScrollDrag := g.scrollDrag.active
	g.updateRetailScrollbarDrag(mouse)
	if mouse.Held(input.MouseButtonLeft) && !leftPressed && !g.scrollDrag.active && g.retailPressed >= 0 {
		x, y := int32(mouse.X), int32(mouse.Y)
		if idx := g.panel.hitTest(x, y); idx >= 0 {
			if idx == g.retailPressed {
				gad, ok := g.currentGadget(idx)
				if !ok || gad.Kind != gui.KindScrollBar {
					goto noRetailArrowRepeat
				}
				geometry, geometryOK := g.retailScrollbarGeometry(gad, g.panel.window.PlacedRect(idx))
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
		g.retailPressed = -1
		x, y := int32(mouse.X), int32(mouse.Y)
		idx := g.panel.hitTest(x, y)
		if idx >= 0 {
			// Retail's GUI pump gives the clicked gadget focus before running
			// its callback, so a following Return/Space activates that same
			// control rather than the previous default.
			g.panel.window.Focus = idx
			gad, _ := g.currentGadget(idx)
			g.retailPressed = idx
			if gad.Kind == gui.KindScrollBar {
				g.clickRetailScrollbar(idx, gad, g.panel.window.PlacedRect(idx), x, y)
			}
		}
	}
	if leftReleased {
		pending := g.retailPressed
		g.retailPressed = -1
		if !wasScrollDrag && pending >= 0 {
			x, y := int32(mouse.X), int32(mouse.Y)
			if idx := g.panel.hitTest(x, y); idx == pending {
				if gad, ok := g.currentGadget(idx); ok {
					switch gad.Kind {
					case gui.KindListBox:
						g.clickList(gad, g.panel.window.PlacedRect(idx), x, y)
					case gui.KindScrollBar:
						g.releaseRetailScrollbar(gad, g.panel.window.PlacedRect(idx), x, y)
					default:
						// Retail's pump returns to the window loop as soon as
						// a callback has run, and re-reads the panel on the
						// next pass. A callback is free to close the window it
						// was invoked from — Start leaves for the loading
						// screen — so nothing after this may touch g.panel.
						g.activateGadget(gad.Name)
						return
					}
				}
			}
		}
	}
	if rightPressed {
		g.retailRightPressed = -1
		if g.mode == modeMenuSkirmish {
			x, y := int32(mouse.X), int32(mouse.Y)
			if idx := g.panel.hitTest(x, y); idx >= 0 {
				if gad, ok := g.currentGadget(idx); ok {
					key := menuKey(gad.Name)
					if strings.HasPrefix(key, "metal") || strings.HasPrefix(key, "energy") || strings.HasPrefix(key, "color") {
						g.retailRightPressed = idx
					}
				}
			}
		}
	}
	if rightReleased {
		pending := g.retailRightPressed
		g.retailRightPressed = -1
		if g.mode == modeMenuSkirmish && pending >= 0 {
			x, y := int32(mouse.X), int32(mouse.Y)
			if idx := g.panel.hitTest(x, y); idx == pending {
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
	if g.panel == nil || g.panel.window == nil {
		// A pointer callback above closed the panel.
		return
	}
	if kbd.KeyDown(input.KeyEscape) {
		g.activateEscape()
		return
	}
	if kbd.KeyDown(input.KeyEnter) || kbd.KeyDown(input.KeySpace) {
		name := ""
		if idx := g.panel.window.Focus; idx >= 0 {
			if gad, ok := g.currentGadget(idx); ok && g.panel.activeOf(gad.Name) {
				name = gad.Name
			}
		}
		if name == "" {
			name = g.panel.window.Header.CrDefault
		}
		if name != "" && g.hasActiveGadget(name) {
			g.activateGadget(name)
			return
		}
	}
	for i, gad := range g.panel.window.Gadgets {
		if i == 0 || gad.QuickKey == 0 || !g.panel.activeOf(gad.Name) {
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
	if g == nil || g.modal == nil || g.modal.window == nil || cl == nil || cl.Input() == nil {
		return
	}
	in := cl.Input()
	if in.Mouse.Pressed(input.MouseButtonLeft) {
		idx := g.modal.hitTest(int32(in.Mouse.X), int32(in.Mouse.Y))
		if idx >= 0 && idx < len(g.modal.window.Gadgets) && strings.EqualFold(g.modal.window.Gadgets[idx].Name, "OK") {
			g.modalPressed = true
		}
	}
	if in.Mouse.Released(input.MouseButtonLeft) {
		if g.modalPressed {
			idx := g.modal.hitTest(int32(in.Mouse.X), int32(in.Mouse.Y))
			if idx >= 0 && idx < len(g.modal.window.Gadgets) && strings.EqualFold(g.modal.window.Gadgets[idx].Name, "OK") {
				g.modal = nil
			}
		}
		g.modalPressed = false
	}
	if in.Kbd.KeyDown(input.KeyEscape) || in.Kbd.KeyDown(input.KeyEnter) || in.Kbd.KeyDown(input.KeySpace) {
		g.modal = nil
	}
}

func quickKeyDown(kbd *client.KeyboardState, quick byte) bool {
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
	if g.panel == nil {
		return
	}
	var name string
	switch g.mode {
	case modeMenuMap:
		name = "MAPNAMES"
	case modeMenuMission:
		if g.missionAny {
			name = "Missions"
		} else {
			name = "Campaign"
		}
	}
	l := g.panel.lists[menuKey(name)]
	if l == nil || len(l.items) == 0 {
		return
	}
	if up {
		l.selected = cycleInt(l.selected, 0, len(l.items)-1, -1)
	} else {
		l.selected = cycleInt(l.selected, 0, len(l.items)-1, 1)
	}
	g.ensureRetailListVisible(name)
	g.commitListSelection(name, l.selected)
}

func (g *gameShell) scrollAt(x, y int32, amount float32) {
	if g.panel == nil || g.panel.window == nil {
		return
	}
	for i, gad := range g.panel.window.Gadgets {
		if gad.Kind != gui.KindListBox || !g.panel.activeOf(gad.Name) {
			continue
		}
		r := g.panel.window.PlacedRect(i)
		if !pointInRect(x, y, r) {
			continue
		}
		l := g.panel.lists[menuKey(gad.Name)]
		if l == nil || len(l.items) == 0 {
			return
		}
		// Ebitengine reports a positive wheel delta for motion toward the
		// top, matching the retail wheel token's sign.
		if amount > 0 {
			l.top--
		} else if amount < 0 {
			l.top++
		}
		if l.top < 0 {
			l.top = 0
		}
		itemHeight := retailListItemHeight(gad, g.retailTextHeight())
		visible := retailVisibleListRows(r, itemHeight)
		maxTop := len(l.items) - visible
		if maxTop < 0 {
			maxTop = 0
		}
		if l.top > maxTop {
			l.top = maxTop
		}
		return
	}
}

func (g *gameShell) clickList(gad gui.Gadget, r gui.Rect, x, y int32) {
	l := g.panel.lists[menuKey(gad.Name)]
	if l == nil || len(l.items) == 0 || !g.hasRetailTextFont() {
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
	idx := l.top + row
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.selected = idx
	g.ensureRetailListVisible(gad.Name)
	g.commitListSelection(gad.Name, idx)
}

func (g *gameShell) commitListSelection(name string, index int) {
	switch {
	case g.mode == modeMenuMap && strings.EqualFold(name, "MAPNAMES"):
		g.mapIdx = index
		g.refreshRetailPanel()
	case g.mode == modeMenuMission && strings.EqualFold(name, "Campaign"):
		g.campaignIdx = index
		g.missionIdx = 0
		g.refreshRetailPanel()
	case g.mode == modeMenuMission && strings.EqualFold(name, "Missions"):
		g.missionIdx = index
		g.refreshRetailPanel()
	}
}

func (g *gameShell) activateEscape() {
	if g.mode == modeMenuMain {
		return
	}
	if g.panel != nil && g.panel.window != nil && g.panel.window.Header.EscDefault != "" &&
		g.hasActiveGadget(g.panel.window.Header.EscDefault) {
		g.activateGadget(g.panel.window.Header.EscDefault)
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
	if g.panel == nil || g.panel.window == nil || !g.panel.activeOf(name) {
		return false
	}
	for _, gad := range g.panel.window.Gadgets {
		if strings.EqualFold(gad.Name, name) {
			return true
		}
	}
	return false
}

func (g *gameShell) activateGadget(name string) {
	key := menuKey(name)
	switch g.mode {
	case modeMenuMain:
		switch key {
		case "single":
			g.openMenu(modeMenuSingle)
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
		case "skirmish":
			g.openMenu(modeMenuSkirmish)
		case "prevmenu":
			g.openMenu(modeMenuMain)
		}
	case modeMenuMission:
		switch key {
		case "prevmenu":
			g.openMenu(modeMenuSingle)
		case "start":
			// The campaign Start leaves for the same loading screen the
			// skirmish Start does [07 §4].
			g.startMissionLoad()
		case "difficulty":
			g.missionDifficultyValue = cycleInt(g.missionDifficultyValue, 0, 2, 1)
			g.panel.setStatus("Difficulty", g.missionDifficultyValue)
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
			g.showRetailMessage(message)
			return
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// which is what actually builds the session [07 §4].
		g.startBattleLoad(g.setup.MapName)
		return
	}
	if key == "selectmap" {
		if len(g.maps) == 0 {
			g.showRetailMessage("There are no multiplayer maps to choose from")
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	g.openMenu(modeMenuMission)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
