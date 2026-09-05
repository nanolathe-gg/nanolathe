package main

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// The display-mode table is the windowed presentation's fixed list, gated on
// the desktop size, then sorted ascending by width and height with everything
// below 640x480 dropped [07 R-FE-02 §9][07 R-FE-01 §6].
func TestRetailDisplayModeTableIsGatedSortedAndFiltered(t *testing.T) {
	small := retailDisplayModes(1024, 768)
	want := []retailDisplayMode{{640, 480}, {800, 600}, {1024, 768}}
	if len(small) != len(want) {
		t.Fatalf("desktop 1024x768 gave %v; want %v", small, want)
	}
	for i, m := range small {
		if m != want[i] {
			t.Fatalf("desktop 1024x768 gave %v; want %v", small, want)
		}
	}
	// Both axes are inclusive, so a desktop exactly at 1280x1024 admits that
	// mode and a desktop one row short of 1200 does not admit 1600x1200.
	if got := retailDisplayModes(1280, 1024); len(got) != 4 || got[3] != (retailDisplayMode{1280, 1024}) {
		t.Fatalf("desktop 1280x1024 gave %v; want the 1280x1024 row admitted", got)
	}
	if got := retailDisplayModes(1600, 1199); len(got) != 4 {
		t.Fatalf("desktop 1600x1199 gave %v; want 1600x1200 withheld", got)
	}
	if got := retailDisplayModes(1600, 1200); len(got) != 5 || got[4] != (retailDisplayMode{1600, 1200}) {
		t.Fatalf("desktop 1600x1200 gave %v; want the 1600x1200 row admitted", got)
	}
	// A desktop smaller than the smallest listed mode still keeps 640x480:
	// the filter drops modes below it, and every row of the fixed list is at
	// least that size.
	if got := retailDisplayModes(0, 0); len(got) != 3 || got[0] != (retailDisplayMode{640, 480}) {
		t.Fatalf("unknown desktop gave %v; want the three unconditional rows", got)
	}
}

// The option sliders' read-out truncates and their opening position ceilings
// [07 R-FE-01 §6 "slider arithmetic"].
func TestRetailSliderArithmetic(t *testing.T) {
	// travel under two reads 0 whatever the knob says.
	if got := retailSliderValue(5, 1, 20); got != 0 {
		t.Fatalf("travel 1 read %d; want 0", got)
	}
	// The read-out is trunc(pos/(travel-1) * max): with travel 90 and max 2,
	// knob 44 is 44/89*2 = 0.98..., which truncates to 0 and not to 1.
	if got := retailSliderValue(44, 90, 2); got != 0 {
		t.Fatalf("knob 44 of travel 90 read %d; want 0 (truncation, not rounding)", got)
	}
	if got := retailSliderValue(45, 90, 2); got != 1 {
		t.Fatalf("knob 45 of travel 90 read %d; want 1", got)
	}
	if got := retailSliderValue(89, 90, 2); got != 2 {
		t.Fatalf("knob at the end of travel 90 read %d; want the maximum 2", got)
	}
	// The position is a ceiling for a non-integral x, not a rounding: value 1
	// of max 2 over travel 90 is 89/2 = 44.5, which opens at 45.
	if got := retailSliderKnob(1, 90, 2); got != 45 {
		t.Fatalf("value 1 of max 2 opened at knob %d; want 45 (ceiling)", got)
	}
	// An integral x is left alone.
	if got := retailSliderKnob(1, 91, 2); got != 45 {
		t.Fatalf("value 1 of max 2 over travel 91 opened at knob %d; want 45 exactly", got)
	}
	if got := retailSliderKnob(0, 90, 2); got != 0 {
		t.Fatalf("value 0 opened at knob %d; want 0", got)
	}
	// A value past the maximum is clamped before the position is computed.
	if got := retailSliderKnob(9, 90, 2); got != 89 {
		t.Fatalf("value past the maximum opened at knob %d; want the end of travel", got)
	}
	// Every mode index round-trips through position and back, which is what
	// keeps a reopened page showing the size that was chosen.
	for max := 1; max <= 4; max++ {
		for value := 0; value <= max; value++ {
			if got := retailSliderValue(retailSliderKnob(value, 90, max), 90, max); got != value {
				t.Fatalf("value %d of max %d round-tripped to %d", value, max, got)
			}
		}
	}
}

