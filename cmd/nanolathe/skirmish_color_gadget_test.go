package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// skirmishRowShell installs the runtime row gadgets on a bare window and puts
// the panel where menuInput finds it, so a test drives the production click
// route over the real Color%d and Allies%d records.
func skirmishRowShell(t *testing.T, controllers, colors []int) (*gameShell, *ui.Panel, *client.Client) {
	t.Helper()
	cfg := newSkirmishMenuConfig("teamcolour")
	cfg.NumPlayers = len(controllers)
	g := &gameShell{setup: cfg, frontend: ui.NewFrontend(modeMenuSkirmish)}
	g.ensureRetailSkirmishControllers()
	copy(g.retailControllers[:], controllers)
	for i, c := range colors {
		g.setup.Players[i].Color = c
	}
	window := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1, Rect: gui.Rect{W: 640, H: 480}}}}
	g.installSkirmishDynamicGadgets(window)
	panel := ui.NewPanel(window)
	g.frontend.Panels.Replace(panel)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	return g, panel, cl
}

// clickRowGadget presses and releases one button over the named row control
// through the ordinary front-end input pass.
func clickRowGadget(t *testing.T, g *gameShell, p *ui.Panel, cl *client.Client, name string, button input.MouseButton) {
	t.Helper()
	index := p.Index(name)
	if index < 1 {
		t.Fatalf("%s is not installed", name)
	}
	r := p.Window.PlacedRect(index)
	mouse := cl.Input().Mouse
	mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	mouse.SetButton(button, true)
	g.menuInput(cl)
	mouse.ResetEdges()
	mouse.SetButton(button, false)
	g.menuInput(cl)
	mouse.ResetEdges()
}

// TestSkirmishRowSurfacesRouteClicks is the regression for the dead colour and
// allegiance gadgets: both are surfaces, and a surface only takes a press
// while its hot word is 1 [07 R-WGT-01 §8], so with the word left clear the
// widget pass never reported a fire and the row callbacks of
// [08 R-SKIR-01 §1] could not run from the mouse at all.
func TestSkirmishRowSurfacesRouteClicks(t *testing.T) {
	// Rows 0 and 1 are live, rows 2 and 3 are open, so only row 1's colour
	// blocks a candidate.
	g, panel, cl := skirmishRowShell(t, []int{1, 2, 0, 0}, []int{0, 1, 2, 3})

	clickRowGadget(t, g, panel, cl, "Color0", input.MouseButtonLeft)
	if got := g.setup.Players[0].Color; got != 2 {
		t.Fatalf("left click on Color0 gave colour %d, want 2 (0 steps to 1, which live row 1 holds)", got)
	}
	if got := panel.StatusOf("Color0"); got != 2 {
		t.Fatalf("Color0 surface frame=%d, want the row's new colour 2", got)
	}
	clickRowGadget(t, g, panel, cl, "Color0", input.MouseButtonRight)
	if got := g.setup.Players[0].Color; got != 0 {
		t.Fatalf("right click on Color0 gave colour %d, want 0 (2 steps to 1, which live row 1 holds)", got)
	}

	group := g.setup.Players[0].AllyGroup
	clickRowGadget(t, g, panel, cl, "Allies0", input.MouseButtonLeft)
	if got := g.setup.Players[0].AllyGroup; got != (group+1)%6 {
		t.Fatalf("click on Allies0 gave ally group %d, want %d", got, (group+1)%6)
	}
}

