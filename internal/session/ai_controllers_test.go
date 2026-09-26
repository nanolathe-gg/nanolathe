package session

import (
	"encoding/json"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// testModernAIStep stands in for the Modern AI controller in this package's
// test build, which cannot link mods/aikit (only a command imports the mod
// list). It runs the retail step and says it is the Modern AI, which is all
// the per-player selection and the order policy ask of it.
type testModernAIStep struct{}

func (testModernAIStep) Step(m *ai.Manager, tick uint32, w *units.World, econ *economy.Service) {
	ai.RetailPlanner{}.Step(m, tick, w, econ)
}

func (testModernAIStep) ControlsModernAI(*ai.Manager) bool { return true }

func init() { RegisterModernAI(testModernAIStep{}) }

// statefulModernAIStep is a think step that is not zero size: it counts the
// players it has answered for.
type statefulModernAIStep struct {
	testModernAIStep
	n *int
}

func (s statefulModernAIStep) ControlsModernAI(*ai.Manager) bool {
	*s.n++
	return true
}

// The Modern AI's think step is installed once, and a nil step, a second one
// or one holding state is a build mistake the registration refuses. It is
// not a rule set: no gameplay word selects it and no listing names it
// (docs/DESIGN_GAMEPLAY_RULES.md "The Modern AI controller").
func TestTheModernAIStepIsInstalledOnceAndIsNotARuleSet(t *testing.T) {
	for name, step := range map[string]ai.ModernAIStep{
		"a nil step":      nil,
		"a second step":   testModernAIStep{},
		"a stateful step": statefulModernAIStep{n: new(int)},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s was installed", name)
				}
			}()
			RegisterModernAI(step)
		}()
	}
	if _, ok := registeredModernAI().(testModernAIStep); !ok {
		t.Fatalf("the installed step is %T after the refusals", registeredModernAI())
	}
	for _, name := range RuleSetNames() {
		if set, _ := LookupRuleSet(name); set.Planner == ai.Planner(testModernAIStep{}) {
			t.Fatalf("rule set %q binds the Modern AI's step", name)
		}
	}
	if _, err := gameplay.Parse("modern-ai"); err == nil {
		t.Fatal("the retired modern-ai word is selectable")
	}
}

// closeCountingExt is a controller kept in Ext that counts its stops.
type closeCountingExt struct{ closed int }

func (c *closeCountingExt) Close() { c.closed++ }

func controllerTestSession() *Session {
	s := &Session{Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}, Econ: &economy.Service{}}
	for _, p := range []uint8{0, 1, 2} {
		s.AI[p] = &ai.Manager{Player: p}
		s.Econ.Players[p].Exists = true
		s.Econ.Players[p].ControllerState = 2
	}
	s.Econ.Players[0].ControllerState = 1
	s.BindRules(StrictRuleSet())
	return s
}

// A computer player marked Modern takes the Modern AI controller's step in
// every reserved set, Strict 3.1 included, and keeps its controller across a
// rule-set switch; a Classic player takes each set's own step
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player", "Per-player
// selection").
func TestAMarkedPlayerKeepsTheModernAIInEveryRuleSet(t *testing.T) {
	s := controllerTestSession()
	modern, classic := s.AI[1], s.AI[2]
	if err := s.setAIController(modern, ai.ControllerModern); err != nil {
		t.Fatal(err)
	}
	ext := &closeCountingExt{}
	modern.Ext = ext
	for _, set := range reservedRuleSets() {
		s.BindRules(set)
		if _, ok := modern.Planner.(testModernAIStep); !ok {
			t.Fatalf("%s: the Modern player runs %T", set.Name, modern.Planner)
		}
		if classic.Planner != set.Planner {
			t.Fatalf("%s: the Classic player runs %T, want the set's %T", set.Name, classic.Planner, set.Planner)
		}
		if !s.ModernAIPlayer(1) || s.ModernAIPlayer(2) {
			t.Fatalf("%s: Modern AI players = %v %v, want player 1 only", set.Name, s.ModernAIPlayer(1), s.ModernAIPlayer(2))
		}
	}
	if modern.Ext != ext || ext.closed != 0 {
		t.Fatal("a rule-set switch stopped the Modern player's controller")
	}
	if classic.Controller != ai.ControllerClassic || s.ModernAIPlayer(0) {
		t.Fatal("a rule-set switch changed who the Modern AI decides for")
	}
}

// Battle entry marks only live computer rows: a human or observer row's
// choice means nothing, and the Survival attacker's passive manager is the
// wave director's.
func TestApplyAIControllersMarksOnlyComputerRows(t *testing.T) {
	s := controllerTestSession()
	s.AI[3] = &ai.Manager{Player: 3, Passive: true}
	cfg := SkirmishConfig{NumPlayers: 4}
	cfg.Players[0] = SkirmishPlayer{Controller: SkirmishControllerHuman, AI: ai.ControllerModern}
	cfg.Players[1] = SkirmishPlayer{Controller: SkirmishControllerComputer, AI: ai.ControllerModern}
	cfg.Players[2] = SkirmishPlayer{Controller: SkirmishControllerComputer}
	cfg.Players[3] = SkirmishPlayer{Controller: SkirmishControllerComputer, AI: ai.ControllerModern}
	if err := applyAIControllers(s, cfg); err != nil {
		t.Fatal(err)
	}
	for p, want := range []ai.Controller{ai.ControllerClassic, ai.ControllerModern, ai.ControllerClassic, ai.ControllerClassic} {
		if got := s.AI[p].Controller; got != want {
			t.Fatalf("player %d is %s, want %s", p, got, want)
		}
	}
}

