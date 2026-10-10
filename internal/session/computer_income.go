package session

import "github.com/nanolathe-gg/nanolathe/internal/ai"

// ComputerIncomeRules is the computer players' income policy seam
// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income"). It answers
// the one word the ledger's computer-production discount and both
// construction refund sites select on [05 R-ECO-01 §3][05 R-ECO-01 §11] for
// every Classic computer player — once for the battle's word and, in a battle
// whose computers each carry their own difficulty, once more for each
// computer seat's (projectSeatIncome); a computer player marked Modern is
// paid in full whatever it answers (projectComputerIncome).
//
// Like UnitLimitRules it is a decision no simulation package owns: neither
// the ledger nor the construction service asks it, because both only read a
// selector word the session installs. The session asks it when a set is
// bound, outside any tick, and projects the one answer onto both services
// (projectComputerIncome), so the two consumers can never disagree.
type ComputerIncomeRules interface {
	// DiscountWord answers the selector word for a difficulty word — the
	// battle's, or a computer seat's own; known reports that the difficulty
	// word is in the vocabulary (0 easy, 1 medium, 2 hard). An answer with
	// ok false leaves both selectors as they are.
	DiscountWord(difficulty int, known bool) (word int, ok bool)
}

// StrictComputerIncome is retail: the discount selects on the difficulty word
// itself, so easy credits a computer player half of its production, medium
// seven tenths and hard the whole amount [05 R-ECO-01 §3]. A word outside the
// vocabulary is not answered, which leaves the selector unset — the
// undiscounted path both consumers already take. It is zero size.
type StrictComputerIncome struct{}

// DiscountWord answers the difficulty word unchanged.
func (StrictComputerIncome) DiscountWord(difficulty int, known bool) (int, bool) {
	return difficulty, known
}

// CommunityComputerIncome is the Community 3.9 layer. No adopted Community
// contract changes the discount, so it promotes the retail answer.
type CommunityComputerIncome struct{ StrictComputerIncome }

// ModernComputerIncome is the Modern layer. Full income for every computer
// player is not a reserved Modern policy — the reserved Modern set keeps the
// retail planner's difficulty ladder, discount included, for its Classic
// players — so it promotes the Community answer.
type ModernComputerIncome struct{ CommunityComputerIncome }

// FullComputerIncome answers the undiscounted word for every computer player
// of the battle, Classic or Modern, so difficulty comes from each planner
// alone and never from income. The AI arena's research set composes it
// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income"); no reserved
// set binds it.
type FullComputerIncome struct{}

// DiscountWord answers hard, the whole-credit word, for every difficulty.
func (FullComputerIncome) DiscountWord(int, bool) (int, bool) { return 2, true }

// computerIncomeRules is the bound set's income policy. A session that never
// bound one, or bound a set literal that left the seam unset, answers as
// retail, the fallback every other seam applies.
func (s *Session) computerIncomeRules() ComputerIncomeRules {
	if s == nil || s.Rules.ComputerIncome == nil {
		return StrictComputerIncome{}
	}
	return s.Rules.ComputerIncome
}

// projectComputerIncome writes the word the ledger's computer-production
// discount and both construction refund sites select on [05 R-ECO-01 §3]
// [05 R-ECO-01 §11] from the bound set, on every bind and in both directions:
// binding a set whose seam answers full income installs the undiscounted word,
// and binding one that answers retail restores the battle's difficulty word.
// Composition installs the difficulty word before any set is bound
// (createAndBindServices, bindConstructionEconomy); this is the only site that
// changes it afterwards, so a switch can never leave the previous set's word
// behind.
//
// A difficulty word outside the vocabulary gets no retail answer and leaves
// both selectors alone. Composition never set them for such a word, and the
// undiscounted word a full-income set may have written is the same answer the
// unset selector already gives, so nothing is guessed.
//
// It first marks the computer players paid in full (projectFullIncomePlayers),
// and finally projects each computer seat's own word where the battle carries
// one (projectSeatIncome).
func (s *Session) projectComputerIncome() {
	s.projectFullIncomePlayers()
	rules := s.computerIncomeRules()
	difficulty, known := sessionDifficultyWord(s)
	if word, ok := rules.DiscountWord(difficulty, known); ok {
		if s.Econ != nil {
			s.Econ.SetEconomySelector(word)
		}
		if s.Build != nil {
			s.Build.ModeSelector = word
		}
	}
	s.projectSeatIncome(rules)
}

// projectSeatIncome gives each computer seat of a battle whose computers carry
// their own difficulty (sessionSeatDifficultyWord) the bound set's answer for
// its own word, on the ledger record both the ledger's credits and the
// construction refunds select on (economy.Service.SetPlayerSelector,
// construction.Service.RefundSelector), so a seat's credits take its own
// discount [05 R-ECO-01 §3][05 R-ECO-01 §11][05 R-SHARE-01 §2]
// (docs/DESIGN_MULTIPLAYER.md §6.6). Every other record — every record of a
// battle with one word — is cleared to the battle's word, which leaves a
// ledger that never had a seat word exactly as it was. A computer seat marked
// Modern carries its word too, but its full-income mark keeps every credit
// whole. It runs outside any tick, in ascending slot order [I1].
func (s *Session) projectSeatIncome(rules ComputerIncomeRules) {
	if s == nil || s.Econ == nil {
		return
	}
	for p := range s.Econ.Players {
		player := uint8(p)
		difficulty, seat := sessionSeatDifficultyWord(s, player)
		if !seat {
			s.Econ.ClearPlayerSelector(player)
			continue
		}
		word, ok := rules.DiscountWord(difficulty, true)
		if !ok {
			// No reserved answer refuses an in-vocabulary word; a set that
			// does leaves the seat on the battle's word, as an unanswered
			// battle word leaves the global selector alone.
			s.Econ.ClearPlayerSelector(player)
			continue
		}
		s.Econ.SetPlayerSelector(player, word)
	}
}

// projectFullIncomePlayers marks on the ledger every computer player the
// lobby, the command line or a save marked Modern (ai.Manager.Controller) as
// paid in full, and clears the mark on every other player
// (docs/DESIGN_ECONOMY_CONSTRUCTION.md "Modern AI full income", user decision
// 2026-09-25: "Full in every mode"). The mark is a lobby choice, not a rule,
// so it holds under every bound set, Strict 3.1 included; a Classic computer
// player keeps the bound set's word. A battle of Classic players marks
// nobody, so its credits are exactly the retail ones. It runs outside any
// tick — on every bind and whenever battle entry or a load marks a player —
// and is a direct indexed walk, never a map range [I1].
func (s *Session) projectFullIncomePlayers() {
	if s == nil || s.Econ == nil {
		return
	}
	for player := range s.Econ.Players {
		full := false
		if player < len(s.AI) {
			m := s.AI[player]
			full = m != nil && m.Controller == ai.ControllerModern
		}
		s.Econ.Players[player].FullIncome = full
	}
}
