package main

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/session"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// bitmaps/loadgame2bg.pcx as the window-less background through the bitmap
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// works. Everything below is that repaint [07 §4].
const (
	retailLoadStages = 6
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// bar at x=0xcd, and the Y values are authored one per row rather than
	// stepped.
	retailLoadLabelX = 0x5a
	retailLoadBarX   = 0xcd
	// The bar is filled to left + progress*7/2 and is inclusive of its right
	// and bottom edges, so a full bar is 351 wide — exactly the LIGHTBAR frame
	// that is stamped over it.
	retailLoadBarBottom = 20
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// first sees its percentage at 100 and drops every non-zero counter by two
	// per repaint. The counter is the shade level handed to the glyph blitter.
	retailLoadFlashStart = 30
	retailLoadFlashStep  = 2
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// thread keeps the machine, which puts the screen at five repaints a
	// second and the flash decay at fifteen of them, three seconds. Nanolathe
	// runs its loader as a goroutine the runtime schedules against the
	// renderer, so the screen is painted at the display rate instead and only
	// the flash is stepped on retail's cadence. Deliberate divergence: the
	// bars move more smoothly than retail's, and nothing else changes.
	retailLoadRepaintSeconds = 0.2
	// The GUI semantic color fields the screen draws with. They are read from
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	retailLoadDoneColor    = 10
	retailLoadWorkingColor = 12
	retailLoadTitleColor   = 15
)

// retailLoadBars are the authored rows, in order. The labels are the retail
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
var retailLoadBars = [retailLoadStages]struct {
	label string
	y     int
}{
	{"Textures", 0x87},
	{"Terrain", 0xb1},
	{"Units", 0xda},
	{"Animation", 0x106},
	{"3D Data", 0x130},
	{"Explosions", 0x15b},
}

// retailLoadStageOf maps one Nanolathe load family onto one of the six retail
// bars.
//
// Deliberate divergence: the screen, its six labels and their geometry are
// retail's, but the attribution is ours, because Nanolathe's loaders are not
// TotalA.exe's. Retail's six bars are fed by six separate loaders — tile
// textures, TNT terrain, unit definitions, GAF animation, 3DO data and the
// explosion/weapon tables. Nanolathe compiles one immutable catalog instead,
// so each bar is driven by the families closest in kind: COB scripts stand in
// for animation because unit animation in TA is the COB VM, the model catalog
// stands in for 3D data, and weapons and features stand in for explosions.
// Textures carries the interface data the client presents. Re-attribute this
// when the renderer grows real texture and animation load steps.
// TODO(question): retail's own per-loader percentages are not recoverable
// from the six progress bytes alone.
var retailLoadStageOf = map[string]int{
	content.FamilySides:        0,
	content.FamilySounds:       0,
	content.FamilyBuildMenus:   0,
	content.FamilyMaps:         1,
	session.FamilyTerrain:      1,
	content.FamilyUnits:        2,
	content.FamilyMovement:     2,
	content.FamilyAIProfiles:   2,
	content.FamilyBattleTables: 2,
	session.FamilyUnitWorld:    2,
	session.FamilyPlacement:    3,
	session.FamilyScripts:      3,
	content.FamilyModels:       4,
	content.FamilyWeapons:      5,
	content.FamilyFeatures:     5,
}

// retailLoadStageWeight is how many families feed each bar, so a bar can show
// its own families completing one at a time.
var retailLoadStageWeight = func() (w [retailLoadStages]int) {
	// Counting into a fixed array: the result does not depend on map order.
	for _, stage := range retailLoadStageOf {
		w[stage]++
	}
	return
}()

type loadResult struct {
	sess *session.Session
	err  error
}

// loadingState is the model behind the loading screen. The loader runs on its
// own goroutine, as retail's does on its own thread, so percent is read by the
// renderer while the loader writes it; nothing else crosses.
type loadingState struct {
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// line on the mission type and skips it for type 1.
	mapName string
	percent [retailLoadStages]atomic.Int32
	family  map[string]int32
	done    chan loadResult

	// Renderer-side only.
	flash   [retailLoadStages]int
	prev    [retailLoadStages]int
	repaint float64
}

func newLoadingState(mapName string) *loadingState {
	return &loadingState{
		mapName: mapName,
		family:  make(map[string]int32, len(retailLoadStageOf)),
		done:    make(chan loadResult, 1),
	}
}

// advanceFlash is one retail repaint's worth of flash bookkeeping: arm a stage
// that has just reached 100, otherwise decay whatever is already armed.
func (l *loadingState) advanceFlash() {
	for i := range l.percent {
		percent := int(l.percent[i].Load())
		switch {
		case percent >= 100 && l.prev[i] < 100:
			l.flash[i] = retailLoadFlashStart
		case l.flash[i] > 0:
			l.flash[i] -= retailLoadFlashStep
			if l.flash[i] < 0 {
				l.flash[i] = 0
			}
		}
		l.prev[i] = percent
	}
}

// report is the content.Progress the loader is given. It recomputes the bar
// its family belongs to as the mean of that bar's families, which is what
// lets a bar fed by several small catalogs rise in steps.
func (l *loadingState) report(family string, percent int) {
	stage, ok := retailLoadStageOf[family]
	if !ok {
		return
	}
	l.family[family] = int32(percent)
	total := int32(0)
	// Integer sum over the bar's families: the result does not depend on map
	// order, so this traversal stays deterministic [INVARIANTS I1].
	for name, value := range l.family {
		if retailLoadStageOf[name] == stage {
			total += value
		}
	}
	weight := int32(retailLoadStageWeight[stage])
	if weight < 1 {
		weight = 1
	}
	l.percent[stage].Store(total / weight)
}

