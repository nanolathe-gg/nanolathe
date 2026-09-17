package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// lobbyOrdinaryUnit is the plain non-commander definition newLobbyEndRuleSession
// installs; both tests below place copies of it for the local player.
func lobbyOrdinaryUnit(t *testing.T, s *Session) *content.UnitDef {
	t.Helper()
	def, ok := s.Catalog.Unit(content.CanonicalKey("corllt"))
	if !ok || def == nil {
		t.Fatal("lobby fixture is missing its ordinary unit definition")
	}
	return def
}

// spawnLocalUnits places n ordinary units for the session's LOCAL player. Both
// rows of the lobby fixture are human, so that slot is the last human row
// [08 R-SKIR-01 §2] "Battle entry: what the record becomes", not slot 0.
func spawnLocalUnits(t *testing.T, s *Session, n int) []pool.Handle {
	t.Helper()
	def := lobbyOrdinaryUnit(t, s)
	out := make([]pool.Handle, 0, n)
	for i := 0; i < n; i++ {
		x := numeric.Fixed(int64(20+8*i) << 16)
		h, err := s.Units.Create(def, s.LocalOwner, x, 0, numeric.Fixed(20<<16))
		if err != nil {
			t.Fatalf("create local unit %d: %v", i, err)
		}
		out = append(out, h)
	}
	return out
}

// TestCommanderDeathSweepsTheLocalPlayerAndPublishesTheEnd is the arm the rest
// of the result suite leaves uncovered, and the one the WU-19-108 play-test
// report names: the *local* player's commander dies while that player still
// has other units alive.
//
// [08 R-SKIR-01 §3]: under rule 1 the kill-record handler runs the owner sweep
// over every live, not-already-dying unit of the dead commander's owner; the
// sweep drives that owner's live count to zero and the ordinary live-count
// predicate — not any commander test — is what ends the battle five 30-tick
// dues later. The end must also reach the committed frame, because the front
// end reads `Frame.Result`, never the authoritative record [I6][03 §2.4].
func TestCommanderDeathSweepsTheLocalPlayerAndPublishesTheEnd(t *testing.T) {
	s, _ := newLobbyEndRuleSession(t, 1, false)
	// The lobby fixture's two rows are both human, so the local player is the
	// last of them [08 R-SKIR-01 §2] "Battle entry: what the record becomes".
	local := int(s.LocalOwner)
	handles := spawnLocalUnits(t, s, 2)
	if live := s.Units.LiveCountForPlayer(local); live != len(handles)+1 {
		t.Fatalf("local live count %d before the kill, want commander plus %d units", live, len(handles))
	}

	killCommander(t, s, local)
	stepLobbyThrough(t, s, 0, 400)

	for i, h := range handles {
		if u := s.Units.Unit(h); u != nil && u.Alive {
			t.Fatalf("local unit %d survived the commander owner sweep [08 R-SKIR-01 §3]", i)
		}
	}
	if live := s.Units.LiveCountForPlayer(local); live != 0 {
		t.Fatalf("local live count %d after the sweep, want zero — the sweep is what the defeat predicate reads [08 R-SKIR-01 §3]", live)
	}
	res := s.GetResult()
	if !res.Ended || res.Reason != ReasonCommanderDeath || res.Kind != "defeat" {
		t.Fatalf("result = %+v, want a latched commander-death defeat [08 R-SKIR-01 §3]", res)
	}
	if s.State != StatePostBattle {
		t.Fatalf("state = %v after the latch, want PostBattle", s.State)
	}
	// The latch is only observable to the front end through the committed
	// frame, and the tick that writes it publishes before Step breaks out of
	// the sub-tick loop [01 §4.4][I6].
	if s.Snapshot == nil {
		t.Fatal("session published no frames")
	}
	cur := s.Snapshot.Current()
	if cur == nil {
		t.Fatal("no committed frame at the latch")
	}
	if !cur.Result.Ended || cur.Result.Reason != ReasonCommanderDeath {
		t.Fatalf("committed frame result = %+v, want the latched commander-death end", cur.Result)
	}
}

// TestCommanderDeathRuleZeroKeepsTheLocalPlayerAlive is the other half of the
// same report: rule 0 ("Game Continues") skips the sweep entirely, so the
// owner keeps every unit and the battle runs on. A rule word that arrives as
// zero where the lobby meant 1 is indistinguishable from "killing the
// commander did not end the skirmish", so the two arms are pinned together
// [08 R-SKIR-01 §3].
func TestCommanderDeathRuleZeroKeepsTheLocalPlayerAlive(t *testing.T) {
	s, _ := newLobbyEndRuleSession(t, 0, false)
	handles := spawnLocalUnits(t, s, 2)

	killCommander(t, s, int(s.LocalOwner))
	stepLobbyThrough(t, s, 0, 400)

	for i, h := range handles {
		if u := s.Units.Unit(h); u == nil || !u.Alive {
			t.Fatalf("local unit %d was swept under rule 0, which skips the sweep [08 R-SKIR-01 §3]", i)
		}
	}
	if s.GetResult().Ended || s.State != StateBattle {
		t.Fatalf("rule 0 ended the battle on commander death: state=%v result=%+v", s.State, s.GetResult())
	}
}
