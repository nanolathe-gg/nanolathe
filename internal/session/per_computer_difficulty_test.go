package session

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The admitted setup carries each computer seat's own word only when the
// computers differ (docs/DESIGN_MULTIPLAYER.md §6.6). Equal computers keep
// the one battle word and leave every row's own word zero, so they compose
// the battle a single-player setup with that word does; the Survival attacker
// is no computer seat and carries none.
func TestMatchSetupCarriesSeatDifficultiesOnlyWhenComputersDiffer(t *testing.T) {
	second := MatchSeat{Role: MatchRoleComputer, Side: 0, Color: 3, AllyGroup: 3, Metal: 1000, Energy: 1000,
		HostSeat: 0, ComputerKind: ai.ControllerClassic, Difficulty: 1, BuilderOptions: orders.DefaultBuilderOptions()}
	for _, survival := range []bool{false, true} {
		r := validMatchRequest(t)
		if survival {
			r = validSurvivalRequest(t)
		}
		at := 3
		r.Seats = append(r.Seats[:at:at], append([]MatchSeat{second}, r.Seats[at:]...)...)
		cfg, _ := matchSkirmishSetup(&r)
		if cfg.SeatDifficulty || cfg.Difficulty != 1 {
			t.Fatalf("survival=%t equal computers: seat words %t, battle word %d", survival, cfg.SeatDifficulty, cfg.Difficulty)
		}
		for i, p := range cfg.Players {
			if p.Difficulty != 0 {
				t.Fatalf("survival=%t equal computers: row %d carries word %d", survival, i, p.Difficulty)
			}
		}
		r.Seats[2].Difficulty = 2
		cfg, _ = matchSkirmishSetup(&r)
		if !cfg.SeatDifficulty || cfg.Difficulty != SkirmishDefaultDifficulty {
			t.Fatalf("survival=%t differing computers: seat words %t, battle word %d", survival, cfg.SeatDifficulty, cfg.Difficulty)
		}
		for i, want := range []int{0, 0, 2, 1, 0} {
			if got := cfg.Players[i].Difficulty; got != want {
				t.Fatalf("survival=%t differing computers: row %d word %d, want %d", survival, i, got, want)
			}
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("survival=%t: %v", survival, err)
		}
	}
}

// The setup's equivalence bytes gain the seats' words only with
// SeatDifficulty, and an out-of-vocabulary seat word is refused.
func TestSeatDifficultySetupBytesAndValidation(t *testing.T) {
	cfg := DirectSkirmishConfig("anywhere")
	base := cfg.NormalizedBytes()
	cfg.Players[1].Difficulty = 2
	if !bytes.Equal(base, cfg.NormalizedBytes()) {
		t.Fatal("an unread row word changed a one-word setup's bytes")
	}
	cfg.SeatDifficulty = true
	hard := cfg.NormalizedBytes()
	if bytes.Equal(base, hard) {
		t.Fatal("the seats' words left the setup's bytes unchanged")
	}
	cfg.Players[1].Difficulty = 0
	if bytes.Equal(hard, cfg.NormalizedBytes()) {
		t.Fatal("one seat's word left the setup's bytes unchanged")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Players[1].Difficulty = 3
	if err := cfg.Validate(); err == nil {
		t.Fatal("a seat word outside the vocabulary validated")
	}
	cfg.SeatDifficulty = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an unread row word failed validation: %v", err)
	}
}

// perSeatSession is a battle of a human (0) and three computer seats: 1 easy,
// 2 hard and 3 medium, which the lobby marks Modern. The battle word is easy,
// which a seat word must override.
func perSeatSession(t *testing.T) *Session {
	t.Helper()
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	cfg := SkirmishConfig{NumPlayers: 4, Difficulty: 0, SeatDifficulty: true}
	cfg.Players[0].Controller = SkirmishControllerHuman
	for p, word := range []int{1: 0, 2: 2, 3: 1} {
		if p == 0 {
			continue
		}
		cfg.Players[p] = SkirmishPlayer{Controller: SkirmishControllerComputer, Difficulty: word}
		s.Econ.Players[p] = economy.Player{Exists: true, ControllerState: 2, EndGameCountdown: -1}
		s.AI[p] = &ai.Manager{Player: uint8(p)}
	}
	cfg.Players[3].AI = ai.ControllerModern
	s.Skirmish = cfg
	if err := s.setAIController(s.AI[3], ai.ControllerModern); err != nil {
		t.Fatal(err)
	}
	return s
}

