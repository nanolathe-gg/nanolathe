package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// attachSettings loads the persisted frontend preferences and applies them to
// the shell, then enables writing them back. The frontend reads preferences
// once during startup and rewrites the whole block when a screen commits a
// change [02 §3].
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
	// The display-option bits reach the presentation as soon as they are
	// read; retail's own loader installs them the same way [07 R-FE-01 §6].
	g.applyRetailVisualOptions(clPtr)
	applyGlowStrength(clPtr, g.display)
	applyGammaOption(clPtr, g.display.Gamma)
}

// applyGlowStrength hands the persisted glow strength to a client, whose
// executor sites copy it to the renderer before every frame
// (DESIGN_GPU_RENDERER §19.4). It is a host preference beside the glow switch
// and never reaches the simulation [I6].
func applyGlowStrength(cl *client.Client, d settings.Display) {
	if cl == nil {
		return
	}
	cl.SetGlowStrength(d.GlowStrength)
}

// applySettings installs a loaded block over the shell's default setup.
func (g *gameShell) applySettings(s settings.Settings) {
	s.Normalize()
	g.missionDifficultyValue = s.Difficulty
	// Scroll speed is the persisted scrollspeed byte [02 "Settings"] [07 §10] C2.
	// It is presentation-only and never touches sim [I6].
	g.scrollSpeed = s.ScrollSpeed
	// `damagebars` becomes bit 0 of the interface-flags word at settings load
	// [07 R-HUD-03 §7][03 R-FX-01 §6].
	applyDamageBarsSetting(s)
	// The display block is the options screen's `VISUALS` page: the size pair
	// the load transition reads and the option values the three two-stage
	// buttons drive [07 R-FE-01 §6][07 R-FE-01 §11].
	g.display = s.Display
	g.setPresentation(startupPresentation(g.opts, s.Presentation))
	g.setGameplay(startupGameplay(g.opts, s.Gameplay))
	g.gameplayFeatures = s.GameplayFeatures
	g.modSetting, g.mutatorSetting = s.Mod, s.Mutators
	g.modernAISetting = s.ModernAI
	g.controlsOffered = s.ControlsOffered
	g.builderOptions = s.BuilderOptions
	g.fullscreen = s.Fullscreen
	// The message-column ring configuration is the interface page's
	// `TXTSCROL`, `MAXLINES` and `UNITCHAT` controls plus `screenchat`, which
	// no screen edits [02 §3][07 R-CAM-01 §7].
	g.messages = s.Messages
	// The audio block, stored game speed, `Interface Type` word and digit-key
	// mux are persistent presentation preferences [03 R-AUD-01 §2]
	// [07 R-CAM-01 §7][07 R-CAM-01 §§4,5].
	g.audioPrefs = s.Audio
	// The retained presentation configuration accepts this before a PCM device
	// exists and applies it again when the platform installs one, so startup
	// ordering cannot discard a saved 3-D selection [03 R-AUD-01 §2][I6].
	applyRetailAudioOptions(g.audioPrefs)
	if g.audioOwner != nil && g.audioOwner.Music != nil {
		music := g.audioOwner.Music
		music.SetVolume(g.audioPrefs.MusicVol)
		music.SetEnabled(g.audioPrefs.MusicMode != 0)
		music.Configure(audio.PlayMode(g.audioPrefs.CDMode), music.DesiredCategory())
	}
	// `speechfx` and the two acknowledgement levels are voice-queue gates. A
	// service that already exists takes the loaded block now; one created later
	// takes it at construction [03 §8.3][03 R-AUD-01 §2].
	g.applyRetailVoiceGates()
	g.gameSpeed = s.GameSpeed
	g.interfaceType = s.InterfaceType
	g.switchAlt = s.SwitchAltEnabled()
	g.clockVisible = s.ClockEnabled()
	// The configured per-player unit limit rides on the setup record into
	// battle entry, where it sizes the unit pool [05 R-SHARE-01 §7]. No
	// screen edits it: retail reads it from the profile file, and the
	// skirmish lobby has no gadget for it [08 R-SKIR-01 §6].
	g.setup.UnitLimit = s.UnitLimit
	if g.opts.UnitLimit != 0 {
		g.setup.UnitLimit = g.opts.UnitLimit
	}

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
		// Only a row stored "classic" plays the Classic AI: a row without
		// the word, including every row of a file written before this
		// encoding, plays the Modern AI (user decision 2026-09-25).
		row.AI = ai.ControllerModern
		if p.AI == settings.PlayerAIClassic {
			row.AI = ai.ControllerClassic
		}
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
	// not later re-derive it from scratch. Retail's loader has no all-Open
	// fallback: rows are stored as read, and the SKIRMISH row build's test of
	// the shown rows is the only place a live pair is forced
	// [08 R-SKIR-01 §1] "Shown rows only".
	g.retailControllersSet = true

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
	// While the Survival screen is open its rows sit in g.setup; the file
	// records the skirmish rows set aside for it.
	setup, controllers := g.persistedSkirmishSetup(), g.persistedSkirmishControllers()
	s := settings.Settings{
		Version:     settings.FileVersion,
		Fullscreen:  g.fullscreen,
		Difficulty:  g.missionDifficultyValue,
		ScrollSpeed: g.scrollSpeed,
		// The whole block is rewritten from live state, so the interface
		// word's bit 0 is what the file records [07 R-HUD-03 §7].
		DamageBars: damageBarsSettingValue(),
		// Written back unchanged: nothing in the frontend edits it, so this
		// preserves whatever the file held rather than inventing a value
		// [08 R-SKIR-01 §6].
		UnitLimit: setup.UnitLimit,
		// The options root's `PREV` ("OK") is one of the save points that
		// rewrite the whole block; the value it saves is whatever the live
		// display record holds [07 R-FE-01 §6][07 R-FE-01 §11].
		Display:          g.display,
		Presentation:     g.presentation,
		Gameplay:         g.gameplay.Normalize(),
		GameplayFeatures: g.gameplayFeatures,
		BuilderOptions:   g.builderOptions,
		Mod:              g.modSetting,
		Mutators:         g.mutatorSetting,
		ModernAI:         g.modernAISetting,
		ControlsOffered:  g.controlsOffered,
		// The interface page's three message controls write into this block;
		// `screenchat` rides through unchanged [02 §3][07 R-CAM-01 §7].
		Messages: g.messages,
		// The sound, music and interface pages' remaining stores
		// [03 R-AUD-01 §2][07 R-CAM-01 §7][07 R-CAM-01 §5].
		Audio:         g.audioPrefs,
		GameSpeed:     g.gameSpeed,
		InterfaceType: g.interfaceType,
		SwitchAlt:     boolInt(g.switchAlt),
		Clock:         boolInt(g.clockVisible),
	}
	s.Skirmish = settings.Skirmish{
		Map:            setup.MapName,
		NumPlayers:     setup.NumPlayers,
		Difficulty:     setup.Difficulty,
		Location:       setup.Location,
		CommanderDeath: setup.CommanderDeath,
		Mapping:        setup.Mapping,
		LineOfSight:    setup.LineOfSight,
		LOSType:        setup.LOSType,
		Players:        make([]settings.Player, session.SkirmishMaxPlayers),
	}
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		row := setup.Players[i]
		s.Skirmish.Players[i] = settings.Player{
			Controller: controllers[i],
			Side:       row.Side,
			Color:      row.Color,
			AllyGroup:  row.AllyGroup,
			Metal:      row.Metal,
			Energy:     row.Energy,
		}
		// A Classic computer row says so; a Modern row, and a row that is
		// not a computer player, whose AI means nothing, store no word.
		if row.AI == ai.ControllerClassic && controllers[i] == 2 {
			s.Skirmish.Players[i].AI = settings.PlayerAIClassic
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
