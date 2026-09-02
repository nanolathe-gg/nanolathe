package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestSessionKindConstantsMatchResearch locks the two session-kind constants
// the campaign (mission.go) and skirmish (skirmish.go) battle-entry
// constructors pass to the pool's player-order builder against
// [08 R-SESS-01 §7]'s kind vocabulary: 1 campaign, 2 skirmish, 3 multiplayer
// (this engine never builds a kind-3 session). A caller must never derive
// this value from mission.Type, a different discriminant for how the
// mission *file* is loaded that happens to share two of its three values.
func TestSessionKindConstantsMatchResearch(t *testing.T) {
	if sessionKindCampaign != 1 {
		t.Fatalf("sessionKindCampaign = %d, want 1 [08 R-SESS-01 §7]", sessionKindCampaign)
	}
	if sessionKindSkirmish != 2 {
		t.Fatalf("sessionKindSkirmish = %d, want 2 [08 R-SESS-01 §7]", sessionKindSkirmish)
	}
}

// TestSessionKindsAlwaysOrderPlayersBySlot proves [08 R-SESS-01 §7]'s
// "Consequence for single-player" directly against the production helper
// both battle-entry call sites use: the pool's player-slice comparator
// consults the peer-identity sort key only when the session kind is 3
// (multiplayer). Campaign and skirmish battle entry now pass the explicit
// sessionKindCampaign/sessionKindSkirmish constants and no longer plumb any
// sort-key array from mission or lobby state — but even fed a maximally
// scrambled array (as if such plumbing still existed), both kinds must
// still produce the identity (slot) order.
func TestSessionKindsAlwaysOrderPlayersBySlot(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs := vfs.New()

	var scrambled [pool.PlayerCount]uint32
	for i := range scrambled {
		// Reverse order: if this were consulted, player 9 would sort ahead
		// of player 0.
		scrambled[i] = uint32(pool.PlayerCount - i)
	}

	for _, kind := range []int{sessionKindCampaign, sessionKindSkirmish} {
		w, err := newBattleSlicedWorldWithCOB(cat, fs, kind, scrambled)
		if err != nil {
			t.Fatalf("kind %d: newBattleSlicedWorldWithCOB: %v", kind, err)
		}
		prevEnd := -1
		for p := 0; p < pool.PlayerCount; p++ {
			start, end, ok := w.SliceForPlayer(p)
			if !ok {
				t.Fatalf("kind %d: player %d has no slice", kind, p)
			}
			if start <= prevEnd {
				t.Fatalf("kind %d: player %d slice [%d,%d] is not strictly after "+
					"the previous player's slice (ended at %d) — the sort key was "+
					"consulted outside session kind 3 [08 R-SESS-01 §7]",
					kind, p, start, end, prevEnd)
			}
			prevEnd = end
		}
	}
}
