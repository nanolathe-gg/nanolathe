package main

// The Survival front end (docs/DESIGN_SURVIVAL.md §9): a Survival button on
// SINGLE, cloned from the authored Skirmish button into the free slot below
// Load Game, and a Survival configuration of SKIRMISH.GUI. The configuration
// swaps its own rows into the shared setup state while the screen is open, so
// every existing skirmish control handler serves it unchanged, and swaps the
// skirmish rows back on the way out. Nothing retail is copied; the new
// controls are clones of authored records with new captions, the pattern of
// the Nanolathe options categories (DESIGN_INTERFACE_HUD_INPUT §3.4.1).

import (
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
)

const (
	survivalButton     = "Survival"
	survivalPaceButton = "SurvivalPace"
	survivalAirButton  = "SurvivalAir"
	survivalSeaButton  = "SurvivalNaval"
	survivalSource     = "NANOLATHE_SURVIVAL"
	// survivalRows is the human plus the two buddy rows.
	survivalRows = 1 + session.SurvivalMaxBuddies
)

// survivalMenuState is the Survival screen's own setup, kept apart from the
// skirmish rows so neither screen disturbs the other.
type survivalMenuState struct {
	initialized bool
	setup       session.SkirmishConfig
	controllers [session.SkirmishMaxPlayers]int
	pace        survival.Pace
	noAir       bool
	noNaval     bool

	// The skirmish state set aside while the Survival screen is open.
	stashSetup          session.SkirmishConfig
	stashControllers    [session.SkirmishMaxPlayers]int
	stashControllersSet bool
}

// addSurvivalButton clones SINGLE's Skirmish button one authored pitch below
// Load Game, the free slot above Previous Menu.
func addSurvivalButton(window *gui.Window) {
	skirmish, options, load := window.GadgetIndex("Skirmish"), window.GadgetIndex("Options"), window.GadgetIndex("LoadGame")
	if skirmish < 0 || options < 0 || load < 0 || window.GadgetIndex(survivalButton) >= 0 {
		return
	}
	button := window.Gadgets[skirmish]
	pitch := window.Gadgets[options].Rect.Y - button.Rect.Y
	button.Rect.Y = window.Gadgets[load].Rect.Y + pitch
	button.Name, button.SourceName = survivalButton, survivalSource
	button.Art, button.Text, button.QuickKey = "", "Survival", 0
	button.Help = "Hold out with up to two allies against endless waves."
	window.Gadgets = append(window.Gadgets, button)
}

// applySurvivalLayout turns a fresh SKIRMISH.GUI clone into the Survival
// setup. Wave Pace takes the start-location control's framed slot and label;
// Air Waves and Naval Waves sit under the player box. Each is a clone of an
// authored control of the same shape with its own caption.
func applySurvivalLayout(window *gui.Window) {
	start, mapping, difficulty := window.GadgetIndex("StartLocation"), window.GadgetIndex("Mapping"), window.GadgetIndex("Difficulty")
	label := -1
	for i := range window.Gadgets {
		if window.Gadgets[i].Kind == gui.KindLabel && window.Gadgets[i].Text == "Location" {
			label = i
		}
	}
	if start < 0 || mapping < 0 || difficulty < 0 || label < 0 {
		return
	}
	clone := func(template gui.Gadget, name, stages string, n uint8, help string) gui.Gadget {
		b := template
		b.Name, b.SourceName, b.Art = name, survivalSource, template.Name
		b.Text, b.Labels, b.Stages, b.Help = stages, strings.Split(stages, "|"), n, help
		return b
	}
	pace := clone(window.Gadgets[difficulty], survivalPaceButton, "Normal|Relaxed|Relentless", 3, "How quickly the waves grow.")
	pace.Rect = window.Gadgets[start].Rect
	window.Gadgets[start].Active = 0
	window.Gadgets[label].Text = "Wave Pace"
	window.Gadgets = append(window.Gadgets, pace)
	const x0, pitch, labelY, buttonY = 45, 130, 290, 306
	for col, c := range []struct{ caption, name, help string }{
		{"Air Waves", survivalAirButton, "Whether waves may be airborne."},
		{"Naval Waves", survivalSeaButton, "Whether waves may come by sea."},
	} {
		l := window.Gadgets[label]
		l.Rect.X, l.Rect.Y = int32(x0+col*pitch), labelY
		l.Text, l.SourceName = c.caption, survivalSource
		b := clone(window.Gadgets[mapping], c.name, "On|Off", 2, c.help)
		b.Rect.X, b.Rect.Y = int32(x0+col*pitch), buttonY
		window.Gadgets = append(window.Gadgets, l, b)
	}
}

