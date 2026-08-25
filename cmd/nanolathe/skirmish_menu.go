package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
)

// The retail skirmish panel exposes these controls in this order. The
// decompiled callback names are Player, Side, Allies, Metal, Energy, Color,
// CommanderDeath, StartLocation, Mapping, LineOfSight, SelectMap and
// Difficulty [08 "Skirmish configuration"] [fmt gui].
const (
	setupControlOpponents = iota
	setupControlDifficulty
	setupControlLocation
	setupControlCommanderDeath
	setupControlMapping
	setupControlLineOfSight
	setupControlBack
	setupControlStart
	setupControlCount
)

const (
	menuButtonW  = 96
	menuButtonH  = 20
	resourceMin  = 200   // retail decrement floor [08 "Skirmish configuration"]
	resourceMax  = 10000 // retail increment cap [08 "Skirmish configuration"]
	resourceStep = 500   // retail resource button step [08 "Skirmish configuration"]
)

func newSkirmishMenuConfig(mapName string) session.SkirmishConfig {
	cfg := session.SkirmishConfig{MapName: mapName}
	cfg.ApplyDefaults()
	// Retail opens the single-player skirmish room with one human and the
	// remaining default slots computer-controlled [08 "Lobby behavior"].
	cfg.Players[0].Controller = session.SkirmishDefaultController
	for i := 1; i < cfg.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
		cfg.Players[i].Controller = 1 // computer slot; session maps it to AI state
	}
	return cfg
}

func (g *gameShell) drawSingleMenu(c *client.Client, a *menuAssets) {
	c.UIFrameRect(152, 100, 336, 250, 250)
	c.UIText(g.fnt, "SINGLE PLAYER", 264, 116, 250)
	c.UIText(g.fnt, "Choose a game", 272, 140, 250)

	campaign := gui.Rect{X: 272, Y: 176, W: menuButtonW, H: menuButtonH}
	skirmish := gui.Rect{X: 272, Y: 216, W: menuButtonW, H: menuButtonH}
	back := gui.Rect{X: 272, Y: 296, W: menuButtonW, H: menuButtonH}
	// Campaign selection is deliberately visible but inert until the campaign
	// mission-list flow is wired. It is never silently treated as skirmish.
	g.drawPickerButton(c, a, "CAMPAIGN", campaign, false)
	g.drawPickerButton(c, a, "SKIRMISH", skirmish, g.clickEdge && inRect(g.clickX, g.clickY, skirmish))
	g.drawPickerButton(c, a, "BACK", back, g.clickEdge && inRect(g.clickX, g.clickY, back))
	if g.menuMessage != "" {
		c.UIText(g.fnt, g.menuMessage, 176, 330, 250)
	}
	if !g.clickEdge {
		return
	}
	switch {
	case inRect(g.clickX, g.clickY, skirmish):
		g.clickEdge = false
		g.menuMessage = ""
		g.mode = modeMenuMap
	case inRect(g.clickX, g.clickY, campaign):
		g.clickEdge = false
		g.menuMessage = "Campaign mission selection is not loaded yet"
	case inRect(g.clickX, g.clickY, back):
		g.clickEdge = false
		g.menuMessage = ""
		g.mode = modeMenuMain
	}
}

func (g *gameShell) setupInput(kbd *client.KeyboardState) {
	if kbd == nil {
		return
	}
	if kbd.KeyDown(input.KeyTab) {
		g.setupFocus = (g.setupFocus + 1) % setupControlCount
	}
	if kbd.KeyDown(input.KeyUp) {
		g.setupFocus = (g.setupFocus - 1 + setupControlCount) % setupControlCount
	}
	if kbd.KeyDown(input.KeyDown) {
		g.setupFocus = (g.setupFocus + 1) % setupControlCount
	}
	if kbd.KeyDown(input.KeyLeft) {
		g.adjustSetupControl(-1)
	}
	if kbd.KeyDown(input.KeyRight) {
		g.adjustSetupControl(1)
	}
	if kbd.KeyDown(input.KeyEnter) || kbd.KeyDown(input.KeySpace) {
		switch g.setupFocus {
		case setupControlBack:
			g.mode = modeMenuMap
		case setupControlStart:
			g.launchSkirmish()
		default:
			g.adjustSetupControl(1)
		}
	}
}

