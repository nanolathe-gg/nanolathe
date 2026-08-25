package main

import (
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
)

// attachSettings loads the persisted frontend preferences and applies them to
// the shell, then enables writing them back. Retail does the read once during
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// process and rewritten whole.
//
// It is called only from the windowed entry point. newGameShell stays free of
// filesystem state so the screenshot path and the tests compose the same
// frontend from the same defaults every time.
func (g *gameShell) attachSettings() {
	loaded, err := settings.Load()
	if err != nil {
		// A missing file is not an error. Anything else — unreadable, corrupt,
		// a version we do not know — reports and starts from the defaults,
		// because refusing to launch over a preferences file is worse than
		// losing the preferences.
		fmt.Fprintf(os.Stderr, "nanolathe: %v (using defaults)\n", err)
	}
	g.applySettings(loaded)
	g.settingsWritable = true
}

// applySettings installs a loaded block over the shell's default setup.
func (g *gameShell) applySettings(s settings.Settings) {
	s.Normalize()
	g.missionDifficultyValue = s.Difficulty

	sk := s.Skirmish
	// ApplyDefaults has already run in newGameShell, so the six scalars below
	// overwrite defaults rather than being overwritten by them.
	g.setup.NumPlayers = sk.NumPlayers
	g.setup.Difficulty = sk.Difficulty
	g.setup.Location = sk.Location
	g.setup.CommanderDeath = sk.CommanderDeath
	g.setup.Mapping = sk.Mapping
	g.setup.LineOfSight = sk.LineOfSight
	g.setup.LOSType = sk.LOSType

	for i := 0; i < session.SkirmishMaxPlayers && i < len(sk.Players); i++ {
		p := sk.Players[i]
		row := &g.setup.Players[i]
		row.Side = p.Side
		row.Color = p.Color
		row.AllyGroup = p.AllyGroup
		row.Metal = p.Metal
		row.Energy = p.Energy
		g.retailControllers[i] = p.Controller
		// Keep the session-side compatibility field consistent with the row,
		// the way cycleRetailController does when the value is changed live.
		if p.Controller == 1 {
			row.Controller = session.SkirmishDefaultController
		} else {
			row.Controller = 1
		}
	}
	// A stored block always describes the row array in full, so the shell must
	// not later re-derive it from scratch.
	g.retailControllersSet = true
	if !g.hasLiveController() {
		// Every row open would leave Start permanently refused. Fall back to
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		g.retailControllersSet = false
		g.ensureRetailSkirmishControllers()
	}

	// The stored map only wins if it is still installed; a map removed from the
	// install falls back to the first of the enumerated list.
	if sk.Map != "" {
		for i, name := range g.maps {
			if name == sk.Map {
				g.setup.MapName = name
				g.mapIdx = i
				break
			}
		}
	}
	g.syncMapIndex()
}

// hasLiveController reports whether any row is a human or a computer player.
func (g *gameShell) hasLiveController() bool {
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		if g.retailControllers[i] != 0 {
			return true
		}
	}
	return false
}

// syncMapIndex points the SELMAP.GUI list at whatever map the setup names, so
// reopening the map screen highlights the current selection rather than the
// first entry.
func (g *gameShell) syncMapIndex() {
	for i, name := range g.maps {
		if name == g.setup.MapName {
			g.mapIdx = i
			return
		}
	}
}

// captureSettings reads the shell's live frontend state back into the
// persisted block.
func (g *gameShell) captureSettings() settings.Settings {
	s := settings.Settings{Version: settings.FileVersion, Difficulty: g.missionDifficultyValue}
	s.Skirmish = settings.Skirmish{
		Map:            g.setup.MapName,
		NumPlayers:     g.setup.NumPlayers,
		Difficulty:     g.setup.Difficulty,
		Location:       g.setup.Location,
		CommanderDeath: g.setup.CommanderDeath,
		Mapping:        g.setup.Mapping,
		LineOfSight:    g.setup.LineOfSight,
		LOSType:        g.setup.LOSType,
		Players:        make([]settings.Player, session.SkirmishMaxPlayers),
	}
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		row := g.setup.Players[i]
		s.Skirmish.Players[i] = settings.Player{
			Controller: g.retailControllers[i],
			Side:       row.Side,
			Color:      row.Color,
			AllyGroup:  row.AllyGroup,
			Metal:      row.Metal,
			Energy:     row.Energy,
		}
	}
	return s
}

// saveSettings writes the whole block back. A failed write is reported and
// otherwise ignored: losing the preferences must never interrupt a game.
func (g *gameShell) saveSettings() {
	if g == nil || !g.settingsWritable {
		return
	}
	if err := g.captureSettings().Save(); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}
