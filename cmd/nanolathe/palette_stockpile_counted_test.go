package main

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func paletteStockpileCommands(b *battleSession) []session.HumanCommand {
	var commands []session.HumanCommand
	for _, command := range b.sess.PendingHumanCommands() {
		if command.Kind == session.HumanStockpile {
			commands = append(commands, command)
		}
	}
	return commands
}

// TestPaletteStockpileToyIsACountedProducer locks the stockpile arm of the
// counted build-page producer [07 R-P0-11 §1]: MAKENUKE/MAKEANTI route to the
// BUILDWEAPON descriptor through the same signed count every other product toy
// uses — +1 left, +5 Shift+left, -1 right, -5 Shift+right — so a right click
// subtracts a queued round instead of being swallowed, and Shift scales the
// count rather than selecting a queue mode.
//
// The Alt batch of twenty is the host divergence of DESIGN_INTERFACE_HUD_INPUT
// §5, which scopes itself out of the stockpile toys, so Alt must leave the
// retail count alone here.
//
// The cue is the routine's first statement, before the descriptor routing and
// before the coalesce, which is why every arm is audible: one or more plays
// `addbuild` and anything else plays `subbuild`.
func TestPaletteStockpileToyIsACountedProducer(t *testing.T) {
	buttons := []gui.Gadget{
		// `commonattribs` bit 0x08 is what stock authors on exactly the eight
		// stockpile buttons [07 R-P0-11 §2]; the name suffix agrees with it.
		{Kind: gui.KindButton, Name: "ARMMAKENUKE", Active: 1, CommonAttribs: 0x08, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
	}
	b, cl, silo := paletteCallbackFactory(t, buttons, nil, 0, 2)
	w := b.hud.windows["armfav1"]
	spy := paletteCallbackCues(t, b, cueAddBuild, cueSubBuild)

	for _, tc := range []struct {
		alt, shift, right bool
		want              int
	}{
		{false, false, false, 1},
		{false, true, false, 5},
		{false, false, true, -1},
		{false, true, true, -5},
		{true, false, false, 1},
		{true, false, true, -1},
	} {
		before := len(paletteStockpileCommands(b))
		cl.Input().Kbd.SetKey(input.KeyAlt, tc.alt)
		paletteCallbackClick(t, b, cl, w, 1, tc.right, tc.shift)
		cl.Input().Kbd.SetKey(input.KeyAlt, false)
		pending := paletteStockpileCommands(b)
		if len(pending) != before+1 {
			t.Fatalf("modifiers %+v produced %v, want one stockpile command", tc, pending)
		}
		got := pending[before].Stockpile
		if got.Unit != silo || got.Count != tc.want {
			t.Fatalf("modifiers %+v produced %+v, want unit %v count %d", tc, got, silo, tc.want)
		}
	}
	want := []string{cueAddBuild, cueAddBuild, cueSubBuild, cueSubBuild, cueAddBuild, cueSubBuild}
	if !slices.Equal(spy.aliases, want) {
		t.Fatalf("stockpile cues %v, want %v", spy.aliases, want)
	}
}
