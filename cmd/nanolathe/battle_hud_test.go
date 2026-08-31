package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestEmptySelectionClosesCommandWindows locks [07 §6] "Command-window switch
// is closed": while the selected-unit count is zero the switch closes the
// command windows down to the root <prefix>MAIN2.GUI and opens nothing, so no
// command page is composed; a non-empty selection with no single builder opens
// <prefix>GEN.GUI.
func TestEmptySelectionClosesCommandWindows(t *testing.T) {
	tank := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armstump"},
		UnitName:         "armstump",
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{tank.CanonicalKey: tank}}
	gen := &gui.Window{Gadgets: []gui.Gadget{{}, {Kind: gui.KindButton, Active: 1, Name: "MOVE"}}}
	hud := &retailBattleHUD{cat: cat, fs: vfs.New(), windows: map[string]*gui.Window{"gen": gen}}

	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: tank.UnitName})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: cat, hud: hud}

	empty := buf.Current()
	if window, _, err := hud.windowForRequired(b, empty); err != nil || window != nil {
		t.Fatalf("empty selection composed a command page: window=%v err=%v", window, err)
	}

	w = buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: tank.UnitName})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	if err := buf.Publish(2); err != nil {
		t.Fatal(err)
	}
	selected := buf.Current()
	if window, _, err := hud.windowForRequired(b, selected); err != nil || window != gen {
		t.Fatalf("single non-builder selection did not open the general page: window=%v err=%v", window, err)
	}
}

// TestProductQueueCountLabel locks the bit-0x04 count format of the count-label
// writer [07 R-P0-11 §2]: one total summed over the selected builder's primary
// and secondary lists, formatted "+%d", cleared at zero, and never counting
// another unit's queue.
func TestProductQueueCountLabel(t *testing.T) {
	f := &frame.Frame{CommandPage: frame.CommandPageView{Builder: 1}}
	f.OrderQueues = []frame.OrderQueueView{
		{
			Unit:      1,
			Primary:   []frame.OrderView{{BuildProduct: "armsolar", BuildCount: 5}, {BuildProduct: "armmex", BuildCount: 3}},
			Secondary: []frame.OrderView{{BuildProduct: "ARMSOLAR", BuildCount: 2}},
		},
		// Another builder's queue must not contribute.
		{Unit: 2, Primary: []frame.OrderView{{BuildProduct: "armsolar", BuildCount: 9}}},
	}
	if got := productQueueCountLabel(f, "ARMSOLAR"); got != "+7" {
		t.Fatalf("summed label = %q, want +7", got)
	}
	if got := productQueueCountLabel(f, "armmex"); got != "+3" {
		t.Fatalf("primary-only label = %q, want +3", got)
	}
	if got := productQueueCountLabel(f, "ARMWIN"); got != "" {
		t.Fatalf("zero total label = %q, want empty", got)
	}
	// No selected builder means no page and no counter.
	if got := productQueueCountLabel(&frame.Frame{OrderQueues: f.OrderQueues}, "ARMSOLAR"); got != "" {
		t.Fatalf("label without a selected builder = %q, want empty", got)
	}
}
