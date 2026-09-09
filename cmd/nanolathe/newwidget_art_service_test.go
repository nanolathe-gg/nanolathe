package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func widgetArtEntry(name string, colors ...byte) formats.GAFEntry {
	e := formats.GAFEntry{Name: name}
	for _, color := range colors {
		pixels := make([]byte, 100)
		for i := range pixels {
			pixels[i] = color
		}
		e.Frames = append(e.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{
			Width: 10, Height: 10, Compressed: 1, Pixels: pixels, Transparent: make([]bool, len(pixels)),
		}})
	}
	return e
}

func widgetArtFixture(t *testing.T, gadgets []gui.Gadget, entries ...formats.GAFEntry) (*gameShell, *ui.Panel, *input.State) {
	t.Helper()
	w := &gui.Window{Rect: gui.Rect{W: 80, H: 30}, Gadgets: append([]gui.Gadget{{Kind: gui.KindPanel, Active: 1}}, gadgets...)}
	shell := &gameShell{
		frontend: ui.NewFrontend(modeMenuMain),
		assets:   &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: w, art: &formats.GAF{Entries: entries}}}},
	}
	shell.installRetailWindowButtonArt(w, shell.assets.panel[modeMenuMain].art)
	p := ui.NewPanel(w)
	shell.frontend.Panels.Replace(p)
	return shell, p, input.NewState()
}

func widgetLeftDown(in *input.State, x, y float32) {
	in.Mouse.SetPosition(x, y)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
}

// The cycle's modulo is the resolved entry count [07 R-WGT-01 §3]. This
// exercises the menu adapter rather than supplying a test-only hook to
// Panel.ServiceFrame.
func TestMenuWidgetCycleUsesResolvedArtCount(t *testing.T) {
	for _, tc := range []struct {
		name, art string
		frames    int
	}{
		{name: "five resolved art frames", art: "FIVE", frames: 5},
		{name: "three resolved art frames", art: "THREE", frames: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			colors := make([]byte, tc.frames)
			for i := range colors {
				colors[i] = byte(i + 1)
			}
			shell, p, in := widgetArtFixture(t, []gui.Gadget{{
				Kind: gui.KindButton, Name: tc.art, Art: tc.art, Active: 1, Attribs: 0x100,
				Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10},
			}}, widgetArtEntry(tc.art, colors...))
			p.SetStatusAt(1, tc.frames-1)
			widgetLeftDown(in, 3, 3)
			shell.serviceMenuWidgets(p, in)
			if got := p.DownAt(1); got != 0 {
				t.Fatalf("cycle down=%d, want wrap through %d resolved frames", got, tc.frames)
			}
			if got := p.StageAt(1); got != 0 {
				t.Fatalf("cycle changed independent stage to %d", got)
			}
		})
	}
}

// In-battle preferences use their own adapter, but must supply the same
// resolved-art count to the shared service [07 R-WGT-01 §3].
func TestBattleOptionsWidgetCycleUsesResolvedArtCount(t *testing.T) {
	shell, p, in := widgetArtFixture(t, []gui.Gadget{{
		Kind: gui.KindButton, Name: "BATTLECYCLE", Art: "BATTLECYCLE", Active: 1,
		Attribs: 0x100, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10},
	}}, widgetArtEntry("BATTLECYCLE", 3, 7, 11, 13, 17))
	previousPanel := optionsPanel
	optionsPanel = p
	t.Cleanup(func() { optionsPanel = previousPanel })
	p.SetStatusAt(1, 4)
	widgetLeftDown(in, 3, 3)
	(&battleSession{shell: shell}).serviceBattleOptionsWidgets(p, in)
	if got := p.DownAt(1); got != 0 {
		t.Fatalf("battle options cycle down=%d, want wrap through five resolved frames", got)
	}
}

