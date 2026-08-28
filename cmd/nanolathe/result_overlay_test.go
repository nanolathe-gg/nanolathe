package main

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

type resultOverlayStage struct {
	hud    *retailBattleHUD
	battle *battleSession
}

func (s resultOverlayStage) DrawUI(c *client.Client, presented client.UIFrame) {
	if presented.Committed != nil {
		s.hud.drawResultOverlay(c, s.battle, presented.Committed.Result)
	}
}

func TestResultTitleFrameUsesExplicitAuthoredOutcome(t *testing.T) {
	h := &retailBattleHUD{
		resultVictoryFrame: &formats.GAFFrame{},
		resultDefeatFrame:  &formats.GAFFrame{},
	}
	if got := h.resultTitleFrame(frame.ResultView{Kind: "victory"}); got != h.resultVictoryFrame {
		t.Fatal("victory did not select authored victory title")
	}
	if got := h.resultTitleFrame(frame.ResultView{Kind: "defeat"}); got != h.resultDefeatFrame {
		t.Fatal("defeat did not select authored defeat title")
	}
	if got := h.resultTitleFrame(frame.ResultView{Draw: true, Kind: "victory"}); got != nil {
		t.Fatal("draw selected a terminal title")
	}
	if got := h.resultTitleFrame(frame.ResultView{Kind: "unknown", WinnerTeam: -1}); got != nil {
		t.Fatal("unknown result selected a terminal title")
	}
}

func TestResultPanelActivatesOnlyEstablishedRoute(t *testing.T) {
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Name: "GADGET0"},
		{Name: "Start"},
		{Name: "MainMenu"},
		{Name: "LoadGame"},
	}}
	panel := ui.NewPanel(window)
	configureResultPanel(nil, nil, panel)
	if panel.ActiveOf("Start") {
		t.Fatal("Start activated without campaign-next provenance")
	}
	if !panel.ActiveOf("MainMenu") {
		t.Fatal("MainMenu not activated for terminal route")
	}
	if panel.ActiveOf("LoadGame") {
		t.Fatal("untraced result control was force-enabled")
	}
}

func TestBattleHUDEditorFocusUsesAuthoredType3Owner(t *testing.T) {
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Name: "GADGET0"},
		{Name: "CHAT", Kind: gui.KindTextBox, Active: 1},
		{Name: "DISABLED", Kind: gui.KindTextBox, Active: 0},
	}}
	panel := ui.NewPanel(window)
	h := &retailBattleHUD{resultPanel: panel}
	panel.SetFocus(1)
	if !h.editorFocused() {
		t.Fatal("focused authored type-3 gadget was not reported")
	}
	panel.SetFocus(2)
	if h.editorFocused() {
		t.Fatal("inactive authored type-3 gadget was reported as focused")
	}
	panel.SetFocus(0)
	if h.editorFocused() {
		t.Fatal("non-editor gadget was reported as focused")
	}
}

func TestResultAssetsRemainOptionalWhenUnavailable(t *testing.T) {
	fs := vfs.New()
	if got := loadGUIOptional(fs, "guis/endmsn.gui", "test ENDMSN"); got != nil {
		t.Fatal("missing ENDMSN GUI unexpectedly loaded")
	}
	if got := loadGAFOptional(fs, "anims/endmsn.gaf", "test endmsn"); got != nil {
		t.Fatal("missing endmsn GAF unexpectedly loaded")
	}
}

func TestResultVisibilityUsesAuthoritativeLatch(t *testing.T) {
	b := &battleSession{sess: &session.Session{}}
	if b.isResultVisible() {
		t.Fatal("unlatched result must not be visible")
	}
	// This test is intentionally limited to the committed result seam; the
	// session package owns construction of a terminal result.
}