// A stored size below 640x480 cannot come from the slider — the table drops
// those modes — so the loader repairs it rather than resizing to it.
func TestSettingsDisplayBlockDefaultsAndRepair(t *testing.T) {
	d := settings.DefaultDisplay()
	if d.Width != 640 || d.Height != 480 || d.Gamma != 12 {
		t.Fatalf("display defaults are %+v; want 640x480 gamma 12 [02 R-KEYS-01 §5]", d)
	}
	if d.AntiAlias == 0 || d.Shadows == 0 || d.FeatureShadows == 0 || d.VehicleShadows == 0 || d.Shading == 0 {
		t.Fatalf("display defaults are %+v; want every option bit set [02 R-KEYS-01 §5]", d)
	}
	broken := settings.Display{Width: 320, Height: 200, Gamma: 99}
	broken.Normalize()
	if broken.Width != 640 || broken.Height != 480 || broken.Gamma != 12 {
		t.Fatalf("repaired block is %+v; want the defaults restored", broken)
	}
	// A stored zero is "off" for a bit and must survive; only a negative value
	// is repaired.
	off := settings.Display{Width: 800, Height: 600, Shading: 0, AntiAlias: -1}
	off.Normalize()
	if off.Width != 800 || off.Height != 600 || off.Shading != 0 || off.AntiAlias != 1 {
		t.Fatalf("repaired block is %+v; want 800x600 with shading off and anti-alias repaired", off)
	}
}

// Resizing the client re-allocates the offscreen; every drawing path reads the
// size at use time, so the composed frame follows [07 R-FE-01 §11].
func TestClientResizeReallocatesTheOffscreen(t *testing.T) {
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.Resize(800, 600)
	if w, h := cl.Size(); w != 800 || h != 600 {
		t.Fatalf("resized client reports %dx%d; want 800x600", w, h)
	}
	if got := cl.ComposeFrame().Bounds(); got.Dx() != 800 || got.Dy() != 600 {
		t.Fatalf("composed frame is %v; want an 800x600 surface", got)
	}
	// A size below the smallest display mode is refused by the shell, but the
	// client itself only refuses a non-positive one.
	cl.Resize(0, 0)
	if w, h := cl.Size(); w != 800 || h != 600 {
		t.Fatalf("degenerate resize changed the size to %dx%d", w, h)
	}
}

// retailAssetShell opens the mounted install and builds the frontend, or skips.
func retailAssetShell(t *testing.T) (*gameShell, *contentSet, *client.Client) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Skipf("retail frontend unavailable: %v", err)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(shell.assets.pal)
	cl.SetFNT(shell.font)
	cl.SetCamera(&camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH})
	cl.SetUIStage(gameShellUIStage{shell: shell})
	return shell, cs, cl
}

