//go:build retail

package session

// A long single-player skirmish (human idle, two computer players) saved
// and reloaded at intervals, the way a player saving mid-game exercises
// states a 600-tick fixture never reaches. Every checkpoint must restore
// the exact unit census the bank was written from.

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

func TestLongSkirmishSaveLoadRoundTrips(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	for _, mapName := range []string{"ashap plateau", "gods of war"} {
		if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
			t.Logf("map %q absent, skipping", mapName)
			continue
		}
		t.Run(mapName, func(t *testing.T) {
			cfg := DirectSkirmishConfig(mapName)
			cfg.NumPlayers = 3
			cfg.Players[2] = cfg.Players[1]
			cfg.Players[2].Side, cfg.Players[2].Color, cfg.Players[2].AllyGroup = 0, 2, 4
			cfg.Gameplay = gameplay.Modern
			cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
			cfg.ApplyDefaults()
			src, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{})
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			dir := t.TempDir()
			const interval = 3000
			checkpoints := 8
			if testing.Short() {
				checkpoints = 2
			}
			for checkpoint := 1; checkpoint <= checkpoints; checkpoint++ {
				stepRetail(src, interval)
				if src.State != StateBattle {
					t.Logf("battle left StateBattle (%d) at tick %d, stopping", src.State, src.Clock.GlobalTick)
					break
				}
				census := retailUnitCensus(src)
				summary := RetailBattleSummary(src, "long probe", "0", src.Skirmish.UnitLimit)
				in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
				if err != nil {
					t.Fatalf("tick %d (%d units): save inputs: %v", src.Clock.GlobalTick, len(census), err)
				}
				path := filepath.Join(dir, "LONG.SAV")
				if err := src.WriteRetailSave(path, in); err != nil {
					t.Fatalf("tick %d (%d units): write save: %v", src.Clock.GlobalTick, len(census), err)
				}
				result, err := LoadRetailSavePath(path, RetailLoadDeps{
					FS: fs, Catalog: cat, SimSeed: 7, CRTSeed: 7,
					UnitLimit: src.Skirmish.UnitLimit, Gameplay: gameplay.Modern,
				})
				if err != nil {
					t.Fatalf("tick %d (%d units): load save: %v", src.Clock.GlobalTick, len(census), err)
				}
				dst := result.Battle.Session
				got := retailUnitCensus(dst)
				diffs := 0
				for id, want := range census {
					if g, ok := got[id]; !ok || g != want {
						if diffs < 3 {
							t.Logf("tick %d unit %d: got %q want %q (present=%v)", src.Clock.GlobalTick, id, g, want, ok)
						}
						diffs++
					}
				}
				t.Logf("tick %d: %d units, %d census diffs, restored %d units", src.Clock.GlobalTick, len(census), diffs, len(got))
				if len(got) != len(census) {
					t.Fatalf("tick %d: restored %d units, want %d", src.Clock.GlobalTick, len(got), len(census))
				}
				if diffs != 0 {
					t.Fatalf("tick %d: %d unit census diffs after restore", src.Clock.GlobalTick, diffs)
				}
			}
		})
	}
}
