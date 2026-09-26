package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// The arena registers its research set itself, since mods/aikit no longer
// does: Modern rules, the host planner and full computer income.
func TestTheArenaRegistersItsRuleSet(t *testing.T) {
	set, ok := session.LookupRuleSet(aikitmod.ArenaSet)
	if !ok {
		t.Fatalf("%q is not registered in the arena", aikitmod.ArenaSet)
	}
	if set.Base != gameplay.Modern {
		t.Fatalf("arena set base %q, want Modern", set.Base)
	}
	if _, host := set.Planner.(aikit.HostPlanner); !host {
		t.Fatalf("arena set planner %T, want aikit.HostPlanner", set.Planner)
	}
	if _, full := set.ComputerIncome.(session.FullComputerIncome); !full {
		t.Fatalf("arena set income %T, want full computer income", set.ComputerIncome)
	}
}
