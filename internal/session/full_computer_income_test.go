package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// fullIncomeTestSet is Modern asking for full computer income, the shape of
// the AI arena's research set (docs/DESIGN_ECONOMY_CONSTRUCTION "Modern AI
// full income"). It is built here because nothing under internal/ may import
// the mod list.
func fullIncomeTestSet() RuleSet {
	set := ModernRuleSet()
	set.Name = "session-test-full-income"
	set.ComputerIncome = FullComputerIncome{}
	return set
}

// computerIncomeSelectors reads both consumers of the computer discount word:
// the ledger's selector and the construction service's refund selector
// [05 R-ECO-01 §3][05 R-ECO-01 §11].
func computerIncomeSelectors(t *testing.T, s *Session) (ledger, construction int) {
	t.Helper()
	if s.Econ.EconomySelector == nil {
		t.Fatal("the ledger selector is unset for an in-vocabulary difficulty word")
	}
	return *s.Econ.EconomySelector, s.Build.ModeSelector
}

// Every bind projects the bound set's word, in both directions: a switch to a
// full-income set installs the undiscounted word, and a switch away restores
// the battle's difficulty word, whichever reserved set is bound next. A
// re-projection of the same set keeps its word.
func TestComputerIncomeFollowsEveryBind(t *testing.T) {
	s := strictNewSessionWithUnits(t, 2, 7, 11)
	s.Skirmish.Difficulty = 0
	s.Mission.Difficulty = 0
	if err := createAndBindServices(s); err != nil {
		t.Fatal(err)
	}
	full := fullIncomeTestSet()
	for _, step := range []struct {
		name string
		set  RuleSet
		want int
	}{
		{"fresh Modern", ModernRuleSet(), 0},
		{"Modern to full income", full, 2},
		{"full income to Modern", ModernRuleSet(), 0},
		{"Modern to full income again", full, 2},
		{"full income to Strict 3.1", StrictRuleSet(), 0},
		{"Strict 3.1 to full income", full, 2},
		{"full income to Community 3.9", CommunityRuleSet(), 0},
	} {
		s.BindRules(step.set)
		ledger, construction := computerIncomeSelectors(t, s)
		if ledger != step.want || construction != step.want {
			t.Fatalf("%s: ledger %d construction %d, want both %d", step.name, ledger, construction, step.want)
		}
		s.RebindRules()
		if again, _ := computerIncomeSelectors(t, s); again != step.want {
			t.Fatalf("%s: re-projection changed the ledger word to %d", step.name, again)
		}
	}
}

// A set bound before the services exist still reaches them: composition
// installs the difficulty word, and the re-projection at the end of
// composition replaces it with the bound set's answer.
func TestComputerIncomeReachesServicesComposedAfterTheBind(t *testing.T) {
	for _, full := range []bool{false, true} {
		s := strictNewSessionWithUnits(t, 2, 7, 11)
		s.Skirmish.Difficulty = 0
		s.Mission.Difficulty = 0
		set := ModernRuleSet()
		if full {
			set = fullIncomeTestSet()
		}
		s.bindRuleServices(set)
		if err := createAndBindServices(s); err != nil {
			t.Fatal(err)
		}
		want := 0
		if full {
			want = 2
		}
		if ledger, construction := computerIncomeSelectors(t, s); ledger != want || construction != want {
			t.Fatalf("full=%t: ledger %d construction %d, want both %d", full, ledger, construction, want)
		}
	}
}