func (g *gameShell) adjustSetupControl(delta int) {
	switch g.setupFocus {
	case setupControlOpponents:
		g.setOpponentCount((g.setup.NumPlayers - 1) + delta)
	case setupControlDifficulty:
		g.setup.Difficulty = cycleInt(g.setup.Difficulty, 0, 2, delta)
	case setupControlLocation:
		g.setup.Location = cycleInt(g.setup.Location, 0, 1, delta)
	case setupControlCommanderDeath:
		g.setup.CommanderDeath = cycleInt(g.setup.CommanderDeath, 0, 1, delta)
	case setupControlMapping:
		g.setup.Mapping = cycleInt(g.setup.Mapping, 0, 1, delta)
	case setupControlLineOfSight:
		g.cycleLineOfSight(delta)
	}
}

// cycleLineOfSight mirrors the single retail LineOfSight gadget. It is a
// three-state control, not two independent toggles: elevation-aware LOS,
// elevation-agnostic LOS, then all mapped terrain visible [08 "Skirmish
// configuration"] [fmt gui].
func (g *gameShell) cycleLineOfSight(delta int) {
	state := 0 // enabled, elevations affect LOS
	if g.setup.LineOfSight == 0 {
		state = 2 // all mapped terrain visible
	} else if g.setup.LOSType == 0 {
		state = 1 // enabled, elevations do not affect LOS
	}
	state = cycleInt(state, 0, 2, delta)
	switch state {
	case 0:
		g.setup.LineOfSight = 1
		g.setup.LOSType = 1
	case 1:
		g.setup.LineOfSight = 1
		g.setup.LOSType = 0
	case 2:
		g.setup.LineOfSight = 0
		g.setup.LOSType = 1
	}
}

func cycleInt(value, min, max, delta int) int {
	if max < min {
		return value
	}
	n := max - min + 1
	value = (value - min + delta) % n
	if value < 0 {
		value += n
	}
	return min + value
}

func (g *gameShell) setOpponentCount(opponents int) {
	if opponents < 1 {
		opponents = 1
	}
	if opponents > session.SkirmishMaxPlayers-1 {
		opponents = session.SkirmishMaxPlayers - 1
	}
	g.setup.NumPlayers = opponents + 1
	// The retail single-player room always owns slot 0. Its opponent-count
	// control adds/removes computer slots in ascending order.
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		switch {
		case i == 0:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
		case i < g.setup.NumPlayers:
			g.setup.Players[i].Controller = 1
		default:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
		}
	}
	g.setup.ApplyDefaults()
	if g.selectedSlot >= g.setup.NumPlayers {
		g.selectedSlot = g.setup.NumPlayers - 1
	}
}

