package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The optional local save stays outside the repository. This exercises the
// production file reader with a paused battle, whose initial presentation
// cannot depend on completing another simulation tick [08 "Load process"].
func TestPlaytestPausedSavePublishesRestoredBattle(t *testing.T) {
	path := os.Getenv("NANOLATHE_PLAYTEST_SAVE")
	if path == "" {
		t.Skip("set NANOLATHE_PLAYTEST_SAVE to a local paused battle save")
	}
	opts := Options{Root: testsupport.RetailRoot(t), Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	loaded, err := session.LoadRetailSavePath(path, session.RetailLoadDeps{FS: cs.fs, SimSeed: 7, CRTSeed: 9, UnitLimit: 250})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Battle == nil {
		t.Fatal("save did not restore a battle")
	}
	s := loaded.Battle.Session
	f := s.Snapshot.Current()
	if f == nil {
		t.Fatal("restored battle has no published frame")
	}
	if !s.Clock.Paused || !f.Paused || f.Tick != s.Clock.GlobalTick {
		t.Fatalf("first frame did not retain saved pause and tick: frame=%d/%v clock=%d/%v", f.Tick, f.Paused, s.Clock.GlobalTick, s.Clock.Paused)
	}
	if s.Clock.SaveBox() != loaded.Battle.Image.Scheduler {
		t.Fatal("initial publication changed the saved scheduler")
	}
	if len(f.Units) == 0 {
		t.Fatal("restored battle published no units")
	}
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		found := false
		for _, view := range f.Units {
			if view.Slot == u.Handle {
				found = true
				if view.X != u.X || view.Y != u.Y || view.Z != u.Z || view.Health != u.Health || view.Owner != u.Owner {
					t.Fatalf("published unit %d differs from its restored state", u.Handle)
				}
				break
			}
		}
		if !found {
			t.Fatalf("restored unit %d missing from the first frame", u.Handle)
		}
	}
	t.Logf("published %d restored units at saved tick %d before any simulation step", len(f.Units), f.Tick)
}
