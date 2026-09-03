package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

func (g *gameShell) openCampaignBriefing() {
	if g == nil || g.cs == nil || g.cs.fs == nil || g.campaignIdx < 0 || g.campaignIdx >= len(g.campaignOptions) {
		reportRetailMessageError(g.showRetailMessage("no campaign selected"))
		return
	}
	campaign := g.campaignOptions[g.campaignIdx]
	if g.missionIdx < 0 || g.missionIdx >= len(campaign.Missions) {
		reportRetailMessageError(g.showRetailMessage("no mission selected"))
		return
	}
	stub := campaign.Missions[g.missionIdx]
	loaded, err := mission.LoadCampaignWithSink(g.cs.fs, campaign.Path, stub.Index, g.missionDifficulty(), 0, nil)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	missionIndex := stub.Index
	_, crtSeed := seedsFor(g.opts)
	crt := session.NewFrontEndCRT(crtSeed)
	request := func() (freshBattleRequest, error) {
		g.saveSettings()
		identity := fmt.Sprintf("%s:MISSION%d", campaign.Path, missionIndex)
		return missionBattleRequest(g.opts, g.cs, identity, g.missionDifficulty(), missionIndex, missionIndex, nil, briefingBattleSeedSource{opts: g.opts, crt: &crt})
	}
	g.briefing = NewCampaignBriefingController(loaded, g.missionSide, &crt, request)
	if g.audioOwner == nil {
		g.audioOwner = audio.NewService(g.cs.fs)
	}
	if clPtr != nil {
		clPtr.SetAudioService(g.audioOwner)
	}
	g.audioOwner.BindCRT(&crt)
	// MSNBRIEF starts narration through the same semantic audio owner that is
	// handed to the battle at load completion [03 R-AUD-02 §1]. Missing WAVs
	// remain silent at the audio boundary.
	g.consumeBriefingAudio(g.briefing.OpeningAudio())
	if loaded.OTA != nil {
		globals := mission.DecodeMissionGlobals(loaded.OTA.Global)
		g.briefing.SetText(readBriefingText(g.cs, globals.Brief))
	}
	g.briefingPanel = g.loadBriefingPanel(g.briefing.planet)
	if g.briefingPanel != nil && g.briefingPanel.Window != nil {
		for i, gadget := range g.briefingPanel.Window.Gadgets {
			if i != 0 && strings.EqualFold(gadget.Name, "TextRegion") {
				height := int(g.briefingPanel.Window.PlacedRect(i).H)
				fontHeight := g.retailTextHeight()
				if fontHeight > 0 {
					g.briefing.SetPageLines(height / (fontHeight + 2))
				}
				break
			}
		}
	}
	g.briefingNowMS = 0
}