// openSurvivalMenu opens SKIRMISH.GUI in its Survival configuration.
func (g *gameShell) openSurvivalMenu() {
	st := &g.survival
	if !st.initialized {
		st.setup = g.setup
		st.setup.NumPlayers = survivalRows
		for i := range st.setup.Players {
			st.setup.Players[i] = session.SkirmishPlayer{}
		}
		for i := 0; i < survivalRows; i++ {
			st.setup.Players[i] = session.SkirmishPlayer{
				Side: i & 1, Color: []int{0, 2, 3}[i], AllyGroup: 2,
				Metal: session.SkirmishDefaultMetal, Energy: session.SkirmishDefaultEnergy,
				Controller: 1,
			}
		}
		if g.setup.NumPlayers > 0 {
			st.setup.Players[0].Side = g.setup.Players[0].Side
			st.setup.Players[0].Color = g.setup.Players[0].Color
		}
		st.setup.Players[0].Controller = session.SkirmishDefaultController
		st.controllers = [session.SkirmishMaxPlayers]int{1}
		st.initialized = true
	}
	if !g.survivalMenu {
		st.stashSetup, st.stashControllers, st.stashControllersSet = g.setup, g.retailControllers, g.retailControllersSet
		// The map follows the skirmish selection until Survival picks its own.
		if st.setup.MapName == "" {
			st.setup.MapName = g.setup.MapName
		}
		st.setup.UnitLimit = g.setup.UnitLimit
		g.setup, g.retailControllers, g.retailControllersSet = st.setup, st.controllers, true
		g.survivalMenu = true
	}
	g.openMenu(modeMenuSkirmish)
}

// closeSurvivalMenu keeps the Survival rows and restores the skirmish ones.
func (g *gameShell) closeSurvivalMenu() {
	if !g.survivalMenu {
		return
	}
	st := &g.survival
	st.setup, st.controllers = g.setup, g.retailControllers
	g.setup, g.retailControllers, g.retailControllersSet = st.stashSetup, st.stashControllers, st.stashControllersSet
	g.survivalMenu = false
}

// persistedSkirmishSetup is the skirmish setup the settings file records,
// which is the set-aside one while the Survival screen is open.
func (g *gameShell) persistedSkirmishSetup() session.SkirmishConfig {
	if g.survivalMenu {
		return g.survival.stashSetup
	}
	return g.setup
}

// persistedSkirmishControllers is persistedSkirmishSetup's controller row.
func (g *gameShell) persistedSkirmishControllers() [session.SkirmishMaxPlayers]int {
	if g.survivalMenu {
		return g.survival.stashControllers
	}
	return g.retailControllers
}

// refreshSurvivalPanel runs after the skirmish refresh on the Survival
// screen: the team is fixed, so the allegiance icons go, and the wave
// controls show their stages.
func (g *gameShell) refreshSurvivalPanel() {
	p := g.activePanel()
	if p == nil || !g.survivalMenu {
		return
	}
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		p.SetActive("Allies"+strconv.Itoa(i), false)
	}
	p.SetHelp("Player0", "You. Your allies share your sight and income.")
	for i := 1; i < survivalRows; i++ {
		p.SetHelp("Player"+strconv.Itoa(i), "Click to add or remove a computer ally.")
	}
	st := &g.survival
	p.SetStageAt(p.Index(survivalPaceButton), clampMenuStage(int(st.pace), 3))
	p.SetStageAt(p.Index(survivalAirButton), boolInt(st.noAir))
	p.SetStageAt(p.Index(survivalSeaButton), boolInt(st.noNaval))
}

