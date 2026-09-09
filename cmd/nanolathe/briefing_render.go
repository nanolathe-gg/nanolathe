package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	battleSeeds := newBattleSeedSource(g.opts)
	request := func() (freshBattleRequest, error) {
		g.saveSettings()
		identity := fmt.Sprintf("%s:MISSION%d", campaign.Path, missionIndex)
		return missionBattleRequest(g.opts, g.cs, identity, g.missionDifficulty(), missionIndex, missionIndex, nil, battleSeeds)
	}
	g.briefing = NewCampaignBriefingController(loaded, g.missionSide, &crt, request)
	if clPtr != nil && clPtr.Input() != nil {
		clPtr.Input().DrainTokens()
	}
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
		g.briefing.text = readBriefingText(g.cs, globals.Brief)
	}
	g.briefingPanel = g.loadBriefingPanel(g.briefing.planet)
	g.installBriefingTextRegion()
	g.briefingNowMS = 0
}

// installBriefingTextRegion resolves the TextRegion gadget's font and hands the
// authored text, the gadget's own size and that font's metric to the pager. The
// gadget's font index is the local side plus one, and a font index selects the
// n-th kind-7 record of the window counting from zero — so Arm gets `armfont`
// and Core `corefont` [08 R-CAMP-01 §2][07 R-WGT-01 §12].
func (g *gameShell) installBriefingTextRegion() {
	if g == nil || g.briefing == nil || g.briefingPanel == nil || g.briefingPanel.Window == nil {
		return
	}
	window := g.briefingPanel.Window
	g.briefingFont = g.loadRetailWindowFont(window, g.missionSide+1)
	if i := window.GadgetIndex("TextRegion"); i >= 0 {
		rect := window.PlacedRect(i)
		g.briefing.SetTextRegion(g.briefing.text, int(rect.W), int(rect.H), g.briefingTextHeight(), g.briefingTextWidth)
	}
}

// loadRetailWindowFont is the FNT of the window's n-th kind-7 font record,
// counting from zero in index order — the screen writes `localSide + 1` into
// the TextRegion gadget's font number at open, which is what the window's
// per-gadget selector then walks. A missing record or an unreadable file
// leaves the font null and the caller keeps the common font
// [07 R-WGT-01 §6][07 R-WGT-01 §12][03 R-FONT-01 §5].
func (g *gameShell) loadRetailWindowFont(window *gui.Window, index int) *formats.FNT {
	if g == nil || g.cs == nil || g.cs.fs == nil || window == nil || index < 0 || index > 127 {
		return nil
	}
	return window.Font(g.cs.fs, uint8(index))
}

// briefingTextWidth and briefingTextHeight are the TextRegion font's metrics.
// The wrapper, the lines-per-page divide and the run pen all measure through
// the same font, so they share one accessor; the front-end GAF font stands in
// only when the authored FNT could not be read [07 R-FE-02 §6][07 R-HUD-03 §10].
func (g *gameShell) briefingTextWidth(text string) int {
	if g != nil && g.briefingFont != nil {
		return client.MeasureText(g.briefingFont, text)
	}
	return g.retailTextWidth(text)
}

