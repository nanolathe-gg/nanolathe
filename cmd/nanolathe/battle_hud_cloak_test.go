package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The side rail's CLOAK gadget is the cloak arm of the same battle-panel
// handler as the two stance gadgets [04 R-STANCE-01 §2]. It had no arm at all
// in the click path, so ARMCLOAK fell through to the catch-all that consumes a
// click without acting: the button painted its stage from the committed pair
// and then did nothing, which is the reported "units cannot cloak".
//
// The direction is a test of the published pair against zero, not a comparison
// with one: only a pair of 0 (every cloak-capable selected unit is visible)
// sends `Cloak_On`, and 1 (cloaked) and 2 (mixed) both send `Cloak_Off`. A pair
// of 3 is not applicable and greys the gadget [07 R-HUD-03 §6], so the press
// never happens; the refusal below is that grey, not a fourth arm.
func TestCloakButtonSendsTheDescriptorTheCommittedPairNames(t *testing.T) {
	// The stock gadget is side-prefixed, the same shape as ARMONOFF beside it,
	// and reaches the arm through the longest-suffix table [07 R-HUD-03 §6].
	for _, gadget := range []string{"ARMCLOAK", "CORCLOAK"} {
		if got := commandButtonName(gadget); got != "CLOAK" {
			t.Fatalf("%s resolved to %q, want CLOAK", gadget, got)
		}
	}

	window := &gui.Window{Gadgets: []gui.Gadget{{}, {
		Kind: gui.KindButton, Active: 1, Name: "ARMCLOAK",
		Rect: gui.Rect{X: 0, Y: 0, W: 20, H: 10},
	}}}

	newSession := func(pair uint8) *battleSession {
		buf := frame.NewBuffer()
		w := buf.BeginWrite()
		w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0})
		w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
		w.CommandPage = frame.CommandPageView{
			MoveStance: 4, FireStance: 4, CloakState: pair, OnOffState: 3,
		}
		if err := buf.Publish(1); err != nil {
			t.Fatal(err)
		}
		h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
		return &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: &content.Catalog{}, hud: h}
	}

	// The `VISIBLE` pair is the only one that cloaks.
	for _, tc := range []struct {
		pair uint8
		want bool
	}{{0, true}, {1, false}, {2, false}} {
		b := newSession(tc.pair)
		if !hudConsumeClick(b.hud, b, 5, 5) {
			t.Fatalf("pair %d: the CLOAK button did not consume its click", tc.pair)
		}
		pending := b.sess.PendingHumanCommands()
		if len(pending) != 1 || pending[0].Kind != session.HumanCloak {
			t.Fatalf("pair %d: enqueued %v, want one HumanCloak", tc.pair, pending)
		}
		if got := pending[0].Cloak.Cloak; got != tc.want {
			t.Fatalf("pair %d: sent Cloak=%v, want %v", tc.pair, got, tc.want)
		}
	}

	// The not-applicable pair greys the gadget, and a greyed button takes no
	// capture and fires nothing [07 R-WGT-01 §3].
	grey := newSession(3)
	if hudConsumeClick(grey.hud, grey, 5, 5) {
		t.Fatal("a greyed CLOAK button consumed its click")
	}
	if pending := grey.sess.PendingHumanCommands(); len(pending) != 0 {
		t.Fatalf("a greyed CLOAK button enqueued %v", pending)
	}
}
