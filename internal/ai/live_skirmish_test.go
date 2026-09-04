package ai_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

func liveSkirmish(t *testing.T, mapName string, seed uint32) *session.Session {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("nanolathe: mounting install failed: logical path %s, providers searched [], expected a readable Total Annihilation install: %v", root, err)
	}
	t.Cleanup(func() { fs.Close() })
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind:           headless.ScenarioDirectOTA,
		Map:            mapName,
		LocalOwner:     -1,
		SimulationSeed: seed,
		CRTSeed:        seed,
		FS:             fs,
	})
	if err != nil {
		t.Skipf("nanolathe: skirmish composition failed: logical path %s, providers searched [], expected a mounted skirmish map: %v", mapName, err)
	}
	return composed.Session
}

func advanceSession(sess *session.Session, ticks uint32) {
	scaled := sess.Clock.ScaledAnchor
	for sess.Clock.GlobalTick < ticks {
		delta := int32(5)
		if remaining := int32(ticks - sess.Clock.GlobalTick); remaining < delta {
			delta = remaining
		}
		scaled += delta
		sess.Step(scaled)
	}
}

func ownedCounts(sess *session.Session, player int) (total, structures int) {
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || int(u.Owner) != player {
			continue
		}
		total++
		if u.Flags&units.BuildingClassStatus != 0 {
			structures++
		}
	}
	return total, structures
}

// TestLiveSkirmishComputerSlotDevelops is the liveness lock for PT3-14: a
// computer slot in a stock skirmish must keep building. Before the fix the
// slot's owned-unit count stopped at the commander plus two metal extractors
// on every map and every seed, because the scatter helper's acceptance limit
// was zero and rejected every geometrically valid non-extractor site
// [08 R-AI-03 §4-A].
//
// The floors are deliberately far below what a healthy run reaches (twenty
// units, sixteen structures at this tick count on this map) so the test locks
// "the planner never goes silent" rather than a particular build order.
func TestLiveSkirmishComputerSlotDevelops(t *testing.T) {
	sess := liveSkirmish(t, "Comet Catcher", 1)
	if sess.World == nil {
		t.Skip("nanolathe: composed session carries no terrain")
	}
	// The manager's SurfaceMetal word and the terrain's uniform per-cell metal
	// seed are the same authored schema key, so a composed session must hand
	// the manager the value the terrain was seeded with [08 R-AI-03 §4-A].
	// Checked first and separately: when the two disagree the scatter helper's
	// acceptance limit is wrong and the build floors below fail for a reason
	// that has nothing to do with the planner.
	cell := sess.World.PlotAt(sess.World.CellW/2, sess.World.CellH/2)
	if cell == nil {
		t.Fatal("nanolathe: terrain has no centre plot cell")
	}
	for player, mgr := range sess.AI {
		if mgr == nil {
			continue
		}
		if mgr.SurfaceMetal != int32(cell.Metal()) {
			t.Fatalf("nanolathe: AI slot %d carries SurfaceMetal %d while the terrain seed is %d: the session binding must read the selected schema's word, not the OTA [GlobalHeader] [08 R-AI-03 §4-A]", player, mgr.SurfaceMetal, cell.Metal())
		}
	}

	computer := -1
	for player, mgr := range sess.AI {
		if mgr != nil && player != int(sess.LocalOwner) {
			computer = player
			break
		}
	}
	if computer < 0 {
		t.Skip("nanolathe: composed skirmish has no computer slot")
	}
	startTotal, startStructures := ownedCounts(sess, computer)
	advanceSession(sess, 12000)
	total, structures := ownedCounts(sess, computer)
	if total <= startTotal || structures <= startStructures {
		t.Fatalf("computer slot %d did not develop: %d units / %d structures at tick 12000, started at %d / %d", computer, total, structures, startTotal, startStructures)
	}
	if total < 8 || structures < 6 {
		t.Fatalf("computer slot %d built %d units and %d structures by tick 12000, want at least 8 and 6", computer, total, structures)
	}
}
