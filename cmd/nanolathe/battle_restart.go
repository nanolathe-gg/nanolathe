package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// battleRestartState is the presentation state carried only while the
// authored RESTART.GUI child is open. The request it produces is consumed by
// the frontend owner outside an authoritative sub-tick [07 R-FE-01 §7]
// [08 R-CAMP-01 §8].
type battleRestartState struct {
	panel *ui.Panel

	missionName  string
	missionName1 string
	difficulty   int
}

// battleRestartRequest contains the retained entry identity the frontend
// re-entry owner needs after it tears down the old battle. Skirmish retains
// the complete setup record so its map, player count and row conversion are
// not reconstructed from the old world; campaign retains the campaign path
// and selected mission index for the required reopen-then-Set sequence
// [08 R-CAMP-01 §8].
type battleRestartRequest struct {
	Campaign bool

	Difficulty     int
	CampaignPath   string
	CampaignIndex  int
	Skirmish       session.SkirmishConfig
	Controllers    [session.SkirmishMaxPlayers]int
	ControllersSet bool
}

// restartDifficulty bounds a value to RESTART.GUI's three authored stages.
// Session setup uses the same 0=Easy, 1=Medium, 2=Hard vocabulary.
func restartDifficulty(value int) int {
	if value < 0 {
		return 0
	}
	if value > 2 {
		return 2
	}
	return value
}

func battleRestartMissionName(b *battleSession) string {
	if b == nil || b.sess == nil || b.sess.Mission == nil {
		return ""
	}
	if b.sess.Mission.Type == mission.TypeCampaign {
		return b.sess.Mission.CampaignMissionName
	}
	return b.sess.Skirmish.MapName
}

func battleRestartDifficulty(b *battleSession) int {
	if b == nil || b.sess == nil {
		return 0
	}
	if b.sess.Mission != nil && b.sess.Mission.Type == mission.TypeCampaign {
		return restartDifficulty(b.sess.Mission.Difficulty)
	}
	return restartDifficulty(b.sess.Skirmish.Difficulty)
}

// openBattleRestartDialog installs the shared widget owner, wraps the
// mission/map name into the two authored labels and focuses Difficulty. It is
// called only after BattleState has replaced EXITMENU with RESTART.GUI
// [07 R-FE-01 §7].
func (b *battleSession) openBattleRestartDialog() {
	if b == nil {
		return
	}
	state := battleRestartState{difficulty: battleRestartDifficulty(b)}
	if b.hud != nil && b.hud.restartWin != nil {
		state.panel = ui.NewPanel(b.hud.restartWin)
		state.setMissionName(b.hud, battleRestartMissionName(b))
		if index := state.panel.Index("Difficulty"); index >= 0 {
			state.panel.SetStageAt(index, state.difficulty)
			state.panel.SetFocus(index)
		}
	}
	b.restart = state
}

func (s *battleRestartState) setMissionName(h *retailBattleHUD, name string) {
	if s == nil {
		return
	}
	measure := func(text string) int { return len(text) }
	if h != nil {
		switch {
		case h.modalFont != nil:
			measure = func(text string) int { return retailGAFTextWidth(h.modalFont, text) }
		case h.guiFont != nil:
			measure = func(text string) int { return client.MeasureText(h.guiFont, text) }
		}
	}
	width := 0
	if h != nil && h.restartWin != nil {
		if index := h.restartWin.GadgetIndex("MISSIONNAME"); index >= 0 {
			width = int(h.restartWin.PlacedRect(index).W)
		}
	}
	if width <= 0 {
		// TODO(T23): tests without the authored record have no label width;
		// use the observed stock width only for that synthetic presentation.
		width = 200
	}
	wrapped := retailWordWrap(name, width, measure)
	parts := strings.SplitN(wrapped, "\r\n", 2)
	s.missionName = parts[0]
	s.missionName1 = ""
	if len(parts) == 2 {
		s.missionName1 = parts[1]
	}
}

// restartGadgetText returns the dynamic labels that RESTART.GUI writes at
// open. All other gadget text remains the immutable authored record.
func (b *battleSession) restartGadgetText(window *gui.Window, index int) string {
	if b == nil || b.hud == nil || window == nil || window != b.hud.restartWin || index < 0 || index >= len(window.Gadgets) {
		return ""
	}
	gadget := window.Gadgets[index]
	switch gui.CallbackName(gadget.Name) {
	case "MISSIONNAME":
		return b.restart.missionName
	case "MISSIONNAME1":
		return b.restart.missionName1
	case "Difficulty":
		stage := b.restartGadgetStage(index)
		if stage >= 0 && stage < len(gadget.Labels) {
			return gadget.Labels[stage]
		}
	}
	return ""
}

