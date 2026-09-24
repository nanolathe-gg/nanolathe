package main

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

const (
	resourceMin  = 200
	resourceMax  = 10000
	resourceStep = 500
)

// skirmishPlayerCountToken applies the hidden *III..*X selector to the
// skirmish window's unclaimed typed-key history [07 R-FE-02 §10]
// [08 R-SKIR-01 §1]. The star is Shift+8 on the
// original keyboard layout; the input adapter supplies the typed character.
func (g *gameShell) skirmishPlayerCountToken(token input.Token) {
	if token.Kind == input.TokenText && token.Rune >= 0 && token.Rune <= 0x7f {
		ch := byte(token.Rune)
		if ch >= 'a' && ch <= 'z' {
			ch -= 'a' - 'A'
		}
		g.skirmishKeys += string(ch)
	} else {
		// An intervening edit or non-ASCII key breaks the typed sequence.
		g.skirmishKeys += "\x00"
	}
	if len(g.skirmishKeys) > 15 {
		g.skirmishKeys = g.skirmishKeys[len(g.skirmishKeys)-15:]
	}
	for i, suffix := range [...]string{"*III", "*IV", "*V", "*VI", "*VII", "*VIII", "*IX", "*X"} {
		if !strings.HasSuffix(g.skirmishKeys, suffix) {
			continue
		}
		g.setup.NumPlayers = i + 3
		if g.selectedSlot >= g.setup.NumPlayers {
			g.selectedSlot = g.setup.NumPlayers - 1
		}
		g.saveSettings()
		// The row builder lays out all rows from the new count, so a new
		// runtime window is needed; the saved per-row choices stay in setup.
		g.openMenuWithTokenFlush(modeMenuSkirmish, false)
		g.playMenuCue("SkirmishCheat")
		// Retail leaves the prefix for *V, *VI and *VII so a longer
		// Roman numeral can be completed without starting over.
		if i < 2 || i > 4 {
			g.skirmishKeys = ""
		}
		return
	}
}

// newSkirmishMenuConfig is the retail setup data initialized before
// SKIRMISH.GUI is opened. The authored GUI is then extended by retail's
// runtime Player%d row builder; the renderer keeps those row states separate
// from this session compatibility representation [08 "Skirmish configuration"].
func newSkirmishMenuConfig(mapName string) session.SkirmishConfig {
	cfg := session.SkirmishConfig{MapName: mapName}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = session.SkirmishDefaultController
	for i := 1; i < cfg.NumPlayers && i < session.SkirmishMaxPlayers; i++ {
		cfg.Players[i].Controller = 1
	}
	return cfg
}

// cycleLineOfSight mirrors the retail three-state LineOfSight gadget:
// mapped terrain visible, elevation-agnostic LOS, then elevation-aware LOS.
func (g *gameShell) cycleLineOfSight(delta int) {
	state := 0
	if g.setup.LineOfSight == 0 {
		state = 2
	} else if g.setup.LOSType == 0 {
		state = 1
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
	// Retail's callback has this reachable branch when incrementing the
	// 200-resource floor [08 "Skirmish configuration"].
	if value == 700 {
		return 500
	}
	return value
}