// applyRetailContinuation maps the typed Summary continuation into the same
// campaign-selection state used by NEWGAME and then enters the authored
// briefing. It never constructs a battle directly: Start remains the typed
// BriefingActionStart boundary [08 R-CAMP-01 §§1–2, 6–8].
func (g *gameShell) applyRetailContinuation(c *session.RetailCampaignContinuation) error {
	if g == nil || c == nil || g.cs == nil || g.cs.fs == nil {
		return fmt.Errorf("nanolathe: retail continuation has no campaign state")
	}
	if c.MissionIndex < 0 || strings.TrimSpace(c.CampaignPath) == "" {
		return fmt.Errorf("nanolathe: retail continuation has incomplete authored identity")
	}
	// Validate the authored mission before changing any shell selection or
	// retiring an active battle. openCampaignBriefing performs the same load as
	// part of its existing route; this preflight keeps failures atomic here.
	if _, err := mission.LoadCampaignWithSink(g.cs.fs, c.CampaignPath, c.MissionIndex, c.Difficulty, 0, nil); err != nil {
		return fmt.Errorf("nanolathe: retail continuation mission: %w", err)
	}
	// Build the selection candidate locally. In particular, do not append a
	// newly discovered campaign to live frontend state until every identity
	// check below has passed.
	options := g.campaignOptions
	optionIndex := -1
	for i := range options {
		candidate := options[i]
		if strings.EqualFold(candidate.Path, c.CampaignPath) || strings.EqualFold(candidate.Name, c.CampaignName) {
			optionIndex = i
			break
		}
	}
	if optionIndex < 0 {
		campaign, err := mission.DiscoverCampaign(g.cs.fs, c.CampaignPath)
		if err != nil {
			return fmt.Errorf("nanolathe: retail continuation campaign: %w", err)
		}
		if campaign == nil {
			return fmt.Errorf("nanolathe: retail continuation campaign is unavailable")
		}
		options = append(append([]mission.Campaign(nil), options...), *campaign)
		optionIndex = len(options) - 1
	}
	missionUIIndex, ok := campaignMissionUIIndex(options, optionIndex, c.MissionIndex)
	if !ok {
		return fmt.Errorf("nanolathe: retail continuation mission index %d is not selectable", c.MissionIndex)
	}
	// Selection/progress are now complete typed state. Only Thumbs is
	// established on the between-missions Summary path. The older ten-slot WL
	// view belongs to a fresh/default campaign lifetime and is reset below;
	// there is no Thumbs-to-WL conversion.
	g.campaignOptions = options
	g.campaignIdx = optionIndex
	g.missionIdx = missionUIIndex
	g.missionDifficultyValue = c.Difficulty
	g.missionSide = c.Side
	applyRetailContinuationProgress(&g.campaignProgress, c.Thumbs)
	g.campaignProgressSet = true

	// This is the continuation commit. A campaign load never uses the battle
	// restoration path; it returns to the normal mission panel and briefing
	// action sequence.
	if g.battle != nil {
		g.teardownBattle(clPtr)
	}
	if g.frontend != nil {
		g.openMenu(modeMenuMission)
	}
	g.openCampaignBriefing()
	if clPtr != nil {
		g.bindFrontendClient(clPtr)
	}
	return nil
}

func applyRetailContinuationProgress(progress *session.BankProgress, thumbs [25]byte) {
	if progress == nil {
		return
	}
	// Thumbs is the established between-missions campaign mark array. Build a
	// fresh value so stale state from an earlier campaign cannot leak
	// into this newly loaded lifetime. WL has no established Summary mapping;
	// it therefore remains its zero/default value [08 R-CAMP-01 §8].
	*progress = session.BankProgress{BetweenMissions: 1, Thumbs: thumbs}
}

// briefingBattleSeedSource carries the front-end CRT state across the battle
// boundary; the simulation seed is selected independently as usual [I4].
type briefingBattleSeedSource struct {
	opts Options
	crt  *rng.CRT
}

func (s briefingBattleSeedSource) NextBattleSeeds() BattleSeeds {
	sim, _ := seedsFor(s.opts)
	var crt uint32
	if s.crt != nil {
		crt = s.crt.State
	}
	return BattleSeeds{Simulation: int32(sim), CRT: crt}
}

func readBriefingText(cs *contentSet, name string) string {
	if cs == nil || cs.fs == nil || strings.TrimSpace(name) == "" {
		return ""
	}
	data, err := cs.fs.ReadFileLimit(briefingMediaPath(name, "txt"), int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return ""
	}
	return string(data)
}

// loadBriefingPanel keeps optional briefing media lazy. The campaign can still
// enter through Start when a bitmap, animation or GUI is absent, matching the
// optional-media skip boundary [08 R-CAMP-01 §2].
func (g *gameShell) loadBriefingPanel(planet BriefingPlanet) *ui.Panel {
	if g == nil || g.assets == nil || g.cs == nil || g.cs.fs == nil {
		return nil
	}
	if g.assets.briefing == nil {
		panel, err := loadRetailPanelStrict(g.cs, "guis/msnbrief.gui", "", "", "MSNBRIEF authored GUI")
		if err == nil || panel != nil {
			g.assets.briefing = panel
		}
	}
	if g.assets.briefing == nil || g.assets.briefing.window == nil {
		return nil
	}
	g.assets.briefing.art = loadBriefingArt(g.cs.fs, planet, g.assets.briefing.art)
	return ui.NewPanel(g.assets.briefing.window)
}