// A staged button releases up, retains its changed stage, and repaints the
// stage's own frame. The same production path must use that stage for its
// caption selection [07 R-WGT-01 §3].
func TestMenuWidgetReleasedStageRepaintsAndPersists(t *testing.T) {
	shell, p, in := widgetArtFixture(t, []gui.Gadget{{
		Kind: gui.KindButton, Name: "STAGE", Art: "STAGE", Active: 1, Stages: 2,
		Text: "Off|On", Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10},
	}}, widgetArtEntry("STAGE", 3, 7, 11, 13))
	widgetLeftDown(in, 3, 3)
	shell.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	shell.serviceMenuWidgets(p, in)
	if p.DownAt(1) != 0 || p.StageAt(1) != 1 {
		t.Fatalf("released staged state down/stage=%d/%d, want 0/1", p.DownAt(1), p.StageAt(1))
	}
	if got := retailGadgetText(p, 1, p.Window.Gadgets[1]); got != "On" {
		t.Fatalf("released staged caption=%q, want On", got)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 80, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	for repaint := 0; repaint != 2; repaint++ {
		snap := cl.ComposeFrameSnapshot()
		if got := snap.Indexed[3*snap.Width+3]; got != 7 {
			t.Fatalf("repaint %d frame=%d, want released stage frame 7", repaint, got)
		}
		if got := retailGadgetText(p, 1, p.Window.Gadgets[1]); got != "On" {
			t.Fatalf("repaint %d caption=%q, want persisted On", repaint, got)
		}
	}
}

// The painter follows Panel capture and held state. Moving outside a captured
// plain button clears its down-state and repaints base; hover alone cannot
// select the pressed frame [07 R-WGT-01 §1][07 R-WGT-01 §3].
func TestMenuWidgetPainterRepaintsOutsideCapture(t *testing.T) {
	shell, p, in := widgetArtFixture(t, []gui.Gadget{{
		Kind: gui.KindButton, Name: "PLAIN", Art: "PLAIN", Active: 1,
		Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10},
	}}, widgetArtEntry("PLAIN", 3, 7, 11))
	widgetLeftDown(in, 3, 3)
	shell.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(30, 3)
	shell.serviceMenuWidgets(p, in)
	if p.DownAt(1) != 0 {
		t.Fatalf("outside captured button stayed down=%d", p.DownAt(1))
	}
	if got := shell.retailButtonArt(p.Window.Gadgets[1], p.DownAt(1), p.StageAt(1), false); got != shell.resolveRetailButtonArt(p.Window.Gadgets[1]).entry.Frames[0].Frame {
		t.Fatal("outside repaint did not choose the base frame")
	}
}

// input.MouseButtonLeft is already the service's left capture bit. This locks
// the adapter/painter boundary against an accidental +1 conversion.
func TestRetailButtonPressedUsesRawCaptureBits(t *testing.T) {
	p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {
		Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 10, H: 10},
	}}})
	p.ServiceFrame(ui.WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, ui.WidgetHooks{})
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 20, Height: 20})
	if err != nil {
		t.Fatal(err)
	}
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	if !retailButtonPressed(cl, p, 1) {
		t.Fatal("left capture bit 1 was not painted as pressed")
	}
	p.ResetPress()
	p.ServiceFrame(ui.WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 2, PointerEvents: []input.PointerEvent{{Kind: input.RightDown, X: 2, Y: 2}}}, ui.WidgetHooks{})
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	cl.Input().Mouse.SetButton(input.MouseButtonRight, true)
	if !retailButtonPressed(cl, p, 1) {
		t.Fatal("right capture bit 2 was not painted as pressed")
	}
}