func TestResultOverlayHonorsCanonicalDismissalState(t *testing.T) {
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Result = frame.ResultView{Ended: true, Kind: "victory"}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{resultVictoryFrame: &formats.GAFFrame{
		Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false},
	}}
	b := &battleSession{}
	c, err := client.New(client.Options{Buffer: buf, Width: 8, Height: 8, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetUIStage(resultOverlayStage{hud: h, battle: b})
	img := c.ComposeFrame()
	if got := img.RGBAAt(4, 4); got != (color.RGBA{R: 7, G: 7, B: 7, A: 255}) {
		t.Fatalf("visible terminal result pixel = %#v, want authored title pixel", got)
	}

	b.battleState().Input.ResultDismissed = true
	img = c.ComposeFrame()
	if got := img.RGBAAt(4, 4); got != (color.RGBA{A: 255}) {
		t.Fatalf("dismissed terminal result pixel = %#v, stale ENDMSN/title art still rendered", got)
	}
}

func TestResultControlRequiresAuthoredStart(t *testing.T) {
	if got := resultActionForControl("START"); got != "result_continue" {
		t.Fatalf("authored Start action = %q", got)
	}
	for _, name := range []string{"Continue", "Retry", "Main Menu", "Skirmish Setup", "synthetic"} {
		if got := resultActionForControl(name); got != "" {
			t.Fatalf("unsupported result control %q mapped to %q", name, got)
		}
	}
}

func TestResultMainMenuActionKeepsSemanticTransition(t *testing.T) {
	called := false
	b := &battleSession{returnToMenu: func(*client.Client) { called = true }}
	b.doResultAction("result_main", nil)
	if !called {
		t.Fatal("result main-menu action did not invoke semantic callback")
	}
	if !b.battleState().Input.ResultDismissed {
		t.Fatal("result main-menu action did not consume the result state")
	}
}

func TestResultContinueRequiresCommittedResult(t *testing.T) {
	// Contradictory live fields are intentionally ignored. A result action has
	// no effect until the terminal frame has published an ended ResultView
	// [03 §2.4][07 §11][I6].
	sess := &session.Session{
		Mission:      &mission.Mission{Type: mission.TypeCampaign},
		CampaignSlot: 0,
		VictoryDone:  true,
		Progress:     session.BankProgress{WL: [10]byte{'W'}},
	}
	sess.Latch.Bits = session.LatchBitEnding | session.LatchBitWin1
	called := false
	b := &battleSession{sess: sess, returnToMenu: func(*client.Client) { called = true }}
	b.doResultAction("result_continue", nil)
	if called {
		t.Fatal("live victory fields overrode the absence of a committed result")
	}
	if b.battleState().Input.ResultDismissed {
		t.Fatal("uncommitted result action was consumed")
	}
}

func TestResultContinuationUsesCommittedKind(t *testing.T) {
	// Only the committed outcome controls whether a campaign can advance. The
	// live session is deliberately contradictory for every case to guard the
	// old WL/latch/trigger inference seam.
	live := &session.Session{
		Mission:      &mission.Mission{Type: mission.TypeCampaign},
		CampaignSlot: 0,
		VictoryDone:  true,
		DefeatDone:   true,
		Progress:     session.BankProgress{WL: [10]byte{'W'}},
	}
	live.Latch.Bits = session.LatchBitEnding | session.LatchBitWin1
	for _, tc := range []struct {
		name string
		view frame.ResultView
		want bool
	}{
		{name: "victory", view: frame.ResultView{Ended: true, Kind: "victory"}, want: true},
		{name: "defeat", view: frame.ResultView{Ended: true, Kind: "defeat"}, want: false},
		{name: "draw", view: frame.ResultView{Ended: true, Kind: "draw", Draw: true}, want: false},
		{name: "unknown", view: frame.ResultView{Ended: true, Kind: "unresolved"}, want: false},
		{name: "draw-flagged-victory", view: frame.ResultView{Ended: true, Kind: "victory", Draw: true}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := frame.NewBuffer()
			write := buf.BeginWrite()
			write.Result = tc.view
			if err := buf.Publish(1); err != nil {
				t.Fatal(err)
			}
			live.Snapshot = buf
			committed := (&battleSession{sess: live}).resultView()
			if got := resultContinuesCampaign(committed); got != tc.want {
				t.Fatalf("committed result %+v: continuation=%t, want %t", committed, got, tc.want)
			}
		})
	}
}

func TestResultStartAvailabilityUsesNextMissionMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "camps"), 0o755); err != nil {
		t.Fatalf("mkdir camps: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "camps", "arm.tdf"), []byte(`
[HEADER]
{
campaignside=ARM;
}
[MISSION0]
{
missionfile=AC01.ota;
}
[MISSION1]
{
missionfile=AC02.ota;
}
`), 0o644); err != nil {
		t.Fatalf("write campaign: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatalf("mount campaign: %v", err)
	}

	sess := &session.Session{Mission: &mission.Mission{
		Type:          mission.TypeCampaign,
		CampaignPath:  "camps/arm.tdf",
		CampaignIndex: 0,
	}}
	window := &gui.Window{Gadgets: []gui.Gadget{{Name: "Start"}, {Name: "MainMenu"}}}
	panel := ui.NewPanel(window)
	configureResultPanel(fs, sess, panel)
	if !panel.ActiveOf("Start") || panel.ActiveOf("MainMenu") {
		t.Fatalf("next-mission metadata did not select authored Start route: start=%t main=%t", panel.ActiveOf("Start"), panel.ActiveOf("MainMenu"))
	}
}
