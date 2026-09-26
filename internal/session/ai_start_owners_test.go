package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// With pre-determined starts a computer player's manager learns which slot
// took each start position (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI
// computer player"); under random starts, and for a manager whose start
// list does not match the placement's records, it learns nothing.
func TestSkirmishPublishesStartOwnersOnlyWhenPredetermined(t *testing.T) {
	specials := []mission.Special{
		{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos1"},
		{Kind: 1, ID: 1, X: 24, Z: 28, Name: "StartPos2"},
		{Kind: 1, ID: 2, X: 40, Z: 44, Name: "StartPos3"},
	}
	positions := [][2]int32{{12, 20}, {24, 28}, {40, 44}}
	for _, location := range []int{1, 0} {
		s := strictNewSessionWithUnits(t, 0, 7, 11)
		s.AI[0] = &ai.Manager{Player: 0, StartPositions: positions}
		s.AI[1] = &ai.Manager{Player: 1, StartPositions: positions[:2]}
		cfg := wu19178Config()
		cfg.Location = location
		if err := skirmishReconstructUnits(s, cfg, wu19178Mission(specials...)); err != nil {
			t.Fatalf("location %d: skirmishReconstructUnits: %v", location, err)
		}
		got := s.AI[0].StartOwners
		if location == 0 {
			if got != nil {
				t.Errorf("random starts published owners %v", got)
			}
			continue
		}
		if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != -1 {
			t.Errorf("pre-determined starts: owners %v, want [0 1 -1]", got)
		}
		if s.AI[1].StartOwners != nil {
			t.Errorf("a manager with a mismatched start list got owners %v", s.AI[1].StartOwners)
		}
	}
}