// startBattleLoad puts the shell on the loading screen and hands the work to a
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// only adopted back on the render goroutine, in step.
func (g *gameShell) startBattleLoad(mapName string) {
	// Start is the commit point for the skirmish setup: the next run of the
	// engine opens SKIRMISH.GUI on the rows this battle was started with.
	g.saveSettings()
	cfg := g.skirmishConfigForStart(mapName)
	g.beginLoad(mapName, modeMenuSkirmish, func(state *loadingState) (*session.Session, error) {
		return session.NewSkirmishWithProgress(g.cs.fs, nil, cfg, state.report)
	})
}

// startMissionLoad is the campaign entry. Retail shows the same screen with
// the same six bars; only the map line is gated off for a mission.
func (g *gameShell) startMissionLoad() {
	if g.campaignIdx < 0 || g.campaignIdx >= len(g.campaignOptions) {
		reportRetailMessageError(g.showRetailMessage("no campaign selected"))
		return
	}
	c := g.campaignOptions[g.campaignIdx]
	if g.missionIdx < 0 || g.missionIdx >= len(c.Missions) {
		reportRetailMessageError(g.showRetailMessage("no mission selected"))
		return
	}
	path := fmt.Sprintf("%s:MISSION%d", c.Path, c.Missions[g.missionIdx].Index)
	difficulty := g.missionDifficulty()
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	g.saveSettings()
	g.beginLoad("", modeMenuMission, func(state *loadingState) (*session.Session, error) {
		return session.NewMissionWithProgress(g.cs.fs, nil, path, difficulty, state.report)
	})
}

func (g *gameShell) beginLoad(mapName string, back shellMode, load func(*loadingState) (*session.Session, error)) {
	state := newLoadingState(mapName)
	g.loading = state
	g.loadingReturn = back
	g.openMenu(modeLoading)
	go func() {
		sess, err := load(state)
		state.done <- loadResult{sess: sess, err: err}
	}()
}

// stepLoading advances the flash on retail's repaint cadence and adopts the
// finished session, or reports the failure the way the screen it was reached
// from reports any other start error.
func (g *gameShell) stepLoading(delta float64) {
	l := g.loading
	if l == nil {
		g.openMenu(g.loadingReturn)
		return
	}
	for l.repaint += delta; l.repaint >= retailLoadRepaintSeconds; l.repaint -= retailLoadRepaintSeconds {
		l.advanceFlash()
	}
	select {
	case res := <-l.done:
		g.loading = nil
		if res.err != nil {
			g.openMenu(g.loadingReturn)
			reportRetailMessageError(g.showRetailMessage(res.err.Error()))
			return
		}
		if err := g.enterBattle(res.sess, res.sess.Catalog); err != nil {
			g.openMenu(g.loadingReturn)
			reportRetailMessageError(g.showRetailMessage(err.Error()))
		}
	default:
	}
}

func (g *gameShell) drawLoadingScreen(c *client.Client) {
	l := g.loading
	if l == nil || g.assets == nil {
		return
	}
	if bg := g.assets.loading; bg != nil {
		c.UIBlitPCX(bg, 0, 0)
	}
	// The map line comes before the rows, and a campaign mission draws none:
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if l.mapName != "" {
		title := fmt.Sprintf("%s: %s", "Map", l.mapName)
		width := g.retailTextWidth(title)
		screenW, screenH := c.Size()
		y := int(math.Trunc(float64(screenH) - float64(g.retailTextHeight())*1.5))
		g.drawRetailString(c, title, screenW/2-width/2, y, -1, g.guiColor(retailLoadTitleColor))
	}
	lightbar := g.retailLightBarFrame()
	for i := range retailLoadBars {
		percent := int(l.percent[i].Load())
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		row := retailLoadBars[i]
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// that same color to the text state and to the bar fill.
		fill := g.guiColor(retailLoadWorkingColor)
		if percent >= 100 {
			fill = g.guiColor(retailLoadDoneColor)
		}
		// Retail's order within a row is label, fill, grille. The label is at
		// x=0x5a and the bar at x=0xcd, so they never overlap, but keep the
		// order anyway.
		g.drawRetailStringLit(c, row.label, retailLoadLabelX, row.y, -1, fill, l.flash[i])
		// left..left+percent*7/2 inclusive, top..top+20 inclusive.
		c.UIFillRect(retailLoadBarX, row.y, percent*7/2+1, retailLoadBarBottom+1, fill)
		if lightbar != nil {
			blitRetailFrame(c, lightbar, retailLoadBarX, row.y)
		}
	}
}

// retailLightBarFrame is the common LIGHTBAR entry's frame 0: a 351x21 metal
// grille whose slots are transparent, so the filled part of the bar shows
// through it. The frame carries authored offsets of (19, -27), which
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// from the pen, so without that the grille would miss its bar. Nanolathe's
// gadget blit ignores GAF offsets already, which lands in the same place.
func (g *gameShell) retailLightBarFrame() *formats.GAFFrame {
	if g == nil || g.assets == nil || g.assets.common == nil {
		return nil
	}
	e, ok := g.assets.common.Find("LIGHTBAR")
	if !ok || len(e.Frames) == 0 {
		return nil
	}
	return e.Frames[0].Frame
}
