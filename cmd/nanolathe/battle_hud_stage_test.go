package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/vfs"
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

// The painter's frame choice [07 R-WGT-01 §3], which completes and corrects
// [07 R-HUD-03 §6]: the base is frame 0 on the named-art path, the runtime
// state indexes off it, greyed art is base + min(state + 2, frames − 1), and a
// greyed cycle button takes the last frame. The authored `status` is the
// down-state word, not a frame base [07 R-HUD-04 §4].
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
	// The authored `status` is the button's **down-state word**, not the frame
	// a stage counts from: a named-art gadget's base is frame 0, and a button
	// authored down draws base + downState [07 R-WGT-01 §3][07 R-HUD-04 §4].
	// This assertion used to read `status` as a base and expect frame 2.
	down := gui.Gadget{Name: "ARMMOVE", Kind: gui.KindButton, Status: 1}
	if got := frameIndex(commandButtonFrame(entry, down, 0, false, false)); got != 1 {
		t.Fatalf("an authored-down button chose frame %d, want its down-state 1", got)
	}
}

// The page-shown bit the command-window switch and the BUILD/ORDERS stages
// both read is the selected builder's status bit 22, taken off the committed
// frame at each use [07 §9][07 R-HUD-03 §6]. Nothing latches it [I6].
func TestCommandPageIsPagedReadsTheCommittedBit(t *testing.T) {
	build := func(flags uint32) *frame.Frame {
		return &frame.Frame{
			Units:       []frame.UnitView{{Slot: 7, Flags: flags}},
			CommandPage: frame.CommandPageView{Builder: 7, PageCount: 4},
		}
	}
	unpaged := build(0)
	paged := build(hud.EncodePageBits(0, 2))
	if commandPageIsPaged(unpaged) {
		t.Fatal("a builder with the page-shown bit clear reported paged")
	}
	if !commandPageIsPaged(paged) {
		t.Fatal("a builder with the page-shown bit set reported unpaged")
	}
	// No builder at all, and a builder handle that names no committed unit,
	// are both the unpaged state: the switch has nothing to read.
	if commandPageIsPaged(&frame.Frame{}) {
		t.Fatal("an empty command page reported paged")
	}
	if commandPageIsPaged(&frame.Frame{CommandPage: frame.CommandPageView{Builder: 9}}) {
		t.Fatal("a stale builder handle reported paged")
	}
	// BUILD and ORDERS are the two halves of that bit [07 R-HUD-03 §6].
	orders, _ := commandButtonState("ORDERS", paged, commandPageIsPaged(paged))
	build1, _ := commandButtonState("BUILD", paged, commandPageIsPaged(paged))
	if orders.stage != 0 || build1.stage != 1 {
		t.Fatalf("paged builder staged ORDERS %d BUILD %d, want 0 and 1", orders.stage, build1.stage)
	}
	orders, _ = commandButtonState("ORDERS", unpaged, commandPageIsPaged(unpaged))
	build0, _ := commandButtonState("BUILD", unpaged, commandPageIsPaged(unpaged))
	if orders.stage != 1 || build0.stage != 0 {
		t.Fatalf("unpaged builder staged ORDERS %d BUILD %d, want 1 and 0", orders.stage, build0.stage)
	}
}

// "Greyed buttons ignore everything" [07 R-WGT-01 §3], and a hidden gadget is
// skipped before the hit test [07 R-WGT-01 §1]. Both verdicts come from the
// selection aggregate the painter reads [07 R-HUD-03 §6], not from the
// authored gadget, so the click path has to consult the same function the
// painter does.
func TestClickPathRefusesDerivedGreyAndHiddenGadgets(t *testing.T) {
	gadget := func(name string, y int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindButton, Active: 1, Name: name, Rect: gui.Rect{X: 0, Y: y, W: 20, H: 10}}
	}
	// PATROL greys when no selected unit carries canpatrol; LOAD hides when no
	// selected unit carries the transport bit.
	window := &gui.Window{Gadgets: []gui.Gadget{{}, gadget("ARMPATROL", 0), gadget("ARMLOAD", 20)}}

	newSession := func(canPatrol, transport bool) *battleSession {
		buf := frame.NewBuffer()
		w := buf.BeginWrite()
		w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0})
		w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
		w.CommandPage = frame.CommandPageView{
			MoveStance: 4, FireStance: 4, CloakState: 3, OnOffState: 3,
			CanPatrol: canPatrol, IsTransport: transport,
		}
		if err := buf.Publish(1); err != nil {
			t.Fatal(err)
		}
		h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
		return &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: &content.Catalog{}, hud: h}
	}

	// A selection that can patrol arms the latch, which is the observable the
	// two refusals below are measured against.
	live := newSession(true, false)
	if !live.hud.consumeClick(live, 5, 5) {
		t.Fatal("a live PATROL button did not consume its click")
	}
	if got := live.battleState().Input.Latch; got != input.LatchPatrol {
		t.Fatalf("live PATROL armed latch %v, want %v", got, input.LatchPatrol)
	}

	greyed := newSession(false, false)
	if greyed.hud.consumeClick(greyed, 5, 5) {
		t.Fatal("a greyed PATROL button consumed its click")
	}
	if got := greyed.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("greyed PATROL armed latch %v, want the idle latch", got)
	}
	if greyed.hud.hitTestFor(greyed, 5, 5) {
		t.Fatal("a greyed button reported a hit for the press/release capture")
	}

	// LOAD without the transport bit is hidden, and a hidden gadget is not
	// clickable either.
	if greyed.hud.consumeClick(greyed, 5, 25) {
		t.Fatal("a hidden LOAD button consumed its click")
	}
	if got := greyed.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("hidden LOAD armed latch %v, want the idle latch", got)
	}
	// With the transport bit LOAD is shown, and clicking it arms its latch.
	shown := newSession(false, true)
	if !shown.hud.consumeClick(shown, 5, 25) {
		t.Fatal("a shown LOAD button did not consume its click")
	}
	if got := shown.battleState().Input.Latch; got != input.LatchLoad {
		t.Fatalf("shown LOAD armed latch %v, want %v", got, input.LatchLoad)
	}
}

