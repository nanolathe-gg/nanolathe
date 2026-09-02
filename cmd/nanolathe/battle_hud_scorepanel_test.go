package main

import (
	"image/color"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/session"
)

// scorePanelStage renders only the score panel so the composed framebuffer is
// exactly what [07 R-HUD-04 §1] paints. `--shot` cannot hold Space, so the
// visual evidence for this panel is these pixel assertions rather than a PNG.
type scorePanelStage struct {
	hud    *retailBattleHUD
	battle *battleSession
}

func (s scorePanelStage) DrawUI(c *client.Client, presented client.UIFrame) {
	s.hud.drawScorePanel(c, s.battle, presented.Committed)
}

// scorePanelTables marks the two operators the panel uses so a composed pixel
// names which one wrote it: the rectangle shader at level -24 addresses SHD
// row 8 (-24 + 32), and the local row's two lightening passes address LHT
// rows 31 and 20 [07 R-HUD-04 §1][03 R-COMP-02 §5].
func scorePanelTables() *palette.Tables {
	t := &palette.Tables{}
	for row := range t.Shade {
		for i := range t.Shade[row] {
			t.Shade[row][i] = byte(i)
		}
	}
	for i := range t.Light {
		t.Light[i] = byte(i % 256)
	}
	// SHD row 8 is the -24 shader; make it write a distinctive index.
	for i := range t.Shade[8] {
		t.Shade[8][i] = 40
	}
	// LHT row 31 then row 20: the first lifts the shaded 40 to 41, the second
	// lifts 41 to 42, so a local row's pixel is 42 and a non-local row's is 40.
	t.Light[31*256+40] = 41
	t.Light[20*256+41] = 42
	// Base entries so the composed RGBA is readable.
	for i := range t.Base {
		t.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
	}
	return t
}

// scorePanelPlayers is the two-slot fixture: both records present with live
// units, ranks in slot order, the local player in slot 1.
func scorePanelPlayers() [frame.PlayerRowSlots]frame.PlayerRow {
	var rows [frame.PlayerRowSlots]frame.PlayerRow
	rows[0] = frame.PlayerRow{Present: true, Name: "Enemy", Controller: 2, LiveUnits: 4, Kills: 3, Losses: 1, Rank: 0}
	rows[1] = frame.PlayerRow{Present: true, Name: "Local", Controller: 1, LiveUnits: 6, Kills: 5, Losses: 2, Rank: 1}
	return rows
}

func scorePanelFixture(t *testing.T, campaign bool) (*client.Client, *retailBattleHUD, *battleSession) {
	t.Helper()
	return scorePanelFixtureWith(t, campaign, scorePanelPlayers(), 1, 2)
}

// scorePanelFixtureWith publishes one committed frame carrying the given
// per-tick player rows, local player and player-count word (one economy row
// per occupied slot, which is what the panel's geometry counts).
func scorePanelFixtureWith(t *testing.T, campaign bool, players [frame.PlayerRowSlots]frame.PlayerRow, local uint8, playerCount int) (*client.Client, *retailBattleHUD, *battleSession) {
	t.Helper()
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Economy = w.Economy[:0]
	for i := 0; i < playerCount; i++ {
		w.Economy = append(w.Economy, frame.EconomyView{Player: uint8(i), Active: true})
	}
	w.Selection.LocalPlayer = local
	w.Players = players
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{}
	if campaign {
		sess.Mission = &mission.Mission{Type: mission.TypeCampaign}
	}
	h := &retailBattleHUD{pal: scorePanelTables()}
	b := &battleSession{sess: sess}
	c, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetUIStage(scorePanelStage{hud: h, battle: b})
	return c, h, b
}

func gray(v byte) color.RGBA { return color.RGBA{R: v, G: v, B: v, A: 255} }

