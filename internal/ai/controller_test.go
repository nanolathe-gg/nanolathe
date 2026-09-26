package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The controller words round-trip, ignore case and space, and nothing else
// parses; the zero value is Classic.
func TestControllerWords(t *testing.T) {
	if Controller(0) != ControllerClassic || ControllerClassic.String() != "classic" || ControllerModern.String() != "modern" {
		t.Fatal("the zero value or a word is wrong")
	}
	for word, want := range map[string]Controller{"classic": ControllerClassic, " Modern ": ControllerModern, "MODERN": ControllerModern} {
		if got, err := ParseController(word); err != nil || got != want {
			t.Fatalf("ParseController(%q) = %v, %v", word, got, err)
		}
	}
	for _, bad := range []string{"", "retail", "modern-ai", "1"} {
		if _, err := ParseController(bad); err == nil {
			t.Fatalf("ParseController(%q) was accepted", bad)
		}
	}
}

type answeringStep struct{ answer bool }

func (answeringStep) Step(*Manager, uint32, *units.World, *economy.Service) {}
func (a answeringStep) ControlsModernAI(*Manager) bool                      { return a.answer }

// Only a step that says it runs the Modern AI for a manager makes that
// manager's player a Modern AI player; the retail and Modern steps never do.
func TestModernAIDecidesAsksTheBoundStep(t *testing.T) {
	var none *Manager
	if none.ModernAIDecides() {
		t.Fatal("a nil manager decided")
	}
	for _, p := range []Planner{nil, RetailPlanner{}, ModernPlanner{}, answeringStep{false}} {
		if (&Manager{Planner: p}).ModernAIDecides() {
			t.Fatalf("%T answered Modern AI", p)
		}
	}
	if !(&Manager{Planner: answeringStep{true}}).ModernAIDecides() {
		t.Fatal("a Modern AI step was not recognized")
	}
}