// Each seat reader takes its own word: the plan gate's and persona's
// difficulty (sessionAIDifficultyFor), the ledger's per-player word and the
// construction refunds' word (projectSeatIncome), under every bind; the human
// and the rows of a one-word battle read the battle's word. Projecting draws
// from neither stream.
func TestSeatReadersTakeTheirOwnWord(t *testing.T) {
	s := perSeatSession(t)
	sim, crt := s.rngSim.Draws(), s.rngCrt.Draws()
	for p, want := range map[uint8]ai.Difficulty{0: ai.DifficultyEasy, 1: ai.DifficultyEasy, 2: ai.DifficultyHard, 3: ai.DifficultyMedium} {
		if got, ok := sessionAIDifficultyFor(s, p); !ok || got != want {
			t.Fatalf("player %d plan gate %q, want %q", p, got, want)
		}
	}
	for _, set := range append(reservedRuleSets(), fullIncomeTestSet(), StrictRuleSet()) {
		s.BindRules(set)
		full := set.Name == fullIncomeTestSet().Name
		for p, word := range map[uint8]int{1: 0, 2: 2, 3: 1} {
			if full {
				word = 2
			}
			if got, ok := s.Econ.PlayerSelector(p); !ok || got != word {
				t.Fatalf("%s: player %d ledger word %d (%t), want %d", set.Name, p, got, ok, word)
			}
			if got := s.Build.RefundSelector(p); got != word {
				t.Fatalf("%s: player %d refund word %d, want %d", set.Name, p, got, word)
			}
		}
		if _, ok := s.Econ.PlayerSelector(0); ok {
			t.Fatalf("%s: the human carries a word of its own", set.Name)
		}
		if !s.Econ.Players[3].FullIncome || s.Econ.Players[1].FullIncome || s.Econ.Players[2].FullIncome {
			t.Fatalf("%s: the full-income mark is not the Modern seat's alone", set.Name)
		}
	}
	// The credits on the Strict 3.1 bind the loop ends with: the easy seat
	// half, the hard seat whole, the Modern seat whole whatever its word.
	for p, want := range map[uint8]float32{1: 500, 2: 1000, 3: 1000} {
		handle := pool.Handle(60 + p)
		s.Econ.CreditFeatureReclaim(handle, p, 1000, 0)
		if got := s.Econ.UnitBuckets(handle)[economy.Metal].Production; got != want {
			t.Fatalf("player %d was credited %v of 1000, want %v", p, got, want)
		}
	}
	if s.rngSim.Draws() != sim || s.rngCrt.Draws() != crt {
		t.Fatal("projecting the seats' words drew from a stream")
	}
	// A battle with one word clears every seat word on its next bind.
	s.Skirmish.SeatDifficulty = false
	s.RebindRules()
	for p := range s.Econ.Players {
		if _, ok := s.Econ.PlayerSelector(uint8(p)); ok {
			t.Fatalf("player %d keeps a seat word in a one-word battle", p)
		}
		if got, ok := sessionAIDifficultyFor(s, uint8(p)); !ok || got != ai.DifficultyEasy {
			t.Fatalf("player %d plan gate %q in a one-word easy battle", p, got)
		}
	}
}

// The death-side unit-reclaim refund is discounted on the reclaiming unit's
// owner's record [05 R-ECO-01 §11], so in a battle whose computers each carry
// their own difficulty it takes that seat's word: the easy seat is refunded
// half, the hard seat in full and the Modern seat in full whatever its word.
// In a battle with one word the same refund takes the battle's.
func TestSeatReclaimRefundTakesTheKillersWord(t *testing.T) {
	s := perSeatSession(t)
	s.BindRules(StrictRuleSet())
	def := s.Catalog.Units["armcom"]
	costly := *def
	costly.BuildCostMetal = 1000
	create := func(owner uint8, at int) *units.Unit {
		t.Helper()
		x := numeric.Fixed(int64((10 + at*5) * 65536))
		h, err := s.Units.Create(def, owner, x, 0, x)
		if err != nil {
			t.Fatal(err)
		}
		return s.Units.Unit(h)
	}
	victim := create(0, 9)
	refund := func(killer *units.Unit) float32 {
		t.Helper()
		victim.Def, victim.Remaining = &costly, 0
		victim.LastDamageCause, victim.EngagementTarget = 5, killer.Handle
		s.Econ.UnitBuckets(killer.Handle)[economy.Metal].Production = 0
		s.finalizeReclaimRefund(victim)
		return s.Econ.UnitBuckets(killer.Handle)[economy.Metal].Production
	}
	killers := map[uint8]*units.Unit{}
	for p := uint8(1); p <= 3; p++ {
		killers[p] = create(p, int(p))
	}
	for p, want := range map[uint8]float32{1: 500, 2: 1000, 3: 1000} {
		if got := refund(killers[p]); got != want {
			t.Fatalf("seat %d's reclaimer was refunded %v of 1000, want %v", p, got, want)
		}
	}
	s.Skirmish.SeatDifficulty = false
	s.RebindRules()
	if got := refund(killers[2]); got != 500 {
		t.Fatalf("a one-word easy battle refunded the hard row's reclaimer %v of 1000, want 500", got)
	}
}

// The Survival attacker, a human, a row past the seat count and an
// out-of-vocabulary word have no seat word, and neither has any row of a
// campaign.
func TestSeatDifficultyWordIsAComputerSeatsAlone(t *testing.T) {
	s := perSeatSession(t)
	if _, ok := sessionSeatDifficultyWord(s, 0); ok {
		t.Fatal("the human has a seat word")
	}
	if _, ok := sessionSeatDifficultyWord(s, 4); ok {
		t.Fatal("a row past the seat count has a seat word")
	}
	s.Skirmish.Players[2].Difficulty = 5
	if _, ok := sessionSeatDifficultyWord(s, 2); ok {
		t.Fatal("an out-of-vocabulary word is a seat word")
	}
	s.Skirmish.Survival.Enabled = true
	if _, ok := sessionSeatDifficultyWord(s, 3); ok {
		t.Fatal("the Survival attacker has a seat word")
	}
	if word, ok := sessionSeatDifficultyWord(s, 1); !ok || word != 0 {
		t.Fatalf("a survivor computer's word %d (%t)", word, ok)
	}
	s.Mission.Type = mission.TypeCampaign
	if _, ok := sessionSeatDifficultyWord(s, 1); ok {
		t.Fatal("a campaign row has a seat word")
	}
}