func writeShellShot(t *testing.T, cl *client.Client, path string) {
	t.Helper()
	if path == "" {
		return
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, cl.ComposeFrame()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// `SINGLE`'s `Options` opens `STARTOPT.GUI` as a child window; its `VISUALS`
// button merges the page's gadgets into that window; `VIDSLDR` writes the
// display size and `VIDVAL` shows it as `%d X %d` [07 R-FE-01 §2][07 R-FE-01 §6].
//
// With NANOLATHE_OPTIONS_SHOT set to a directory, the root and the merged page
// are also written there as PNGs.
func TestRetailOptionsScreenVisualsPageDrivesDisplayMode(t *testing.T) {
	shell, _, cl := retailAssetShell(t)
	shotDir := os.Getenv("NANOLATHE_OPTIONS_SHOT")

	shell.openMenu(modeMenuSingle)
	if !shell.hasActiveGadget("Options") {
		t.Fatal("SINGLE.GUI has no active Options gadget")
	}
	shell.activateGadget("Options")
	if !shell.retailOptionsActive() {
		t.Fatal("Options did not open the options root over SINGLE")
	}
	if shell.frontend.Panels.Under() == nil {
		t.Fatal("the options root replaced SINGLE instead of opening over it")
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/options-root.png")
	}

	shell.activateGadget("VISUALS")
	if optionsState.page != "visuals" {
		t.Fatalf("VISUALS merged page %q", optionsState.page)
	}
	slider := shell.retailOptionsSlider("VIDSLDR")
	if slider == nil {
		t.Fatal("the merged page installed no VIDSLDR slider")
	}
	if got := optionsPanel.TextOf("VIDVAL"); got != "640 X 480" {
		t.Fatalf("VIDVAL opened showing %q; want \"640 X 480\"", got)
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/options-visuals.png")
	}

	// Moving the knob to the second row of the table selects 800x600.
	shell.moveRetailSlider("vidsldr", slider, retailSliderKnob(1, slider.travel, slider.max))
	if shell.display.Width != 800 || shell.display.Height != 600 {
		t.Fatalf("VIDSLDR at index 1 wrote %dx%d; want 800x600", shell.display.Width, shell.display.Height)
	}
	if got := optionsPanel.TextOf("VIDVAL"); got != "800 X 600" {
		t.Fatalf("VIDVAL reads %q; want \"800 X 600\"", got)
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/options-visuals-800.png")
	}

	// `UNDO` restores the display size from the entry snapshot and reopens the
	// page; the reopened slider shows the restored size [07 R-FE-01 §6].
	shell.activateGadget("UNDO")
	if shell.display.Width != 640 || shell.display.Height != 480 {
		t.Fatalf("UNDO left %dx%d; want the 640x480 snapshot", shell.display.Width, shell.display.Height)
	}
	if got := optionsPanel.TextOf("VIDVAL"); got != "640 X 480" {
		t.Fatalf("after UNDO VIDVAL reads %q; want \"640 X 480\"", got)
	}

	// Choose 800x600 again and leave through `PREV` ("OK"), which is the save
	// point; `CANCEL` would discard it instead.
	slider = shell.retailOptionsSlider("VIDSLDR")
	shell.moveRetailSlider("vidsldr", slider, retailSliderKnob(1, slider.travel, slider.max))
	shell.activateGadget("PREV")
	if shell.retailOptionsActive() {
		t.Fatal("PREV did not pop the options root")
	}
	if shell.display.Width != 800 || shell.display.Height != 600 {
		t.Fatalf("PREV left %dx%d; want the chosen 800x600", shell.display.Width, shell.display.Height)
	}
	// The captured block carries the chosen mode into the settings file.
	if got := shell.captureSettings().Display; got.Width != 800 || got.Height != 600 {
		t.Fatalf("captured settings carry %dx%d; want 800x600", got.Width, got.Height)
	}

	// The load transition is the size pair's only reader: it compares the pair
	// to the surface and resizes when they differ [07 R-FE-01 §11].
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: 4096, MapH: 4096}
	shell.applyDisplayMode(cl)
	if w, h := cl.Size(); w != 800 || h != 600 {
		t.Fatalf("the load transition left the surface at %dx%d; want 800x600", w, h)
	}
	if shell.cam.ViewW != 800 || shell.cam.ViewH != 600 {
		t.Fatalf("the load transition left the viewport at %dx%d", shell.cam.ViewW, shell.cam.ViewH)
	}
	// Opening any authored menu screen forces the front end back to 640x480
	// whatever the pair holds [07 R-FE-02 §2].
	previousClient := clPtr
	clPtr = cl
	defer func() { clPtr = previousClient }()
	shell.openMenu(modeMenuMain)
	if w, h := cl.Size(); w != retailScreenW || h != retailScreenH {
		t.Fatalf("the front end runs at %dx%d; want 640x480", w, h)
	}
}

// Escape on the options root activates `PREV`: the window authors no
// `escdefault`, so Escape binds to the first button whose name begins `PREV`
// or `Cancel` [07 R-FE-01 §12].
func TestRetailOptionsRootEscapeClosesThroughPrev(t *testing.T) {
	shell, _, _ := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")
	if !shell.retailOptionsActive() {
		t.Fatal("Options did not open the options root")
	}
	shell.activateEscape()
	if shell.retailOptionsActive() {
		t.Fatal("Escape did not close the options root")
	}
}

// A battle composed after the load transition runs at the chosen display mode:
// the terrain view grows with the surface while the authored HUD windows keep
// their 640x480 rectangles [07 R-FE-01 §11][02 §6].
//
// With NANOLATHE_OPTIONS_SHOT set to a directory, the battle is written there
// at both sizes for comparison.
func TestBattleComposesAtTheChosenDisplayMode(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Skipf("battle composition unavailable: %v", err)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.fs)
	b, err := composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)

	shotDir := os.Getenv("NANOLATHE_OPTIONS_SHOT")
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/battle-640.png")
	}

	// This is the load transition's effect, applied to an already-composed
	// battle: the surface moves and the viewport follows it.
	shell := &gameShell{display: settings.Display{Width: 800, Height: 600}, cam: b.cam}
	shell.applyDisplayMode(cl)
	if w, h := cl.Size(); w != 800 || h != 600 {
		t.Fatalf("the battle surface is %dx%d; want 800x600", w, h)
	}
	if b.cam.ViewW != 800 || b.cam.ViewH != 600 {
		t.Fatalf("the battle viewport is %dx%d; want 800x600", b.cam.ViewW, b.cam.ViewH)
	}
	img := cl.ComposeFrame()
	if got := img.Bounds(); got.Dx() != 800 || got.Dy() != 600 {
		t.Fatalf("the composed battle frame is %v; want 800x600", got)
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/battle-800.png")
	}
}

