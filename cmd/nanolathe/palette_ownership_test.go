package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"testing"
)

// The GUI visits gadgets, not two independent keyboard/pointer lists:
// the earlier pointer activation wins over a later quickkey [07 §3].
func TestPaletteOwnershipOneIndexedPass(t *testing.T) {
	art := &formats.GAFEntry{Name: "CYCLE", Frames: make([]formats.GAFFrameRef, 4)}
	b, cl, w := paletteViewer(t, []gui.Gadget{
		{Kind: gui.KindButton, Name: "CYCLE", Active: 1, Attribs: guiAttribCycle, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}, ButtonArt: art, ButtonArtResolved: true},
		{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
	})
	cl.Input().Mouse.SetPosition(5, 5)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if got := b.hud.palettePanels[w].StatusAt(1); got != 1 {
		t.Fatalf("cycle status=%d, want exactly one advancement", got)
	}
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("later quickkey fired: latch=%v", got)
	}
	if got := b.hud.palettePanels[w].Focused(); got != 1 {
		t.Fatalf("first firing index=%d, want 1", got)
	}
}
