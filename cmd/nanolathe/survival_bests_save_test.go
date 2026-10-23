package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// A quick load keeps the original setup identity and cannot turn a run that
// used cheats or changed rules into an eligible new attempt (DESIGN_SURVIVAL
// §8.1, §11). The current host's best is reread rather than saved in the bank.
func TestSurvivalBestSurvivesSaveAndLoad(t *testing.T) {
	smartOn := content.SwitchOn()
	for _, tc := range []struct {
		name        string
		command     session.HumanCommandKind
		smartMakers bool
	}{
		{name: "eligible"},
		{name: "cheated", command: session.HumanATM},
		{name: "changed rules", command: session.HumanGameplay},
		// Loaded under another Smart makers selection than the save
		// recorded (docs/DESIGN_MODS_MUTATORS.md D26).
		{name: "Smart makers changed", smartMakers: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, b, _, _ := startAutosaveBattle(t, true)
			run := b.survivalBest
			if run == nil || run.key == "" || run.excluded != "" || run.err != nil {
				t.Fatalf("fresh eligibility: %+v", run)
			}
			if tc.command != 0 {
				b.noteSurvivalCommand(session.HumanCommand{Kind: tc.command})
			}
			key, content, excluded := run.key, run.content, run.excluded
			before, sim, crt := b.sess.UnitStateChecksum(), *b.sess.SimRNG(), *b.sess.CrtRNG()
			if err := g.writeSlotSave(quicksaveName); err != nil {
				t.Fatal(err)
			}
			if before != b.sess.UnitStateChecksum() || sim != *b.sess.SimRNG() || crt != *b.sess.CrtRNG() {
				t.Fatal("saving host eligibility altered the simulation")
			}
			// A different attempt can improve the record after this save.
			if _, _, err := settings.StoreSurvivalBest(key, settings.SurvivalBest{Score: 50}); err != nil {
				t.Fatal(err)
			}
			if tc.smartMakers {
				g.opts.Mutators.SmartMakers = smartOn
				excluded = "Smart makers changed at load"
			}
			g.quickload()
			if g.battle == b {
				t.Fatal("quick load did not replace the battle")
			}
			run = g.battle.survivalBest
			if run == nil || run.key != key || run.content != content || run.excluded != excluded || run.err != nil {
				t.Fatalf("restored eligibility: %+v, want key %q and exclusion %q", run, key, excluded)
			}
			if excluded == "" && (!run.exists || run.best.Score != 50) {
				t.Fatalf("load did not reread the host's current best: %+v", run)
			}
			g.battle.finishSurvivalBest(frame.ResultView{Ended: true, Kind: "defeat", Survival: &frame.SurvivalResult{Score: 100, Waves: 2}})
			want := int64(100)
			if excluded != "" {
				want = 50
			}
			best, exists, err := settings.LoadSurvivalBest(key)
			if err != nil || !exists || best.Score != want {
				t.Fatalf("best after resumed defeat: %+v, %t, %v; want %d", best, exists, err, want)
			}
		})
	}
}

func TestSurvivalSaveDoesNotInventBestEligibility(t *testing.T) {
	g, b, _, dir := startAutosaveBattle(t, true)
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "missing"},
		{name: "excluded", raw: json.RawMessage(`{"version":1,"excluded":"cheats used"}`)},
	} {
		run, err := restoreSurvivalBest(tc.raw, b.sess, g.cs.fs, false)
		if err != nil || run == nil || run.excluded == "" {
			t.Fatalf("%s: run %+v, err %v", tc.name, run, err)
		}
	}
	for _, raw := range []string{`null`, `{`, `{"version":2}`, `{"version":1}`, `{"version":1,"key":"bad","excluded":"cheats used"}`} {
		if _, err := restoreSurvivalBest(json.RawMessage(raw), b.sess, g.cs.fs, false); err == nil {
			t.Errorf("accepted malformed eligibility %s", raw)
		}
	}
	// Changed content cannot put this score under the original setup's key.
	var rec survivalBestRecord
	if err := json.Unmarshal(b.recordSurvivalBest(), &rec); err != nil {
		t.Fatal(err)
	}
	rec.Content = strings.Repeat("00", 32)
	raw, _ := json.Marshal(rec)
	run, err := restoreSurvivalBest(raw, b.sess, g.cs.fs, false)
	if err != nil || run == nil || run.excluded != "content changed since save" {
		t.Fatalf("changed content eligibility: %+v, %v", run, err)
	}
	// A load under another Smart makers selection than the save recorded
	// leaves the original key too, and says why
	// (docs/DESIGN_MODS_MUTATORS.md D26).
	run, err = restoreSurvivalBest(b.recordSurvivalBest(), b.sess, g.cs.fs, true)
	if err != nil || run == nil || run.excluded != "Smart makers changed at load" {
		t.Fatalf("Smart makers changed at load: %+v, %v", run, err)
	}
	// Invalid host metadata fails while the existing battle remains usable.
	path := filepath.Join(dir, "bad.SAV")
	if err := g.writeSaveWithSidecar(path, func() error { return g.writeBattleSave(path, "bad") }); err != nil {
		t.Fatal(err)
	}
	sc, ok, err := save.ReadSidecar(path)
	if err != nil || !ok {
		t.Fatal(err)
	}
	sc.SurvivalBest = json.RawMessage(`{"version":2}`)
	if err := save.WriteSidecar(path, sc); err != nil {
		t.Fatal(err)
	}
	if err := g.loadRetailSavePath(path); err == nil || g.battle != b {
		t.Fatalf("invalid metadata changed the battle: %v", err)
	}
}