// A press inside the knob takes the capture and a drag maps pointer
// displacement onto the knob; a press on the track beside it captures but does
// not move the knob, and the release that ends a drag does not also fire the
// synthesised arrow step [07 R-WGT-01 §5 "Pointer"].
func TestRetailOptionsSliderPointerCaptureAndDrag(t *testing.T) {
	shell, _, _ := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")
	shell.activateGadget("VISUALS")
	slider := shell.retailOptionsSlider("VIDSLDR")
	if slider == nil {
		t.Fatal("the merged page installed no VIDSLDR slider")
	}
	index, rect := -1, gui.Rect{}
	for i, gad := range optionsPanel.Window.Gadgets {
		if menuKey(gad.Name) == "vidsldr" {
			index, rect = i, optionsPanel.Window.PlacedRect(i)
			break
		}
	}
	if index < 0 {
		t.Fatal("VIDSLDR is not on the merged window")
	}
	gad := optionsPanel.Window.Gadgets[index]
	barX := int(rect.X) + slider.arrowW
	knobCentre := int32(barX + 1 + slider.knob + slider.knobSize/2)
	midY := rect.Y + rect.H/2

	// A press on the track beside the knob captures and leaves the knob alone.
	shell.clickRetailScrollbar(index, gad, rect, int32(barX)+rect.W/2, midY)
	if optionsState.drag.active {
		t.Fatal("a press beside the knob started a drag")
	}
	if slider.knob != 0 {
		t.Fatalf("a press beside the knob moved it to %d", slider.knob)
	}

	// A press inside the knob starts one, and dragging right raises the knob by
	// the pointer displacement.
	shell.clickRetailScrollbar(index, gad, rect, knobCentre, midY)
	if !optionsState.drag.active {
		t.Fatal("a press inside the knob did not take the capture")
	}
	held := &input.MouseState{}
	held.SetPosition(float32(knobCentre)+30, float32(midY))
	held.SetButton(input.MouseButtonLeft, true)
	shell.updateRetailSliderDrag(held)
	if slider.knob != 30 {
		t.Fatalf("a 30-pixel drag left the knob at %d; want 30", slider.knob)
	}
	// The read-out followed the knob.
	if want := retailSliderValue(30, slider.travel, slider.max); shell.display.Width != optionsState.modes[want].W {
		t.Fatalf("the drag left %dx%d; want mode index %d", shell.display.Width, shell.display.Height, want)
	}

	// Releasing ends the drag; the release must not also step an arrow, even
	// though the pointer is past the track's right edge.
	freed := &input.MouseState{}
	freed.SetPosition(float32(rect.X+rect.W+40), float32(midY))
	shell.updateRetailSliderDrag(freed)
	before := slider.knob
	shell.releaseRetailScrollbar(gad, rect, rect.X+rect.W+40, midY)
	if slider.knob != before {
		t.Fatalf("the release that ended the drag stepped the knob to %d; want %d", slider.knob, before)
	}
	if optionsState.drag.active || optionsState.drag.ended {
		t.Fatal("the release left drag state behind")
	}

	// A release on the right arrow with no drag in flight is the arrow step.
	shell.releaseRetailScrollbar(gad, rect, rect.X+rect.W-1, midY)
	if slider.knob != before+1 {
		t.Fatalf("the right arrow left the knob at %d; want %d", slider.knob, before+1)
	}
	shell.releaseRetailScrollbar(gad, rect, rect.X, midY)
	if slider.knob != before {
		t.Fatalf("the left arrow left the knob at %d; want %d", slider.knob, before)
	}
}