// The retail discount is kept by every reserved layer: each answers the
// difficulty word unchanged and gives no answer for a word outside the
// vocabulary. Only the research answer replaces it.
func TestReservedSetsKeepTheRetailComputerDiscount(t *testing.T) {
	for _, set := range reservedRuleSets() {
		for difficulty := 0; difficulty <= 2; difficulty++ {
			if word, ok := set.ComputerIncome.DiscountWord(difficulty, true); !ok || word != difficulty {
				t.Fatalf("%s answered (%d,%t) for difficulty %d, want the difficulty word", set.Name, word, ok, difficulty)
			}
		}
		if _, ok := set.ComputerIncome.DiscountWord(0, false); ok {
			t.Fatalf("%s answered a word outside the vocabulary", set.Name)
		}
	}
	for difficulty := 0; difficulty <= 2; difficulty++ {
		if word, ok := (FullComputerIncome{}).DiscountWord(difficulty, true); !ok || word != 2 {
			t.Fatalf("full income answered (%d,%t) for difficulty %d, want the undiscounted word", word, ok, difficulty)
		}
	}
	if _, strict := (&Session{}).computerIncomeRules().(StrictComputerIncome); !strict {
		t.Fatal("an unbound session must answer the retail discount")
	}
}

// A computer player the lobby marks Modern is paid in full under every rule
// set, Strict 3.1 included, while a Classic computer player keeps the bound
// set's word (user decision 2026-09-25: "Full in every mode";
// docs/DESIGN_ECONOMY_CONSTRUCTION "Modern AI full income"). The mark reaches
// the ledger's credits and the construction refunds' discount test, follows
// every bind, and draws from neither stream. A battle with no Modern player
// marks nobody, so its credits stay the retail ones.
func TestAModernPlayerIsPaidInFullInEveryRuleSet(t *testing.T) {
	s := strictNewSessionWithUnits(t, 2, 7, 11)
	s.Skirmish.Difficulty = 0
	s.Mission.Difficulty = 0
	if err := createAndBindServices(s); err != nil {
		t.Fatal(err)
	}
	const classic, modern = 1, 2
	for _, p := range []uint8{classic, modern} {
		s.Econ.Players[p].Exists = true
		s.Econ.Players[p].ControllerState = 2
		s.AI[p] = &ai.Manager{Player: p}
	}
	s.BindRules(StrictRuleSet())
	for p := range s.Econ.Players {
		if s.Econ.Players[p].FullIncome {
			t.Fatalf("player %d is paid in full in a battle of Classic players", p)
		}
	}
	sim, crt := s.rngSim.Draws(), s.rngCrt.Draws()
	if err := s.setAIController(s.AI[modern], ai.ControllerModern); err != nil {
		t.Fatal(err)
	}
	for _, set := range append(reservedRuleSets(), fullIncomeTestSet(), StrictRuleSet()) {
		s.BindRules(set)
		word, _ := computerIncomeSelectors(t, s)
		if want := map[bool]int{false: 0, true: 2}[set.Name == fullIncomeTestSet().Name]; word != want {
			t.Fatalf("%s: the Classic players' word is %d, want %d", set.Name, word, want)
		}
		if !s.Econ.Players[modern].FullIncome || s.Econ.Players[classic].FullIncome {
			t.Fatalf("%s: paid in full: classic %t modern %t, want the Modern player only", set.Name, s.Econ.Players[classic].FullIncome, s.Econ.Players[modern].FullIncome)
		}
		if s.Econ.DiscountsCredit(modern) || !s.Econ.DiscountsCredit(classic) {
			t.Fatalf("%s: the ledger discounts the wrong computer player", set.Name)
		}
		if s.Build.IsSpecialSecondState(modern) || !s.Build.IsSpecialSecondState(classic) {
			t.Fatalf("%s: the construction refunds discount the wrong computer player", set.Name)
		}
	}
	// The credits themselves, on the Strict 3.1 bind the loop ends with: the
	// Classic computer player is credited half on easy, the Modern one all.
	for p, want := range map[uint8]float32{classic: 500, modern: 1000} {
		handle := pool.Handle(40 + p)
		s.Econ.CreditFeatureReclaim(handle, p, 1000, 0)
		if got := s.Econ.UnitBuckets(handle)[economy.Metal].Production; got != want {
			t.Fatalf("player %d was credited %v of 1000, want %v", p, got, want)
		}
	}
	if s.rngSim.Draws() != sim || s.rngCrt.Draws() != crt {
		t.Fatal("paying a Modern player in full drew from a stream")
	}
}
