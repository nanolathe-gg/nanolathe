package main

import (
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// resultBar describes one dynamic kind-13 statistic column. The x positions
// and labels are the authored ENDMSN population order [08 R-CAMP-01 §7].
// Bars are created in seven groups, not by scanning the live world.
type resultBar struct {
	Label string
	X     int
}

var resultBars = [...]resultBar{
	{Label: "Kills", X: 112},
	{Label: "Losses", X: 186},
	{Label: "EProduced", X: 260},
	{Label: "MProduced", X: 334},
	{Label: "EWasted", X: 408},
	{Label: "MWasted", X: 482},
	{Label: "Score", X: 556},
}

func resultPlayerColorRect(row int) gui.Rect {
	return gui.Rect{X: 16, Y: int32(93 + 20*row), W: 91, H: 21}
}

func resultBarOrigin(row, column int) gui.Rect {
	if column < 0 || column >= len(resultBars) {
		return gui.Rect{}
	}
	return gui.Rect{X: int32(resultBars[column].X), Y: int32(93 + 20*row)}
}

// resultBarFrame is the authored kind-13 record geometry. W/H are the
// stored dimensions; the bevel painter treats the record rectangle as
// inclusive, so its footprint is 68x19 [07 R-HUD-03 §11].
func resultBarFrame(row, column int) (x, y, x2, y2 int) {
	origin := resultBarOrigin(row, column)
	return int(origin.X), int(origin.Y), int(origin.X) + 67, int(origin.Y) + 18
}

// resultBarFillX returns the inclusive foreground endpoint in the authored
// 64-pixel inner span. Go's signed integer division has the retail truncation
// toward zero required by the scaled fill expression [07 R-HUD-03 §11].
func resultBarFillX(x, current, max int) (int, bool) {
	if max <= 0 || current < 0 {
		return x + 1, false
	}
	return x + 2 + (63*current)/max, true
}

// resultBarValue is deliberately a closed switch: these are the seven
// ENDMSN columns, in authored order, and not a reflection over ResultScore.
func resultBarValue(row frame.ResultScore, column int) int {
	switch column {
	case 0:
		return row.Kills
	case 1:
		return row.Losses
	case 2:
		return row.EnergyProduced
	case 3:
		return row.MetalProduced
	case 4:
		return row.EnergyWasted
	case 5:
		return row.MetalWasted
	case 6:
		return row.Score
	default:
		return 0
	}
}

// resultBarStep is the kind-13 animation increment. The authored multiplier
// is a float32 0.06666667 (value/15), narrowed toward zero after the multiply;
// retain that operation rather than replacing it with a rounded fill fraction
// [07 R-HUD-03 §11].
func resultBarStep(value int) int {
	step := int(float32(value) * float32(0.06666667))
	if step < 1 {
		return 1
	}
	return step
}

func resultWon(view frame.ResultView) bool {
	return view.Ended && !view.Draw && strings.EqualFold(view.Kind, "victory")
}

func resultRoute(fs vfs.FSOps, sess *session.Session, view frame.ResultView) bool {
	if sess == nil || sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign || !view.Ended {
		return false
	}
	// A loss remains on ENDMSN even when no successor exists; a final win
	// routes directly to MainMenu [08 R-CAMP-01 §8].
	if !resultWon(view) {
		return true
	}
	return resultStartAvailable(fs, sess)
}

// drawResultOverlay composes the authored ENDMSN window and the established
// outcome title art. Layout, controls, labels, and background art all come
// from the retail GUI/GAF records; there is intentionally no generated panel,
// dim layer, text, or button geometry here [07 §11][08 "Session end and reporting"].
func (h *retailBattleHUD) drawResultOverlay(c *client.Client, b *battleSession, view frame.ResultView) {
	if h == nil || c == nil || b == nil || !view.Ended || b.battleState().Input.ResultDismissed {
		return
	}
	if b.postBattle != nil && b.postBattle.State() != session.PostBattleEndMission {
		if b.postBattle.State() == session.PostBattleGlamour && b.postBattleGlamour != nil {
			c.UIBlitPCX(b.postBattleGlamour, 0, 0)
		} else if b.postBattle.State() >= session.PostBattleFadeSetup && b.postBattle.State() <= session.PostBattleOutcome {
			b.applyPostBattleFade(c)
		}
		return
	}
	b.prepareResultPanel()
	if h.resultPanel != nil {
		if b.postBattle != nil {
			_, route := b.postBattle.NextMission()
			configureResultControls(h.resultPanel, route)
		} else {
			configureResultPanelForView(h.fs, b.sess, view, h.resultPanel)
		}
	}
	// `ENDMSN`'s background is the outcome bitmap the population step installed
	// — `outcome1` on a routing result, `outcome0` otherwise — blitted whole at
	// the window origin, under the authored panel [08 R-CAMP-01 §8].
	if b.shell != nil && b.shell.resultBackground != nil {
		c.UIBlitPCX(b.shell.resultBackground, 0, 0)
	}
	if h.resultWin != nil {
		h.drawGUIWindow(c, h.resultWin, h.resultGAF, "")
	}

	// ENDMSN uses the in-game title bank at the frontend title anchor. Missing
	// optional art remains a diagnostic from HUD loading and does not acquire a
	// synthetic text substitute [08 R-CAMP-01 §8][fmt gaf].
	title := h.resultTitleFrame(view)
	if title == nil {
		return
	}
	w, _ := c.Size()
	c.UIBlitAnchor(title, w/2, 28)
	h.drawResultStats(c, b, view)
}

// resultTitleFrame selects only the two authored terminal outcomes. Draw and
// any result kind not established by the retail result contract have no title;
// in particular, they must not inherit the victory art by default [07 §11].
func (h *retailBattleHUD) resultTitleFrame(view frame.ResultView) *formats.GAFFrame {
	if h == nil || view.Draw {
		return nil
	}
	switch strings.ToLower(view.Kind) {
	case "victory":
		return h.victoryFrame
	case "defeat":
		return h.defeatFrame
	default:
		return nil
	}
}

// resultStartAvailable mirrors the retail ENDMSN initializer's outcome choice:
// Start is enabled only when a campaign has a discovered next mission. A
// missing provenance or discovery error leaves the result non-continuable; it
// does not invent a replacement route [07 §11][08 "Progression"].
func resultStartAvailable(fs vfs.FSOps, sess *session.Session) bool {
	if fs == nil || sess == nil || sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign {
		return false
	}
	path := sess.Mission.CampaignPath
	index := sess.Mission.CampaignIndex
	if path == "" {
		return false
	}
	if index < 0 {
		index = sess.CampaignSlot
	}
	if index < 0 {
		return false
	}
	_, hasNext, err := mission.NextCampaignMission(fs, path, index)
	return err == nil && hasNext
}

// configureResultPanel applies the established ENDMSN outcome activation.
// Retail files carry these controls inactive and the end-mission initializer
// enables the single route selected by campaign progression; unrelated
// controls remain inactive rather than being force-enabled [07 §11].
// prepareResultPanel opens the authored controls during ENDMSN population,
// before the first input pass. Drawing may call it for standalone previews,
// but is never required to establish input ownership [08 R-CAMP-01 §8].
func (b *battleSession) prepareResultPanel() {
	if b == nil || b.hud == nil {
		return
	}
	h := b.hud
	h.openResultWindow()
	if h.resultPanel == nil && h.resultWin != nil {
		h.resultPanel = ui.NewPanel(h.resultWin)
		flushWindowTokens(b.cl)
		configureResultPanel(h.fs, b.sess, h.resultPanel)
	}
	if b.postBattle != nil {
		_, route := b.postBattle.NextMission()
		configureResultControls(h.resultPanel, route)
		if route {
			b.populateResultMissions()
		}
	}
}

// populateResultMissions binds mark glyph bytes to the authored mission list
// once; later input passes preserve its chosen row [08 R-CAMP-01 §8].
func (b *battleSession) populateResultMissions() {
	if b == nil || b.hud == nil || b.hud.resultPanel == nil || b.postBattle == nil || b.sess == nil || b.sess.Mission == nil {
		return
	}
	panel := b.hud.resultPanel
	panel.SetStageAt(panel.Index("Difficulty"), b.postBattle.Summary().Difficulty)
	list := panel.ListAt(panel.Index("Missions"))
	if list != nil && list.Len() > 0 {
		return
	}
	campaign, err := mission.DiscoverCampaign(b.fs, b.sess.Mission.CampaignPath)
	if err != nil || campaign == nil {
		return
	}
	items := make([]string, len(campaign.Missions))
	selected, _ := b.postBattle.NextMission()
	row := 0
	for i, stub := range campaign.Missions {
		mark := byte(0xfd)
		if stub.Index >= 0 && stub.Index < len(b.sess.Progress.Thumbs) {
			switch b.sess.Progress.Thumbs[stub.Index] {
			case 'L':
				mark = 0xff
			case 'W':
				mark = 0xfe
			}
		}
		items[i] = string([]byte{mark, ' '}) + stub.Name
		if stub.Index == selected {
			row = i
		}
	}
	metric := 0
	if b.shell != nil {
		metric = b.shell.retailTextHeight()
	}
	index := panel.Index("Missions")
	panel.FillTextListAt(index, items, nil, metric)
	// Screen-owned selection uses scroll-to and the fill's retained limit,
	// including a zero limit when every row fits [07 R-WGT-01 §4].
	if list := panel.ListAt(index); list != nil {
		list.SetSelected(row)
		rows := (int(panel.Window.PlacedRect(index).H) - 2) / (metric + 1)
		top := list.Top()
		if row < top || row >= top+rows {
			top = row
		}
		panel.SetListTopAt(index, top, panel.ListMaxTopAt(index))
	}
	panel.SetFocus(index)
}

func configureResultPanel(fs vfs.FSOps, sess *session.Session, panel *ui.Panel) {
	if panel == nil {
		return
	}
	start := resultStartAvailable(fs, sess)
	configureResultControls(panel, start)
}

func configureResultControls(panel *ui.Panel, route bool) {
	if panel == nil {
		return
	}
	// ENDMSN starts with every authored control inactive. The opener enables
	// the complete route set when continuable, while MainMenu remains active
	// in either branch; no synthetic Continue/Retry control is introduced
	// [08 R-CAMP-01 §8].
	for _, name := range []string{"Start", "LoadGame", "SaveGame", "KNOB", "Missions", "Difficulty", "AdjustDiff"} {
		panel.SetActive(name, route)
	}
	// MainMenu remains available in both authored branches. For a terminal
	// result retail relocates it to the lower action row [08 R-CAMP-01 §8].
	panel.SetActive("MainMenu", true)
	if !route && panel.Window != nil {
		if i := panel.Window.GadgetIndex("MainMenu"); i >= 0 {
			panel.Window.Gadgets[i].Rect.Y = 416
		}
	} else if route && panel.Window != nil {
		if i := panel.Window.GadgetIndex("MainMenu"); i >= 0 && panel.Window.Gadgets[i].Rect.Y == 416 {
			gadget := &panel.Window.Gadgets[i]
			// RawY is the authored value retained by the GUI loader. Restore
			// its resolved position when this panel instance is reused.
			switch gadget.Rect.RawY {
			case -1:
				gadget.Rect.Y = (480 - gadget.Rect.H) / 2
			case -2:
				gadget.Rect.Y = 480 - gadget.Rect.H
			default:
				gadget.Rect.Y = gadget.Rect.RawY
			}
		}
	}
}

func configureResultPanelForView(fs vfs.FSOps, sess *session.Session, view frame.ResultView, panel *ui.Panel) {
	configureResultControls(panel, resultRoute(fs, sess, view))
}

// resultActionForControl returns the typed action emitted by an authored
// control. Unknown names remain inert and cannot become routes by spelling
// similarity [07 §11].
func resultActionForControl(name string) ui.ResultAction {
	return ui.ResultActionForControl(name)
}

// Result presentation state is HUD-owned and reset when a new committed
// result arrives. It contains only animation progress; the immutable rows and
// maxima always come from frame.ResultView [03 §2.4][I6].
type resultPresentation struct {
	initialized bool
	tick        uint32
	kind        string
	rows        []frame.ResultScore
	max         [7]int
	group       int // highest normally revealed group; -1 before first reveal
	deadline    uint32
	active      [7]bool
	barDue      [10][7]uint32
	barDueSet   [10][7]bool
	animating   [10][7]bool
	animSet     [10][7]bool
	current     [10][7]int
}

func sameResultRows(a, b []frame.ResultScore) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (h *retailBattleHUD) ensureResultPresentation(view frame.ResultView, b *battleSession) {
	if h == nil {
		return
	}
	if h.resultState.initialized && h.resultState.tick == view.Tick && h.resultState.kind == view.Kind && sameResultRows(h.resultState.rows, view.Scores) && h.resultState.max == view.ColumnMaxima {
		return
	}
	h.resultState = resultPresentation{
		initialized: true,
		tick:        view.Tick,
		kind:        view.Kind,
		rows:        append([]frame.ResultScore(nil), view.Scores...),
		max:         view.ColumnMaxima,
		group:       -1,
		// The results state inherits an expired reveal deadline, so Kills can
		// be shown on the first presentation pass [08 R-CAMP-01 §7].
		deadline: 0,
	}
	if b != nil {
		b.playUICue(nil, "ActivateAllStatBars")
	}
}

func resultKeyPressed(in *input.State) bool {
	if in == nil || in.Kbd == nil {
		return false
	}
	for key := input.Key(1); key < input.KeyCount; key++ {
		if in.Kbd.KeyDown(key) {
			return true
		}
	}
	return false
}

func (h *retailBattleHUD) advanceResultReveal(now uint32, c *client.Client, b *battleSession) {
	if h == nil || !h.resultState.initialized || h.resultState.group >= len(resultBars)-1 {
		return
	}
	// This client has only the single-player result surface. A future network
	// result presenter must explicitly disable the shortcut [08 R-CAMP-01 §6].
	skip := c != nil && resultKeyPressed(c.Input())
	if !skip && h.resultState.deadline >= now {
		return
	}
	// A shortcut activates every group, but this pass still executes the
	// ordinary one-group reveal and cue; it must not loop through seven groups.
	if skip {
		if b != nil {
			b.playUICue(c, "ActivateAllStatBars")
		}
		for i := range h.resultState.active {
			h.resultState.active[i] = true
		}
	}
	if h.resultState.group+1 < len(resultBars) {
		h.resultState.group++
		h.resultState.active[h.resultState.group] = true
	}
	h.resultState.deadline = now + 10
	if b != nil {
		cue := "EndGameStatBar"
		if h.resultState.group == len(resultBars)-1 {
			cue = "EndGameScore"
		}
		b.playUICue(c, cue)
	}
}

// Results become interactive once every bar is visible and has reached its
// target. Exact equality completes even if the gadget's animation flag remains
// set; keyboard reveal can activate all groups early [08 R-CAMP-01 §6].
func (h *retailBattleHUD) resultBarsComplete() bool {
	if h == nil || !h.resultState.initialized {
		return false
	}
	for row, score := range h.resultState.rows {
		for column := range resultBars {
			if !h.resultState.active[column] || h.resultState.current[row][column] < resultBarValue(score, column) {
				return false
			}
		}
	}
	return true
}

func (h *retailBattleHUD) advanceResultBars(now uint32) {
	if h == nil || !h.resultState.initialized || h.resultState.group < 0 {
		return
	}
	for row := range h.resultState.rows {
		for column := 0; column < len(resultBars); column++ {
			if !h.resultState.active[column] {
				continue
			}
			target := resultBarValue(h.resultState.rows[row], column)
			// The kind-13 body is entered only while current < target. Exact
			// equality leaves animating set, but is not rescheduled or serviced.
			if h.resultState.current[row][column] >= target {
				continue
			}
			if !h.resultState.barDueSet[row][column] {
				h.resultState.barDueSet[row][column] = true
				h.resultState.barDue[row][column] = 0
			}
			if !h.resultState.animSet[row][column] {
				h.resultState.animSet[row][column] = true
				h.resultState.animating[row][column] = true
			}
			if !h.resultState.animating[row][column] {
				continue
			}
			// The service uses a strict due comparison. A jump still performs
			// one step and schedules from the sampled presentation unit [07 §11].
			if h.resultState.barDue[row][column] >= now {
				continue
			}
			next := h.resultState.current[row][column] + resultBarStep(target)
			if next > target {
				next = target
				h.resultState.animating[row][column] = false
			}
			h.resultState.current[row][column] = next
			h.resultState.barDue[row][column] = now + 1
		}
	}
}

func (h *retailBattleHUD) drawResultBar(c *client.Client, row, column, current, max int) {
	if h == nil || c == nil {
		return
	}
	x, y, x2, y2 := resultBarFrame(row, column)
	// Kind-13's standard raised two-pixel bevel is the shared art-less GUI
	// primitive: field 0 top/left, field 17 bottom/right, field 20 fill
	// [07 R-FE-02 §4]. The score bar's authored inner fields are dcb[8]/dcb[4].
	c.UIFillRect(x, y, x2-x+1, y2-y+1, h.guiColor(20))
	light, dark := h.guiColor(0), h.guiColor(17)
	c.UIFillRect(x, y, x2-x+1, 1, light)
	c.UIFillRect(x, y+1, x2-x, 1, light)
	c.UIFillRect(x, y, 1, y2-y+1, light)
	c.UIFillRect(x+1, y, 1, y2-y, light)
	c.UIFillRect(x2, y+1, 1, y2-y, dark)
	c.UIFillRect(x2-1, y+2, 1, y2-y-1, dark)
	c.UIFillRect(x+1, y2, x2-x, 1, dark)
	c.UIFillRect(x+2, y2-1, x2-x-1, 1, dark)

	innerX, innerY, innerW, innerH := x+2, y+2, 64, 15
	c.UIFillRect(innerX, innerY, innerW, innerH, h.guiColor(8))
	if fillX, ok := resultBarFillX(x, current, max); ok {
		if fillX > x+65 {
			fillX = x + 65
		}
		if fillX >= innerX {
			c.UIFillRect(innerX, innerY, fillX-innerX+1, innerH, h.guiColor(4))
		}
	}
	// The kind-13 painter selects the window's GAF-font slot 1 (hattfont11)
	// for its decimal and restores slot 0 afterwards; the number goes through
	// the GAF pen with no width limit and mode 0, centred at
	// `x + trunc(w/2) - trunc(tw/2)`, `y + trunc(h/2) - trunc(metric/2)` with
	// `metric` the capital-I height plus two. Only a null slot reaches the FNT
	// drawer [03 R-FONT-01 §6][07 R-HUD-03 §11].
	text := resultBarText(current)
	if h.modalFontSmall != nil {
		textWidth := retailGAFTextWidth(h.modalFontSmall, text)
		fontMetric := retailGAFTextHeight(h.modalFontSmall)
		drawRetailGAFText(c, h.modalFontSmall, text, x+33-textWidth/2, y+9-fontMetric/2, -1)
	} else if h.console != nil {
		textWidth := client.MeasureText(h.console, text)
		fontMetric := int(h.console.Height)
		c.UIText(h.console, text, x+33-textWidth/2, y+9-fontMetric/2, h.guiColor(15))
	}
}

// drawResultStats paints the dynamic ENDMSN rows. The authored window owns
// its background and controls; these rows are the runtime stat-bar appender
// records described by [08 R-CAMP-01 §7] and [07 R-HUD-03 §11].
func (h *retailBattleHUD) drawResultStats(c *client.Client, b *battleSession, view frame.ResultView) {
	if h == nil || c == nil || !view.Ended {
		return
	}
	h.ensureResultPresentation(view, b)
	// The reveal/climb deadlines below are presentation units of the ENDMSN
	// sequence's own clock [08 R-CAMP-01 §7][07 R-HUD-03 §11], not the battle
	// camera's scroll-pass clock. viewerStep stops driving the scroll pass
	// (and therefore stops advancing scrollAnchor) the moment the result
	// overlay takes the frame [07 §10], so a draw call reached only through
	// that overlay must not read scrollAnchor: it is frozen at whatever value
	// the last battle frame left it, which starves every strict `now`
	// comparison below after the one reveal/step that frozen value happens to
	// admit. b.postBattleNow() is the same 30-Hz unit counter the post-battle
	// controller itself steps on (stepPostBattle), so it keeps advancing for
	// as long as ENDMSN is being drawn.
	now := uint32(0)
	if b != nil {
		now = b.postBattleNow()
	}
	h.advanceResultReveal(now, c, b)
	h.advanceResultBars(now)
	if h.resultBarsComplete() && c.Cursors() != nil {
		c.Cursors().Hidden = false
		c.Cursors().SetIndex(render.CursorNormal)
	}
	for rowIndex, row := range h.resultState.rows {
		playerColor := resultPlayerColorRect(rowIndex)
		if h.logos != nil {
			if entry, ok := h.logos.Find("32xlogos"); ok && int(row.Logo) < len(entry.Frames) {
				if logo := entry.Frames[row.Logo].Frame; logo != nil {
					// The authored logo surface excludes the one-pixel source
					// border before stretching it to PlayerColor [07 §11].
					width, height := c.Size()
					c.UIBlitFrameSourceRectScaledClipped(logo, 1, 1, int(logo.Width)-1, int(logo.Height)-1, int(playerColor.X), int(playerColor.Y), int(playerColor.W), int(playerColor.H), 0, 0, width, height)
				}
			}
		}
		if h.console != nil && row.Name != "" {
			textWidth := client.MeasureText(h.console, row.Name)
			fontMetric := int(h.console.Height)
			x := int(playerColor.X) + (90-textWidth)/2
			y := int(playerColor.Y) + (20-fontMetric)/2
			c.UITextWidth(h.console, row.Name, x, y, 90, h.guiColor(15))
		}
		for column := 0; column < len(resultBars); column++ {
			if !h.resultState.active[column] {
				continue
			}
			current := h.resultState.current[rowIndex][column]
			max := h.resultState.max[column]
			h.drawResultBar(c, rowIndex, column, current, max)
		}
	}
}

func resultBarText(value int) string {
	return strconv.Itoa(value)
}

// editorFocused reports the authored type-3 focus owner available to the
// battle HUD. Battle side/modal windows have no runtime ui.Panel focus owner
// or text-input dispatcher; the shared authored result panel is the only
// battle-owned panel that can carry such focus. Returning false here is
// therefore an evidence-backed absence, not a second focus model [07 §4][07
// §6].
func (h *retailBattleHUD) editorFocused() bool {
	if h == nil || h.resultPanel == nil || h.resultPanel.Window == nil {
		return false
	}
	index := h.resultPanel.Focused()
	if index < 0 || index >= len(h.resultPanel.Window.Gadgets) {
		return false
	}
	gadget := h.resultPanel.Window.Gadgets[index]
	return gadget.Kind == gui.KindTextBox && gadget.Active != 0 && gadget.GrayedOut == 0 && h.resultPanel.ActiveAt(index)
}