// The options family's cue column: `CANCEL` alone plays `Previous`, and every
// other recognised control — the four page buttons, `PREV`, and the pages' own
// `RESTORE`, `UNDO` and stage buttons — plays `Options` [07 R-FE-01 §2]
// [07 R-FE-01 §6]. `SINGLE`'s own `Options` button keeps its `options` cue from
// frontendCue, because the root is not open yet when it fires.
func TestRetailOptionsCueColumn(t *testing.T) {
	for key, want := range map[string]string{
		"sound":    "Options",
		"music":    "Options",
		"speeds":   "Options",
		"visuals":  "Options",
		"prev":     "Options",
		"restore":  "Options",
		"undo":     "Options",
		"anti":     "Options",
		"shading":  "Options",
		"bshadows": "Options",
		"cancel":   "Previous",
	} {
		if got := retailOptionsCue(key); got != want {
			t.Errorf("retailOptionsCue(%q) = %q, want %q [07 R-FE-01 §2][07 R-FE-01 §6]", key, got, want)
		}
	}
	// A slider is driven by its own value callback, which plays no cue, so a
	// drag or an arrow step must stay silent [07 R-FE-01 §6].
	for _, key := range []string{"vidsldr", "gamma"} {
		if got := retailOptionsCue(key); got != "" {
			t.Errorf("retailOptionsCue(%q) = %q, want silence: a slider's value callback plays nothing", key, got)
		}
	}
	// The button that opens the root is `SINGLE`'s, not the root's, and the
	// screen cue column still owns it.
	if got := frontendCue(ui.ModeSingle, "options"); got != "options" {
		t.Errorf("frontendCue(SINGLE, options) = %q, want %q [07 R-FE-01 §2]", got, "options")
	}
	// The options root leaves the shell mode alone, so the screen underneath
	// keeps its own column; none of the root's gadget names collide with it.
	for _, key := range []string{"sound", "music", "speeds", "visuals", "prev", "cancel", "restore", "undo"} {
		if got := frontendCue(ui.ModeSingle, key); got != "" {
			t.Errorf("frontendCue(SINGLE, %q) = %q; the options root's controls must not take SINGLE's column", key, got)
		}
	}
}

