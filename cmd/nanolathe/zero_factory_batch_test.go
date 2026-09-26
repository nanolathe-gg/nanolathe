package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Exercise the retained product callback, not only its count helper: the
// optional host preference must reach the ordinary signed factory command.
func TestZeroFactoryBatchReachesPaletteCommand(t *testing.T) {
	buttons := []gui.Gadget{{Kind: gui.KindButton, Name: "ARMFAV", Active: 1, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}}
	b, cl, factory := paletteCallbackFactory(t, buttons, []string{"armfav"}, 0, 1)
	if b.shell == nil {
		b.shell = &gameShell{}
	}
	w := b.hud.windows["armfav1"]
	cl.Input().Kbd.SetKey(input.KeyCtrl, true)
	for _, tc := range []struct {
		enabled int
		right   bool
		want    int
	}{{0, false, 5}, {1, false, 100}, {1, true, -100}} {
		b.shell.presentation.FactoryHundredBatch = tc.enabled
		before := len(paletteFactoryCommands(b))
		paletteCallbackClick(t, b, cl, w, 1, tc.right, true)
		pending := paletteFactoryCommands(b)
		if len(pending) != before+1 {
			t.Fatalf("product click enqueued %d commands, want one", len(pending)-before)
		}
		got := pending[before].FactoryBuild
		if got.Builder != factory || got.Product != "armfav" || got.Count != tc.want {
			t.Fatalf("factory batch command = %+v, want count %d", got, tc.want)
		}
	}
}