// A pointer release arrives at the options callback after the shared service
// has already advanced the stage. SPEECH and UNITCHAT must consume that stage,
// whereas their direct/key callback path advances it itself [07 R-WGT-01 §3].
func TestMenuWidgetOptionsPointerConsumesServiceStage(t *testing.T) {
	w := &gui.Window{Rect: gui.Rect{W: 80, H: 30}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "SPEECH", Active: 1, Stages: 3, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}},
		{Kind: gui.KindButton, Name: "UNITCHAT", Active: 1, Stages: 3, Rect: gui.Rect{X: 20, Y: 2, W: 10, H: 10}},
	}}
	p := ui.NewPanel(w)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(p)
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	optionsPanel = p
	optionsAssets = &retailPanelAssets{window: w}
	optionsState = &retailOptionsState{serviceStageIndex: -1}
	t.Cleanup(func() {
		optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState
	})
	in := input.NewState()
	for _, tc := range []struct {
		name string
		want int
	}{
		{name: "SPEECH", want: 5},
		{name: "UNITCHAT", want: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := p.Index(tc.name)
			r := p.Window.Gadgets[index].Rect
			widgetLeftDown(in, float32(r.X+1), float32(r.Y+1))
			shell.serviceMenuWidgets(p, in)
			in.Mouse.ResetEdges()
			in.Mouse.SetButton(input.MouseButtonLeft, false)
			shell.serviceMenuWidgets(p, in)
			if got := p.StageAt(index); got != 1 {
				t.Fatalf("pointer release stage=%d, want 1 (one service advance)", got)
			}
			if tc.name == "SPEECH" {
				if got := shell.audioPrefs.UnitChat; got != tc.want {
					t.Fatalf("SPEECH stored unit chat %d, want %d", got, tc.want)
				}
			} else if got := shell.messages.UnitChatText; got != tc.want {
				t.Fatalf("UNITCHAT stored text level %d, want %d", got, tc.want)
			}
		})
	}
}

// Only the service-fired staged callback consumes Panel.StageAt directly.
// Direct callbacks keep their authoritative preference source, except for the
// two acknowledgement controls whose source is the panel selection
// [07 R-WGT-01 §3].
func TestRetailOptionsStageSeparatesServiceAndCallbackSources(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "MODE", Active: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "TRACKMODE", Active: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "TRACKTYPE", Active: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "LEFTCLICK", Active: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "SPEECH", Active: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "UNITCHAT", Active: 1, Stages: 3},
	}}
	p := ui.NewPanel(w)
	for i := 1; i < len(w.Gadgets); i++ {
		p.SetStageAt(i, 2)
	}
	oldPanel, oldState := optionsPanel, optionsState
	optionsPanel, optionsState = p, &retailOptionsState{serviceStageIndex: -1}
	t.Cleanup(func() { optionsPanel, optionsState = oldPanel, oldState })
	shell := &gameShell{}
	for _, name := range []string{"MODE", "TRACKMODE", "TRACKTYPE", "LEFTCLICK"} {
		if got := shell.retailOptionsStage(name, 3, 0); got != 1 {
			t.Fatalf("direct %s stage=%d, want authoritative current advanced to 1", name, got)
		}
	}
	for _, name := range []string{"SPEECH", "UNITCHAT"} {
		if got := shell.retailOptionsStage(name, 3, 0); got != 0 {
			t.Fatalf("direct %s stage=%d, want panel stage 2 advanced to 0", name, got)
		}
	}
	optionsState.serviceStageIndex = p.Index("MODE")
	optionsState.serviceStageActive = true
	if got := shell.retailOptionsStage("MODE", 3, 0); got != 2 {
		t.Fatalf("service MODE stage=%d, want already advanced panel stage 2", got)
	}
}