func (g *gameShell) briefingTextHeight() int {
	if g != nil && g.briefingFont != nil && g.briefingFont.Height != 0 {
		return int(g.briefingFont.Height)
	}
	return g.retailTextHeight()
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
	window := gui.CloneWindow(g.assets.briefing.window)
	g.installRetailWindowButtonArt(window, g.assets.briefing.art)
	return ui.NewPanel(window)
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
	if p := g.briefingPanel; p != nil && p.Window != nil {
		frame := pointerFrame(in, widgetTokens(in), false)
		frame.TokenMode = true
		// TODO(question): MSNBRIEF's navigation word is initialized enabled;
		// its full transition census remains open [07 R-WGT-01 §2].
		frame.KeyNavigation = true
		if in.Kbd != nil {
			frame.ShiftHeld, frame.AltHeld = in.Kbd.HasShift(), in.Kbd.KeyHeld(input.KeyAlt)
		}
		result := p.ServiceFrame(frame, ui.WidgetHooks{})
		in.DiscardTokens(result.ConsumedTokens)
		if result.Fired {
			switch gui.CallbackName(p.Window.Gadgets[result.FiredIndex].Name) {
			case "Start":
				g.dispatchBriefing(BriefingActionStart)
			case "PrevMenu":
				g.dispatchBriefing(BriefingActionPrev)
			case "SHUTUP":
				g.dispatchBriefing(BriefingActionShutup)
			}
			return
		}
	}
	mouse, _ := publishedPointer(in)
	if mouse.Pressed(input.MouseButtonLeft) && g.briefingPanel != nil && g.briefingPanel.Window != nil {
		window := g.briefingPanel.Window
		x, y := int32(mouse.X), int32(mouse.Y)
		// A press takes the capture, and a greyed gadget never captures
		// [07 R-WGT-01 §1 "Capture"][07 R-WGT-01 §13].
		if idx := g.briefingPanel.PressTest(x, y); idx >= 0 {
			switch gui.CallbackName(window.Gadgets[idx].Name) {
			case "Start":
				g.dispatchBriefing(BriefingActionStart)
			case "PrevMenu":
				g.dispatchBriefing(BriefingActionPrev)
			case "SHUTUP":
				g.dispatchBriefing(BriefingActionShutup)
			}
			return
		}
		// MSNBRIEF's stock `TextRegion` and `MOREBAR` are authored as inert
		// kind-5 labels (attribute 0x10, no quickkey), so the generic
		// label/button capture above never selects them [07 R-WGT-01 §7] — a
		// click there is *never* a fired gadget in the shared sense. The
		// transition table nonetheless lists a click on either one as its own
		// row, paging the text same as MOREBAR alone [07 R-FE-01 §2
		// "TextRegion / MOREBAR"][07 R-HUD-03 §10]: MSNBRIEF's own paging
		// routine hit-tests both gadget rectangles directly. Window.HitTest
		// only gates on Active/hidden, not on the per-kind press-handler
		// eligibility PressTest requires, so it is the direct stand-in for
		// that dedicated hit test.
		if idx := window.HitTest(x, y); idx >= 0 {
			switch gui.CallbackName(window.Gadgets[idx].Name) {
			case "MOREBAR", "MORE", "TextRegion":
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
		// The rotation column's sequence is registered with the frame-advance
		// timer when the briefing GAF loads; a missing sequence leaves the
		// gadget without animation [08 R-CAMP-01 §2].
		if e, ok := g.assets.briefing.art.Find(b.planet.Rotate); ok {
			b.SetRotationSequence(e)
		} else {
			b.SetRotationSequence(nil)
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
		if i == 0 || !panel.ActiveAt(i) {
			continue
		}
		r := panel.Window.PlacedRect(i)
		// Screen-specific painters are installed through first named lookup;
		// a later duplicate retains its generic gadget painter [07 R-FE-02 §5].
		name := gui.GadgetName(gad.Name)
		if panel.Window.GadgetIndex(name) != i {
			name = ""
		}
		switch name {
		case "PANORAMA":
			g.drawBriefingPanorama(c, b, r)
		case "PLANET":
			g.drawBriefingPlanet(c, b, r)
		case "SOLARSYSTEM":
			g.drawBriefingSolarSystem(c, b, r)
		case "TextRegion":
			g.drawBriefingText(c, b, r)
		case "MOREBAR", "MORE":
			// MOREBAR is a kind-5 label the pager captions; the screen leaves
			// its own font number alone, so on MSNBRIEF it selects the first
			// kind-7 record (`smlfont`) and the label painter takes the FNT
			// path with the caption colour installed raw. Only the text
			// region and the solar system get `localSide + 1`
			// [08 R-CAMP-01 §2][07 R-WGT-01 §6][03 R-FONT-01 §6].
			if font := g.windowGadgetFont(panel, gad); font != nil {
				g.drawRetailLabelFNT(c, panel, gad, r, b.MoreCaption(), font, b.CaptionColor())
			} else {
				g.drawBriefingLine(c, b.MoreCaption(), int(r.X)+5, int(r.Y), b.CaptionColor())
			}
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
	if f := briefingFrame(g.assets.briefing.art, gad.Art, p.StatusAt(index)); f != nil {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	} else if f := g.retailButtonFrame(gad, p.StatusAt(index), false); f != nil && gad.Kind == gui.KindButton {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
	g.drawRetailTextState(c, p, index, gad, r)
}

// panoramaStrip lays the panorama scroller's tiling: the pen starts at
// `gadget.x - scroll` and the frame index starts at **0**, each blit advancing
// the pen by its own frame's width. Retail runs the loop counter from 0 to the
// frame count inclusive — one blit more than there are frames, the index taken
// modulo the count — and lets the gadget clip discard whatever falls outside;
// with the stock 4-frame 2440-pixel sequence in a 550-pixel gadget that count
// covers the gadget for every value of `scroll`, so stopping at the right edge
// paints the same visible pixels [08 R-CAMP-01 §2].
func panoramaStrip(frames int, widthAt func(int) int, scroll, rectX, rectW int, blit func(index, x int)) {
	if frames <= 0 || widthAt == nil || blit == nil {
		return
	}
	x := rectX - scroll
	for n := 0; n <= frames && x < rectX+rectW; n++ {
		index := n % frames
		w := widthAt(index)
		if w <= 0 {
			return
		}
		blit(index, x)
		x += w
	}
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
		if ref.Frame != nil {
			total += int(ref.Frame.Width)
		}
	}
	if total > 0 {
		b.scroll %= total
	}
	// The gadget's frame word — `(now / 3) mod frameCount`, kept by the
	// controller — selects nothing that is drawn: retail fetches that frame
	// only to test the pointer against null before painting the strip and the
	// mask. Tiling from it instead of from 0 would shift the whole strip by a
	// frame width (640 px in stock content) every three presentation ticks
	// [08 R-CAMP-01 §2].
	if e.Frames[b.PanoramaFrame()%len(e.Frames)].Frame == nil {
		return
	}
	panoramaStrip(len(e.Frames), func(i int) int {
		if e.Frames[i].Frame == nil {
			return 0
		}
		return int(e.Frames[i].Frame.Width)
	},
		b.scroll, int(r.X), int(r.W), func(index, x int) {
			// The strip is clipped to the PANORAMA rectangle: stock frames are
			// 640 px wide in a 550 px gadget, so the tail hangs outside it
			// [08 R-CAMP-01 §2].
			c.UIBlitClipped(e.Frames[index].Frame, x, int(r.Y), int(r.X), int(r.Y), int(r.W), int(r.H))
		})
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
	f := e.Frames[b.RotationFrame()%len(e.Frames)].Frame
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

// drawBriefingText emits the current page's labels the way the pager creates
// them: one per line at `(regionX + 5, regionY + (fontHeight+2)/2 +
// i × (fontHeight+2))`, in the side's plain text colour, with each `&X…&` run
// drawn over the label at the pen the pager measured [07 R-HUD-03 §10]
// [07 R-FE-02 §7].
func (g *gameShell) drawBriefingText(c *client.Client, b *campaignBriefingController, r gui.Rect) {
	if b == nil || c == nil {
		return
	}
	lines := b.Lines()
	if len(lines) == 0 {
		return
	}
	step := g.briefingTextHeight() + 2
	x := int(r.X) + 5
	plain := b.PlainColor()
	for i, line := range lines {
		y := int(r.Y) + step/2 + i*step
		g.drawBriefingLine(c, line.Text, x, y, plain)
		for _, run := range line.Runs {
			g.drawBriefingLine(c, run.Text, x+run.X, y, run.Color(b.localSide))
		}
	}
}

// drawBriefingLine draws one label of the text region. The pager gives its
// labels no width, so nothing here truncates: the wrapper is what keeps a line
// inside the region [07 R-HUD-03 §10].
func (g *gameShell) drawBriefingLine(c *client.Client, text string, x, y int, color byte) {
	if text == "" {
		return
	}
	if g != nil && g.briefingFont != nil {
		width, _ := c.Size()
		c.UITextWidth(g.briefingFont, text, x, y, width-x, color)
		return
	}
	g.drawRetailString(c, text, x, y, -1, color)
}