// restartGadgetStage supplies RESTART.GUI's selected stage to the painter.
func (b *battleSession) restartGadgetStage(index int) int {
	if b == nil || b.restart.panel == nil {
		return 0
	}
	return restartDifficulty(b.restart.panel.StageAt(index))
}

// handleBattleRestartInput adapts RESTART.GUI to the common indexed widget
// service. It owns only the dialog's focus, capture and three-stage button;
// the modal transition and fresh session entry remain at their established
// owners [07 R-WGT-01 §1][07 R-WGT-01 §3][08 R-CAMP-01 §8].
func (b *battleSession) handleBattleRestartInput(in *input.State, cl *client.Client) {
	if b == nil || b.restart.panel == nil || in == nil || in.Mouse == nil {
		return
	}
	p := b.restart.panel
	result := b.serviceBattleChildPanel(p, b.hud.restartWin, b.hud.modalPage(b.hud.restartWin), in)
	if result.Fired {
		b.activateBattleRestartGadget(result.FiredIndex, cl)
	}
}

func (b *battleSession) activateBattleRestartGadget(index int, cl *client.Client) {
	if b == nil || b.restart.panel == nil || b.restart.panel.Window == nil || index < 0 || index >= len(b.restart.panel.Window.Gadgets) {
		return
	}
	if gui.CallbackName(b.restart.panel.Window.Gadgets[index].Name) == "Difficulty" {
		b.restart.difficulty = restartDifficulty(b.restart.panel.StageAt(index))
	}
	b.activateBattleMenuButton(b.restart.panel.Window.Gadgets[index].Name, cl)
}

// battleRestartRequest captures the old battle's entry identity before the
// consumer tears it down. It deliberately does not call Session.Retry: retail
// enters a fresh normal load with fresh entry RNG state [08 R-CAMP-01 §8].
func (b *battleSession) battleRestartRequest() (battleRestartRequest, bool) {
	if b == nil || b.sess == nil || b.sess.Mission == nil {
		return battleRestartRequest{}, false
	}
	request := battleRestartRequest{Difficulty: restartDifficulty(b.restart.difficulty)}
	if b.sess.Mission.Type == mission.TypeCampaign {
		if b.sess.Mission.CampaignPath == "" || b.sess.Mission.CampaignIndex < 0 {
			return battleRestartRequest{}, false
		}
		request.Campaign = true
		request.CampaignPath = b.sess.Mission.CampaignPath
		request.CampaignIndex = b.sess.Mission.CampaignIndex
		return request, true
	}
	if b.sess.Mission.Type != mission.TypeSkirmish {
		return battleRestartRequest{}, false
	}
	request.Skirmish = b.sess.Skirmish
	if b.shell != nil && b.shell.retailControllersSet && !b.shell.importedRetailBattle {
		request.Skirmish = b.shell.setup
		request.Controllers, request.ControllersSet = b.shell.retailControllers, true
	}
	request.Skirmish.Difficulty = request.Difficulty
	if request.Skirmish.MapName == "" || request.Skirmish.NumPlayers <= 0 {
		return battleRestartRequest{}, false
	}
	return request, true
}

// acceptBattleRestart hands the prepared request to the frontend lifecycle
// owner after RESTART's constant-success disc gate and archive remount. Enter
// and Escape produce no ordinary-mode token, so this child is pointer-fired
// only; its header defaults do not create an input exception [07 §2]
// [08 R-CAMP-01 §8].
func (b *battleSession) acceptBattleRestart(cl *client.Client) {
	request, ok := b.battleRestartRequest()
	if !ok || b == nil || b.restartBattle == nil {
		return
	}
	if b.shell != nil && !b.shell.prepareBattleRestartContent() {
		return
	}
	b.restartBattle(cl, request)
}

// prepareBattleRestartContent models the dialog's disc gate and archive
// remount. The observed gate compares its result to one and receives one
// immediately, then reopens the archive set before the request is raised.
// This build has no physical-disc service, so the gate is the established
// success value; openContent is the existing remount boundary [08 R-CAMP-01
// §8][02 §1].
func (g *gameShell) prepareBattleRestartContent() bool {
	if g == nil || g.cs == nil || g.cs.root == "" {
		return false
	}
	// g.opts.Root may have been redirected to SAVEGAME storage. Archive
	// remount uses the original content roots retained by the mounted set.
	// The resolved content profile rides along, so a remount keeps the
	// directory table this run selected rather than re-detecting it
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	remount := Options{Root: g.cs.root, Roots: g.cs.roots, Remaster: g.opts.Remaster, ContentProfile: g.cs.profile}
	if !g.cs.manualRoots {
		// Remount the base roots and select the running mod explicitly — none
		// when none is running — so the fresh set mounts what the battle ran
		// on and still knows which mod that was, never the saved choice
		// (docs/DESIGN_MODS_MUTATORS.md §4.3).
		remount.Roots, remount.Mod, remount.ModSet, remount.modBaseRoots = g.cs.baseRoots, g.cs.modSelector(), true, true
	}
	fresh, err := openContent(remount)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return false
	}
	old := g.cs
	g.cs = fresh
	if old != nil {
		_ = old.Close()
	}
	return true
}