// loadBriefingArt resolves the planet-specific GAF for every newly opened
// briefing. A missing optional asset leaves the prior successful art intact;
// this keeps a media failure from replacing a usable panel with nil [08
// R-CAMP-01 §2].
func loadBriefingArt(fs vfs.FSOps, planet BriefingPlanet, previous *formats.GAF) *formats.GAF {
	if fs == nil {
		return previous
	}
	path := "anims/" + strings.ToLower(planet.Brief) + ".gaf"
	art, err := formats.LoadGAFFile(fs, path)
	if err != nil {
		return previous
	}
	return art
}

func (g *gameShell) briefingInput(cl *client.Client) {
	b := g.briefing
	if b == nil || cl == nil || cl.Input() == nil {
		return
	}
	in := cl.Input()
	if in.Kbd.KeyDown(input.KeyEscape) {
		g.dispatchBriefing(BriefingActionPrev)
		return
	}
	if in.Kbd.KeyDown(input.KeyEnter) || in.Kbd.KeyDown(input.KeySpace) {
		g.dispatchBriefing(BriefingActionStart)
		return
	}
	if in.Mouse.Pressed(input.MouseButtonLeft) && g.briefingPanel != nil && g.briefingPanel.Window != nil {
		// A press takes the capture, and a greyed gadget never captures
		// [07 R-WGT-01 §1 "Capture"][07 R-WGT-01 §13].
		if idx := g.briefingPanel.PressTest(int32(in.Mouse.X), int32(in.Mouse.Y)); idx >= 0 {
			name := g.briefingPanel.Window.Gadgets[idx].Name
			switch menuKey(name) {
			case "start":
				g.dispatchBriefing(BriefingActionStart)
			case "prevmenu":
				g.dispatchBriefing(BriefingActionPrev)
			case "shutup":
				g.dispatchBriefing(BriefingActionShutup)
			case "morebar", "more":
				g.dispatchBriefing(BriefingActionMore)
			}
		}
	}
}

func (g *gameShell) dispatchBriefing(action BriefingAction) {
	if g == nil || g.briefing == nil {
		return
	}
	// `MSNBRIEF`'s cue column [07 R-FE-01 §2]: `PrevMenu` plays `Previous` and
	// `TextRegion`/`MOREBAR` plays `More`; the `Start` and `SHUTUP` rows carry
	// no cue. It runs in the screen handler that consumes the fired result
	// [07 R-WGT-01 §3].
	switch action {
	case BriefingActionPrev:
		g.playMenuCue("Previous")
	case BriefingActionMore:
		g.playMenuCue("More")
	}
	event, err := g.briefing.Dispatch(action)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	if action == BriefingActionPrev {
		g.consumeBriefingAudio(event.Audio)
		g.briefing, g.briefingPanel = nil, nil
		g.openMenu(modeMenuMission)
		return
	}
	g.consumeBriefingAudio(event.Audio)
	if event.Valid {
		g.briefing, g.briefingPanel = nil, nil
		g.beginFreshBattleLoad("", modeMenuMission, event.Request, func(sess *session.Session) {
			g.adoptCampaignProgress(sess)
		})
	}
}

func (g *gameShell) drawBriefing(c *client.Client) {
	b := g.briefing
	if b == nil || c == nil {
		return
	}
	panel := g.briefingPanel
	if panel == nil || panel.Window == nil {
		return
	}
	if g.assets != nil && g.assets.briefing != nil && g.assets.briefing.art != nil {
		if e, ok := g.assets.briefing.art.Find(b.planet.Panorama); ok {
			b.SetPanoramaFrameCount(len(e.Frames))
		}
	}
	b.Update(g.briefingNowMS, briefingPresentationTick(g.briefingNowMS))
	// The authored side background names are mbriefarm/mbriefcore. Missing
	// optional art leaves the indexed surface unchanged; no replacement image
	// is generated [08 R-CAMP-01 §2].
	if bg := g.briefingBackground(); bg != nil {
		c.UIBlitPCX(bg, int(panel.Window.Rect.X), int(panel.Window.Rect.Y))
	}
	for i, gad := range panel.Window.Gadgets {
		if i == 0 || !panel.ActiveOf(gad.Name) {
			continue
		}
		r := panel.Window.PlacedRect(i)
		switch menuKey(gad.Name) {
		case "panorama":
			g.drawBriefingPanorama(c, b, r)
		case "planet":
			g.drawBriefingPlanet(c, b, r)
		case "solarsystem":
			g.drawBriefingSolarSystem(c, b, r)
		case "textregion":
			g.drawBriefingText(c, b, r)
		default:
			g.drawBriefingGadget(c, panel, i, gad, r)
		}
	}
}