// The panel is hidden until the hold flag is set, and then slides in from the
// right edge one quarter of the remaining distance per composed frame
// [07 R-HUD-04 §1].
func TestScorePanelDrawsOnlyWhileHeldAndSlidesFromTheRightEdge(t *testing.T) {
	c, h, b := scorePanelFixture(t, false)
	img := c.ComposeFrame()
	if got := img.RGBAAt(639, 40); got != gray(0) {
		t.Fatalf("unheld frame painted the right edge: %#v", got)
	}
	if h.score != 0 {
		t.Fatalf("unheld slide word = %d, want 0", h.score)
	}

	b.panelHoldFlag = true
	img = c.ComposeFrame()
	if h.score != 31 {
		t.Fatalf("first held frame slide word = %d, want 31 (125/4)", h.score)
	}
	// x0 = 640 - 31 = 609: the panel body is darkened from there rightward and
	// nothing to its left is touched.
	if got := img.RGBAAt(609, 40); got != gray(40) {
		t.Fatalf("panel body pixel at x0 = %#v, want the -24 shader's mark", got)
	}
	if got := img.RGBAAt(608, 40); got != gray(0) {
		t.Fatalf("pixel left of x0 = %#v, want untouched background", got)
	}
	// y0 is 32 and y1 is 40*2 + 46 = 126.
	if got := img.RGBAAt(620, 31); got != gray(0) {
		t.Fatalf("pixel above y0 = %#v, want untouched background", got)
	}
	if got := img.RGBAAt(620, 126); got != gray(0) {
		t.Fatalf("pixel at y1 = %#v, want untouched background (y1 is exclusive)", got)
	}
	// x 610 is inside the body but left of the local row's highlight, which
	// starts at x0+4.
	if got := img.RGBAAt(610, 125); got != gray(40) {
		t.Fatalf("last panel scanline = %#v, want the -24 shader's mark", got)
	}

	// Releasing the hold retracts it; once retracted nothing is drawn again.
	b.panelHoldFlag = false
	for i := 0; i < 32 && h.score != 0; i++ {
		c.ComposeFrame()
	}
	if h.score != 0 {
		t.Fatalf("released slide word = %d, want 0", h.score)
	}
	img = c.ComposeFrame()
	if got := img.RGBAAt(639, 40); got != gray(0) {
		t.Fatalf("retracted panel still painted: %#v", got)
	}
}

// The local player's row is lightened twice, at 31 then 20; another player's
// row is not [07 R-HUD-04 §1].
func TestScorePanelLightensOnlyTheLocalPlayersRow(t *testing.T) {
	c, h, b := scorePanelFixture(t, false)
	b.panelHoldFlag = true
	for i := 0; i < 24 && h.score != hud.ScorePanelWidth; i++ {
		c.ComposeFrame()
	}
	img := c.ComposeFrame()
	if h.score != hud.ScorePanelWidth {
		t.Fatalf("slide word = %d, want the open detent 125", h.score)
	}
	// The published rows are player 0 then player 1; the local player is 1, so
	// the second drawn row (top 87) carries the highlight and the first (47)
	// does not.
	if got := img.RGBAAt(600, 90); got != gray(42) {
		t.Fatalf("local row pixel = %#v, want the 31-then-20 lightening", got)
	}
	if got := img.RGBAAt(600, 50); got != gray(40) {
		t.Fatalf("non-local row pixel = %#v, want the shaded body only", got)
	}
	// The highlight spans (x0+4)..(x1-4) inclusive of the left edge only.
	if got := img.RGBAAt(516, 90); got != gray(40) {
		t.Fatalf("pixel left of the highlight = %#v, want the shaded body", got)
	}
	if got := img.RGBAAt(519, 90); got != gray(42) {
		t.Fatalf("highlight left edge = %#v, want the lightened row", got)
	}
}