// A save records the players marked Modern and a load restores exactly them;
// a record written before the choice existed, and a load with no record,
// restore every computer player Classic.
func TestTheSaveRecordCarriesTheModernPlayers(t *testing.T) {
	src := controllerTestSession()
	if err := src.setAIController(src.AI[2], ai.ControllerModern); err != nil {
		t.Fatal(err)
	}
	rec := RecordAIControllers(src)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back AIControllers
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Modern) != 1 || back.Modern[0] != 2 {
		t.Fatalf("record %s, want player 2 Modern", data)
	}
	dst := controllerTestSession()
	if err := restoreAIControllers(dst, &back); err != nil {
		t.Fatal(err)
	}
	if dst.AI[2].Controller != ai.ControllerModern || dst.AI[1].Controller != ai.ControllerClassic || !dst.ModernAIPlayer(2) || dst.ModernAIPlayer(1) {
		t.Fatal("the load did not restore the saved controllers")
	}
	if !dst.Econ.Players[2].FullIncome || dst.Econ.Players[1].FullIncome {
		t.Fatal("the load did not pay exactly the Modern player in full")
	}
	var old AIControllers
	if err := json.Unmarshal([]byte(`{"seed":9,"overrides":[{"player":2,"params":"jitter=0"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*AIControllers{"a record from before the choice": &old, "no record": nil} {
		plain := controllerTestSession()
		if err := restoreAIControllers(plain, r); err != nil {
			t.Fatal(err)
		}
		for p := range plain.AI {
			if m := plain.AI[p]; m != nil && (m.Controller != ai.ControllerClassic || plain.ModernAIPlayer(uint8(p)) || plain.Econ.Players[p].FullIncome) {
				t.Fatalf("%s: player %d loaded Modern", name, p)
			}
		}
	}
}

// A command line names a computer row by its lobby number, or every computer
// row with "all", which a named row overrides in either order; anything else
// is refused rather than ignored.
func TestComputerAIChoicesNameComputerRows(t *testing.T) {
	for _, bad := range []string{"", "2", "0=modern", "11=modern", "02=modern", "2=smart", "x=classic", "all", "every=modern", "all=smart"} {
		if _, err := ParseComputerAI(bad); err == nil {
			t.Fatalf("ParseComputerAI(%q) was accepted", bad)
		}
	}
	choice, err := ParseComputerAI(" 3 = Modern ")
	if err != nil || choice != (ComputerAI{Row: 3, Controller: ai.ControllerModern}) {
		t.Fatalf("ParseComputerAI = %+v, %v", choice, err)
	}
	cfg := SurvivalSkirmishConfig("m", 2, SurvivalOptions{})
	if err := cfg.ApplyComputerAI([]ComputerAI{{Row: 2, Controller: ai.ControllerModern}, {Row: 3, Controller: ai.ControllerClassic}}); err != nil {
		t.Fatal(err)
	}
	if cfg.Players[1].AI != ai.ControllerModern || cfg.Players[2].AI != ai.ControllerClassic {
		t.Fatal("the buddies were not marked")
	}
	every, err := ParseComputerAI(" ALL = modern")
	if err != nil || every != (ComputerAI{Row: ComputerAIEveryRow, Controller: ai.ControllerModern}) {
		t.Fatalf("ParseComputerAI(all) = %+v, %v", every, err)
	}
	classicThree := ComputerAI{Row: 3, Controller: ai.ControllerClassic}
	for _, choices := range [][]ComputerAI{{every, classicThree}, {classicThree, every}} {
		c := SurvivalSkirmishConfig("m", 2, SurvivalOptions{})
		if err := c.ApplyComputerAI(choices); err != nil {
			t.Fatal(err)
		}
		if c.Players[0].AI != ai.ControllerClassic || c.Players[1].AI != ai.ControllerModern || c.Players[2].AI != ai.ControllerClassic || c.Players[3].AI != ai.ControllerClassic {
			t.Fatalf("%+v marked rows %v %v %v %v, want only the buddy in row 2 Modern", choices, c.Players[0].AI, c.Players[1].AI, c.Players[2].AI, c.Players[3].AI)
		}
	}
	for name, choices := range map[string][]ComputerAI{
		"the human":    {{Row: 1, Controller: ai.ControllerModern}},
		"the attacker": {{Row: 4, Controller: ai.ControllerModern}},
		"an empty row": {{Row: 5, Controller: ai.ControllerModern}},
		"a row twice":  {{Row: 2, Controller: ai.ControllerModern}, {Row: 2, Controller: ai.ControllerClassic}},
		"all twice":    {every, every},
	} {
		c := SurvivalSkirmishConfig("m", 2, SurvivalOptions{})
		if err := c.ApplyComputerAI(choices); err == nil {
			t.Fatalf("marking %s was accepted", name)
		}
	}
	if c := SurvivalSkirmishConfig("m", 0, SurvivalOptions{}); c.ApplyComputerAI([]ComputerAI{every}) == nil {
		t.Fatal("all was accepted in a battle with no computer player")
	}
}

// A setup of Classic computer players keeps its equivalence bytes; marking a
// row Modern changes them.
func TestClassicRowsKeepTheSetupBytes(t *testing.T) {
	cfg := DirectSkirmishConfig("m")
	before := string(cfg.NormalizedBytes())
	cfg.Players[1].AI = ai.ControllerClassic
	if string(cfg.NormalizedBytes()) != before {
		t.Fatal("a Classic row changed the setup bytes")
	}
	cfg.Players[1].AI = ai.ControllerModern
	if string(cfg.NormalizedBytes()) == before {
		t.Fatal("a Modern row left the setup bytes unchanged")
	}
}
