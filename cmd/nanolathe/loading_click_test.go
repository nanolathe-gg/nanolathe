package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

// TestSkirmishStartLeavesForLoadingScreen is the regression for the crash that
// shipped with the loading screen: SKIRMISH's Start callback closes the panel
// on its way to the loading screen, and menuInput carried on dereferencing it.
// Retail's pump returns to the window loop as soon as a callback has run, so
// nothing after an activation may touch the panel.
func TestSkirmishStartLeavesForLoadingScreen(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("content: %v", err)
	}
	defer cs.Close()

	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatalf("newGameShell: %v", err)
	}
	// The Start gate wants at least one human and one computer row in
	// different alliances, so give it a setup it will accept.
	shell.ensureRetailSkirmishControllers()
	shell.setup.NumPlayers = 2
	shell.retailControllers[0] = 1
	shell.retailControllers[1] = 2
	shell.setup.Players[0].AllyGroup = 0
	shell.setup.Players[1].AllyGroup = 1
	shell.openMenu(modeMenuSkirmish)

	idx := -1
	panel := shell.panels.Top()
	for i, gad := range panel.Window.Gadgets {
		if gui.Name16Equal(gad.Name, "Start") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("SKIRMISH.GUI has no Start gadget")
	}
	rect := panel.Window.PlacedRect(idx)

	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480, Headless: true})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	mouse := cl.Input().Mouse
	mouse.InjectMouseMove(float32(rect.X+rect.W/2), float32(rect.Y+rect.H/2))
	mouse.InjectMouseButton(input.MouseButtonLeft, true)
	shell.menuInput(cl)
	mouse.ClearEdges()
	mouse.InjectMouseButton(input.MouseButtonLeft, false)

	// Before the fix this panicked here, one statement past the callback.
	shell.menuInput(cl)

	if shell.mode != modeLoading {
		if modal := shell.panels.Modal(); modal != nil {
			t.Fatalf("Start was refused: %q", modal.Message())
		}
		t.Fatalf("mode after Start = %v, want the loading screen", shell.mode)
	}
	if shell.panels.Top() != nil {
		t.Error("the loading screen owns no .GUI panel")
	}
	if shell.loading == nil {
		t.Fatal("no loading state was installed")
	}
	// The load runs on its own goroutine and is abandoned when the test ends;
	// the screen itself is what is under test here.
	shell.drawLoadingScreen(cl)
}
