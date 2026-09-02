package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestOwnSelectableUnitClauses locks the four clauses of the shared
// selection-eligibility predicate [08 R-TRIG-01 §3][07 R-CAM-01 §2], one at a
// time: status bit 5 (0x20) set, construction remaining == 0.0, the
// post-capture grace counter == 0 (always so in single-player), and either no
// carrier or a carrier whose cargo-selectable bit 30 is set — which is a static
// mirror of the carrier definition's `isairbase` [04 R-UNIT-06 §3].
func TestOwnSelectableUnitClauses(t *testing.T) {
	airbase := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armasp"},
		UnitName:         "armasp", IsAirBase: true,
	}
	transport := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armatlas"},
		UnitName:         "armatlas",
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		airbase.CanonicalKey:   airbase,
		transport.CanonicalKey: transport,
	}}
	b := &battleSession{sess: &session.Session{LocalOwner: 0}, cat: cat}

	carrierView := func(slot pool.Handle, def string) frame.UnitView {
		return frame.UnitView{Slot: slot, Owner: 0, DefName: def,
			Flags: units.ClassifierEligibleStatus}
	}
	f := &frame.Frame{Units: []frame.UnitView{
		carrierView(10, "armasp"),
		carrierView(11, "armatlas"),
	}}
	subject := func(mut func(*frame.UnitView)) frame.UnitView {
		v := frame.UnitView{Slot: 1, Owner: 0, DefName: "armatlas",
			Flags: units.ClassifierEligibleStatus}
		mut(&v)
		return v
	}

	cases := []struct {
		name string
		mut  func(*frame.UnitView)
		want bool
	}{
		{"free, eligible, finished", func(*frame.UnitView) {}, true},
		{"slot zero", func(v *frame.UnitView) { v.Slot = 0 }, false},
		{"another player", func(v *frame.UnitView) { v.Owner = 1 }, false},
		{"eligibility bit clear", func(v *frame.UnitView) { v.Flags &^= units.ClassifierEligibleStatus }, false},
		{"still building", func(v *frame.UnitView) { v.BuildRemaining = 0.5 }, false},
		{"aboard an airbase", func(v *frame.UnitView) { v.Carrier = 10 }, true},
		{"aboard an ordinary transport", func(v *frame.UnitView) { v.Carrier = 11 }, false},
		{"carrier not in the frame", func(v *frame.UnitView) { v.Carrier = 99 }, false},
	}
	for _, tc := range cases {
		if got := b.ownSelectableUnit(f, subject(tc.mut)); got != tc.want {
			t.Errorf("%s: ownSelectableUnit = %v, want %v [08 R-TRIG-01 §3]", tc.name, got, tc.want)
		}
	}
}

// unitInfoSeamWindow is a stand-in for the authored `UNITINFOX.GUI` header:
// stock names `DONE` as escdefault, crdefault and defaultfocus, and `DONE` is
// an active, un-greyed button [07 R-WGT-01 §2][fmt gui].
func unitInfoSeamWindow() *gui.Window {
	return &gui.Window{
		Rect:    gui.Rect{X: 203, Y: 105, W: 325, H: 190},
		OriginX: 203, OriginY: 105,
		Header: gui.Header{CrDefault: "DONE", EscDefault: "DONE", DefaultFocus: "DONE"},
		Gadgets: []gui.Gadget{
			{Name: "HEADER"},
			{Name: "DONE", Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 20, Y: 145, W: 96, H: 20}},
		},
	}
}

func pressed(keys ...input.Key) *input.KeyboardState {
	k := &input.KeyboardState{}
	for _, key := range keys {
		k.SetKey(key, true)
	}
	return k
}

// TestUnitInfoOwnsTheKeyboardWhileOpen locks the keyboard-ownership seam
// [07 §3][07 R-WGT-01 §2]: while the child window is up it consumes the queued
// tokens, Escape fires `escdefault` and Enter fires `crdefault` (both `DONE`),
// and every other token — F1 included — is swallowed rather than reaching the
// battle hotkey dispatcher.
func TestUnitInfoOwnsTheKeyboardWhileOpen(t *testing.T) {
	resetUnitInfoState(t)
	b := &battleSession{sess: &session.Session{LocalOwner: 0}, cat: unitInfoTestCatalog(),
		hud: &retailBattleHUD{fs: vfs.New()}, fs: vfs.New()}

	// Closed: the window owns nothing and the dispatcher keeps its tokens.
	if b.unitInfoConsumeKeys(pressed(input.KeyEscape)) {
		t.Fatal("a closed screen consumed a token [07 §3]")
	}

	for _, tc := range []struct {
		name  string
		key   input.Key
		close bool
	}{
		{"escape fires escdefault", input.KeyEscape, true},
		{"enter fires crdefault", input.KeyEnter, true},
		{"F1 is swallowed, not a second close", input.KeyF1, false},
		{"tab is swallowed", input.KeyTab, false},
	} {
		unitInfoUI = &unitInfoScreen{window: unitInfoSeamWindow()}
		if !b.unitInfoConsumeKeys(pressed(tc.key)) {
			t.Errorf("%s: the open window did not own the token [07 §3]", tc.name)
		}
		if closed := !unitInfoOpen(); closed != tc.close {
			t.Errorf("%s: closed = %v, want %v [07 R-WGT-01 §2]", tc.name, closed, tc.close)
		}
	}

	// A window naming no escdefault does not close on Escape: the matrix fires
	// the named gadget "when it exists and is active" [07 R-WGT-01 §2].
	w := unitInfoSeamWindow()
	w.Header.EscDefault = ""
	unitInfoUI = &unitInfoScreen{window: w}
	if !b.unitInfoConsumeKeys(pressed(input.KeyEscape)) || !unitInfoOpen() {
		t.Error("Escape closed a window with no escdefault [07 R-WGT-01 §2]")
	}
	// A greyed default button does not fire either.
	w = unitInfoSeamWindow()
	w.Gadgets[1].GrayedOut = 1
	unitInfoUI = &unitInfoScreen{window: w}
	if !b.unitInfoConsumeKeys(pressed(input.KeyEnter)) || !unitInfoOpen() {
		t.Error("Enter fired a greyed crdefault button [07 R-WGT-01 §2]")
	}
	// An inactive default gadget does not fire.
	w = unitInfoSeamWindow()
	w.Gadgets[1].Active = 0
	unitInfoUI = &unitInfoScreen{window: w}
	if !b.unitInfoConsumeKeys(pressed(input.KeyEscape)) || !unitInfoOpen() {
		t.Error("Escape fired an inactive escdefault gadget [07 R-WGT-01 §2]")
	}
}
