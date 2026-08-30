package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
)

// The command-button stage and grey table [07 R-HUD-03 §6] read off the
// committed selection aggregate [07 §9]. ONOFF is the row the second playtest
// reported: a selected solar collector staged nothing because the old painter
// keyed on the command page's builder handle, which a non-builder never fills.
func TestCommandButtonStageFromSelectionAggregate(t *testing.T) {
	cases := []struct {
		name      string
		gadget    string
		page      frame.CommandPageView
		paged     bool
		wantStage int
		wantGrey  bool
		wantHide  bool
	}{
		// One activated onoffable building: the pair folds to its state.
		{name: "onoff activated", gadget: "ARMONOFF", page: frame.CommandPageView{OnOffState: 1, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 1},
		// One deactivated onoffable building.
		{name: "onoff deactivated", gadget: "CORONOFF", page: frame.CommandPageView{OnOffState: 0, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 0},
		// Two onoffable units that disagree fold to the disagreement value,
		// which is staged, not greyed.
		{name: "onoff mixed", gadget: "ARMONOFF", page: frame.CommandPageView{OnOffState: 2, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 2},
		// Nothing onoffable selected: the pair keeps the value its fold starts
		// from, which is the value that greys the button.
		{name: "onoff none", gadget: "ARMONOFF", page: frame.CommandPageView{OnOffState: 3, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 3, wantGrey: true},
		{name: "cloak none", gadget: "CORCLOAK", page: frame.CommandPageView{OnOffState: 3, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 3, wantGrey: true},
		{name: "cloak engaged", gadget: "ARMCLOAK", page: frame.CommandPageView{OnOffState: 3, CloakState: 1, MoveStance: 4, FireStance: 4}, wantStage: 1},
		// The three-bit stance fields grey at 4 and stage at 0..3.
		{name: "moveord none", gadget: "ARMMOVEORD", page: frame.CommandPageView{OnOffState: 3, CloakState: 3, MoveStance: 4, FireStance: 4}, wantStage: 4, wantGrey: true},
		{name: "moveord roam", gadget: "ARMMOVEORD", page: frame.CommandPageView{OnOffState: 3, CloakState: 3, MoveStance: 2, FireStance: 4}, wantStage: 2},
		{name: "fireord disagree", gadget: "CORFIREORD", page: frame.CommandPageView{OnOffState: 3, CloakState: 3, MoveStance: 4, FireStance: 3}, wantStage: 3},
		// BUILD and ORDERS are the two halves of the page-shown bit and grey
		// together when the selection owns no page.
		{name: "build paged", gadget: "ARMBUILD", page: frame.CommandPageView{Builder: 7, PageCount: 2}, paged: true, wantStage: 1},
		{name: "orders paged", gadget: "ARMORDERS", page: frame.CommandPageView{Builder: 7, PageCount: 2}, paged: true, wantStage: 0},
		{name: "orders unpaged", gadget: "CORORDERS", page: frame.CommandPageView{Builder: 7, PageCount: 2}, wantStage: 1},
		{name: "build no builder", gadget: "ARMBUILD", page: frame.CommandPageView{}, wantGrey: true},
		// The eight capability buttons grey when no selected unit can perform
		// the command.
		{name: "move capable", gadget: "ARMMOVE", page: frame.CommandPageView{CanMove: true}},
		{name: "move incapable", gadget: "CORMOVE", page: frame.CommandPageView{}, wantGrey: true},
		{name: "reclaim incapable", gadget: "ARMRECLAIM", page: frame.CommandPageView{}, wantGrey: true},
		// The transport trio hides rather than greys.
		{name: "load without transport", gadget: "ARMLOAD", page: frame.CommandPageView{}, wantHide: true},
		{name: "load with transport", gadget: "ARMLOAD", page: frame.CommandPageView{IsTransport: true}},
		{name: "unload without transport", gadget: "CORUNLOAD", page: frame.CommandPageView{}, wantGrey: true},
		{name: "blast with transport", gadget: "ARMBLAST", page: frame.CommandPageView{IsTransport: true}, wantHide: true},
		{name: "blast without blast bit", gadget: "ARMBLAST", page: frame.CommandPageView{}, wantGrey: true},
		{name: "blast with blast bit", gadget: "CORBLAST", page: frame.CommandPageView{CanBlast: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &frame.Frame{CommandPage: tc.page}
			got, ok := commandButtonState(commandButtonName(tc.gadget), f, tc.paged)
			if !ok {
				t.Fatalf("%s is not a command button", tc.gadget)
			}
			if got.stage != tc.wantStage || got.grey != tc.wantGrey || got.hidden != tc.wantHide {
				t.Fatalf("%s: stage %d grey %v hidden %v, want stage %d grey %v hidden %v",
					tc.gadget, got.stage, got.grey, got.hidden, tc.wantStage, tc.wantGrey, tc.wantHide)
			}
		})
	}
}

// The table's names are authored with the side's nameprefix, so the longest
// matching suffix decides the row: ARMUNLOAD is UNLOAD, not LOAD, and
// ARMMOVEORD is MOVEORD, not MOVE [07 R-HUD-03 §6].
func TestCommandButtonNameTakesTheLongestSuffix(t *testing.T) {
	cases := map[string]string{
		"ARMUNLOAD":  "UNLOAD",
		"ARMLOAD":    "LOAD",
		"CORMOVEORD": "MOVEORD",
		"CORMOVE":    "MOVE",
		"ARMFIREORD": "FIREORD",
		"ARMONOFF":   "ONOFF",
		"ARMSTOPB":   "",
		"ARMSOLAR":   "",
		"ARMNEXT":    "",
	}
	for gadget, want := range cases {
		if got := commandButtonName(gadget); got != want {
			t.Fatalf("%s resolved to %q, want %q", gadget, got, want)
		}
	}
}

// The painter's frame choice [07 R-HUD-03 §6]: mouse-up art is the authored
// starting frame plus the stage, greyed art is that plus min(stage + 2,
// frames − 1) — except for a cycle button, which takes the last frame.
func TestCommandButtonFrameChoice(t *testing.T) {
	entry := &formats.GAFEntry{Name: "ARMONOFF"}
	for i := 0; i < 4; i++ {
		entry.Frames = append(entry.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: uint16(i)}})
	}
	frameIndex := func(f *formats.GAFFrame) int {
		if f == nil {
			t.Fatal("no frame chosen")
		}
		return int(f.Width)
	}
	cycle := gui.Gadget{Name: "ARMONOFF", Kind: gui.KindButton, Attribs: guiAttribCycle}
	plain := gui.Gadget{Name: "ARMMOVE", Kind: gui.KindButton}

	if got := frameIndex(commandButtonFrame(entry, cycle, 1, false, false)); got != 1 {
		t.Fatalf("activated on/off chose frame %d, want 1", got)
	}
	// A cycle button has no held look: a press advances its state and fires
	// immediately [07 R-WGT-01 §3].
	if got := frameIndex(commandButtonFrame(entry, cycle, 1, false, true)); got != 1 {
		t.Fatalf("held on/off chose frame %d, want 1", got)
	}
	if got := frameIndex(commandButtonFrame(entry, cycle, 3, true, false)); got != 3 {
		t.Fatalf("greyed cycle button chose frame %d, want the last frame 3", got)
	}
	if got := frameIndex(commandButtonFrame(entry, plain, 0, false, false)); got != 0 {
		t.Fatalf("live command button chose frame %d, want 0", got)
	}
	if got := frameIndex(commandButtonFrame(entry, plain, 0, false, true)); got != 1 {
		t.Fatalf("held command button chose frame %d, want 1", got)
	}
	if got := frameIndex(commandButtonFrame(entry, plain, 0, true, false)); got != 2 {
		t.Fatalf("greyed command button chose frame %d, want 2", got)
	}
	// The authored starting frame offsets the whole choice.
	staged := gui.Gadget{Name: "ARMONOFF", Kind: gui.KindButton, Status: 1}
	if got := frameIndex(commandButtonFrame(entry, staged, 1, false, false)); got != 2 {
		t.Fatalf("status 1 stage 1 chose frame %d, want 2", got)
	}
}
