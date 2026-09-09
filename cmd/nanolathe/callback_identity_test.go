package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestFiredFrontendCallbacksRequireTheAuthoredRecordName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"SINGLE", true},
		{"single", false},
		{"SINGLE ", false},
		{" SINGLE", false},
		{"SINGLE\x00suffix", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			panel := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindButton, Name: tc.name, Active: 1}}})
			shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
			shell.frontend.Panels.Replace(panel)
			shell.activateWidgetGadget(panel, ui.ServiceResult{Fired: true, FiredIndex: 1})
			if got := shell.frontend.Mode == modeMenuSingle; got != tc.want {
				t.Fatalf("fired %q transitioned=%t, want %t", tc.name, got, tc.want)
			}
		})
	}
}

func TestFiredSaveLoadCallbacksUseTerminatedExactNames(t *testing.T) {
	s := newSaveLoadScreen(saveScreenMode, t.TempDir(), saveLoadFromFrontend)
	if got := s.Activate("LOAD\x00tail"); got != saveLoadCommit {
		t.Fatalf("terminated LOAD action = %d, want commit", got)
	}
	for _, name := range []string{"load", "LOAD ", " LOAD"} {
		if got := s.Activate(name); got != saveLoadNone {
			t.Fatalf("altered %q action = %d, want none", name, got)
		}
	}
}

func TestFiredOptionsCallbacksUseTerminatedExactNames(t *testing.T) {
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	t.Cleanup(func() { optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState })
	window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindButton, Name: "MODE", Active: 1}}}
	panel := ui.NewPanel(window)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(panel)
	optionsPanel = panel
	optionsAssets = &retailPanelAssets{window: window}
	optionsState = &retailOptionsState{serviceStageIndex: -1}
	if !shell.activateRetailOptionsGadget("MODE\x00tail") || shell.audioPrefs.SoundMode != 1 {
		t.Fatal("terminated MODE callback did not reach its exact handler")
	}
	for _, name := range []string{"mode", "MODE ", " MODE"} {
		if shell.activateRetailOptionsGadget(name) {
			t.Fatalf("altered options callback %q was consumed", name)
		}
	}
}

func TestOptionsSlidersKeepFiredRecordIdentity(t *testing.T) {
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	t.Cleanup(func() { optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState })
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindScrollBar, Name: "FXVOL", Active: 1},
		{Kind: gui.KindScrollBar, Name: "FXVOL           ", Active: 1}, // 16 bytes, not FXVOL
		{Kind: gui.KindScrollBar, Name: "fxvol", Active: 1},
		{Kind: gui.KindScrollBar, Name: "FXVOL", Active: 1},
	}}
	panel := ui.NewPanel(window)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(panel)
	optionsPanel = panel
	optionsAssets = &retailPanelAssets{window: window}
	optionsState = &retailOptionsState{sliders: map[int]*retailSliderState{
		1: {travel: 5, max: 64},
		2: {travel: 5, max: 64},
		3: {travel: 5, max: 64},
		4: {travel: 5, max: 64},
	}}

	shell.moveRetailSliderAt(1, optionsState.sliders[1], 2)
	if optionsState.sliders[1].knob != 2 || shell.audioPrefs.FXVol != 32 {
		t.Fatalf("first FXVOL knob/value=%d/%d, want 2/32", optionsState.sliders[1].knob, shell.audioPrefs.FXVol)
	}
	shell.moveRetailSliderAt(2, optionsState.sliders[2], 3)
	shell.moveRetailSliderAt(3, optionsState.sliders[3], 4)
	if shell.audioPrefs.FXVol != 32 || optionsState.sliders[1].knob != 2 {
		t.Fatalf("altered slider names changed FXVOL state: value=%d first knob=%d", shell.audioPrefs.FXVol, optionsState.sliders[1].knob)
	}
	if optionsState.sliders[2].knob != 3 || optionsState.sliders[3].knob != 4 {
		t.Fatalf("altered slider records shared knob state: spaced=%d folded=%d", optionsState.sliders[2].knob, optionsState.sliders[3].knob)
	}
	shell.moveRetailSliderAt(4, optionsState.sliders[4], 1)
	if optionsState.sliders[1].knob != 2 || optionsState.sliders[4].knob != 1 || shell.audioPrefs.FXVol != 16 {
		t.Fatalf("duplicate FXVOL did not retain record identity: first=%d duplicate=%d value=%d", optionsState.sliders[1].knob, optionsState.sliders[4].knob, shell.audioPrefs.FXVol)
	}
	// The legacy scrollbar-arrow path receives the fired record index too. A
	// duplicate must move only its own state rather than re-resolving FXVOL.
	shell.adjustRetailSlider(4, 1)
	if optionsState.sliders[1].knob != 2 || optionsState.sliders[4].knob != 2 {
		t.Fatalf("legacy duplicate slider arrow changed first=%d duplicate=%d", optionsState.sliders[1].knob, optionsState.sliders[4].knob)
	}
}