func (g *gameShell) drawSkirmishSetup(c *client.Client, a *menuAssets) {
	c.UIFrameRect(28, 28, 584, 408, 250)
	c.UIText(g.fnt, "SKIRMISH SETUP", 264, 38, 250)
	c.UIText(g.fnt, "MAP", 48, 58, 250)
	c.UIText(g.fnt, g.setup.MapName, 84, 58, 255)
	mapRect := gui.Rect{X: 480, Y: 50, W: menuButtonW, H: menuButtonH}
	g.drawPickerButton(c, a, "CHANGE MAP", mapRect, g.clickEdge && inRect(g.clickX, g.clickY, mapRect))

	c.UIFrameRect(42, 82, 350, 282, 250)
	c.UIText(g.fnt, "PLAYER", 52, 90, 250)
	c.UIText(g.fnt, "SIDE", 142, 90, 250)
	c.UIText(g.fnt, "ALLY", 198, 90, 250)
	c.UIText(g.fnt, "METAL", 244, 90, 250)
	c.UIText(g.fnt, "ENERGY", 314, 90, 250)
	active := g.setup.NumPlayers
	if active < 0 {
		active = 0
	}
	if active > session.SkirmishMaxPlayers {
		active = session.SkirmishMaxPlayers
	}
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		y := int32(106 + i*22)
		row := gui.Rect{X: 48, Y: y - 2, W: 336, H: 20}
		if i == g.selectedSlot {
			c.UIFillRect(int(row.X), int(row.Y), int(row.W), int(row.H), 30)
		}
		if i >= active {
			c.UIText(g.fnt, fmt.Sprintf("%d  OPEN", i+1), 54, int(y), 120)
			continue
		}
		p := g.setup.Players[i]
		controller := "HUMAN"
		if p.Controller != session.SkirmishDefaultController {
			controller = "COMPUTER"
		}
		c.UIText(g.fnt, fmt.Sprintf("%d %-8s", i+1, controller), 54, int(y), 255)
		c.UIText(g.fnt, sideName(p.Side), 142, int(y), 255)
		c.UIText(g.fnt, fmt.Sprintf("%d", p.AllyGroup), 204, int(y), 255)
		c.UIText(g.fnt, fmt.Sprintf("%d", p.Metal), 244, int(y), 255)
		c.UIText(g.fnt, fmt.Sprintf("%d", p.Energy), 314, int(y), 255)
		if g.clickEdge && inRect(g.clickX, g.clickY, row) {
			g.selectedSlot = i
			g.clickEdge = false
		}
	}

	c.UIFrameRect(404, 82, 194, 282, 250)
	c.UIText(g.fnt, "ROUND SETTINGS", 424, 90, 250)
	setupRows := []struct {
		label string
		value string
	}{
		{"OPPONENTS", fmt.Sprintf("%d", g.setup.NumPlayers-1)},
		{"DIFFICULTY", difficultyName(g.setup.Difficulty)},
		{"START LOCATION", locationName(g.setup.Location)},
		{"COMMANDER DEATH", commanderDeathName(g.setup.CommanderDeath)},
		{"MAPPING", mappingName(g.setup.Mapping)},
		{"LINE OF SIGHT", lineOfSightName(g.setup.LineOfSight, g.setup.LOSType)},
	}
	for i, row := range setupRows {
		control := i
		y := int32(110 + i*24)
		r := gui.Rect{X: 414, Y: y - 2, W: 174, H: 20}
		if g.setupFocus == control {
			c.UIFillRect(int(r.X), int(r.Y), int(r.W), int(r.H), 30)
		}
		c.UIText(g.fnt, row.label, 418, int(y), 250)
		c.UIText(g.fnt, row.value, 532, int(y), 255)
		c.UIText(g.fnt, "<", 506, int(y), 255)
		c.UIText(g.fnt, ">", 584, int(y), 255)
		if g.clickEdge && inRect(g.clickX, g.clickY, r) {
			g.setupFocus = control
			g.adjustSetupControl(1)
			g.clickEdge = false
		}
	}

	g.drawSelectedPlayer(c, active)

	back := gui.Rect{X: 116, Y: 408, W: menuButtonW, H: menuButtonH}
	start := gui.Rect{X: 428, Y: 408, W: menuButtonW, H: menuButtonH}
	g.drawPickerButton(c, a, "BACK", back, g.setupFocus == setupControlBack && g.clickEdge && inRect(g.clickX, g.clickY, back))
	g.drawPickerButton(c, a, "START", start, g.setupFocus == setupControlStart && g.clickEdge && inRect(g.clickX, g.clickY, start))
	if g.clickEdge && inRect(g.clickX, g.clickY, back) {
		g.clickEdge = false
		g.mode = modeMenuMap
		return
	}
	if g.clickEdge && inRect(g.clickX, g.clickY, start) {
		g.clickEdge = false
		g.launchSkirmish()
		return
	}
	if g.menuMessage != "" {
		c.UIText(g.fnt, g.menuMessage, 42, 386, 250)
	}
	if g.clickEdge && inRect(g.clickX, g.clickY, mapRect) {
		g.clickEdge = false
		g.mode = modeMenuMap
	}
}

