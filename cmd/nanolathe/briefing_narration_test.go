package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type briefingStreamOutput struct {
	plays, stops int
}

func (*briefingStreamOutput) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (o *briefingStreamOutput) PlayStream(*audio.Sample, float64) error        { o.plays++; return nil }
func (o *briefingStreamOutput) StopStream()                                    { o.stops++ }

func installBriefingStreamOutput(t *testing.T) *briefingStreamOutput {
	t.Helper()
	old := audio.GlobalOutput()
	o := &briefingStreamOutput{}
	audio.SetGlobalOutput(o)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	return o
}

// A complete click must deliver one staged callback, not a second toggle on
// the initial down edge [07 R-WGT-01 §3][03 R-AUD-02 §1].
func TestBriefingNarrationClickStopsAndRestartsOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "camps", "briefs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "camps", "briefs", "voice.wav"), loopTestWAV(), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	o := installBriefingStreamOutput(t)
	p := ui.NewPanel(&gui.Window{Rect: gui.Rect{W: 200, H: 100}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "SHUTUP", Active: 1, Stages: 2, Rect: gui.Rect{X: 10, Y: 10, W: 80, H: 20}},
	}})
	p.SetStageAt(1, 1)
	g := &gameShell{briefing: NewCampaignBriefingController(briefingMission(t, "Lava"), 0, &countingRand{}, nil), briefingPanel: p, audioOwner: audio.NewService(fs)}
	cl, err := client.New(client.Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	g.consumeBriefingAudio(g.briefing.OpeningAudio())
	g.audioOwner.TickStream(60)
	if o.plays != 1 {
		t.Fatal("opening narration never played")
	}
	g.briefingNowMS = 2000
	cl.Input().Mouse.SetPosition(20, 20)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	g.briefingInput(cl)
	if !g.briefing.NarrationOn() || o.stops != 0 {
		t.Fatal("press fired narration before release")
	}
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	g.briefingInput(cl)
	if g.briefing.NarrationOn() || p.StageAt(1) != 0 || o.stops != 1 {
		t.Fatal("release did not stop narration exactly once")
	}
	g.audioOwner.TickStream(120)
	if o.plays != 1 {
		t.Fatal("stop click also scheduled a restart")
	}
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	g.briefingInput(cl)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	g.briefingInput(cl)
	if !g.briefing.NarrationOn() || p.StageAt(1) != 1 {
		t.Fatal("second click did not enable narration")
	}
	g.audioOwner.TickStream(119)
	if o.plays != 1 {
		t.Fatal("restart ignored the authored delay")
	}
	g.audioOwner.TickStream(120)
	if o.plays != 2 || o.stops != 1 {
		t.Fatalf("restart plays/stops=%d/%d", o.plays, o.stops)
	}
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	g.briefingInput(cl)
	cl.Input().Mouse.SetPosition(150, 70)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	g.briefingInput(cl)
	if !g.briefing.NarrationOn() || p.StageAt(1) != 1 || o.stops != 1 {
		t.Fatal("release outside narration button changed its state")
	}
}

// Reopening a briefing starts its clock before scheduling the delayed stream;
// a previous visit's elapsed time must not lengthen the delay [08 R-CAMP-01 §2].
func TestRetailBriefingNarrationReopenDelay(t *testing.T) {
	g, _ := retailShellForTest(t)
	o := installBriefingStreamOutput(t)
	g.missionSide = 0
	g.openMenu(modeMenuMission)
	found := false
	for i, campaign := range g.campaignOptions {
		if campaign.Path == "camps/arm campaign.tdf" {
			g.campaignIdx, found = i, true
			break
		}
	}
	if !found {
		t.Fatal("Arm campaign missing")
	}
	g.missionIdx = 0
	g.briefingNowMS = 50000
	g.openCampaignBriefing()
	if g.briefing == nil || g.briefingPanel == nil {
		t.Fatal("briefing did not open")
	}
	idx := g.briefingPanel.Index("SHUTUP")
	if idx < 0 || g.briefingPanel.StageAt(idx) != 1 {
		t.Fatal("narration toggle did not start enabled")
	}
	g.audioOwner.TickStream(59)
	if o.plays != 0 {
		t.Fatal("narration started before its delay")
	}
	g.audioOwner.TickStream(60)
	if o.plays != 1 {
		t.Fatalf("reopened narration plays=%d; path=%q", o.plays, g.briefing.narrationPath)
	}
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	r := g.briefingPanel.Window.PlacedRect(idx)
	cl.Input().Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	g.briefingInput(cl)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	g.briefingInput(cl)
	if g.briefing.NarrationOn() || g.briefingPanel.StageAt(idx) != 0 || o.stops != 1 {
		t.Fatal("stock narration button did not stop the decoded stream once")
	}
}