// restartBattle is the frontend-owned consumer of RESTART.GUI's prepared
// request. It retires the old session before returning to the ordinary entry
// routes; it never restores the old simulation or its RNG [08 R-CAMP-01 §8].
func (g *gameShell) restartBattle(cl *client.Client, request battleRestartRequest) {
	if g == nil || g.battle == nil || g.battle.sess == nil {
		return
	}
	old := g.battle.sess
	g.stopOrdinaryAudio()
	g.teardownBattle(cl)
	if request.Campaign {
		// teardown writes the current mission W/L mark. Keep that updated bank
		// while re-opening the campaign; a restart must not reset its marks.
		g.campaignProgress = old.Progress
		g.campaignProgressSet = true
		g.missionDifficultyValue = request.Difficulty
		g.openMenu(modeMenuMission)
		g.bindFrontendClient(cl)
		g.restartCampaignEntry(request)
		return
	}
	g.restartSkirmishEntry(request)
}

// restartCampaignEntry performs the retail reopen-then-Set sequence. Loading
// index zero is the campaign reopen; openCampaignBriefing loads the selected
// index after the shell has installed it [08 R-CAMP-01 §§1, 8].
func (g *gameShell) restartCampaignEntry(request battleRestartRequest) {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		reportRetailMessageError(g.showRetailMessage(unavailableBattleContentError().Error()))
		return
	}
	if _, err := mission.LoadCampaignWithSink(g.cs.fs, request.CampaignPath, 0, request.Difficulty, 0, nil); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	options := g.campaignOptions
	optionIndex := -1
	for i := range options {
		// The request is an entry identity, not a UI search key. Preserve its
		// exact bytes, including case and any NUL termination, at this seam.
		if options[i].Path == request.CampaignPath {
			optionIndex = i
			break
		}
	}
	if optionIndex < 0 {
		campaign, err := mission.DiscoverCampaign(g.cs.fs, request.CampaignPath)
		if err != nil || campaign == nil {
			if err == nil {
				err = unavailableBattleContentError()
			}
			reportRetailMessageError(g.showRetailMessage(err.Error()))
			return
		}
		options = append(append([]mission.Campaign(nil), options...), *campaign)
		optionIndex = len(options) - 1
	}
	missionUIIndex, ok := campaignMissionUIIndex(options, optionIndex, request.CampaignIndex)
	if !ok {
		reportRetailMessageError(g.showRetailMessage("no mission selected"))
		return
	}
	g.campaignOptions = options
	g.campaignIdx = optionIndex
	g.missionIdx = missionUIIndex
	g.openCampaignBriefing()
}

// restartSkirmishEntry restores the retained setup through the same row
// conversion used by SKIRMISH.GUI, then enters a fresh loading request. The
// map is deliberately not preflighted here; its load belongs to that normal
// entry path [08 R-CAMP-01 §8].
func (g *gameShell) restartSkirmishEntry(request battleRestartRequest) {
	if g == nil {
		return
	}
	// A Survival battle restarts from its own setup, which already holds the
	// attacker row; it never passes through the skirmish rows.
	if request.Skirmish.Survival.Enabled {
		fresh, err := skirmishBattleRequest(g.opts, g.cs, request.Skirmish, headless.ScenarioSurvival, nil, newBattleSeedSource(g.opts))
		if err != nil {
			reportRetailMessageError(g.showRetailMessage(err.Error()))
			return
		}
		g.lastBattleSurvival = true
		g.beginFreshBattleLoad(request.Skirmish.MapName, modeMenuSkirmish, fresh, nil)
		return
	}
	g.setup = request.Skirmish
	g.retailControllers = [session.SkirmishMaxPlayers]int{}
	for i := 0; i < g.setup.NumPlayers && i < len(g.setup.Players); i++ {
		if g.setup.Players[i].Controller == session.SkirmishDefaultController {
			g.retailControllers[i] = 1
		} else {
			g.retailControllers[i] = 2
		}
	}
	g.retailControllersSet = true
	if request.ControllersSet {
		g.retailControllers = request.Controllers
	}
	cfg := g.skirmishConfigForStart(request.Skirmish.MapName)
	fresh, err := skirmishBattleRequest(g.opts, g.cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(g.opts))
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	g.beginFreshBattleLoad(cfg.MapName, modeMenuSkirmish, fresh, nil)
}