// One row is drawn per qualifying slot, in rank order, and a vacated rank is
// compacted in the same frame [07 R-HUD-04 §1]. Five slots are published:
// slot 1 carries the neutral side 10 and slot 4 is a watcher, so neither
// qualifies; the three that do collapse onto ranks 0, 1, 2 and are drawn at
// 47, 87 and 127. The local player is slot 3, whose row is the third drawn —
// which is only true if the two rejected slots consumed no row.
func TestScorePanelDrawsOneRowPerQualifyingSlotInRankOrder(t *testing.T) {
	var rows [frame.PlayerRowSlots]frame.PlayerRow
	rows[0] = frame.PlayerRow{Present: true, Name: "A", Controller: 1, LiveUnits: 1, Rank: 0}
	rows[1] = frame.PlayerRow{Present: true, Name: "Neutral", Controller: 1, LiveUnits: 1, Side: hud.ScoreSideExcluded, Rank: 1}
	rows[2] = frame.PlayerRow{Present: true, Name: "B", Controller: 2, LiveUnits: 1, Rank: 2}
	rows[3] = frame.PlayerRow{Present: true, Name: "Local", Controller: 1, LiveUnits: 1, Rank: 3}
	rows[4] = frame.PlayerRow{Present: true, Name: "Watcher", Controller: 1, LiveUnits: 1, Watcher: true, Rank: 4}
	c, h, b := scorePanelFixtureWith(t, false, rows, 3, 5)
	b.panelHoldFlag = true
	for i := 0; i < 24 && h.score != hud.ScorePanelWidth; i++ {
		c.ComposeFrame()
	}
	img := c.ComposeFrame()
	if h.score != hud.ScorePanelWidth {
		t.Fatalf("slide word = %d, want the open detent 125", h.score)
	}
	// Drawn rows 0 and 1 (tops 47 and 87) are slots 0 and 2, neither local.
	if got := img.RGBAAt(600, 50); got != gray(40) {
		t.Fatalf("first drawn row = %#v, want the shaded body only", got)
	}
	if got := img.RGBAAt(600, 90); got != gray(40) {
		t.Fatalf("second drawn row = %#v, want the shaded body only", got)
	}
	// Drawn row 2 (top 127) is slot 3, the local player.
	if got := img.RGBAAt(600, 130); got != gray(42) {
		t.Fatalf("third drawn row = %#v, want the local player's lightening", got)
	}
	// There is no fourth row: the two rejected slots drew nothing, so the
	// remaining panel body is unlit.
	if got := img.RGBAAt(600, 170); got != gray(40) {
		t.Fatalf("fourth row band = %#v, want the shaded body only", got)
	}
}

// A present slot whose controller byte is outside 1..3 fails the row filter,
// so the panel draws its body and headings with no rows at all
// [07 R-HUD-04 §1].
func TestScorePanelDrawsNoRowWhenNoSlotQualifies(t *testing.T) {
	var rows [frame.PlayerRowSlots]frame.PlayerRow
	// Present, but the controller byte is outside 1..3.
	rows[0] = frame.PlayerRow{Present: true, Name: "Idle", Controller: 0, LiveUnits: 1, Rank: 0}
	c, h, b := scorePanelFixtureWith(t, false, rows, 0, 1)
	b.panelHoldFlag = true
	for i := 0; i < 24 && h.score != hud.ScorePanelWidth; i++ {
		c.ComposeFrame()
	}
	img := c.ComposeFrame()
	// The body is still darkened; no row is lightened.
	if got := img.RGBAAt(600, 50); got != gray(40) {
		t.Fatalf("unqualified slot drew a row: %#v", got)
	}
}

// A campaign mission is session kind 1; the composer never calls the panel
// there and its slide word stays inert [07 R-HUD-04 §1].
func TestScorePanelNeverDrawsInACampaignMission(t *testing.T) {
	c, h, b := scorePanelFixture(t, true)
	b.panelHoldFlag = true
	for i := 0; i < 24; i++ {
		c.ComposeFrame()
	}
	if h.score != 0 {
		t.Fatalf("campaign slide word = %d, want 0 (inert)", h.score)
	}
	img := c.ComposeFrame()
	if got := img.RGBAAt(639, 40); got != gray(0) {
		t.Fatalf("campaign frame painted the score panel: %#v", got)
	}
}

// The flash arrays decay once per committed tick, whether or not the panel is
// showing [07 R-HUD-04 §1].
func TestScoreFlashDecaysOncePerCommittedTickWhileHidden(t *testing.T) {
	c, h, _ := scorePanelFixture(t, false)
	h.scoreFlash.Credit(1, 0)
	c.ComposeFrame()
	c.ComposeFrame()
	c.ComposeFrame()
	if got := h.scoreFlash.Kills[1]; got != 28 {
		t.Fatalf("kill flash after three composed frames of one tick = %d, want 28", got)
	}
	if got := h.scoreFlash.Losses[0]; got != 28 {
		t.Fatalf("loss flash = %d, want 28", got)
	}
}
