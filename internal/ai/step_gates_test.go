package ai

import "testing"

// A replacement controller decides only where the retail step would run its
// tasks: a computer slot (controller 2) that is not Passive. The Survival
// attacker is a Passive computer slot whose units the wave director orders
// (docs/DESIGN_SURVIVAL.md §4.1); it keeps the engine upkeep but no brain may
// drive it [08 "Dispatch gates and order sinks"].
func TestStepGatesPassiveNeverDecides(t *testing.T) {
	for _, c := range []struct {
		ctrl             uint8
		passive          bool
		eligible, decide bool
	}{
		{ctrl: 2, eligible: true, decide: true},
		{ctrl: 2, passive: true, eligible: true, decide: false},
		{ctrl: 1, eligible: true, decide: false},
		{ctrl: 3, eligible: true, decide: false},
		{ctrl: 0, eligible: false, decide: false},
	} {
		m := &Manager{Player: 0, Passive: c.passive}
		eligible, decide := m.StepGates(runtimeEconomy(0, c.ctrl))
		if eligible != c.eligible || decide != c.decide {
			t.Errorf("controller %d passive %v: gates (%v, %v), want (%v, %v)", c.ctrl, c.passive, eligible, decide, c.eligible, c.decide)
		}
	}
}
