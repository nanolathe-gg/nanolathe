//go:build retail

package aikit

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// A battle with one Classic and one Modern computer player, under Strict 3.1
// and under Modern: the Modern player runs the util+tac controller and the
// Classic one its set's own step, the battle plays, a save carries both
// choices and a load with the record restores them and plays on, while a
// load without the record plays both Classic (docs/DESIGN_SESSIONS_AI_SAVE.md
// "Modern AI computer player", "Per-player selection").
func TestMixedControllersPlayAndSurviveALoadRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "The Pass"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("retail map %q is absent", mapName)
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := session.DirectSkirmishConfig(mapName)
			cfg.NumPlayers = 3
			cfg.Players[2] = cfg.Players[1]
			cfg.Players[2].Side, cfg.Players[2].Color, cfg.Players[2].AllyGroup = 0, 2, 4
			cfg.Players[2].AI = ai.ControllerModern
			cfg.Gameplay = mode
			cfg.RNGSimSeed, cfg.RNGCrtSeed = 29, 29
			cfg.ApplyDefaults()
			src, err := session.NewSkirmishWithEntryOptions(fs, cat, cfg, session.SkirmishEntryOptions{})
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			setStep := session.RuleSetForMode(mode).Planner
			check := func(s *session.Session, when string, modern bool) {
				t.Helper()
				if s.Rules.Name != string(mode) {
					t.Fatalf("%s: bound %q, want %q", when, s.Rules.Name, mode)
				}
				if s.AI[1].Planner != setStep || s.AI[1].Controller != ai.ControllerClassic || s.ModernAIPlayer(1) {
					t.Fatalf("%s: the Classic player runs %T", when, s.AI[1].Planner)
				}
				if _, ok := s.AI[1].Ext.(*aikit.Host); ok {
					t.Fatalf("%s: the Classic player built a Modern controller", when)
				}
				h, hosted := s.AI[2].Ext.(*aikit.Host)
				if modern != (hosted && h != nil && s.ModernAIPlayer(2) && s.AI[2].Controller == ai.ControllerModern) {
					t.Fatalf("%s: player 2 Modern = %v, want %v (controller %s, planner %T)", when, !modern, modern, s.AI[2].Controller, s.AI[2].Planner)
				}
			}
			stepBattle(src, 900)
			check(src, "entry", true)

			var carried session.AIControllers
			data, err := json.Marshal(session.RecordAIControllers(src))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &carried); err != nil {
				t.Fatal(err)
			}
			in, err := src.RetailBattleSaveInputs(session.RetailBattleSummary(src, "mixed ai", "0", src.Skirmish.UnitLimit), save.Camera{})
			if err != nil {
				t.Fatalf("battle save inputs: %v", err)
			}
			path := filepath.Join(t.TempDir(), "MIXEDAI.SAV")
			if err := src.WriteRetailSave(path, in); err != nil {
				t.Fatalf("write save: %v", err)
			}
			closeControllers(src)

			deps := session.RetailLoadDeps{FS: fs, Catalog: cat, SimSeed: 31, CRTSeed: 31, UnitLimit: src.Skirmish.UnitLimit, Gameplay: mode, AIControllers: &carried}
			loaded, err := session.LoadRetailSavePath(path, deps)
			if err != nil || loaded.Battle == nil || loaded.Battle.Session == nil {
				t.Fatalf("load: %v", err)
			}
			dst := loaded.Battle.Session
			stepBattle(dst, 600)
			check(dst, "load", true)
			closeControllers(dst)

			deps.AIControllers = nil
			plain, err := session.LoadRetailSavePath(path, deps)
			if err != nil || plain.Battle == nil || plain.Battle.Session == nil {
				t.Fatalf("load without the record: %v", err)
			}
			stepBattle(plain.Battle.Session, 300)
			check(plain.Battle.Session, "load without the record", false)
			closeControllers(plain.Battle.Session)
		})
	}
}

// A Survival battle under Strict 3.1 with one buddy of each kind: the Modern
// buddy plays the survival brain, the Classic buddy the retail step
// (docs/DESIGN_SURVIVAL.md §16).
func TestSurvivalBuddiesPlayTheirOwnAIRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "Painted Desert"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("retail map %q is absent", mapName)
	}
	cfg := session.SurvivalSkirmishConfig(mapName, 2, session.SurvivalOptions{})
	if err := cfg.ApplyComputerAI([]session.ComputerAI{{Row: 3, Controller: ai.ControllerModern}}); err != nil {
		t.Fatal(err)
	}
	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSurvival, Gameplay: gameplay.Strict31, Map: mapName, LocalOwner: -1,
		Skirmish: cfg, Difficulty: 1, SimulationSeed: 5, CRTSeed: 5, FS: fs, Catalog: cat,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := battle.Session
	defer closeControllers(s)
	for s.Clock.GlobalTick < 1800 && s.State != session.StatePostBattle {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	if _, ok := s.AI[1].Ext.(*aikit.Host); ok || s.AI[1].Planner != (ai.RetailPlanner{}) {
		t.Fatalf("the Classic buddy runs %T", s.AI[1].Planner)
	}
	h, ok := s.AI[2].Ext.(*aikit.Host)
	if !ok || h == nil {
		t.Fatal("the Modern buddy has no controller")
	}
	h.Join()
	if got := h.Brain().Name(); got != "survival" {
		t.Fatalf("the Modern buddy plays %s", got)
	}
	if s.ModernAIPlayer(3) {
		t.Fatal("the attacker counts as a Modern AI player")
	}
}
