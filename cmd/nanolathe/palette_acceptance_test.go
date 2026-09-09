package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// publishPaletteFrame changes only the already committed presentation frame.
// These acceptance cases exercise a host input pass, not a simulation tick.
func publishPaletteFrame(t *testing.T, b *battleSession, change func(*frame.Frame)) {
	t.Helper()
	current, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed palette frame")
	}
	copy := *current
	copy.Selection.Handles = append([]pool.Handle(nil), current.Selection.Handles...)
	copy.Units = append([]frame.UnitView(nil), current.Units...)
	next := b.sess.Snapshot.BeginWrite()
	*next = copy
	change(next)
	if err := b.sess.Snapshot.Publish(current.Tick + 1); err != nil {
		t.Fatal(err)
	}
}

func TestPaletteAcceptanceTokenKeyEdgeAndDynamicOrder(t *testing.T) {
	b, cl, w := paletteViewer(t, []gui.Gadget{
		{Kind: gui.KindButton, Name: "MOVE", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
	})

	// Native input supplies both a text token and the matching physical edge.
	// The palette must use the token and select the first indexed record.
	cl.Input().Kbd.SetKey(input.KeyA, true)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	cl.Input().Kbd.SetKey(input.KeyA, false)
	if got := b.battleState().Input.Latch; got != input.LatchMove {
		t.Fatalf("first duplicate latch=%v, want move", got)
	}
	if got := b.hud.palettePanels[w].Focused(); got != 1 {
		t.Fatalf("first duplicate focus=%d, want 1", got)
	}

	// The same key reaches the next record only after the committed capability
	// verdict greys the first one [07 R-WGT-01 §§1,3].
	b.battleState().Input.Latch = input.LatchNormal
	publishPaletteFrame(t, b, func(f *frame.Frame) {
		f.CommandPage.CanMove = false
		// The earlier command may have published the real non-attacker fixture's
		// capabilities. Re-author the second control's capability for this case.
		f.CommandPage.CanAttack = true
	})
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchAttack {
		t.Fatalf("grey first duplicate latch=%v, want attack", got)
	}
	if got := b.hud.palettePanels[w].Focused(); got != 2 {
		t.Fatalf("grey first duplicate focus=%d, want 2", got)
	}
}

func TestPaletteAcceptanceTokenClaimSuppressesNAndTResiduals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      input.Key
		token    rune
		residual func(*battleSession) bool
	}{
		{"unit-cycle N", input.KeyN, 'n', func(b *battleSession) bool { return b.currentUnit != 0 }},
		{"follow T", input.KeyT, 't', func(b *battleSession) bool { return b.cam.Tracked() != 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: byte(tc.token), Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
			cl.Input().Kbd.SetKey(tc.key, true)
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: tc.token})
			b.viewerStep(0, cl)
			cl.Input().Kbd.SetKey(tc.key, false)
			if got := b.battleState().Input.Latch; got != input.LatchAttack {
				t.Fatalf("palette latch=%v, want attack", got)
			}
			if tc.residual(b) {
				t.Fatal("claimed palette token also reached its residual battle hotkey")
			}
		})
	}
}

func TestPaletteAcceptanceUnitInfoDoneOwnsWholeHostFrame(t *testing.T) {
	b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
	info := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "DONE", Active: 1, QuickKey: 'D'},
	}}
	unitInfoUI = &unitInfoScreen{window: info, panel: ui.NewPanel(info)}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'd'})
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})

	b.viewerStep(0, cl)
	if unitInfoOpen() {
		t.Fatal("UNITINFO DONE did not close its child")
	}
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("UNITINFO close leaked its second token to palette latch=%v", got)
	}
	if got := cl.Input().PendingTokens(); got != 0 {
		t.Fatalf("second token pending=%d", got)
	}
}

func TestPaletteAcceptanceCaptureClearsAcrossWindowIdentity(t *testing.T) {
	b, cl, first := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
	in := cl.Input()
	in.Mouse.SetPosition(5, 5)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.viewerStep(0, cl)
	old := b.hud.palettePanels[first]
	if old == nil || old.CaptureIndex() != 1 {
		got := -1
		if old != nil {
			got = old.CaptureIndex()
		}
		t.Fatalf("press capture=%d, want 1", got)
	}

	// A changed command-window identity owns the release. It must first clear
	// the capture retained by the old identity [07 R-WGT-01 §1].
	second := &gui.Window{Rect: first.Rect, Gadgets: append([]gui.Gadget(nil), first.Gadgets...)}
	b.hud.windows["gen"] = second
	other := placeUnit(b, "armfav", numeric.Fixed(30<<16), numeric.Fixed(20<<16))
	publishPaletteFrame(t, b, func(f *frame.Frame) {
		f.Selection.Handles = []pool.Handle{other.Handle}
		f.Selection.Primary = other.Handle
		f.Selection.Count = 1
	})
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.viewerStep(0, cl)
	if got := old.CaptureIndex(); got != -1 {
		t.Fatalf("old panel capture=%d after different-window release, want none", got)
	}
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("different-window release activated old latch=%v", got)
	}

	// Reopening the old pointer must not resurrect its former capture.
	b.hud.windows["gen"] = first
	publishPaletteFrame(t, b, func(f *frame.Frame) {
		f.Selection.Handles = []pool.Handle{other.Handle}
		f.Selection.Primary = other.Handle
		f.Selection.Count = 1
	})
	b.viewerStep(0, cl)
	if got := old.CaptureIndex(); got != -1 {
		t.Fatalf("reopened panel revived capture=%d", got)
	}
}

func TestPaletteAcceptanceCycleUsesResolvedArtFrames(t *testing.T) {
	entry := &formats.GAFEntry{Name: "CYCLE", Frames: make([]formats.GAFFrameRef, 4)}
	b, cl, w := paletteViewer(t, []gui.Gadget{{
		Kind: gui.KindButton, Name: "CYCLE", Active: 1, Attribs: guiAttribCycle,
		Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}, ButtonArt: entry, ButtonArtResolved: true,
	}})
	in := cl.Input()
	in.Mouse.SetPosition(5, 5)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.viewerStep(0, cl)
	if got := b.hud.palettePanels[w].StatusAt(1); got != 1 {
		t.Fatalf("cycle status=%d, want 1 from resolved four-frame art", got)
	}
}