// Every page the options root opens merges, installs its controls from the live
// preference block and writes its own store [07 R-FE-01 §6][03 R-AUD-01 §2]
// [03 R-AUD-01 §4][07 R-CAM-01 §7].
//
// With NANOLATHE_OPTIONS_SHOT set to a directory, each page is written there.
func TestRetailOptionsEveryPageOpensAndPersists(t *testing.T) {
	shell, _, cl := retailAssetShell(t)
	shotDir := os.Getenv("NANOLATHE_OPTIONS_SHOT")
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")
	if !shell.retailOptionsActive() {
		t.Fatal("Options did not open the options root")
	}

	// Each page's own sliders and stage buttons.
	for _, page := range []struct {
		button  string
		key     string
		sliders []string
		stages  []string
	}{
		{"SOUND", "sound", []string{"fxvol"}, []string{"MODE", "SPEECH"}},
		{"MUSIC", "music", []string{"musicvol"}, []string{"NOTRAK", "TRACKMODE", "TRACKTYPE"}},
		{"SPEEDS", "speeds", []string{"game", "screen", "txtscrol", "maxlines"}, []string{"LEFTCLICK", "UNITCHAT"}},
		{"VISUALS", "visuals", []string{"gamma", "vidsldr"}, []string{"ANTI", "SHADING", "BSHADOWS"}},
	} {
		shell.activateGadget(page.button)
		if optionsState.page != page.key {
			t.Fatalf("%s merged page %q; want %q", page.button, optionsState.page, page.key)
		}
		for _, name := range page.sliders {
			if shell.retailOptionsSlider(name) == nil {
				t.Errorf("page %s installed no %s slider", page.key, name)
			}
		}
		for _, name := range page.stages {
			if !shell.hasActiveGadget(name) {
				t.Errorf("page %s did not merge its %s control", page.key, name)
			}
		}
		if shotDir != "" {
			writeShellShot(t, cl, shotDir+"/options-"+page.key+".png")
		}
	}

	// The interface page's four sliders each write their own store, and its two
	// stage buttons write theirs [07 R-CAM-01 §7][07 R-CAM-01 §5].
	shell.activateGadget("SPEEDS")
	for _, c := range []struct {
		slider string
		value  int
		read   func() int
	}{
		{"screen", 40, func() int { return shell.scrollSpeed }},
		{"txtscrol", 7, func() int { return shell.messages.TextScroll }},
		{"maxlines", 18, func() int { return shell.messages.TextLines }},
		{"game", 14, func() int { return shellGameSpeed }},
	} {
		s := shell.retailOptionsSlider(c.slider)
		if s == nil {
			t.Fatalf("the interface page installed no %s slider", c.slider)
		}
		shell.moveRetailSlider(c.slider, s, retailSliderKnob(c.value, s.travel, s.max))
		if got := c.read(); got != c.value {
			t.Errorf("%s at value %d stored %d", c.slider, c.value, got)
		}
	}
	shell.activateGadget("LEFTCLICK")
	if shellInterfaceType != settings.InterfaceTypeRightClick {
		t.Errorf("LEFTCLICK left Interface Type %d; want %d", shellInterfaceType, settings.InterfaceTypeRightClick)
	}
	shell.activateGadget("UNITCHAT")
	if shell.messages.UnitChatText != 10 {
		t.Errorf("UNITCHAT from Medium left the text level %d; want 10", shell.messages.UnitChatText)
	}

	// The sound page's gauge and its two stage buttons [03 R-AUD-01 §2].
	shell.activateGadget("SOUND")
	fx := shell.retailOptionsSlider("fxvol")
	if fx == nil {
		t.Fatal("the sound page installed no FXVOL slider")
	}
	shell.moveRetailSlider("fxvol", fx, retailSliderKnob(40, fx.travel, fx.max))
	if shellAudio.FXVol != 40 {
		t.Errorf("FXVOL at 40 stored %d", shellAudio.FXVol)
	}
	// `SPEECH` writes both halves: bit 6 and the voice level as stage times five.
	optionsPanel.SetStatus("SPEECH", 0)
	shell.activateGadget("SPEECH")
	if shellAudio.SpeechFX != 1 || shellAudio.UnitChat != 5 {
		t.Errorf("SPEECH at Medium stored speechfx %d unitchat %d; want 1 and 5", shellAudio.SpeechFX, shellAudio.UnitChat)
	}
	// `MODE` Off greys the gauge, the test button and the speech gauge and
	// deactivates the volume caption [03 R-AUD-01 §2].
	shellAudio.SoundMode = settings.SoundMode3D
	shell.activateGadget("MODE")
	if shellAudio.SoundMode != settings.SoundModeOff {
		t.Fatalf("MODE from 3D left mode %d; want Off", shellAudio.SoundMode)
	}
	if optionsPanel.ActiveOf("VOLTEXT") {
		t.Error("Sound Mode Off left the VOLTEXT caption active")
	}
	for _, name := range []string{"FXVOL", "TEST", "SPEECH"} {
		if !retailGadgetGreyed(optionsAssets.window, name) {
			t.Errorf("Sound Mode Off left %s ungreyed", name)
		}
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/options-sound-off.png")
	}

	// The music page shows `NO DISC` with no tracks, and greys the transport
	// while music is off [03 R-AUD-01 §4].
	shell.activateGadget("MUSIC")
	if optionsState.tracks == 0 && optionsPanel.TextOf("TRACKNUM") != retailNoDiscText {
		t.Errorf("TRACKNUM with no tracks reads %q; want %q", optionsPanel.TextOf("TRACKNUM"), retailNoDiscText)
	}
	shell.activateGadget("NOTRAK")
	if shellAudio.MusicMode != 0 {
		t.Fatalf("NOTRAK left musicmode %d; want 0", shellAudio.MusicMode)
	}
	for _, name := range []string{"MUSICVOL", "CDPLAY", "CDSTOP", "CDNEXT", "CDPREV", "TRACKMODE", "TRACKTYPE"} {
		if !retailGadgetGreyed(optionsAssets.window, name) {
			t.Errorf("music off left %s ungreyed", name)
		}
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/options-music-off.png")
	}

	// `PREV` ("OK") is the save point: the captured block carries every page's
	// edits [07 R-FE-01 §6][07 R-FE-01 §11].
	shell.activateGadget("PREV")
	captured := shell.captureSettings()
	if captured.Audio.FXVol != 40 || captured.Audio.SoundMode != settings.SoundModeOff || captured.Audio.MusicMode != 0 {
		t.Errorf("captured audio block is %+v", captured.Audio)
	}
	if captured.GameSpeed != 14 || captured.InterfaceType != settings.InterfaceTypeRightClick {
		t.Errorf("captured game speed %d interface type %d; want 14 and 1", captured.GameSpeed, captured.InterfaceType)
	}
	if captured.ScrollSpeed != 40 || captured.Messages.TextScroll != 7 || captured.Messages.TextLines != 18 {
		t.Errorf("captured scroll %d textscroll %d textlines %d", captured.ScrollSpeed, captured.Messages.TextScroll, captured.Messages.TextLines)
	}
}

