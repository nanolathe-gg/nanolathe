package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// newgameTestWindow is a synthetic stand-in for NEWGAME.GUI: just enough
// gadgets (by name) for refreshMissionPanel to drive, at the play-any
// layout's rectangles [07 §4 (R-FE-01 §4)].
func newgameTestWindow() *gui.Window {
	return &gui.Window{
		Rect: gui.Rect{W: 640, H: 480},
		Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel, Active: 1},
			{Kind: gui.KindListBox, Name: "Campaign", Active: 1, Rect: gui.Rect{Y: 308, H: 48, W: 200}},
			{Kind: gui.KindScrollBar, Name: "CampaignKnob", Active: 1, Rect: gui.Rect{Y: 308, H: 48}},
			{Kind: gui.KindListBox, Name: "Missions", Active: 1, Rect: gui.Rect{Y: 386, H: 62, W: 200}},
			{Kind: gui.KindScrollBar, Name: "MissionsKnob", Active: 1, Rect: gui.Rect{Y: 386, H: 62}},
		},
	}
}

// TestMissionMenuShowsMissionListRegardlessOfEntryButton locks [07 §4
// "SINGLE, NEWGAME and the briefing screens" (R-FE-01 §4)]: NEWGAME.GUI's
// opener flag is 0 (campaign-only, mission list hidden) or 1 (play-any, both
// lists shown); every live call site — both `NewCamp` and `AnyMsn` on
// SINGLE.GUI — passes 1, so the flag-0 layout is authored but unreachable.
// Nanolathe previously hid the "Missions" list whenever the campaign screen
// was opened via `NewCamp` (missionAny == false), reproducing exactly the
// unreachable layout and dropping OTA's level list under the campaign list —
// the play-test report this fixes.
func TestMissionMenuShowsMissionListRegardlessOfEntryButton(t *testing.T) {
	campaigns := []mission.Campaign{
		{
			Name:     "Arm Campaign",
			Document: mustParseCampaignHeader(t, "ARM"),
			Missions: []mission.Stub{
				{Index: 0, Name: "Arm Mission One"},
				{Index: 1, Name: "Arm Mission Two"},
			},
		},
		{
			Name:     "Core Campaign",
			Document: mustParseCampaignHeader(t, "CORE"),
			Missions: []mission.Stub{
				{Index: 0, Name: "Core Mission One"},
			},
		},
	}

	for _, any := range []bool{false, true} {
		window := newgameTestWindow()
		shell := &gameShell{
			frontend: ui.NewFrontend(modeMenuMission),
			assets:   &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMission: {window: window}}},
		}
		shell.frontend.Panels.Replace(ui.NewPanel(window))
		shell.missionAny = any
		shell.campaigns = campaigns

		shell.refreshMissionPanel()

		p := shell.activePanel()
		if !p.ActiveOf("Missions") || !p.ActiveOf("MissionsKnob") {
			t.Fatalf("missionAny=%v: Missions list is not active", any)
		}
		if !p.ActiveOf("Campaign") || !p.ActiveOf("CampaignKnob") {
			t.Fatalf("missionAny=%v: Campaign list is not active", any)
		}
		items, selected, _, ok := p.ListValues("Missions")
		if !ok {
			t.Fatalf("missionAny=%v: Missions list has no values", any)
		}
		want := []string{"Arm Mission One", "Arm Mission Two"}
		if len(items) != len(want) {
			t.Fatalf("missionAny=%v: Missions items=%v, want %v", any, items, want)
		}
		for i := range want {
			if items[i] != want[i] {
				t.Fatalf("missionAny=%v: Missions items=%v, want %v", any, items, want)
			}
		}
		if selected != 0 {
			t.Fatalf("missionAny=%v: Missions selected=%d, want 0", any, selected)
		}
		campaignItems, _, _, ok := p.ListValues("Campaign")
		if !ok || len(campaignItems) != 1 || campaignItems[0] != "Arm Campaign" {
			t.Fatalf("missionAny=%v: Campaign items=%v, want [Arm Campaign] (side-filtered)", any, campaignItems)
		}
	}
}

// mustParseCampaignHeader builds a minimal parsed TDF document carrying only
// the HEADER campaignside key the side filter reads [08 "Enumeration of
// campaigns"].
func mustParseCampaignHeader(t *testing.T, side string) *formats.Document {
	t.Helper()
	doc, err := formats.ParseTDF([]byte("[HEADER]\n{\ncampaignside=" + side + ";\n}\n"))
	if err != nil {
		t.Fatalf("parse fixture campaign header: %v", err)
	}
	return doc
}
