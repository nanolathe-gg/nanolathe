package main

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
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
		Kind:              session.PostBattleSkirmish,
		MissionIndex:      -1,
		Windowed:          true,
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
		if cfg.Difficulty < 0 && b.shell != nil {
			cfg.Difficulty = b.shell.missionDifficulty()
		}
		cfg.Progress = &b.sess.Progress
		cfg.ProgressCommitted = true
		if b.shell != nil {
			cfg.LocalSide = b.shell.missionSide
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
	name = strings.TrimLeft(name, `/\\`)
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
		} else if in != nil && in.Mouse != nil && in.Mouse.Pressed(input.MouseButtonLeft) && b.postBattle.Handle(session.PostBattleControlMouse, uint32(now)) {
			b.consumePostBattleEffects(uint32(now), cl)
		}
		return
	}
	if b.postBattle.State() != session.PostBattleEndMission || b.hud == nil {
		return
	}
	if action := b.hud.handleResultInput(in); action != ui.ResultActionNone {
		b.doResultAction(action, cl)
	}
}

func (b *battleSession) consumePostBattleEffects(now uint32, cl *client.Client) {
	if b == nil || b.postBattle == nil {
		return
	}
	effects := b.postBattle.Effects()
	for b.postBattleEffectPos < len(effects) {
		effect := effects[b.postBattleEffectPos]
		b.postBattleEffectPos++
		switch effect.Kind {
		case session.PostBattleEffectStopMusic:
			if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
				b.sess.Audio.Music.Stop()
			}
		case session.PostBattleEffectGlamourSound:
			if b.sess != nil && b.sess.Audio != nil {
				b.sess.Audio.StartStream(effect.Resource, 0, 60, now)
			}
		case session.PostBattleEffectOutcomeArt:
			// Outcome1/Outcome0 are frontend ENDMSN bitmap resources. The
			// existing result panel owns those copies; only a glamour resource
			// is decoded through the glamour PCX path.
			if effect.Resource != "Outcome1" && effect.Resource != "Outcome0" {
				if image, ok := b.loadPostBattleImage(effect.Resource); ok {
					b.postBattleGlamour = image
				}
			}
		case session.PostBattleEffectGlamourFadeStep:
			b.advancePostBattleGlamourFade(now, cl)
		case session.PostBattleEffectFadeOutStep:
			b.postBattleFadeLevels = append(b.postBattleFadeLevels, effect.Level)
		case session.PostBattleEffectRouteRouter:
			b.routePostBattleRouter(cl)
		}
	}
	if b.sess != nil && b.sess.Audio != nil {
		b.sess.Audio.TickStream(now)
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
	b.shell.campaignProgress = b.sess.Progress
	b.shell.campaignProgressSet = true
	uiIndex, ok := campaignMissionUIIndex(b.shell.campaignOptions, b.shell.campaignIdx, index)
	if !ok {
		return
	}
	b.shell.missionIdx = uiIndex
	b.shell.teardownBattle(cl)
	b.shell.openCampaignBriefing()
	b.shell.bindFrontendClient(cl)
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
	b.postBattleFadeCur = postBattlePaletteBytes(b.postBattleGlamour)
	for i := range b.postBattleFadeCur {
		b.postBattleFadeDst[i] = b.postBattleNormalPal.Base[i/4][i%4]
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

func (b *battleSession) restorePostBattlePalette(cl *client.Client) {
	if b == nil || cl == nil || b.postBattleNormalPal == nil {
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