// retailGadgetGreyed reports one gadget's greyed word.
func retailGadgetGreyed(window *gui.Window, name string) bool {
	if window == nil {
		return false
	}
	for _, gad := range window.Gadgets {
		if strings.EqualFold(gad.Name, name) {
			return gad.GrayedOut != 0
		}
	}
	return false
}

// `CANCEL` discards every page's edits, not just the visuals page's
// [07 R-FE-01 §6].
func TestRetailOptionsCancelDiscardsEveryPage(t *testing.T) {
	shell, _, _ := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")
	before := shell.retailOptionsSnapshot()

	shell.activateGadget("SOUND")
	fx := shell.retailOptionsSlider("fxvol")
	if fx == nil {
		t.Fatal("the sound page installed no FXVOL slider")
	}
	shell.moveRetailSlider("fxvol", fx, retailSliderKnob(3, fx.travel, fx.max))
	shell.activateGadget("SPEEDS")
	shell.activateGadget("LEFTCLICK")
	shell.activateGadget("CANCEL")

	if shellAudio != before.audio {
		t.Errorf("CANCEL left the audio block %+v; want the entry copy %+v", shellAudio, before.audio)
	}
	if shellInterfaceType != before.interfaceType {
		t.Errorf("CANCEL left Interface Type %d; want %d", shellInterfaceType, before.interfaceType)
	}
}

