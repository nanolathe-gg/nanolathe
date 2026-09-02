package ai

import "testing"

// The catalog the grammar fixtures build holds three definitions whose
// canonical keys sort armck < armflash < corck. Catalog order is load-bearing
// for the plan gate below, so the tests name the keys in that order.

// TestPerDefinitionPlanGateLeaksIntoLaterDefinitions locks the gate rule of
// [08 R-AI-01 §18]: the pass opens the gate ONCE at its start and never resets
// it per definition, so a fragment whose `plan` does not match the active
// difficulty closes the gate for every later definition of that pass until
// another fragment's `plan` reopens it.
func TestPerDefinitionPlanGateLeaksIntoLaterDefinitions(t *testing.T) {
	authored := map[string]string{
		// armck is walked first and closes the gate: `easy` under hard.
		"armck": "plan easy",
		// armflash is walked next; its weight is inside the closed gate.
		"armflash": "weight ARMFLASH 0.5",
		// corck reopens the gate for itself.
		"corck": "plan hard\nweight CORCK 0.5",
	}
	p := grammarProfile("", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, authored))

	if got := p.WeightFor("ARMFLASH"); got != 100 {
		t.Fatalf("ARMFLASH = %d, want the default 100: the gate armck closed must still be closed [08 R-AI-01 §18]", got)
	}
	if got := p.WeightFor("CORCK"); got != 50 {
		t.Fatalf("CORCK = %d, want 50: its own `plan hard` reopens the gate [08 R-AI-01 §18]", got)
	}

	// Without armck's `plan`, the same ARMFLASH fragment applies — which is
	// what makes the assertion above about the gate and not about the fragment.
	open := map[string]string{"armflash": "weight ARMFLASH 0.5"}
	q := grammarProfile("", DifficultyHard)
	q.ApplyUnitDefinitions(grammarCatalog(t, open))
	if got := q.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("ARMFLASH with no earlier `plan` = %d, want 50 [08 R-AI-01 §18]", got)
	}
}

// TestCategoryFragmentAppliesTwicePerComputerPlayer locks the multiplication of
// [08 R-AI-01 §18]: a fragment naming a CATEGORY locks nothing, both passes run
// it, and the pass pair runs once per computer player — so with k computer
// players it is applied 2·k times to every manager's table.
func TestCategoryFragmentAppliesTwicePerComputerPlayer(t *testing.T) {
	authored := map[string]string{"armck": "weight LEVEL1 0.5"}
	// 100 → 50 → 25 → 12 (12.5 truncates) → 6. The truncation is the
	// applier's own [08 R-AI-01 §12] [I3], so the expected values are spelled
	// out rather than computed.
	for _, tc := range []struct {
		players int
		want    int32
	}{
		{1, 25},
		{2, 6},
	} {
		p := grammarProfile("", DifficultyHard)
		p.ApplyUnitDefinitionsForPlayers(grammarCatalog(t, authored), tc.players)
		for _, member := range []string{"ARMCK", "ARMFLASH", "CORCK"} {
			if got := p.WeightFor(member); got != tc.want {
				t.Fatalf("%d computer player(s): %s = %d, want %d (2·k applications) [08 R-AI-01 §18]",
					tc.players, member, got, tc.want)
			}
		}
	}
}

// TestExactFragmentAppliesOnceThenLocks is the other half of the same rule: a
// fragment naming a type EXACTLY applies on its first run and sets that type's
// weight lock, so every later pass — the second pass of the same player and
// every pass of every later computer player — is refused by the handler
// [08 R-AI-01 §18].
func TestExactFragmentAppliesOnceThenLocks(t *testing.T) {
	authored := map[string]string{"armck": "weight ARMCK 0.5"}
	for _, players := range []int{1, 2, 3} {
		p := grammarProfile("", DifficultyHard)
		p.ApplyUnitDefinitionsForPlayers(grammarCatalog(t, authored), players)
		if got := p.WeightFor("ARMCK"); got != 50 {
			t.Fatalf("%d computer player(s): ARMCK = %d, want 50 — an exact naming applies once and locks [08 R-AI-01 §18]", players, got)
		}
	}
}

// TestBothPassesAdmitEveryKeyword locks the kinds rule of [08 R-AI-01 §18]: the
// passes differ only in the lock vector they consult when they skip a
// definition, and each hands the whole fragment to the shared dispatcher with
// `plan`, `weight` and `limit` all admitted. A fragment's `limit` therefore
// runs in the WEIGHT pass, where the old reading executed nothing.
func TestBothPassesAdmitEveryKeyword(t *testing.T) {
	// An exact `limit` naming locks the type in the first pass that runs it,
	// so the value that survives is the first application's, not a doubled one.
	authored := map[string]string{"armck": "limit ARMCK 9"}
	p := grammarProfile("", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, authored))
	if got := p.LimitFor("ARMCK"); got != 9 {
		t.Fatalf("ARMCK limit = %d, want 9 — a fragment's `limit` is admitted by both passes [08 R-AI-01 §18]", got)
	}

	// And the weight pass's skip is the WEIGHT lock alone: a definition whose
	// fragment locked its own weight is walked again by the limit pass, where
	// its `limit` still lands.
	both := map[string]string{"armck": "weight ARMCK 0.5\nlimit ARMCK 9"}
	q := grammarProfile("", DifficultyHard)
	q.ApplyUnitDefinitions(grammarCatalog(t, both))
	if got := q.WeightFor("ARMCK"); got != 50 {
		t.Fatalf("ARMCK weight = %d, want 50 [08 R-AI-01 §18]", got)
	}
	if got := q.LimitFor("ARMCK"); got != 9 {
		t.Fatalf("ARMCK limit = %d, want 9 [08 R-AI-01 §18]", got)
	}
}
