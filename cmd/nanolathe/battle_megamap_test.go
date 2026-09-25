package main

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// megamapTestBattle is the ON-05 fixture with the overview preference set.
func megamapTestBattle(t *testing.T, overview int) (*battleSession, *client.Client) {
	t.Helper()
	b := newTestBattle(testCatalogON05(), testWorldON05(64, 48))
	p := settings.DefaultPresentation()
	p.Overview = overview
	b.hostPresentation = &p
	if b.cl == nil {
		t.Fatal("fixture has no client")
	}
	return b, b.cl
}

func megamapStep(b *battleSession, cl *client.Client, prepare func(in *input.State)) {
	in := cl.Input()
	in.Kbd.ResetEdges()
	in.Mouse.ResetEdges()
	if prepare != nil {
		prepare(in)
	}
	b.viewerStep(0, cl)
}

// With the Megamap overview a released Tab toggles the view and never opens
// options; F2 still does. With the Zoom overview Tab opens options as before
// (DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestMegamapTabTogglesOnReleaseInsteadOfOptions(t *testing.T) {
	b, cl := megamapTestBattle(t, settings.OverviewMegamap)
	megamapStep(b, cl, func(in *input.State) { in.Kbd.SetKey(input.KeyTab, true) })
	if b.megamapShown() || b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("Tab press: shown=%v modal=%d, want nothing until release", b.megamapShown(), b.battleState().Modal())
	}
	megamapStep(b, cl, func(in *input.State) { in.Kbd.SetKey(input.KeyTab, false) })
	if !b.megamapShown() {
		t.Fatal("Tab release did not enter the megamap")
	}
	megamapStep(b, cl, func(in *input.State) { in.Kbd.SetKey(input.KeyTab, true) })
	megamapStep(b, cl, func(in *input.State) { in.Kbd.SetKey(input.KeyTab, false) })
	if b.megamapShown() || b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatal("second Tab release did not leave the megamap")
	}
	megamapStep(b, cl, func(in *input.State) { in.Kbd.SetKey(input.KeyF2, true) })
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatal("F2 no longer opens options in the Megamap overview")
	}

	z, zcl := megamapTestBattle(t, settings.OverviewZoom)
	megamapStep(z, zcl, func(in *input.State) { in.Kbd.SetKey(input.KeyTab, true) })
	if z.megamapShown() || z.battleState().Modal() != ui.BattleModalOptions {
		t.Fatal("Zoom overview: Tab must keep opening options")
	}
}

// A wheel notch back enters; a notch forward leaves and, with
// WheelMoveMegaMap, centres the camera on the pointer's map point.
func TestMegamapWheelEntersAndLeaves(t *testing.T) {
	b, cl := megamapTestBattle(t, settings.OverviewMegamap)
	megamapStep(b, cl, func(in *input.State) {
		in.Mouse.SetPosition(300, 200)
		in.Mouse.SetWheel(0, -1)
	})
	if !b.megamapShown() {
		t.Fatal("wheel back did not enter")
	}
	lens := b.megamapLens()
	wx, wz := lens.ScreenToWorld(lens.X+lens.W/2, lens.Y+lens.H/4)
	want := *b.cam
	want.JumpToBattleViewCenter(wx, wz)
	megamapStep(b, cl, func(in *input.State) {
		in.Mouse.SetPosition(float32(lens.X+lens.W/2), float32(lens.Y+lens.H/4))
		in.Mouse.SetWheel(0, 1)
	})
	if b.megamapShown() {
		t.Fatal("wheel forward did not leave")
	}
	if b.cam.X != want.X || b.cam.Z != want.Z {
		t.Fatalf("camera = %d,%d, want centred on the pointer's map point %d,%d", b.cam.X, b.cam.Z, want.X, want.Z)
	}

	// With the Zoom overview the wheel never toggles.
	z, zcl := megamapTestBattle(t, settings.OverviewZoom)
	megamapStep(z, zcl, func(in *input.State) { in.Mouse.SetWheel(0, -1) })
	if z.megamapShown() {
		t.Fatal("Zoom overview entered the megamap")
	}
}

// While shown, the pointer's world point is the lens conversion with the
// terrain height, and every consumer — cursorWorld and pickTarget — takes it.
func TestMegamapPointerWorldMapping(t *testing.T) {
	b, cl := megamapTestBattle(t, settings.OverviewMegamap)
	b.setSurfaceSize(640, 480)
	b.setMegamapShown(true, cl)
	lens := b.megamapLens()
	x, y := lens.X+lens.W/3, lens.Y+lens.H/2
	wx, wz := lens.Unproject(x-lens.X, y-lens.Y)
	gx, gy, gz, ok := b.sess.GroundPointAt(wx, wz)
	if !ok {
		t.Fatal("no ground point")
	}
	cx, cy, cz := b.cursorWorld(x, y)
	if cx != gx || cy != gy || cz != gz {
		t.Fatalf("cursorWorld = %v,%v,%v, want megamap point %v,%v,%v", cx, cy, cz, gx, gy, gz)
	}
	if _, _, pos := b.pickTarget(x, y); pos == nil || pos.X != gx || pos.Z != gz {
		t.Fatalf("pickTarget position = %+v", pos)
	}
	// In the margin the point clamps onto the image rather than reaching the
	// hidden world.
	if lens.X > lens.ViewX {
		mx, _, _ := b.cursorWorld(lens.ViewX, y)
		if mx != 0 {
			t.Fatalf("margin pointer resolved x=%v, want the image's left edge", mx)
		}
	}
	b.setMegamapShown(false, cl)
	if _, _, _, owned := b.megamapCursorWorld(x, y); owned {
		t.Fatal("hidden megamap still owns the pointer")
	}
}