func (g *gameShell) drawSelectedPlayer(c *client.Client, active int) {
	if active <= 0 {
		return
	}
	if g.selectedSlot < 0 {
		g.selectedSlot = 0
	}
	if g.selectedSlot >= active {
		g.selectedSlot = active - 1
	}
	p := &g.setup.Players[g.selectedSlot]
	c.UIText(g.fnt, fmt.Sprintf("SLOT %d", g.selectedSlot+1), 418, 284, 250)
	c.UIText(g.fnt, fmt.Sprintf("SIDE %s", sideName(p.Side)), 418, 302, 255)
	c.UIText(g.fnt, fmt.Sprintf("ALLY %d", p.AllyGroup), 418, 320, 255)
	c.UIText(g.fnt, fmt.Sprintf("COLOR %d", p.Color), 512, 302, 255)
	metalMinus := gui.Rect{X: 418, Y: 338, W: 24, H: 18}
	metalPlus := gui.Rect{X: 568, Y: 338, W: 24, H: 18}
	energyMinus := gui.Rect{X: 418, Y: 358, W: 24, H: 18}
	energyPlus := gui.Rect{X: 568, Y: 358, W: 24, H: 18}
	c.UIText(g.fnt, fmt.Sprintf("METAL %d", p.Metal), 454, 340, 255)
	c.UIText(g.fnt, fmt.Sprintf("ENERGY %d", p.Energy), 454, 360, 255)
	for _, b := range []struct {
		r     gui.Rect
		label string
	}{
		{metalMinus, "-"}, {metalPlus, "+"}, {energyMinus, "-"}, {energyPlus, "+"},
	} {
		c.UIFrameRect(int(b.r.X), int(b.r.Y), int(b.r.W), int(b.r.H), 250)
		c.UIText(g.fnt, b.label, int(b.r.X)+8, int(b.r.Y)+2, 255)
	}
	if !g.clickEdge {
		return
	}
	switch {
	case inRect(g.clickX, g.clickY, metalMinus):
		p.Metal = decreaseResource(p.Metal)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, metalPlus):
		p.Metal = increaseResource(p.Metal)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, energyMinus):
		p.Energy = decreaseResource(p.Energy)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, energyPlus):
		p.Energy = increaseResource(p.Energy)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, gui.Rect{X: 414, Y: 296, W: 86, H: 20}):
		p.Side = cycleInt(p.Side, 0, 1, 1)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, gui.Rect{X: 414, Y: 314, W: 86, H: 20}):
		p.AllyGroup = cycleInt(p.AllyGroup, 0, 5, 1)
		g.clickEdge = false
	case inRect(g.clickX, g.clickY, gui.Rect{X: 508, Y: 296, W: 84, H: 20}):
		p.Color = nextPlayerColor(g.setup, g.selectedSlot, 1)
		g.clickEdge = false
	}
}

func clampResource(value int) int {
	if value < resourceMin {
		return resourceMin
	}
	if value > resourceMax {
		return resourceMax
	}
	return value
}

func decreaseResource(value int) int {
	value -= resourceStep
	if value < resourceMin {
		return resourceMin
	}
	return value
}

func increaseResource(value int) int {
	value += resourceStep
	if value > resourceMax-1 {
		value = resourceMax
	}
	// This seemingly odd 700→500 branch is present in the retail callback's
	// increment path [08 "Skirmish configuration"] and is reachable from the
	// 200-resource floor.
	if value == 700 {
		return 500
	}
	return value
}

func nextPlayerColor(cfg session.SkirmishConfig, slot, delta int) int {
	used := [session.SkirmishMaxPlayers]bool{}
	for i := 0; i < cfg.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
		if i != slot && cfg.Players[i].Color >= 0 && cfg.Players[i].Color < len(used) {
			used[cfg.Players[i].Color] = true
		}
	}
	for n := 0; n < len(used); n++ {
		candidate := cycleInt(cfg.Players[slot].Color, 0, len(used)-1, delta*(n+1))
		if !used[candidate] {
			return candidate
		}
	}
	return cfg.Players[slot].Color
}

func (g *gameShell) launchSkirmish() {
	if g.setup.MapName == "" || g.mapIdx < 0 || g.mapIdx >= len(g.maps) {
		g.menuMessage = "Select a skirmish map first"
		return
	}
	name := g.setup.MapName
	if err := g.startBattle(name); err != nil {
		g.menuMessage = "Cannot start: " + compactMenuError(err.Error())
		return
	}
	g.menuMessage = ""
}

func compactMenuError(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 72 {
		return s[:72]
	}
	return s
}

func sideName(side int) string {
	if side&1 == 1 {
		return "CORE"
	}
	return "ARM"
}

func difficultyName(value int) string {
	switch value {
	case 0:
		return "EASY"
	case 2:
		return "HARD"
	default:
		return "MEDIUM"
	}
}

func locationName(value int) string {
	if value == 0 {
		return "RANDOM"
	}
	return "FIXED"
}

func commanderDeathName(value int) string {
	if value == 0 {
		return "CONTINUE"
	}
	return "ENDS"
}

func mappingName(value int) string {
	if value == 0 {
		return "VISIBLE"
	}
	return "EXPLORE"
}

func lineOfSightName(enabled, losType int) string {
	if enabled == 0 {
		return "MAPPED"
	}
	if losType == 0 {
		return "FLAT"
	}
	return "ELEVATION"
}
