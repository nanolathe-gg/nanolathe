package main

import "github.com/nanolathe-gg/nanolathe/internal/session"

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