func TestOptionsScrollbarPainterUsesFiredRecordIndex(t *testing.T) {
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	t.Cleanup(func() { optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState })
	left := gui.Rect{X: 0, Y: 0, W: 24, H: 8}
	right := gui.Rect{X: 0, Y: 12, W: 24, H: 8}
	window := &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindScrollBar, Name: "FXVOL", Active: 1, Rect: left}, {Kind: gui.KindScrollBar, Name: "FXVOL", Active: 1, Rect: right}}}
	panel := ui.NewPanel(window)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{common: callbackSliderArt(), panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: window}}}}
	shell.frontend.Panels.Replace(panel)
	optionsPanel = panel
	optionsAssets = &retailPanelAssets{window: window}
	optionsState = &retailOptionsState{sliders: map[int]*retailSliderState{
		1: {knob: 0, knobSize: 1, travel: 20, arrowW: 1, max: 1},
		2: {knob: 10, knobSize: 1, travel: 20, arrowW: 1, max: 1},
	}}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	snapshot := cl.ComposeFrameSnapshot()
	if got := snapshot.Indexed[3*snapshot.Width+1]; got != 14 {
		t.Fatalf("first duplicate knob pixel = %d, want first record's knob at x=1", got)
	}
	if got := snapshot.Indexed[15*snapshot.Width+11]; got != 14 {
		t.Fatalf("second duplicate knob pixel = %d, want second record's knob at x=11", got)
	}
}

func callbackSliderArt() *formats.GAF {
	frames := make([]formats.GAFFrameRef, 20)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Compressed: 1, Pixels: []byte{byte(i + 1)}, Transparent: []bool{false}}
	}
	return &formats.GAF{Entries: []formats.GAFEntry{{Name: "SLIDERS", Frames: frames}}}
}

func TestListCallbacksUseTerminatedNames(t *testing.T) {
	for _, tc := range []struct {
		mode  shellMode
		name  string
		field func(*gameShell) int
	}{
		{modeMenuMap, "MAPNAMES", func(g *gameShell) int { return g.mapIdx }},
		{modeMenuMission, "Campaign", func(g *gameShell) int { return g.campaignIdx }},
		{modeMenuMission, "Missions", func(g *gameShell) int { return g.missionIdx }},
	} {
		shell := &gameShell{frontend: ui.NewFrontend(tc.mode)}
		shell.commitListSelection(tc.name+"\x00tail", 2)
		if got := tc.field(shell); got != 2 {
			t.Fatalf("%s selection=%d, want 2", tc.name, got)
		}
		shell.commitListSelection(tc.name+" ", 3)
		if got := tc.field(shell); got != 2 {
			t.Fatalf("spaced %s changed selection to %d", tc.name, got)
		}
	}
}

func TestDynamicCallbacksRejectNoncanonicalRowNames(t *testing.T) {
	for _, prefix := range []string{"Player", "Side", "Allies", "Metal", "Energy", "Color"} {
		for _, suffix := range []string{"+1", "01", "1 ", "-0"} {
			if _, _, ok := dynamicSlot(prefix + suffix); ok {
				t.Fatalf("accepted alias %q", prefix+suffix)
			}
		}
		if row, kind, ok := dynamicSlot(prefix + "1\x00tail"); !ok || row != 1 || kind != prefix {
			t.Fatalf("terminated %s row=%d kind=%q ok=%v", prefix, row, kind, ok)
		}
	}
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuSkirmish)}
	shell.setup.Players[1].Metal = 1000
	shell.activateDynamicSkirmishGadget("Metal01")
	if shell.setup.Players[1].Metal != 1000 {
		t.Fatal("aliased left callback changed resources")
	}
	shell.activateDynamicSkirmishGadget("Metal1\x00tail")
	if shell.setup.Players[1].Metal == 1000 {
		t.Fatal("terminated left callback did not change resources")
	}
}

func TestOptionsCallbackRetainsLaterStagedRecord(t *testing.T) {
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	t.Cleanup(func() { optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState })
	window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, {Kind: gui.KindButton, Name: "MODE", Active: 1, Stages: 3}, {Kind: gui.KindButton, Name: "MODE\x00tail", Active: 1, Stages: 3}, {Kind: gui.KindButton, Name: "SPEECH", Active: 1, Stages: 3}, {Kind: gui.KindButton, Name: "SPEECH", Active: 1, Stages: 3}}}
	panel := ui.NewPanel(window)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(panel)
	optionsPanel = panel
	optionsAssets = &retailPanelAssets{window: window}
	optionsState = &retailOptionsState{serviceStageIndex: -1}
	panel.SetStageAt(1, 0)
	panel.SetStageAt(2, 2)
	shell.activateWidgetGadget(panel, ui.ServiceResult{Fired: true, FiredIndex: 2, FiredButton: 1, StageAdvanced: true})
	if shell.audioPrefs.SoundMode != 2 {
		t.Fatalf("later MODE stage=%d, want 2", shell.audioPrefs.SoundMode)
	}
	// A key callback also retains the fired record, but advances its stage in
	// the callback instead of treating it as a pre-advanced pointer gesture.
	panel.SetStageAt(3, 0)
	panel.SetStageAt(4, 2)
	shell.activateGadgetAt(panel, 4)
	if shell.audioPrefs.UnitChat != 0 {
		t.Fatalf("later SPEECH key callback value=%d, want wrapped stage zero", shell.audioPrefs.UnitChat)
	}
	if optionsState.callbackIndex != 0 || optionsState.serviceStageActive {
		t.Fatal("callback identity leaked after dispatch")
	}
}
