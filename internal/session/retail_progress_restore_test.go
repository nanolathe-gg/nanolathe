package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// Both load routes copy the same campaign marks; an in-battle load must keep
// earlier results when its current mission ends [08 R-SAVE-02 §2]
// [08 R-CAMP-01 §8].
func TestRetailBattleRestorePreservesCampaignProgress(t *testing.T) {
	for _, marks := range []string{"WL" + strings.Repeat("U", 23), "", "W", strings.Repeat("L", 26)} {
		t.Run(marks, func(t *testing.T) {
			s, _ := newRestoreCoreFixture(t, 0)
			s.Mission = &mission.Mission{Type: mission.TypeCampaign}
			s.CampaignSlot = 12
			s.Latch = NewEndLatch()
			image := &save.BattleImage{Summary: save.Summary{Gametype: GametypeCampaign, Thumbs: marks}}
			if err := RestoreRetailBattleCore(&RetailBattleStage{Session: s, Image: image}); err != nil {
				t.Fatal(err)
			}
			want := marks
			if len(want) != 25 {
				want = strings.Repeat("U", 25)
			}
			if got := RetailBattleSummary(s, "", "", 250).Thumbs; got != want {
				t.Fatalf("restored save marks = %q, want %q", got, want)
			}
			s.CommitCampaignTeardown()
			want = want[:12] + "L" + want[13:]
			if got := RetailBattleSummary(s, "", "", 250).Thumbs; got != want {
				t.Fatalf("marks after teardown = %q, want %q", got, want)
			}
		})
	}
}