// Fallback resolution considers only each four-frame group's base. It keeps
// the first equally scoring base; base zero remains installed when no score
// improves the initial bound [07 R-WGT-01 §3].
func TestRetailButtonFallbackBaseDrivesGeometry(t *testing.T) {
	buttons := widgetArtEntry("BUTTONS0", 1, 2, 3, 4, 5, 6, 7, 8, 9)
	buttons.Frames[1].Frame.Width, buttons.Frames[1].Frame.Height = 20, 20 // ignored: not a group base
	buttons.Frames[4].Frame.Width, buttons.Frames[4].Frame.Height = 15, 15
	buttons.Frames[8].Frame.Width, buttons.Frames[8].Frame.Height = 25, 25
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "MISSING", Active: 1, Rect: gui.Rect{W: 20, H: 20}},
	}}
	shell := &gameShell{
		frontend: ui.NewFrontend(modeMenuMain),
		assets:   &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{buttons}}, panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: w}}},
	}
	shell.installRetailWindowButtonArt(w, nil)
	p := ui.NewPanel(w)
	shell.frontend.Panels.Replace(p)
	art := shell.resolveRetailButtonArt(w.Gadgets[1])
	if art.entry != &shell.assets.common.Entries[0] || art.base != 4 {
		t.Fatalf("fallback entry/base=%p/%d, want BUTTONS0 base 4", art.entry, art.base)
	}
	if got := w.Gadgets[1].Rect; got.W != 15 || got.H != 15 {
		t.Fatalf("fallback geometry=%dx%d, want base frame 15x15", got.W, got.H)
	}
	buttons.Frames[4].Frame.Width, buttons.Frames[4].Frame.Height = 10, 10
	buttons.Frames[8].Frame.Width, buttons.Frames[8].Frame.Height = 10, 10
	tooFar := gui.Gadget{Kind: gui.KindButton, Name: "MISSING", Active: 1, Rect: gui.Rect{W: 510, H: 510}}
	tooFar.Rect.W, tooFar.Rect.H = 510, 510 // score 1000, strictly not better than the initial bound
	if art := shell.buildRetailButtonArt(tooFar, nil); art.entry != &shell.assets.common.Entries[0] || art.base != 0 {
		t.Fatalf("fallback at initial score bound entry/base=%p/%d; want BUTTONS0 base 0", art.entry, art.base)
	}
}

// The generic builder installs a fallback's forced staged record before the
// panel exists. The later pointer release advances only Panel.StageAt; it
// must not rewrite the installed stages or attributes [07 R-WGT-01 §3].
func TestRetailButtonInstallerForcesStageBeforePanelService(t *testing.T) {
	stage := widgetArtEntry("stagebuttn1", 1, 2, 3, 4)
	shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{stage}}}}
	for _, tc := range []struct {
		name   string
		text   string
		stages uint8
	}{
		{name: "one authored stage", text: "Choice", stages: 1},
		{name: "Off On label", text: "Off|On", stages: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &gui.Window{Rect: gui.Rect{W: 80, H: 30}, Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel, Active: 1},
				{Kind: gui.KindButton, Name: "MODE", Text: tc.text, Active: 1, Stages: tc.stages, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}},
			}}
			shell.installRetailWindowButtonArt(w, nil)
			gad := w.Gadgets[1]
			if gad.Art != "" || gad.ButtonArt == nil || gad.ButtonArt.Name != "stagebuttn1" || gad.ArtFrame != 0 || gad.Stages != 2 || gad.Attribs != 0x4001 {
				t.Fatalf("installed staged record=%+v; want authored art name, stagebuttn1 identity, base 0, stages 2, attrs 0x4001", gad)
			}
			p := ui.NewPanel(w)
			p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 3}}}, ui.WidgetHooks{})
			p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, PointerEvents: []input.PointerEvent{{Kind: input.LeftUp, X: 3, Y: 3}}}, ui.WidgetHooks{})
			if got := p.StageAt(1); got != 1 {
				t.Fatalf("released installed stage=%d, want 1", got)
			}
			if gad := p.Window.Gadgets[1]; gad.Stages != 2 || gad.Attribs != 0x4001 {
				t.Fatalf("panel service changed installed record to stages/attrs=%d/%#x", gad.Stages, gad.Attribs)
			}
		})
	}
}

