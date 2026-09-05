package main

import "github.com/nanolathe/nanolathe/internal/session"

const (
	resourceMin  = 200
	resourceMax  = 10000
	resourceStep = 500
)

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

// setOpponentCount retains the retail slot initialization used by the
// authored Player%d gadgets when an installed GUI provides them.
func (g *gameShell) setOpponentCount(opponents int) {
	if opponents < 1 {
		opponents = 1
	}
	if opponents > session.SkirmishMaxPlayers-1 {
		opponents = session.SkirmishMaxPlayers - 1
	}
	g.setup.NumPlayers = opponents + 1
	g.retailControllersSet = true
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		switch {
		case i == 0:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
			g.retailControllers[i] = 1
		case i < g.setup.NumPlayers:
			g.setup.Players[i].Controller = 1
			g.retailControllers[i] = 2
		default:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
			g.retailControllers[i] = 0
		}
	}
	g.setup.ApplyDefaults()
	if g.selectedSlot >= g.setup.NumPlayers {
		g.selectedSlot = g.setup.NumPlayers - 1
	}
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

func lineOfSightName(enabled, losType int) string {
	if enabled == 0 {
		return "MAPPED"
	}
	if losType == 0 {
		return "FLAT"
	}
	return "ELEVATION"
}
