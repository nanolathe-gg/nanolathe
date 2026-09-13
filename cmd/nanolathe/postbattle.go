package main

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func (b *battleSession) postBattleNow() uint32 {
	if b == nil || b.postBattleClock <= 0 {
		return 0
	}
	return uint32(b.postBattleClock)
}

// consumeBriefingAudio is the frontend side of the one semantic audio owner.
// Delayed streaming is scheduled by that owner and remains silent only when
// the optional output or authored media is unavailable [03 R-AUD-02 §1].
func (g *gameShell) consumeBriefingAudio(effects []BriefingAudioEffect) {
	if g == nil || g.audioOwner == nil {
		return
	}
	now := uint32(briefingPresentationTick(g.briefingNowMS))
	for _, effect := range effects {
		switch effect.Kind {
		case BriefingAudioStart:
			g.audioOwner.StartStream(effect.Path, effect.Volume, uint32(effect.Delay), now)
		case BriefingAudioStop:
			g.audioOwner.StopStream()
		}
	}
	g.audioOwner.TickStream(now)
}

func (g *gameShell) adoptCampaignProgress(sess *session.Session) {
	if g == nil || sess == nil || sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign || !g.campaignProgressSet {
		return
	}
	sess.Progress = g.campaignProgress
	// The copy is consumed by this battle entry. Keeping the flag set is
	// deliberate: a retry/successor selected from this same results surface
	// must preserve the same bank value.
}

// ensurePostBattleController installs exactly one controller from the first
// frozen terminal frame. All provenance needed after that point is copied
// before the controller starts stepping [03 §2.4][08 R-CAMP-01 §6].
func (b *battleSession) ensurePostBattleController() {
	if b == nil || b.postBattle != nil {
		return
	}
	view := b.resultView()
	if !view.Ended || b.sess == nil {
		return
	}
	cfg := session.PostBattleConfig{
		Kind:         session.PostBattleSkirmish,
		MissionIndex: -1,
		// The portable movie player also supports windowed presentation.
		// Admit the retail movie route independently of host window mode
		// [08 R-CAMP-01 §6]; individual absent files skip in the shell.
		EndingMedia:       b.shell != nil,
		CampaignCDOK:      true, // no CD backend exists, so state 8 cannot strand
		ProgressCommitted: false,
	}
	if b.sess.Mission != nil && b.sess.Mission.Type == mission.TypeCampaign {
		cfg.Kind = session.PostBattleCampaign
		cfg.CampaignPath = b.sess.Mission.CampaignPath
		cfg.Campaign = b.sess.Mission.CampaignPath
		cfg.Mission = b.sess.Mission.CampaignMissionName
		cfg.Map = b.sess.Mission.TerrainKey
		cfg.MissionIndex = b.sess.Mission.CampaignIndex
		if cfg.MissionIndex < 0 {
			cfg.MissionIndex = b.sess.CampaignSlot
		}
		cfg.Difficulty = b.sess.Mission.Difficulty
		// Players is the Summary count a between-missions save carries; the
		// load screen renders `???` for a zero value [08 R-SAVE-02 §3].
		cfg.Players = int(session.RetailPlayerCount(b.sess))
		if cfg.Difficulty < 0 && b.shell != nil {
			cfg.Difficulty = b.shell.missionDifficulty()
		}
		cfg.Progress = &b.sess.Progress
		cfg.ProgressCommitted = true
		cfg.LocalSide, _ = b.sess.SideForOwner(int(b.sess.LocalOwner))
		if b.shell != nil {
			cfg.DifficultyOut = &b.shell.missionDifficultyValue
		}
		if b.fs != nil && cfg.CampaignPath != "" && cfg.MissionIndex >= 0 {
			if c, err := mission.DiscoverCampaign(b.fs, cfg.CampaignPath); err == nil {
				for _, stub := range c.Missions {
					if stub.Index != cfg.MissionIndex+1 {
						continue
					}
					cfg.HasNext = true
					cfg.NextMission = stub.Name
					break
				}
			}
		}
		if b.sess.Mission.OTA != nil {
			g := mission.DecodeMissionGlobals(b.sess.Mission.OTA.Global)
			cfg.Nomovie = g.NoMovie != 0
			cfg.Glamour = g.Glamour
			cfg.GlamourSound = briefingMediaPath(g.GlamourSound, "wav")
			// The PCX decoder is an optional-media boundary. A failed decode
			// leaves GlamourLoaded false and the controller advances to ENDMSN.
			if path := postBattleGlamourPath(g.Glamour); path != "" && b.fs != nil {
				if image, err := formats.LoadPCXFile(b.fs, path); err == nil && image != nil {
					cfg.GlamourLoaded = true
					b.postBattleGlamour = image
				}
			}
		}
	}
	b.postBattle = session.NewPostBattleController(view, cfg)
	b.postBattleLastUnit = -1
}