// A left drag of at least nine pixels on both axes over the image selects the
// own units inside it; the world path receives no button state
// [draw-engine-interface "Input while shown"].
func TestMegamapBoxSelectsOwnUnitsAndHidesTheClick(t *testing.T) {
	b, s, builder := placeClickFixture(t, 64, 48)
	cl, err := client.New(client.Options{Buffer: s.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	p := settings.DefaultPresentation()
	p.Overview = settings.OverviewMegamap
	b.hostPresentation = &p
	b.setSurfaceSize(640, 480)
	s.Step(s.Clock.ScaledAnchor + 1)
	if _, ok := b.currentSnapshot(); !ok {
		t.Fatal("no published frame")
	}
	b.setMegamapShown(true, cl)
	lens := b.megamapLens()
	in := cl.Input()
	pointer := func(kind input.PointerEventKind, x, y int32) input.Sample {
		in.EnqueuePointer(input.PointerEvent{Kind: kind, X: x, Y: y, Buttons: input.MouseButtons{Left: kind == input.LeftDown}})
		in.PublishPointer()
		sample := b.pointerSample(in, 0)
		return b.serviceMegamapPointer(in, sample, cl)
	}
	sample := pointer(input.LeftDown, lens.X, lens.Y)
	if sample.PressedButtons[input.MouseButtonLeft] || sample.Pointer.Kind != input.PointerEventNone || !b.megamap.boxActive {
		t.Fatalf("press reached the world path or started no box: %+v box=%v", sample.Pointer, b.megamap.boxActive)
	}
	in.UpdatePointerMotion(input.PointerEvent{X: lens.X + lens.W - 1, Y: lens.Y + lens.H - 1})
	in.PublishPointer()
	b.serviceMegamapPointer(in, b.pointerSample(in, 0), cl)
	pointer(input.LeftUp, lens.X+lens.W-1, lens.Y+lens.H-1)
	var selected bool
	for _, c := range s.PendingHumanCommands() {
		if c.Kind == session.HumanSelectionReplace {
			for _, h := range c.Selection.Handles {
				selected = selected || h == builder
			}
		}
	}
	if !selected {
		t.Fatalf("box selection did not select the own unit: %+v", s.PendingHumanCommands())
	}
}

// Each `Megamap*Color` of -1 keeps the ring's research default — entry 6 for
// weapon slot 1, raw index 1 for slots 2 and 3, entry 10 for radar and sonar,
// entry 12 for the jammers, entry 15 for interceptors — and any other value
// is the palette index itself [draw-engine-interface "`Megamap*Color` keys"].
func TestMegamapRingColorOverrides(t *testing.T) {
	logical := func(entry byte) byte { return 100 + entry }
	defaults := megamapRingColors([8]int{-1, -1, -1, -1, -1, -1, -1, -1}, logical)
	if defaults != [8]byte{106, 1, 1, 110, 110, 112, 112, 115} {
		t.Fatalf("defaults = %v", defaults)
	}
	got := megamapRingColors([8]int{0, -1, 250, -1, 7, -1, -1, 3}, logical)
	if got != [8]byte{0, 1, 250, 110, 7, 112, 112, 3} {
		t.Fatalf("overrides = %v", got)
	}
}

// The megamap dash chain is spaced by the world length of a 20×20
// image-pixel diagonal, phased by (age mod 20) of that spacing, and measured
// along the segment clamped to the map while sprites start from the
// unclamped anchor [draw-engine-interface "Selection and order overlay"].
func TestMegamapDashChainSpacingAndPhase(t *testing.T) {
	type sprite struct {
		frame int
		x     float64
	}
	var got []sprite
	emit := func(frame int, x, _ float64) { got = append(got, sprite{frame, x}) }
	// Half scale: trunc(20/0.5) = 40 on both axes, spacing √3200.
	spacing := math.Sqrt(3200)
	megamapDashChain([2]float64{0, 0}, [2]float64{200, 0}, 0.5, 0.5, 1000, 1000, 25, 2, 3, emit)
	// age 25: phase (25 mod 20) × spacing / 20, frame (25/2) mod 3 = 0.
	if len(got) != 4 || got[0].frame != 0 || got[1].frame != 1 || math.Abs(got[0].x-5*spacing/20) > 1e-9 || math.Abs(got[1].x-got[0].x-spacing) > 1e-9 {
		t.Fatalf("chain = %+v", got)
	}
	// A segment no longer than one spacing draws nothing.
	got = nil
	megamapDashChain([2]float64{0, 0}, [2]float64{56, 0}, 0.5, 0.5, 1000, 1000, 0, 1, 1, emit)
	if len(got) != 0 {
		t.Fatalf("short segment drew %+v", got)
	}
	// The length comes from the clamped endpoints, the positions from the
	// unclamped anchor: -100..200 clamps to 0..200.
	got = nil
	megamapDashChain([2]float64{-100, 0}, [2]float64{200, 0}, 0.5, 0.5, 1000, 1000, 0, 1, 1, emit)
	if len(got) != 4 || got[0].x != -100 || got[3].x >= 100 {
		t.Fatalf("clamped chain = %+v", got)
	}
}
