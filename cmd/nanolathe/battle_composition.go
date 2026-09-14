package main

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// freshBattleRequest is a value-only command adapter around the shared
// authoritative request. Callers construct it completely before handing it
// to the composition seam; the seam never reaches back into menu state
// [08 R-ENTRY-01 §2–§8].
type freshBattleRequest struct {
	value headless.FreshBattleRequest
}

const headlessScenarioSkirmish = headless.ScenarioSkirmish

func unavailableBattleContentError() error {
	return fmt.Errorf("nanolathe: session load failed: content mount is unavailable: logical path <request>, providers searched [none], expected a mounted skirmish map or campaign mission")
}

func directMapBattleRequest(opts Options, cs *contentSet, source BattleSeedSource) (freshBattleRequest, error) {
	cfg := session.DirectSkirmishConfig(opts.Map)
	cfg.UnitLimit = loadedSettings().UnitLimit
	return skirmishBattleRequest(opts, cs, cfg, headless.ScenarioDirectOTA, nil, source)
}

func skirmishBattleRequest(opts Options, cs *contentSet, cfg session.SkirmishConfig, kind headless.ScenarioKind, progress content.Progress, source BattleSeedSource) (freshBattleRequest, error) {
	if cs == nil || cs.fs == nil {
		return freshBattleRequest{}, unavailableBattleContentError()
	}
	if cfg.MapName == "" {
		cfg.MapName = opts.Map
	}
	// ApplyDefaults installs every missing-value default, the configured
	// per-player unit limit among them. That limit rides on the setup record
	// from the persisted preferences through to battle entry, where it sizes
	// the unit pool [05 R-SHARE-01 §7][08 R-SKIR-01 §6]. The command-line
	// override also applies to menu starts and direct map captures.
	cfg.ApplyDefaults()
	if opts.UnitLimit != 0 {
		cfg.UnitLimit = opts.UnitLimit
	}
	seeds := BattleSeeds{Simulation: int32(cfg.RNGSimSeed), CRT: cfg.RNGCrtSeed}
	if source != nil {
		seeds = source.NextBattleSeeds()
	}
	cfg.RNGSimSeed = uint32(seeds.Simulation)
	cfg.RNGCrtSeed = seeds.CRT
	localOwner := session.LocalOwnerForConfig(cfg)
	watching := localOwner >= 0 && localOwner < len(cfg.Players) && cfg.Players[localOwner].IsObserver()
	return freshBattleRequest{value: headless.FreshBattleRequest{
		Kind: kind, Map: cfg.MapName, Difficulty: cfg.Difficulty, Skirmish: cfg,
		LocalOwner: localOwner, Watching: watching,
		SimulationSeed: cfg.RNGSimSeed, CRTSeed: cfg.RNGCrtSeed,
		FS: cs.fs, Progress: progress,
		PresentationWidth: retailScreenW, PresentationHeight: retailScreenH,
	}}, nil
}

func missionBattleRequest(opts Options, cs *contentSet, identity string, difficulty, campaignIndex, campaignSlot int, progress content.Progress, source BattleSeedSource) (freshBattleRequest, error) {
	if cs == nil || cs.fs == nil {
		return freshBattleRequest{}, unavailableBattleContentError()
	}
	var seeds BattleSeeds
	if source != nil {
		seeds = source.NextBattleSeeds()
	}
	return freshBattleRequest{value: headless.FreshBattleRequest{
		Kind: headless.ScenarioCampaign, Mission: identity,
		CampaignIndex: campaignIndex, CampaignSlot: campaignSlot,
		Difficulty: difficulty, LocalOwner: -1,
		SimulationSeed: uint32(seeds.Simulation), CRTSeed: seeds.CRT,
		FS: cs.fs, Progress: progress,
		PresentationWidth: retailScreenW, PresentationHeight: retailScreenH,
	}}, nil
}

func composeAuthoritativeBattle(request freshBattleRequest) (headless.FreshBattle, error) {
	return headless.ComposeFreshBattle(request.value)
}
