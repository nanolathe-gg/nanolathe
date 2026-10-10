//go:build retail

package replay_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"

	// The Modern computer player's think step, which the game links.
	_ "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// A retail skirmish with a Classic and a Modern computer player, recorded
// through a minute of one-to-five-tick and paused pumps, plays back headless
// to the same state with every checksum matched. The Modern player's
// private generator and background thinking are its own; nothing about them
// is recorded, and the battle still reproduces.
func TestSinglePlayerRecordingPlaysBackRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "ashap plateau"
	cfg := session.DirectSkirmishConfig(mapName)
	cfg.NumPlayers = 3
	cfg.Players[2] = cfg.Players[1]
	cfg.Players[2].Side, cfg.Players[2].Color, cfg.Players[2].AllyGroup = 0, 2, 4
	cfg.Players[1].AI = ai.ControllerClassic
	cfg.Players[2].AI = ai.ControllerModern
	request := headless.FreshBattleRequest{Kind: headless.ScenarioSkirmish, Map: mapName, Skirmish: cfg, Gameplay: gameplay.Modern,
		Difficulty: 2, SimulationSeed: 7, CRTSeed: 5006, FS: fs, Catalog: cat, LocalOwner: -1}
	rec := recordSinglePlayer(t, request, 600)
	if rec.ticks < 1500 {
		t.Fatalf("recorded %d ticks", rec.ticks)
	}
	t.Logf("single-player retail, Classic and Modern computers, %d ticks: %d bytes", rec.ticks, len(rec.data))
	checkSinglePlayerPlayback(t, rec, replay.Content{FS: fs, Catalog: cat})
}