// Checkbox fallback wins before the staged fallback arm can force Off|On or a
// one-stage button. The shared staged tail still finalizes its attributes and
// captions after CHECKBOX is selected [07 R-WGT-01 §3].
func TestRetailButtonInstallerCheckboxPrecedesStageForcing(t *testing.T) {
	checkbox := widgetArtEntry("CHECKBOX", 1, 2, 3, 4)
	stage := widgetArtEntry("stagebuttn1", 5, 6, 7, 8)
	shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{checkbox, stage}}}}
	w := &gui.Window{Rect: gui.Rect{W: 80, H: 30}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "CHECK", Text: "Off|On", Active: 1, Attribs: guiAttribCheckbox, Stages: 3, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}},
	}}
	shell.installRetailWindowButtonArt(w, nil)
	gad := w.Gadgets[1]
	if gad.Art != "" || gad.ButtonArt != &shell.assets.common.Entries[0] || gad.ArtFrame != 0 || gad.Stages != 3 || gad.Attribs != 1 {
		t.Fatalf("installed checkbox record=%+v; want authored art name, CHECKBOX identity, base 0, stages 3, attrs 1", gad)
	}
	if got := gad.Labels; len(got) != 2 || got[0] != "Off" || got[1] != "On" {
		t.Fatalf("installed checkbox labels=%v, want [Off On]", got)
	}
	p := ui.NewPanel(w)
	p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 3}}}, ui.WidgetHooks{})
	p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, PointerEvents: []input.PointerEvent{{Kind: input.LeftUp, X: 3, Y: 3}}}, ui.WidgetHooks{})
	if got := retailGadgetText(p, 1, p.Window.Gadgets[1]); got != "On" {
		t.Fatalf("released checkbox caption=%q, want On", got)
	}
}

// The installed pointer retains the selected bank even if that bank and the
// fallback bank both contain a same-named entry. Reinstalling the reused
// window must retain it too [07 R-WGT-01 §3].
func TestRetailButtonInstallerKeepsSelectedArtBank(t *testing.T) {
	common := widgetArtEntry("BUTTONS0", 1, 2, 3, 4)
	own := widgetArtEntry("BUTTONS0", 9, 10, 11, 12)
	shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{common}}}}
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "MISSING", Active: 1, Rect: gui.Rect{W: 10, H: 10}},
	}}
	page := &formats.GAF{Entries: []formats.GAFEntry{own}}
	shell.installRetailWindowButtonArt(w, page)
	gad := w.Gadgets[1]
	if gad.Art != "" || gad.ButtonArt != &shell.assets.common.Entries[0] || !gad.ButtonArtResolved {
		t.Fatalf("installed fallback record did not retain common identity: %+v", gad)
	}
	if got := shell.retailButtonArt(gad, 0, 0, false); got != shell.assets.common.Entries[0].Frames[0].Frame {
		t.Fatal("painter re-resolved fallback art through the own bank")
	}
	shell.installRetailWindowButtonArt(w, page)
	if got := w.Gadgets[1].ButtonArt; got != &shell.assets.common.Entries[0] {
		t.Fatal("reused window changed its installed fallback bank")
	}
}

// A named staged button keeps its own-bank entry when the common bank contains
// the same name. It also runs the shared tail before caption and pointer
// service read the installed record [07 R-WGT-01 §3].
func TestRetailButtonInstallerFinalizesNamedStagedRecord(t *testing.T) {
	own := widgetArtEntry("NAMED", 1, 2, 3, 4)
	common := widgetArtEntry("NAMED", 5, 6, 7, 8)
	shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{common}}}}
	w := &gui.Window{Rect: gui.Rect{W: 80, H: 30}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "NAMED", Text: "Off|On", Active: 1, Attribs: guiAttribCheckbox, Stages: 2, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}},
	}}
	page := &formats.GAF{Entries: []formats.GAFEntry{own}}
	shell.installRetailWindowButtonArt(w, page)
	gad := w.Gadgets[1]
	if gad.ButtonArt != &page.Entries[0] || gad.Art != "" || gad.Stages != 2 || gad.Attribs != 1 || len(gad.Labels) != 2 {
		t.Fatalf("installed named staged record=%+v; want own entry and finalized staged fields", gad)
	}
	if got := shell.retailButtonArt(gad, 0, 0, false); got != page.Entries[0].Frames[0].Frame {
		t.Fatal("painter re-resolved the named entry through the common bank")
	}
	p := ui.NewPanel(w)
	p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 3}}}, ui.WidgetHooks{})
	p.ServiceFrame(ui.WidgetFrame{PointerX: 3, PointerY: 3, PointerEvents: []input.PointerEvent{{Kind: input.LeftUp, X: 3, Y: 3}}}, ui.WidgetHooks{})
	if got := retailGadgetText(p, 1, p.Window.Gadgets[1]); got != "On" {
		t.Fatalf("released named caption=%q, want On", got)
	}
}