func postBattleGlamourPath(name string) string {
	name = strings.TrimLeft(name, `/\`)
	if name == "" {
		return ""
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return "bitmaps/glamour/" + name + ".pcx"
}

// stepPostBattle advances only whole 30-Hz presentation units. Effects are
// drained by index, so a repaint or repeated input edge cannot replay one
// semantic side effect [03 §2.4][I6].
func (b *battleSession) stepPostBattle(delta float64, in *input.State, cl *client.Client) {
	if b == nil || b.postBattle == nil {
		return
	}
	if b.shell != nil && (b.shell.saveLoadPanelActive() || (b.shell.frontend != nil && b.shell.frontend.Panels.Modal() != nil)) {
		// The save/load dialog owns input while it is up [07 R-FE-01 §8].
		b.shell.menuInput(cl)
		return
	}
	if delta > 0 {
		b.postBattleClock += delta * 30
	}
	now := int64(b.postBattleClock)
	for b.postBattleLastUnit < now && !b.postBattle.Routed() {
		b.postBattleLastUnit++
		b.postBattle.Step(uint32(b.postBattleLastUnit), false)
		b.consumePostBattleEffects(uint32(b.postBattleLastUnit), cl)
	}
	if b.postBattle.Routed() {
		return
	}
	// Glamour input is a semantic key/click only while the glamour state is
	// active. ENDMSN controls are handled by the authored panel below.
	if b.postBattle.State() == session.PostBattleGlamour {
		if resultKeyPressed(in) && b.postBattle.Handle(session.PostBattleControlKey, uint32(now)) {
			b.consumePostBattleEffects(uint32(now), cl)
		} else if mouse, _ := publishedPointer(in); in != nil && in.Mouse != nil && mouse.Pressed(input.MouseButtonLeft) && b.postBattle.Handle(session.PostBattleControlMouse, uint32(now)) {
			b.consumePostBattleEffects(uint32(now), cl)
		}
		return
	}
	if b.postBattle.State() != session.PostBattleEndMission || b.hud == nil {
		return
	}
	b.prepareResultPanel()
	name, ok := b.hud.resultControlName(in)
	if !ok {
		return
	}
	switch gui.CallbackName(name) {
	case "Difficulty":
		if b.postBattle.Handle(session.PostBattleControlDifficulty, uint32(now)) {
			if b.shell != nil {
				b.shell.setup.Difficulty = b.postBattle.Summary().Difficulty
				b.shell.playMenuCue("SKirmish")
			}
			b.prepareResultPanel()
		}
	case "Start", "Missions":
		if b.prepareResultMissionSelection() {
			b.doResultAction(ui.ResultActionContinue, cl)
		}
	case "SaveGame":
		// ENDMSN's SaveGame opens the save dialog, and a save taken there is
		// the between-missions bank that carries campaign progress across
		// process runs [08 R-CAMP-01 §8] [07 R-FE-01 §10].
		if b.shell != nil {
			b.shell.openSaveLoadScreenReporting(saveScreenMode, saveLoadFromResults)
		}
	case "LoadGame":
		if b.shell != nil {
			b.shell.openSaveLoadScreenReporting(loadScreenMode, saveLoadFromResults)
		}
	default:
		if action := resultActionForControl(name); action != ui.ResultActionNone {
			b.doResultAction(action, cl)
		}
	}
}

// prepareResultMissionSelection validates the selected authored mission before
// the controller marks Start routed. A failed load leaves ENDMSN usable
// [08 R-CAMP-01 §8].
func (b *battleSession) prepareResultMissionSelection() bool {
	if b == nil || b.shell == nil || b.sess == nil || b.sess.Mission == nil || b.hud == nil || b.hud.resultPanel == nil {
		return false
	}
	list := b.hud.resultPanel.ListAt(b.hud.resultPanel.Index("Missions"))
	campaign, err := mission.DiscoverCampaign(b.fs, b.sess.Mission.CampaignPath)
	if err != nil || campaign == nil || list == nil || list.Selected() < 0 || list.Selected() >= len(campaign.Missions) {
		return false
	}
	index := campaign.Missions[list.Selected()].Index
	if _, err := mission.LoadCampaignWithSink(b.fs, campaign.Path, index, b.postBattle.Summary().Difficulty, 0, nil); err != nil {
		reportRetailMessageError(b.shell.showRetailMessage(err.Error()))
		return false
	}
	if b.shell.loadBriefingPanel(BriefingPlanet{}) == nil {
		err := b.shell.briefingUnavailableError()
		if messageErr := b.shell.showRetailMessage(err.Error()); messageErr != nil {
			reportRetailMessageError(err)
		}
		return false
	}
	return b.postBattle.SelectMission(index)
}

func (b *battleSession) consumePostBattleEffects(now uint32, cl *client.Client) {
	if b == nil || b.postBattle == nil {
		return
	}
	effects := b.postBattle.Effects()
	var movies []string
	for b.postBattleEffectPos < len(effects) {
		effect := effects[b.postBattleEffectPos]
		b.postBattleEffectPos++
		switch effect.Kind {
		case session.PostBattleEffectStopMusic:
			if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
				b.sess.Audio.Music.Stop()
			}
			// The results controller leaves the darkening fade through the same
			// 640x480 enforcement routine the shell loader uses, before the
			// outcome art is prepared: the glamour picture and `ENDMSN` are
			// front-end screens and run at 640x480 whatever display mode the
			// battle was at [07 R-FE-02 §2][07 R-FE-01 §10].
			if b.shell != nil {
				b.shell.applyDisplaySize(cl, retailScreenW, retailScreenH)
			}
		case session.PostBattleEffectGlamourSound:
			if b.sess != nil && b.sess.Audio != nil {
				b.sess.Audio.StartStream(effect.Resource, 0, 60, now)
			}
		case session.PostBattleEffectOutcomeArt:
			// Outcome1/Outcome0 are ENDMSN's own background bitmaps: the
			// preparer only pre-caches them, and the population step below is
			// what installs one. Only a glamour resource is decoded here
			// [08 R-CAMP-01 §6][08 R-CAMP-01 §8].
			if effect.Resource != "Outcome1" && effect.Resource != "Outcome0" {
				if image, ok := b.loadPostBattleImage(effect.Resource); ok {
					b.postBattleGlamour = image
				}
			}
		case session.PostBattleEffectPopulateEndMission:
			b.prepareResultPanel()
			b.installEndMissionBackground(cl)
		case session.PostBattleEffectGlamourFadeStep:
			b.advancePostBattleGlamourFade(now, cl)
		case session.PostBattleEffectFadeOutStep:
			b.postBattleFadeLevels = append(b.postBattleFadeLevels, effect.Level)
		case session.PostBattleEffectEndingMovie:
			movies = append(movies, "data/"+effect.Resource)
		case session.PostBattleEffectRouteRouter:
			// Teardown clears b.shell. Keep the returning shell and start its
			// sequence only after battle resources and audio are retired.
			shell := b.shell
			b.routePostBattleRouter(cl)
			if shell != nil && len(movies) > 0 {
				reportRetailMessageError(shell.startMovieSequence(cl, movies...))
			}
			return
		}
	}
	if b.sess != nil && b.sess.Audio != nil {
		b.sess.Audio.TickStream(now)
	}
}

// installEndMissionBackground is the population step's background install:
// `outcome1` when the results route to another mission, `outcome0` otherwise,
// handed to the bitmap cache with `ENDMSN` open. That call both makes the
// bitmap the window's background and installs the bitmap's own 256-entry
// palette — which is what takes the display back off the glamour image's
// palette, since nothing else restores it between the glamour fade and the
// statistics screen [08 R-CAMP-01 §8].
func (b *battleSession) installEndMissionBackground(cl *client.Client) {
	if b == nil || b.shell == nil || b.fs == nil || b.postBattle == nil {
		return
	}
	name := "outcome0"
	if _, route := b.postBattle.NextMission(); route {
		name = "outcome1"
	}
	image, err := formats.LoadPCXFile(b.fs, "bitmaps/"+name+".pcx")
	if err != nil || image == nil {
		// Optional media: a missing outcome bitmap leaves the window on its
		// authored panel art, the same skip every other frontend bitmap takes.
		return
	}
	b.shell.resultBackground = image
	base := b.hud
	if base == nil || base.pal == nil {
		return
	}
	if b.postBattleNormalPal == nil {
		b.postBattleNormalPal = base.pal
	}
	// Only the 256 display entries move. The GUI colour map and the light
	// tables are built once at start-up and are not rebuilt by a background
	// install [03 §4.3].
	pal := *base.pal
	for i, entry := range image.Palette {
		if i >= len(pal.Base) {
			break
		}
		pal.Base[i] = [4]byte{entry.R, entry.G, entry.B, 0}
	}
	b.shell.resultBackgroundPal = pal
	if cl != nil {
		cl.SetPalette(&b.shell.resultBackgroundPal)
	}
}

func (b *battleSession) loadPostBattleImage(resource string) (*formats.PCX, bool) {
	if b == nil || b.fs == nil || resource == "" {
		return nil, false
	}
	path := postBattleGlamourPath(resource)
	if path == "" {
		return nil, false
	}
	image, err := formats.LoadPCXFile(b.fs, path)
	return image, err == nil && image != nil
}

func (b *battleSession) routePostBattleRouter(cl *client.Client) {
	b.restorePostBattlePalette(cl)
	if b == nil || b.shell == nil {
		if b != nil && b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		return
	}
	b.shell.returnFromBattle(cl)
}

// routePostBattleStart returns to the campaign briefing for both successor
// and defeat-retry selections. No battle is loaded directly from ENDMSN.
func (b *battleSession) routePostBattleStart(cl *client.Client) {
	if b == nil || b.postBattle == nil || b.shell == nil || b.sess == nil {
		return
	}
	index, ok := b.postBattle.SelectedMission()
	if !ok {
		return
	}
	b.restorePostBattlePalette(cl)
	// The battle's own teardown clears every back-reference it holds, `shell`
	// included, so the shell is taken here and used through this local for the
	// rest of the route. Reaching for `b.shell` after the teardown gives a nil
	// receiver whose methods are all no-ops: that is what left the front end on
	// its battle mode with no battle installed — a screen that draws nothing
	// and accepts no input, the black screen the play-test hit after Start.
	shell := b.shell
	shell.campaignProgress = b.sess.Progress
	shell.campaignProgressSet = true
	side, known := b.sess.SideForOwner(int(b.sess.LocalOwner))
	if !known {
		return
	}
	selection, err := shell.resolveCampaignSelection(b.sess.Mission.CampaignPath, index, side, shell.missionDifficulty())
	if err != nil {
		reportRetailMessageError(shell.showRetailMessage(err.Error()))
		return
	}
	shell.installCampaignSelection(selection)
	shell.teardownBattle(cl)
	// `ENDMSN`'s `Start`/`Missions` leaves host mode 7 for the shell
	// controller, whose next pass opens `MSNBRIEF` under host mode 2
	// [07 R-FE-01 §10]. The mission panel is the screen that briefing's own
	// `PrevMenu` returns to, and opening it is also what puts the surface back
	// to 640x480.
	shell.openMenu(modeMenuMission)
	shell.openCampaignBriefing()
	shell.bindFrontendClient(cl)
}

func campaignMissionUIIndex(campaigns []mission.Campaign, campaignIndex, authoredIndex int) (int, bool) {
	if campaignIndex < 0 || campaignIndex >= len(campaigns) {
		return 0, false
	}
	for i, stub := range campaigns[campaignIndex].Missions {
		if stub.Index == authoredIndex {
			return i, true
		}
	}
	return 0, false
}

func postBattlePaletteBytes(pcx *formats.PCX) (out [1024]byte) {
	if pcx == nil {
		return out
	}
	for i, color := range pcx.Palette {
		base := i * 4
		// PCX supplies RGB only. The fourth PALETTEENTRY byte is the saved
		// display-palette pad/flags byte, initialized to zero [fmt pcx].
		out[base], out[base+1], out[base+2], out[base+3] = color.R, color.G, color.B, 0
	}
	return out
}

func postBattleFadeStep(cur, dst byte) int16 {
	delta := int(dst) - int(cur)
	if delta > 0 {
		step := delta / 5
		if step < 1 {
			step = 1
		}
		return int16(step)
	}
	if delta < 0 {
		step := delta / 5
		if step > -1 {
			step = -1
		}
		return int16(step)
	}
	return 0
}

func (b *battleSession) initPostBattleGlamourFade(cl *client.Client) {
	if b == nil || b.postBattleFadeReady || b.postBattleGlamour == nil || b.hud == nil || b.hud.pal == nil {
		return
	}
	b.postBattleNormalPal = b.hud.pal
	// The fade runs black → the glamour image's own palette. The image is
	// blitted once, at full opacity, into a display whose palette has just been
	// zeroed; only the palette moves after that, five steps per byte, so the
	// picture rises out of black and ends in its authored colours
	// [08 R-CAMP-01 §6].
	b.postBattleFadeCur = [1024]byte{}
	b.postBattleFadeDst = postBattlePaletteBytes(b.postBattleGlamour)
	for i := range b.postBattleFadeCur {
		b.postBattleFadeStep[i] = postBattleFadeStep(b.postBattleFadeCur[i], b.postBattleFadeDst[i])
	}
	b.postBattleFadePal = *b.postBattleNormalPal
	b.postBattleFadeReady = true
	if cl != nil {
		b.installPostBattleFadePalette(cl)
	}
}

func (b *battleSession) installPostBattleFadePalette(cl *client.Client) {
	if b == nil || cl == nil || !b.postBattleFadeReady {
		return
	}
	for i := range b.postBattleFadeCur {
		b.postBattleFadePal.Base[i/4][i%4] = b.postBattleFadeCur[i]
	}
	cl.SetPalette(&b.postBattleFadePal)
}

func (b *battleSession) advancePostBattleGlamourFade(now uint32, cl *client.Client) {
	b.initPostBattleGlamourFade(cl)
	if b == nil || !b.postBattleFadeReady {
		return
	}
	done := true
	for i := range b.postBattleFadeCur {
		cur, dst := int(b.postBattleFadeCur[i]), int(b.postBattleFadeDst[i])
		step := int(b.postBattleFadeStep[i])
		if step > 0 && cur+step > dst {
			cur = dst
		} else if step < 0 && cur+step < dst {
			cur = dst
		} else {
			cur += step
		}
		b.postBattleFadeCur[i] = byte(cur)
		if cur != dst {
			done = false
		}
	}
	b.installPostBattleFadePalette(cl)
	if done {
		b.postBattle.GlamourFadeDone(now)
	}
}

// restorePostBattlePalette is the results screen's own cleanup: closing
// `ENDMSN` frees the palette buffers and the display goes back to the palette
// the front end runs on [08 R-CAMP-01 §6]. The outcome bitmap goes with them,
// so a later results screen never draws the previous one's background.
func (b *battleSession) restorePostBattlePalette(cl *client.Client) {
	if b == nil {
		return
	}
	if b.shell != nil {
		b.shell.resultBackground = nil
	}
	if cl == nil || b.postBattleNormalPal == nil {
		return
	}
	cl.SetPalette(b.postBattleNormalPal)
	b.postBattleNormalPal = nil
	b.postBattleFadeReady = false
}

// applyPostBattleFade reapplies the finite emitted sequence after the client
// has composed a fresh committed frame. Applying levels here avoids both the
// viewer-step overwrite and accidental compounding across host frames.
func (b *battleSession) applyPostBattleFade(cl *client.Client) {
	if b == nil || cl == nil || b.hud == nil {
		return
	}
	w, h := cl.Size()
	for _, level := range b.postBattleFadeLevels {
		cl.UIShadeRect(b.hud.pal, 0, 0, w, h, level)
	}
}
