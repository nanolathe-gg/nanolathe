package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The result adapter must paint the populated mark bytes, selected row and
// retained scroll top, not merely expose them to input [08 R-CAMP-01 §8].
func TestCampaignResultPainterShowsMarksSelectionAndScrolledRows(t *testing.T) {
	font := formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	font.Frames['I'].Frame = &formats.GAFFrame{Width: 1, Height: 2}
	for _, glyph := range []struct {
		char  byte
		pixel byte
	}{{0xfd, 31}, {0xfe, 32}, {0xff, 34}, {'A', 33}, {'B', 35}, {' ', 0}} {
		font.Frames[glyph.char].Frame = &formats.GAFFrame{Width: 1, Height: 1, YOffset: 2, Pixels: []byte{glyph.pixel}, Transparent: []bool{false}}
	}
	pal := &palette.Tables{}
	for i := range pal.Base {
		pal.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
	}
	for i := range pal.Light {
		pal.Light[i] = 201
	}
	shell := &gameShell{assets: &menuAssets{gafFont: &formats.GAF{Entries: []formats.GAFEntry{font}}, pal: pal}}
	window := &gui.Window{Rect: gui.Rect{W: 24, H: 32}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindListBox, Name: "Missions", Active: 1, Attribs: 1, Rect: gui.Rect{X: 3, Y: 3, W: 16, H: 19}}}}
	panel := ui.NewPanel(window)
	panel.FillTextListAt(1, []string{"\xfd A", "\xfe B", "\xff A", "\xfd B"}, nil, 4)
	panel.ListAt(1).SetSelected(1)
	h := &retailBattleHUD{shell: shell, pal: pal, resultWin: window, resultPanel: panel}
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 24, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(pal)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { h.drawGUIWindow(c, window, nil, "") }))
	shot := c.ComposeFrameSnapshot()
	if shot.Indexed[5*24+5] != 31 || shot.Indexed[5*24+7] != 33 || shot.Indexed[10*24+5] != 201 {
		t.Fatal("result painter omitted mark, text or selected-row lightening")
	}
	panel.ListAt(1).SetTop(2)
	panel.ListAt(1).SetSelected(3)
	shot = c.ComposeFrameSnapshot()
	if shot.Indexed[5*24+5] != 34 || shot.Indexed[5*24+7] != 33 || shot.Indexed[10*24+5] != 201 {
		t.Fatal("result painter ignored retained scroll top or selection")
	}
	if panel.ListAt(1).Top() != 2 || shot.Indexed[2*24+5] != 0 {
		t.Fatal("result painter changed scroll state or wrote beyond the list")
	}

}

func TestCampaignResultInitialSelectionUsesFillScrollLimit(t *testing.T) {
	dir := t.TempDir()
	data := "[HEADER]{campaignside=ARM;}[MISSION0]{missionname=First;missionfile=one.ota;}[MISSION1]{missionname=Second;missionfile=two.ota;}[MISSION2]{missionname=Third;missionfile=three.ota;}"
	if err := os.WriteFile(filepath.Join(dir, "campaign.tdf"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ height, top int }{{64, 0}, {2, 1}} {
		panel := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindListBox, Name: "Missions", Rect: gui.Rect{H: int32(tc.height)}}}})
		b := &battleSession{fs: fs, hud: &retailBattleHUD{resultPanel: panel}, sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign, CampaignPath: "campaign.tdf"}}}
		b.postBattle = session.NewPostBattleController(frame.ResultView{Ended: true, Kind: "defeat"}, session.PostBattleConfig{Kind: session.PostBattleCampaign, MissionIndex: 2})
		b.populateResultMissions()
		list := panel.ListAt(1)
		if list.Selected() != list.Len()-1 || list.Top() != tc.top || list.Top() != panel.ListMaxTopAt(1) {
			t.Fatalf("height=%d selection=%d top=%d max=%d; want last row at fill limit %d", tc.height, list.Selected(), list.Top(), panel.ListMaxTopAt(1), tc.top)
		}
	}
}

func TestCampaignCoreBriefingUsesAuthoredBackground(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	g := &gameShell{cs: cs, missionSide: 1}
	bg := g.briefingBackground()
	if bg == nil || bg.Width != 640 || bg.Height != 480 {
		t.Fatal("Core briefing lost the authored mbriefcor background")
	}
}

// Admission uses the font active at entry, even if the gadget then selects a
// shorter FNT for its glyphs [07 R-WGT-01 §4].
func TestCampaignResultListCachesAdmissionMetricBeforeFontSelection(t *testing.T) {
	dir := t.TempDir()
	// One authored glyph: code 255, one pixel wide and high.
	if err := os.WriteFile(filepath.Join(dir, "row.fnt"), []byte{1, 0, 0, 255, 6, 0, 1, 128}, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	g := &gameShell{cs: &contentSet{fs: fs}, font: &formats.FNT{Height: 4}}
	window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindListBox, Name: "Missions", Attribs: 0x101, ColorF: 7, Rect: gui.Rect{X: 1, Y: 1, W: 10, H: 8}}, {Kind: gui.KindFont, FilePath: "row.fnt"}}}
	p := ui.NewPanel(window)
	p.FillTextListAt(1, []string{"\xff", "\xff"}, nil, 4)
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 16, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	c.SetFNT(g.font)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { g.drawRetailList(c, p, 1, window.Gadgets[1], window.Gadgets[1].Rect) }))
	shot := c.ComposeFrameSnapshot()
	if shot.Indexed[3*16+3] != 7 || shot.Indexed[8*16+3] != 0 {
		t.Fatal("list admission followed selected FNT height instead of cached entry metric")
	}
}