// Button lookup does not use logos.gaf, and the fallback special case carries
// the builder's forced two-stage attribute into painting [07 R-WGT-01 §3].
func TestRetailButtonFallbackOffOnAvoidsLogos(t *testing.T) {
	buttons := widgetArtEntry("BUTTONS0", 1, 2, 3, 4)
	staged := widgetArtEntry("stagebuttn1", 5, 6, 7, 8)
	logos := widgetArtEntry("SWITCH", 9)
	shell := &gameShell{assets: &menuAssets{
		common: &formats.GAF{Entries: []formats.GAFEntry{buttons, staged}},
		logos:  &formats.GAF{Entries: []formats.GAFEntry{logos}},
	}}
	art := shell.resolveRetailButtonArt(gui.Gadget{Kind: gui.KindButton, Name: "SWITCH", Text: "Off|On", Stages: 2, Rect: gui.Rect{W: 10, H: 10}})
	if art.entry != &shell.assets.common.Entries[1] {
		t.Fatal("button lookup selected logos art instead of forced stagebuttn1 fallback")
	}
	if art.gadget.Stages != 2 || art.gadget.Attribs != 0x4001 {
		t.Fatalf("Off|On fallback stages/attrs=%d/%#x, want 2/0x4001", art.gadget.Stages, art.gadget.Attribs)
	}
}

// Grey arrows always use their base, while a staged button uses the generic
// pressed frame only when its authored stages leave frames beyond it
// [07 R-WGT-01 §3].
func TestRetailButtonArtGreyArrowAndShortStage(t *testing.T) {
	arrow := widgetArtEntry("ARROW", 1, 2, 3, 4)
	stage := widgetArtEntry("STAGE", 5, 6)
	shell := &gameShell{assets: &menuAssets{common: &formats.GAF{Entries: []formats.GAFEntry{arrow, stage}}}}
	arrowGadget := gui.Gadget{Kind: gui.KindButton, Name: "ARROW", Attribs: 0x1800, Rect: gui.Rect{W: 10, H: 10}}
	if got := shell.retailButtonArt(arrowGadget, 1, 0, true); got != shell.assets.common.Entries[0].Frames[0].Frame {
		t.Fatal("grey arrow did not retain its base frame")
	}
	stageGadget := gui.Gadget{Kind: gui.KindButton, Name: "STAGE", Stages: 2, Rect: gui.Rect{W: 10, H: 10}}
	if got := shell.retailButtonArt(stageGadget, 1, 1, false); got != shell.assets.common.Entries[1].Frames[1].Frame {
		t.Fatal("short staged art used a synthetic pressed frame instead of current stage")
	}
}

// MSGBOX uses the same service state as ordinary buttons, preserving the
// captured down-state until release so its painter selects the armed frame.
func TestModalInputUsesWidgetDownState(t *testing.T) {
	shell, p, _ := widgetArtFixture(t, []gui.Gadget{{
		Kind: gui.KindButton, Name: "OK", Art: "OK", Active: 1, Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10},
	}}, widgetArtEntry("OK", 3, 7, 11))
	shell.frontend.Panels.PushModal(p)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 80, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	cl.Input().Mouse.SetPosition(3, 3)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	shell.modalInput(cl)
	if got := p.DownAt(1); got != 1 {
		t.Fatalf("modal down-state=%d, want armed", got)
	}
	if !retailButtonPressed(cl, p, 1) {
		t.Fatal("modal capture was not visible to the button painter")
	}
	if got := shell.retailButtonArt(p.Window.Gadgets[1], p.DownAt(1), p.StageAt(1), false); got != shell.resolveRetailButtonArt(p.Window.Gadgets[1]).entry.Frames[1].Frame {
		t.Fatal("modal down-state did not select the armed frame")
	}
	cl.Input().Mouse.ResetEdges()
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	shell.modalInput(cl)
	if shell.frontend.Panels.Modal() != nil {
		t.Fatal("modal OK release did not close the modal")
	}
}
