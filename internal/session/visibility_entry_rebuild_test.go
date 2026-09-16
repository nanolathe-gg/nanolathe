package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestDeathmatchRespawnRefillsTheMappedWordGrid locks the respawn half of
// [08 R-ENTRY-01 §7]: the visibility rebuild is called with the FULL argument at
// every commander respawn [08 R-SKIR-01 §3], so step 1 refills the whole mapped
// word grid from mode bit 0. Under retail's default Unmapped mode that zeroes
// the grid, and ground the dead player explored before the death stops being
// mapped. Republishing live observers alone left that map memory in place.
func TestDeathmatchRespawnRefillsTheMappedWordGrid(t *testing.T) {
	s, _ := newLobbyEndRuleSession(t, int(CommanderDeathDeathmatch), false)
	s.World = minimalTerrain()
	// As in the respawn draw-order test, this isolates the respawn from
	// production movement composition.
	s.Movement = nil
	// The synthetic lobby config leaves the mode word at zero (Mapped +
	// Permanent), where the word grid is all-ones and step 1 is unobservable.
	// Retail's shipped default is Unmapped + LineOfSight [03 R-VIS-01 §1].
	s.Vis = visibility.New(s.World, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled|visibility.ModeTerrainRay)
	word := s.Vis.WordMask()
	if len(word) == 0 {
		t.Fatal("fixture has no visibility word grid")
	}
	// Stand in for ground explored before the death: a word-grid bit far from
	// anything the respawned commander can cover.
	remembered := len(word) - 1
	word[remembered] |= 1 << uint(s.LocalOwner)

	h := poolHandle(commanderHandles(s)[0][0])
	s.Units.Destroy(h, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h, 0); !result.Freed {
		t.Fatal("commander was not finalized")
	}
	s.NotifyDeathFinalized(0, 0)
	// Six dues: the first arms the shared countdown, the sixth selects the
	// respawn [08 R-TRIG-01 §6][08 R-SKIR-01 §3].
	for tick := uint32(30); tick <= 180; tick += 30 {
		s.EvaluateResult(tick)
	}
	respawned := false
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && s.isCommanderForOwner(u) {
			respawned = true
			break
		}
	}
	if !respawned {
		t.Fatal("deathmatch did not respawn a commander; the assertion below would be vacuous")
	}
	if got := s.Vis.WordMask()[remembered] & (1 << uint(s.LocalOwner)); got != 0 {
		t.Fatalf("respawn kept pre-death map memory at the remembered cell (%#x); [08 R-ENTRY-01 §7] step 1 refills the whole grid from mode bit 0", got)
	}
}

// TestWatcherVisibilityClearForcesTheBulkRebuild locks the second half of the
// watcher clear: clearing mode bits 0 and 1 is Mapped + Permanent, and retail
// "forces one bulk rebuild" so both stores fill all-visible
// [03 R-VIS-01 §4] pass 1, [03 R-VIS-01 §1], [08 R-SKIR-01 §3],
// [08 R-ENTRY-01 §7]. Clearing the bits alone left a watcher's local slot
// looking at solid unexplored fog, because the fog window seeds an unset word
// bit as unexplored.
func TestWatcherVisibilityClearForcesTheBulkRebuild(t *testing.T) {
	const all = visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay
	s := &Session{Econ: &economy.Service{}, LocalOwner: 2, Vis: visibility.New(minimalTerrain(), all)}
	// Step 2 refills only slots that are live, human/computer/remote and not
	// side 10, so the watcher's own record must pass that test for its byte
	// grid to be refilled [08 R-ENTRY-01 §7] step 2.
	p := &s.Econ.Players[2]
	p.Exists = true
	p.ControllerState = 1
	p.Watcher = true

	if err := finishBattleEntry(s, nil); err != nil {
		t.Fatal(err)
	}

	const mask2 = visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
	if s.Vis.Mode()&mask2 != 0 {
		t.Fatalf("watcher clear left mode %#x, want mode bits 0 and 1 clear", s.Vis.Mode())
	}
	for i, got := range s.Vis.WordMask() {
		if got != 0x03FF {
			t.Fatalf("mapped word cell %d = %#x after the watcher clear, want every usable player bit", i, got)
		}
	}
	grid := s.Vis.ByteGrid(2)
	if len(grid) == 0 {
		t.Fatal("local watcher has no byte grid")
	}
	for i, got := range grid {
		if got != 1 {
			t.Fatalf("watcher byte cell %d = %d after the clear, want the all-visible fill", i, got)
		}
	}
}