// Each page's `RESTORE` writes its own defaults and reopens the page
// [07 R-FE-01 §6][03 R-AUD-01 §2][03 R-AUD-01 §4][07 R-CAM-01 §7].
func TestRetailOptionsRestorePerPageDefaults(t *testing.T) {
	shell, _, _ := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")

	shell.activateGadget("SOUND")
	shellAudio.FXVol, shellAudio.SoundMode, shellAudio.UnitChat, shellAudio.AckFX = 3, settings.SoundModeOff, 0, 0
	shell.activateGadget("RESTORE")
	if shellAudio.FXVol != settings.DefaultFXVol || shellAudio.SoundMode != settings.SoundModeMono ||
		shellAudio.UnitChat != settings.MaxUnitChat || shellAudio.AckFX != 1 {
		t.Errorf("sound RESTORE left %+v", shellAudio)
	}

	shell.activateGadget("MUSIC")
	shellAudio.MusicVol, shellAudio.CDMode, shellAudio.MusicMode = 5, 1, 0
	shell.activateGadget("RESTORE")
	if shellAudio.MusicVol != settings.DefaultMusicVol || shellAudio.CDMode != settings.DefaultCDMode || shellAudio.MusicMode != 1 {
		t.Errorf("music RESTORE left musicvol %d cdmode %d musicmode %d", shellAudio.MusicVol, shellAudio.CDMode, shellAudio.MusicMode)
	}

	shell.activateGadget("SPEEDS")
	shell.scrollSpeed, shellGameSpeed, shellInterfaceType = 3, 20, 1
	shell.messages.TextScroll, shell.messages.TextLines, shell.messages.UnitChatText = 1, 1, 0
	shell.activateGadget("RESTORE")
	if shell.scrollSpeed != settings.DefaultScrollSpeed || shellGameSpeed != settings.DefaultGameSpeed ||
		shellInterfaceType != settings.DefaultInterfaceType {
		t.Errorf("interface RESTORE left scroll %d speed %d iface %d", shell.scrollSpeed, shellGameSpeed, shellInterfaceType)
	}
	if shell.messages.TextScroll != settings.DefaultTextScroll || shell.messages.TextLines != settings.DefaultTextLines ||
		shell.messages.UnitChatText != settings.DefaultUnitChatText || shellAudio.UnitChat != settings.MaxUnitChat {
		t.Errorf("interface RESTORE left messages %+v unitchat %d", shell.messages, shellAudio.UnitChat)
	}
}

// The gauge's device level is retail's own `v << 10`, clamped to the 16-bit
// mixer word [03 R-AUD-01 §2].
func TestRetailWaveVolumeScale(t *testing.T) {
	if got := retailWaveVolumeScale(0); got != 0 {
		t.Errorf("gauge 0 scaled to %v; want 0 — a zero gauge is a closed play gate", got)
	}
	if got := retailWaveVolumeScale(settings.MaxFXVol); got != 1 {
		t.Errorf("gauge %d scaled to %v; want 1 — 64 << 10 saturates the mixer word", settings.MaxFXVol, got)
	}
	if got, want := retailWaveVolumeScale(27), float64(27<<10)/float64(0xFFFF); got != want {
		t.Errorf("the default gauge scaled to %v; want %v", got, want)
	}
}
