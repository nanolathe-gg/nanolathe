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

// TestUnitInfoOwnsNoKeyboardAndClosesOnSelection locks the corrected seam
// [07 §3][07 R-WGT-01 §1 step 3][07 R-WGT-01 §2][07 R-HUD-04 §3].
//
// A battle window's token-mode word is zero, so the GUI pass peeks the token
// instead of popping it: every battle hotkey still runs underneath the open
// screen, and the key matrix — the rows that would fire `escdefault` and
// `crdefault` — never runs at all. Nothing keyboard-shaped closes the screen
// directly. What does close it is the command-panel page close that every
// selection change runs, which pops each window above the command window; that
// is the chain Escape reaches, by deselecting first.
//
// This replaces TestUnitInfoOwnsTheKeyboardWhileOpen, which asserted the
// inverse (the window swallowing every token, Escape and Enter firing `DONE`).
func TestUnitInfoOwnsNoKeyboardAndClosesOnSelection(t *testing.T) {
	resetUnitInfoState(t)
	b := &battleSession{sess: &session.Session{LocalOwner: 0}, cat: unitInfoTestCatalog(),
		hud: &retailBattleHUD{fs: vfs.New()}, fs: vfs.New()}

	// There is no consume-keys seam left to call: the only way the screen can
	// take a token would be a method on the session, and none exists. The
	// observable half of that is F1 — the one token that reaches the dispatcher
	// while the screen is up — no longer closing it.
	unitInfoUI = &unitInfoScreen{window: unitInfoSeamWindow()}
	b.openUnitInfo()
	if !unitInfoOpen() {
		t.Error("F1 closed the unit information screen; it only ever opens [07 R-HUD-03 §8]")
	}

	// A selection change runs the page close, which pops the screen.
	unitInfoUI = &unitInfoScreen{window: unitInfoSeamWindow()}
	_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
	if unitInfoOpen() {
		t.Error("a selection change left the screen open [07 R-HUD-04 §3]")
	}

	// The pointer over the open screen is a modal covering the pointer, so world
	// picking is gated out and F1 there resolves no subject [07 §3].
	unitInfoUI = &unitInfoScreen{window: unitInfoSeamWindow()}
	r := unitInfoUI.window.Rect
	if b.overWorld(r.X+r.W/2, r.Y+r.H/2) {
		t.Error("the open screen did not gate world picking out [07 §3]")
	}
	if !b.overWorld(r.X+r.W+40, r.Y) {
		t.Error("a point outside the screen was gated as if covered [07 §3]")
	}
}
