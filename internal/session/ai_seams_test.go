package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// TestBattleAIBindsTheSessionUnitLimit locks the first seam: the session's
// per-player unit limit reaches every computer player's strategic state before
// the first tick, because it is the only global the class routine's
// half-capacity comparison reads [08 R-AI-01 §13]. A skirmish supplies the
// established missing-preference default of 250 [08 R-SKIR-01 §6].
func TestBattleAIBindsTheSessionUnitLimit(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	want := uint16(sessionUnitLimit(sess))
	if want == 0 {
		t.Fatal("the session computed no per-player unit limit")
	}
	bound := 0
	for slot, manager := range sess.AI {
		if manager == nil {
			continue
		}
		got, ok := manager.Strategic.UnitLimit()
		if !ok {
			t.Fatalf("slot %d: the strategic state has no bound unit limit", slot)
		}
		if got != want {
			t.Fatalf("slot %d: bound unit limit = %d, want %d", slot, got, want)
		}
		bound++
	}
	if bound == 0 {
		t.Fatal("the skirmish composed no AI manager to bind")
	}
}

// TestBattleAIBindsTheDifficultyWord locks the second seam: the battle's
// difficulty word selects the profile's plan gate rather than the loader's
// last-plan fallback [08 R-AI-01 §12]. A skirmish takes the lobby value, whose
// missing-value default is 1, Medium [08 "Skirmish configuration"].
func TestBattleAIBindsTheDifficultyWord(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	if sess.Skirmish.Difficulty != SkirmishDefaultDifficulty {
		t.Fatalf("fixture sanity: skirmish difficulty = %d, want the default %d",
			sess.Skirmish.Difficulty, SkirmishDefaultDifficulty)
	}
	seen := 0
	for slot, manager := range sess.AI {
		if manager == nil || manager.Profile == nil {
			continue
		}
		if manager.Profile.Plan != ai.DifficultyMedium {
			t.Fatalf("slot %d: profile plan = %q, want %q", slot, manager.Profile.Plan, ai.DifficultyMedium)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("the skirmish composed no AI manager carrying a profile")
	}

	// The gate is what the word selects, and stock ai/default.txt makes the
	// difference legible: `Weight PLANT` is 3 under medium and 4 under hard, on
	// top of the same `Weight ARM 0.2`, so ARMLAB scores 60 and then 80; and
	// `Limit ARMFIDO` moves from 15 to 18.
	profile := sess.AI[1].Profile
	profile.ApplyUnitDefinitions(sess.Catalog)
	if got := profile.WeightFor("ARMLAB"); got != 60 {
		t.Fatalf("medium: ARMLAB weight = %d, want 60", got)
	}
	if got := profile.LimitFor("ARMFIDO"); got != 15 {
		t.Fatalf("medium: ARMFIDO limit = %d, want 15", got)
	}
	profile.SetDifficulty(ai.DifficultyHard)
	profile.ApplyUnitDefinitions(sess.Catalog)
	if got := profile.WeightFor("ARMLAB"); got != 80 {
		t.Fatalf("hard: ARMLAB weight = %d, want 80", got)
	}
	if got := profile.LimitFor("ARMFIDO"); got != 18 {
		t.Fatalf("hard: ARMFIDO limit = %d, want 18", got)
	}
}

// TestSessionAIDifficultyMapsBothSources keeps the word's two established
// writers apart: the lobby setting for a skirmish and the campaign difficulty
// control for a campaign [08 R-AI-01 §12]. A value outside the vocabulary is
// reported absent rather than guessed.
func TestSessionAIDifficultyMapsBothSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		sess *Session
		want ai.Difficulty
		ok   bool
	}{
		{"skirmish easy", &Session{Skirmish: SkirmishConfig{Difficulty: 0}}, ai.DifficultyEasy, true},
		{"skirmish medium", &Session{Skirmish: SkirmishConfig{Difficulty: 1}}, ai.DifficultyMedium, true},
		{"skirmish hard", &Session{Skirmish: SkirmishConfig{Difficulty: 2}}, ai.DifficultyHard, true},
		{"skirmish out of range", &Session{Skirmish: SkirmishConfig{Difficulty: 7}}, "", false},
		{
			"campaign wins over the skirmish word",
			&Session{
				Skirmish: SkirmishConfig{Difficulty: 1},
				Mission:  &mission.Mission{Type: mission.TypeCampaign, Difficulty: 2},
			},
			ai.DifficultyHard, true,
		},
		{
			// A mission that is not a campaign load carries -1.
			"non-campaign mission keeps the lobby word",
			&Session{
				Skirmish: SkirmishConfig{Difficulty: 2},
				Mission:  &mission.Mission{Type: mission.TypeSkirmish, Difficulty: -1},
			},
			ai.DifficultyHard, true,
		},
		{
			"campaign with no difficulty is absent",
			&Session{Mission: &mission.Mission{Type: mission.TypeCampaign, Difficulty: -1}},
			"", false,
		},
	} {
		got, ok := sessionAIDifficulty(tc.sess)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%s: sessionAIDifficulty = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := sessionAIDifficulty(nil); ok {
		t.Fatal("a nil session reports no difficulty word")
	}
}
