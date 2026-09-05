package session

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/save"
)

// TestEnemyCommanderDeathEndsTheMatchBeforeAndAfterALoad locks the whole
// rule-word-1 chain of [08 R-SKIR-01 §3] on real content, in the one place a
// play-test found it broken: the commander-identity test, the owner sweep it
// gates, the live count the sweep drives to zero, the kind-2 victory sweep of
// [08 R-TRIG-01 §6] and the end latch six settlement dues later.
//
// It runs the chain twice on the same battle — once on the composed session
// and once on the same session written to a bank and restored — because the
// identity test is where a load used to break it. [08 R-SKIR-01 §2] "Save
// persistence": a load "restores the five [rule words] into the setup record
// and the map name", nothing more, so a restored battle's setup rows read back
// as side 0 for every slot. The identity test read the owner's side off those
// rows, so after a load every dead unit was compared against side 0's
// commander name and a CORE player's commander death raised nothing at all —
// no storage-bonus clear, no owner sweep, no elimination, and a match that
// never ended. The side now comes off the player record, which battle entry
// writes ([08 R-SKIR-01 §2] "Row-to-player conversion") and the bank persists
// ([08 "Player records"]).
//
// Both arms kill through the ordinary damage funnel rather than a direct
// destroy, so the assertion covers the path a player's weapons take.
func TestEnemyCommanderDeathEndsTheMatchBeforeAndAfterALoad(t *testing.T) {
	f := loadRetailFixture(t)
	f.cfg.RNGSimSeed, f.cfg.RNGCrtSeed = 7, 7
	fresh := f.session(t)
	// Long enough for the computer player to own more than its commander: with
	// a single unit the live count would reach zero from the kill alone and the
	// owner sweep would not be under test.
	stepRetail(fresh, 2500)
	if live := fresh.Units.LiveCountForPlayer(1); live < 2 {
		t.Fatalf("computer player owns %d units before the kill; the sweep needs something to sweep", live)
	}

	in, err := fresh.RetailBattleSaveInputs(RetailBattleSummary(fresh, "wu19228", "0"), save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "WU19228.SAV")
	if err := fresh.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail save: %v", err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat, SimSeed: 7, CRTSeed: 7, UnitLimit: fresh.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail save: %v", err)
	}
	if loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("the bank did not come back as a battle")
	}

	for _, arm := range []struct {
		name string
		s    *Session
	}{{"composed", fresh}, {"restored", loaded.Battle.Session}} {
		s := arm.s
		if rule := CommanderDeathMode(s.Skirmish.CommanderDeath); rule != CommanderDeathEnds {
			t.Fatalf("%s: rule word %d, want the retail default 1 [08 R-SKIR-01 §1]", arm.name, rule)
		}
		commander := retailUnit(s, 1, retailCORE)
		if commander == nil {
			t.Fatalf("%s: the computer player has no CORE commander", arm.name)
		}
		if !s.isCommanderForOwner(commander) {
			t.Fatalf("%s: the CORE player's commander is not recognised as its side's commander [08 R-SKIR-01 §3]", arm.name)
		}
		if !s.Combat.ApplySelfDestructDamage(s.Units, commander.Handle, s.Clock.GlobalTick) {
			t.Fatalf("%s: the killing packet was not applied", arm.name)
		}
		// The kill lands on an arbitrary tick and the end-condition block runs
		// only on the local slot's 30-tick settlement due, so the first true
		// due is up to one due away; from there the shared countdown takes six
		// true dues to cross below zero — 150 ticks [08 R-TRIG-01 §6]
		// "Countdown and latch". Seven dues plus a tick of slack.
		start := s.Clock.GlobalTick
		bound := uint32(30*7 + 1)
		scaled := s.Clock.ScaledAnchor
		for s.Clock.GlobalTick < start+bound && !s.GetResult().Ended {
			scaled += 5
			s.Step(scaled)
		}
		result := s.GetResult()
		if !result.Ended {
			t.Fatalf("%s: the match did not end %d ticks after the enemy commander died: live0=%d live1=%d countdown=%d [08 R-SKIR-01 §3]",
				arm.name, s.Clock.GlobalTick-start, s.Units.LiveCountForPlayer(0), s.Units.LiveCountForPlayer(1), s.Latch.Countdown)
		}
		if result.Kind != "victory" || result.Reason != ReasonCommanderDeath {
			t.Fatalf("%s: result %q/%q, want victory by commander death", arm.name, result.Kind, result.Reason)
		}
		// The sweep, not the killing packet, is what emptied the slot: the
		// owner keeps nothing when the rule word is 1 [08 R-SKIR-01 §3].
		if live := s.Units.LiveCountForPlayer(1); live != 0 {
			t.Fatalf("%s: the swept player still owns %d units", arm.name, live)
		}
		if s.State != StatePostBattle {
			t.Fatalf("%s: session state %v after the latch, want post-battle", arm.name, s.State)
		}
	}
}
