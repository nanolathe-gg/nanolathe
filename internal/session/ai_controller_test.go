package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// controllerTestPlanner stands in for a think step that keeps a controller in
// the manager's Ext, the shape of the Modern AI planners.
type controllerTestPlanner struct{}

func (controllerTestPlanner) Step(*ai.Manager, uint32, *units.World, *economy.Service) {}

// controllerTestExt records whether the session stopped it.
type controllerTestExt struct{ closed int }

func (c *controllerTestExt) Close() { c.closed++ }

func controllerTestSet() RuleSet {
	set := ModernRuleSet()
	set.Name = "session-test-controller"
	set.Planner = controllerTestPlanner{}
	return set
}

// A bind that replaces the think step stops and drops the controller the
// previous step kept, so a switch back starts a fresh one instead of resuming
// a stale observation and its pending batch. Re-projecting the same set keeps
// it, and a manager with no bound step keeps a host-installed Ext.
func TestReplacingThePlannerReleasesItsController(t *testing.T) {
	s := &Session{Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.AI[0] = &ai.Manager{Player: 0}
	s.AI[2] = &ai.Manager{Player: 2}
	hostInstalled := &controllerTestExt{}
	s.AI[2].Ext = hostInstalled // bound below from nil: a host installed it
	s.BindRules(controllerTestSet())
	if s.AI[2].Ext != hostInstalled || hostInstalled.closed != 0 {
		t.Fatal("binding over an unbound manager dropped a host-installed controller")
	}
	controller := &controllerTestExt{}
	s.AI[0].Ext = controller
	s.RebindRules()
	if s.AI[0].Ext != controller || controller.closed != 0 {
		t.Fatal("re-projecting the same set released its controller")
	}
	s.BindRules(ModernRuleSet())
	if s.AI[0].Ext != nil || controller.closed != 1 {
		t.Fatalf("replacing the think step left Ext %v, closed %d times", s.AI[0].Ext, controller.closed)
	}
	if s.AI[2].Ext != nil || hostInstalled.closed != 1 {
		t.Fatal("replacing the think step kept the other player's controller")
	}
	s.BindRules(controllerTestSet())
	if s.AI[0].Ext != nil {
		t.Fatal("switching back resurrected a released controller")
	}
}

// Battle exit stops every controller, so an asynchronous worker never
// outlives its battle; a controller without Close is simply dropped.
func TestTeardownStopsEveryAIController(t *testing.T) {
	s := &Session{}
	stoppable := &controllerTestExt{}
	s.AI[1] = &ai.Manager{Player: 1, Ext: stoppable}
	s.AI[4] = &ai.Manager{Player: 4, Ext: struct{}{}}
	s.teardown(0)
	if stoppable.closed != 1 || s.AI[1].Ext != nil || s.AI[4].Ext != nil {
		t.Fatalf("teardown left controllers: closed %d, ext %v / %v", stoppable.closed, s.AI[1].Ext, s.AI[4].Ext)
	}
}