// activateSurvivalGadget handles the Survival screen's own controls and the
// row controls it changes. It reports whether it consumed the name.
func (g *gameShell) activateSurvivalGadget(name string) bool {
	st := &g.survival
	switch name {
	case "PrevMenu":
		g.closeSurvivalMenu()
		g.saveSettings()
		g.openMenu(modeMenuSingle)
		return true
	case "Start":
		g.startSurvivalBattle()
		return true
	case survivalPaceButton:
		st.pace = survival.Pace((int(st.pace) + 1) % 3)
	case survivalAirButton:
		st.noAir = !st.noAir
	case survivalSeaButton:
		st.noNaval = !st.noNaval
	default:
		slot, kind, ok := dynamicSlot(name)
		if !ok {
			return false
		}
		switch kind {
		case "Player":
			// Row 0 is always the human; a buddy row is Open or Computer.
			if slot > 0 && slot < survivalRows {
				if g.retailControllers[slot] == 0 {
					g.retailControllers[slot] = 2
					g.resolveRetailColorConflict(slot)
				} else {
					g.retailControllers[slot] = 0
				}
			}
		case "Allies":
		default:
			return false
		}
	}
	g.refreshRetailPanel()
	return true
}

// survivalStartError is the Survival screen's start gate: the map must load.
// Start positions and opponents do not matter here.
func (g *gameShell) survivalStartError() string {
	if strings.TrimSpace(g.setup.MapName) == "" {
		return "The terrain for the selected map does not exist."
	}
	d := g.mapDataFor(g.setup.MapName)
	if d == nil || d.ota == nil || d.tnt == nil {
		return "The terrain for the selected map does not exist."
	}
	return ""
}

// survivalConfig converts the Survival rows to a battle setup.
func (g *gameShell) survivalConfig() session.SkirmishConfig {
	players := []session.SkirmishPlayer{g.setup.Players[0]}
	for i := 1; i < survivalRows; i++ {
		if g.retailControllers[i] != 0 {
			players = append(players, g.setup.Players[i])
		}
	}
	st := &g.survival
	cfg := session.SurvivalConfigFor(g.setup.MapName, players, session.SurvivalOptions{
		Pace: st.pace, NoAir: st.noAir, NoNaval: st.noNaval,
	})
	cfg.CommanderDeath = g.setup.CommanderDeath
	cfg.Mapping = g.setup.Mapping
	cfg.LineOfSight = g.setup.LineOfSight
	cfg.LOSType = g.setup.LOSType
	cfg.Difficulty = g.setup.Difficulty
	cfg.UnitLimit = g.setup.UnitLimit
	return cfg
}

func (g *gameShell) startSurvivalBattle() {
	if message := g.survivalStartError(); message != "" {
		reportRetailMessageError(g.showRetailMessage(message))
		return
	}
	cfg := g.survivalConfig()
	g.closeSurvivalMenu()
	g.saveSettings()
	request, err := skirmishBattleRequest(g.opts, g.cs, cfg, headless.ScenarioSurvival, nil, newBattleSeedSource(g.opts))
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	g.lastBattleSurvival = true
	g.beginFreshBattleLoad(cfg.MapName, modeMenuSkirmish, request, nil)
}

// openSetupAfterBattle returns to the setup screen the battle came from.
func (g *gameShell) openSetupAfterBattle() {
	if g.lastBattleSurvival {
		g.openSurvivalMenu()
		return
	}
	g.openMenu(modeMenuSkirmish)
}