// The hovered-gadget index the footer reads is set for any button whose
// rectangle contains the pointer, with no grey test; only hidden gadgets are
// skipped, and before the hit test [07 R-HUD-03 §1][07 R-WGT-01 §1].
func TestHoveredGadgetKeepsGreyedButtonsAndSkipsHiddenOnes(t *testing.T) {
	gadget := func(name string, y int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindButton, Active: 1, Name: name, Rect: gui.Rect{X: 0, Y: y, W: 20, H: 10}}
	}
	window := &gui.Window{Gadgets: []gui.Gadget{{}, gadget("ARMPATROL", 0), gadget("ARMLOAD", 20)}}
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	w.CommandPage = frame.CommandPageView{MoveStance: 4, FireStance: 4, CloakState: 3, OnOffState: 3}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: &content.Catalog{}, hud: h}
	f := buf.Current()

	h.updateHoveredGadget(b, f, 0, 5, 5)
	if _, name := h.hoveredGadgetSource(); name != "ARMPATROL" {
		t.Fatalf("greyed PATROL hovered as %q, want ARMPATROL", name)
	}
	h.updateHoveredGadget(b, f, 0, 5, 25)
	if index, name := h.hoveredGadgetSource(); index != hud.NoGadget || name != "" {
		t.Fatalf("hidden LOAD hovered as %d %q, want no gadget", index, name)
	}
}

// The command-window switch reads the page-shown bit first: page 0 with that
// bit clear is the orders state and opens the side's "%sGEN.GUI", while a page
// N >= 1 composes "%s%d.GUI" from the builder's own internal name, so page 1 is
// ARMCOM1.GUI [07 R-HUD-03 §6].
func TestCommandWindowNameSwitchesOnThePageShownBit(t *testing.T) {
	if got := commandWindowName("ARM", "ARMCOM", false, 0); got != "armgen" {
		t.Fatalf("orders window = %q, want armgen", got)
	}
	if got := commandWindowName("COR", "CORCOM", true, 1); got != "corcom1" {
		t.Fatalf("page 1 window = %q, want corcom1", got)
	}
	if got := commandWindowName("ARM", "ARMCOM", true, 4); got != "armcom4" {
		t.Fatalf("page 4 window = %q, want armcom4", got)
	}
	// A page-shown bit over a zero page field is the definition word A bit 31
	// branch, which is written by probing guis/<internal name>0.GUI at
	// definition load; no stock unit ships one (WU-17-13's census of the
	// reference install's 375 guis/ entries), so "<name>0.GUI" is never
	// composed and the orders window stands.
	if got := commandWindowName("ARM", "ARMCOM", true, 0); got != "armgen" {
		t.Fatalf("page 0 with the bit set = %q, want the orders window", got)
	}
}

// A BUILD click sets the page-shown bit, and the page it brings back is the one
// the builder's page field still holds: selecting page 0 clears bit 22 and
// leaves bits 23-25 alone [07 §9], so the field is the builder's memory of
// where it was. Page 1 stands in for a builder that has never left the orders
// page — see the TODO(question) at buildButtonPage.
func TestBuildButtonPageRestoresTheRememberedPage(t *testing.T) {
	build := func(flags uint32, count uint16) *frame.Frame {
		return &frame.Frame{
			Units:       []frame.UnitView{{Slot: 7, Flags: flags}},
			CommandPage: frame.CommandPageView{Builder: 7, PageCount: count},
		}
	}
	// Page 3, then the orders page: the field survives the round trip.
	remembered := hud.EncodePageBits(hud.EncodePageBits(0, 3), 0)
	if hud.IsPaged(remembered) {
		t.Fatal("selecting page 0 left the page-shown bit set")
	}
	if got := buildButtonPage(build(remembered, 5)); got != 3 {
		t.Fatalf("BUILD after page 3 selects page %d, want 3", got)
	}
	if got := buildButtonPage(build(0, 5)); got != 1 {
		t.Fatalf("BUILD on a builder that has never paged selects page %d, want 1", got)
	}
	// A remembered page the builder no longer has falls back the same way.
	if got := buildButtonPage(build(remembered, 2)); got != 1 {
		t.Fatalf("BUILD with an out-of-range remembered page selects %d, want 1", got)
	}
	if got := buildButtonPage(&frame.Frame{}); got != 0 {
		t.Fatalf("BUILD with no builder selects page %d, want 0", got)
	}
}