// TestSkirmishColorStepStaysInRange locks the Color%d contract of
// [08 R-SKIR-01 §1]: one frame per click in the clicked direction, modulo the
// logo frame count, re-stepping past live rows' colours, never storing -1.
func TestSkirmishColorStepStaysInRange(t *testing.T) {
	const logoCount = session.SkirmishMaxPlayers

	// Wrap in both directions with no other live row to step past.
	solo := func(color int) *gameShell {
		g := &gameShell{setup: newSkirmishMenuConfig("teamcolour")}
		g.ensureRetailSkirmishControllers()
		g.setup.NumPlayers = 1
		g.setup.Players[0].Color = color
		return g
	}
	if got := solo(logoCount-1).nextRetailPlayerColor(0, 1); got != 0 {
		t.Fatalf("left click at the last frame = %d, want 0", got)
	}
	if got := solo(0).nextRetailPlayerColor(0, -1); got != logoCount-1 {
		t.Fatalf("right click at frame 0 = %d, want %d", got, logoCount-1)
	}

	// Re-stepping walks in the clicked direction and skips only live rows.
	// Row 3 is open, so its colour does not block.
	g := &gameShell{setup: newSkirmishMenuConfig("teamcolour")}
	g.ensureRetailSkirmishControllers()
	g.setup.NumPlayers = 4
	g.retailControllers = [session.SkirmishMaxPlayers]int{1, 2, 2, 0}
	for i, c := range []int{0, 1, 2, 3} {
		g.setup.Players[i].Color = c
	}
	if got := g.nextRetailPlayerColor(0, 1); got != 3 {
		t.Fatalf("left click past live 1 and 2 = %d, want 3", got)
	}
	if got := g.nextRetailPlayerColor(0, -1); got != logoCount-1 {
		t.Fatalf("right click from 0 = %d, want %d", got, logoCount-1)
	}

	// Ten live rows: every click has to terminate inside the frame range,
	// and none of them may store the -1 that belongs to the controller
	// cycle. Repeated clicks are the player-visible case from the setup
	// screen.
	full := func() *gameShell {
		g := &gameShell{setup: newSkirmishMenuConfig("teamcolour")}
		g.ensureRetailSkirmishControllers()
		g.setup.NumPlayers = logoCount
		for i := 0; i < logoCount; i++ {
			g.retailControllers[i] = 2
			g.setup.Players[i].Color = i
		}
		g.retailControllers[0] = 1
		return g
	}
	for _, delta := range []int{1, -1} {
		g := full()
		for click := 0; click < 3*logoCount; click++ {
			g.setup.Players[0].Color = g.nextRetailPlayerColor(0, delta)
			if c := g.setup.Players[0].Color; c < 0 || c >= logoCount {
				t.Fatalf("click %d with delta %d stored colour %d", click, delta, c)
			}
		}
	}
	// A row whose colour another live row already holds still terminates.
	g = full()
	g.setup.Players[0].Color = 5
	if got := g.nextRetailPlayerColor(0, 1); got < 0 || got >= logoCount {
		t.Fatalf("duplicated colour stepped to %d", got)
	}
}

// TestSkirmishControllerCycleResolvesColorConflict locks the placement of the
// collision scan: it runs when a row becomes live, tests every configured row
// including open ones, and is the only path that can store -1
// [08 R-SKIR-01 §1].
func TestSkirmishControllerCycleResolvesColorConflict(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("teamcolour")}
	g.ensureRetailSkirmishControllers()
	g.setup.NumPlayers = 3
	g.retailControllers = [session.SkirmishMaxPlayers]int{1, 2, 0}
	for i, c := range []int{0, 1, 0} {
		g.setup.Players[i].Color = c
	}
	g.cycleRetailController(2)
	if g.retailControllers[2] == 0 {
		t.Fatal("row 2 did not become live")
	}
	// Colour 0 collides with live row 0; the rescan skips 1 because open and
	// live rows alike block, and lands on the first free logo.
	if got := g.setup.Players[2].Color; got != 2 {
		t.Fatalf("row 2 colour after becoming live = %d, want 2", got)
	}
	// No collision, no rewrite.
	g.setup.Players[1].Color = 7
	g.retailControllers[1] = 0
	g.cycleRetailController(1)
	if got := g.setup.Players[1].Color; got != 7 {
		t.Fatalf("conflict-free row was rewritten to %d", got)
	}
}