func (g *gameShell) briefingBackground() *formats.PCX {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return nil
	}
	name := "core"
	if g.missionSide == 0 {
		name = "arm"
	}
	bg, _ := formats.LoadPCXFile(g.cs.fs, "bitmaps/mbrief"+name+".pcx")
	return bg
}

func briefingFrame(gaf *formats.GAF, name string, idx int) *formats.GAFFrame {
	if gaf == nil {
		return nil
	}
	e, ok := gaf.Find(name)
	if !ok || len(e.Frames) == 0 {
		return nil
	}
	if idx < 0 {
		idx = 0
	}
	idx %= len(e.Frames)
	return e.Frames[idx].Frame
}

func (g *gameShell) drawBriefingGadget(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if g == nil || g.briefing == nil || g.assets == nil || g.assets.briefing == nil {
		return
	}
	if f := briefingFrame(g.assets.briefing.art, gad.Art, p.StatusOf(gad.Name)); f != nil {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	} else if f := g.retailButtonFrame(gad, p.StatusOf(gad.Name), false); f != nil && gad.Kind == gui.KindButton {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
	g.drawRetailTextState(c, p, index, gad, r)
}

func (g *gameShell) drawBriefingPanorama(c *client.Client, b *campaignBriefingController, r gui.Rect) {
	if g.assets == nil || g.assets.briefing == nil || g.assets.briefing.art == nil {
		return
	}
	e, ok := g.assets.briefing.art.Find(b.planet.Panorama)
	if !ok || len(e.Frames) == 0 {
		return
	}
	total := 0
	for _, ref := range e.Frames {
		total += int(ref.Frame.Width)
	}
	if total > 0 {
		b.scroll %= total
	}
	x := int(r.X) - b.scroll
	for n := 0; x < int(r.X+r.W); n++ {
		f := e.Frames[(b.panoramaFrame+n)%len(e.Frames)].Frame
		if f == nil || f.Width == 0 {
			break
		}
		blitRetailFrame(c, f, x, int(r.Y))
		x += int(f.Width)
	}
	if mask := briefingFrame(g.assets.briefing.art, "Panmask", b.localSide); mask != nil {
		blitRetailFrame(c, mask, 0, 0)
	}
}

func (g *gameShell) drawBriefingPlanet(c *client.Client, b *campaignBriefingController, r gui.Rect) {
	if g.assets == nil || g.assets.briefing == nil || g.assets.briefing.art == nil {
		return
	}
	e, ok := g.assets.briefing.art.Find(b.planet.Rotate)
	if !ok || len(e.Frames) == 0 {
		return
	}
	f := e.Frames[b.rotateFrame%len(e.Frames)].Frame
	if f == nil {
		return
	}
	blitRetailFrame(c, f, int(r.X)+int(r.W-int32(f.Width))/2, int(r.Y)+int(r.H-int32(f.Height))/2)
}

func (g *gameShell) drawBriefingSolarSystem(c *client.Client, b *campaignBriefingController, r gui.Rect) {
	if b == nil || b.mission == nil || b.mission.OTA == nil {
		return
	}
	globals := mission.DecodeMissionGlobals(b.mission.OTA.Global)
	x, y := int(r.X)+80, int(r.Y)+20
	g.drawRetailString(c, fmt.Sprintf("Wind Speed : %d", b.windSpeed), x, y, int(r.W), g.guiColor(15))
	g.drawRetailString(c, fmt.Sprintf("Gravity : %.1f", float32(globals.Gravity)), x, y+20, int(r.W), g.guiColor(15))
}

func (g *gameShell) drawBriefingText(c *client.Client, b *campaignBriefingController, r gui.Rect) {
	if b == nil || b.text == "" {
		return
	}
	page := b.pageText()
	g.drawRetailString(c, page, int(r.X), int(r.Y), int(r.W), g.guiColor(15))
}
