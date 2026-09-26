package main

import (
	"strconv"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The side callback and widget service must agree on the authored faction
// count; otherwise Core cannot be selected in a GoK-first three-side set.
func TestSkirmishSideCycleUsesAuthoredCount(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			shell, _, cl := skirmishRowShell(t, []int{1, 2}, []int{0, 1})
			shell.skirmishSides = count
			shell.setup.Players[0].Side = 0
			window := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}
			shell.installSkirmishDynamicGadgets(window)
			panel := ui.NewPanel(window)
			shell.frontend.Panels.Replace(panel)
			n := count
			if n == 0 {
				n = 2
			}
			index := panel.Index("Side0")
			if got := int(panel.Window.Gadgets[index].Stages); got != n {
				t.Fatalf("stages = %d, want %d", got, n)
			}
			for step := 1; step <= n+1; step++ {
				clickRowGadget(t, shell, panel, cl, "Side0", input.MouseButtonLeft)
				if got := shell.setup.Players[0].Side; got != step%n {
					t.Fatalf("click %d side = %d, want %d", step, got, step%n)
				}
				if panel.StageAt(index) != step%n || panel.DownAt(index) != 0 {
					t.Fatalf("click %d retained stage/down = %d/%d", step, panel.StageAt(index), panel.DownAt(index))
				}
			}
		})
	}
}
