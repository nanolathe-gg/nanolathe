package main

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
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
	if !shell.activePanel().ActiveOf("Options") {
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
	shell.moveRetailSlider("VIDSLDR", slider, retailSliderKnob(1, slider.travel, slider.max))
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
	shell.moveRetailSlider("VIDSLDR", slider, retailSliderKnob(1, slider.travel, slider.max))
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
		if gad.Name == "VIDSLDR" {
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
	shell.releaseRetailScrollbar(index, gad, rect, rect.X+rect.W+40, midY)
	if slider.knob != before {
		t.Fatalf("the release that ended the drag stepped the knob to %d; want %d", slider.knob, before)
	}
	if optionsState.drag.active || optionsState.drag.ended {
		t.Fatal("the release left drag state behind")
	}

	// A release on the right arrow with no drag in flight is the arrow step.
	shell.releaseRetailScrollbar(index, gad, rect, rect.X+rect.W-1, midY)
	if slider.knob != before+1 {
		t.Fatalf("the right arrow left the knob at %d; want %d", slider.knob, before+1)
	}
	shell.releaseRetailScrollbar(index, gad, rect, rect.X, midY)
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
		{"SOUND", "sound", []string{"FXVOL"}, []string{"MODE", "SPEECH"}},
		{"MUSIC", "music", []string{"MUSICVOL"}, []string{"NOTRAK", "TRACKMODE", "TRACKTYPE"}},
		{"SPEEDS", "speeds", []string{"GAME", "SCREEN", "TXTSCROL", "MAXLINES"}, []string{"LEFTCLICK", "UNITCHAT"}},
		{"VISUALS", "visuals", []string{"GAMMA", "VIDSLDR"}, []string{"ANTI", "SHADING", "BSHADOWS"}},
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
			if !shell.activePanel().ActiveOf(name) {
				t.Errorf("page %s did not merge its %s control", page.key, name)
			}
		}
		// These six are authored staged buttons: their labels and art consume
		// the stage byte, while the other rows in this table are toggles that
		// retain down-state [07 R-WGT-01 §3].
		for _, name := range []string{"MODE", "SPEECH", "TRACKMODE", "TRACKTYPE", "LEFTCLICK", "UNITCHAT"} {
			if index := shell.activePanel().Index(name); index >= 0 && shell.activePanel().Window.Gadgets[index].Stages == 0 {
				t.Errorf("page %s merged %s without authored stages", page.key, name)
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
		{"SCREEN", 40, func() int { return shell.scrollSpeed }},
		{"TXTSCROL", 7, func() int { return shell.messages.TextScroll }},
		{"MAXLINES", 18, func() int { return shell.messages.TextLines }},
		{"GAME", 14, func() int { return shell.gameSpeed }},
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
	if shell.interfaceType != settings.InterfaceTypeRightClick {
		t.Errorf("LEFTCLICK left Interface Type %d; want %d", shell.interfaceType, settings.InterfaceTypeRightClick)
	}
	if got := optionsPanel.StageAt(optionsPanel.Index("LEFTCLICK")); got != settings.InterfaceTypeRightClick || optionsPanel.DownAt(optionsPanel.Index("LEFTCLICK")) != 0 {
		t.Errorf("LEFTCLICK stage/down=%d/%d, want 1/0", got, optionsPanel.DownAt(optionsPanel.Index("LEFTCLICK")))
	}
	shell.activateGadget("UNITCHAT")
	if shell.messages.UnitChatText != 10 {
		t.Errorf("UNITCHAT from Medium left the text level %d; want 10", shell.messages.UnitChatText)
	}
	if got := optionsPanel.StageAt(optionsPanel.Index("UNITCHAT")); got != 2 || optionsPanel.DownAt(optionsPanel.Index("UNITCHAT")) != 0 {
		t.Errorf("UNITCHAT stage/down=%d/%d, want 2/0", got, optionsPanel.DownAt(optionsPanel.Index("UNITCHAT")))
	}

	// The sound page's gauge and its two stage buttons [03 R-AUD-01 §2].
	shell.activateGadget("SOUND")
	fx := shell.retailOptionsSlider("FXVOL")
	if fx == nil {
		t.Fatal("the sound page installed no FXVOL slider")
	}
	shell.moveRetailSlider("FXVOL", fx, retailSliderKnob(40, fx.travel, fx.max))
	if shell.audioPrefs.FXVol != 40 {
		t.Errorf("FXVOL at 40 stored %d", shell.audioPrefs.FXVol)
	}
	// `SPEECH` writes both halves: bit 6 and the voice level as stage times five.
	optionsPanel.SetStageAt(optionsPanel.Index("SPEECH"), 0)
	shell.activateGadget("SPEECH")
	if shell.audioPrefs.SpeechFX != 1 || shell.audioPrefs.UnitChat != 5 {
		t.Errorf("SPEECH at Medium stored speechfx %d unitchat %d; want 1 and 5", shell.audioPrefs.SpeechFX, shell.audioPrefs.UnitChat)
	}
	if got := optionsPanel.StageAt(optionsPanel.Index("SPEECH")); got != 1 || optionsPanel.DownAt(optionsPanel.Index("SPEECH")) != 0 {
		t.Errorf("SPEECH stage/down=%d/%d, want 1/0", got, optionsPanel.DownAt(optionsPanel.Index("SPEECH")))
	}
	// `MODE` Off greys the gauge, the test button and the speech gauge and
	// deactivates the volume caption [03 R-AUD-01 §2].
	shell.audioPrefs.SoundMode = settings.SoundMode3D
	shell.activateGadget("MODE")
	if shell.audioPrefs.SoundMode != settings.SoundModeOff {
		t.Fatalf("MODE from 3D left mode %d; want Off", shell.audioPrefs.SoundMode)
	}
	if got := optionsPanel.StageAt(optionsPanel.Index("MODE")); got != settings.SoundModeOff || optionsPanel.DownAt(optionsPanel.Index("MODE")) != 0 {
		t.Errorf("MODE stage/down=%d/%d, want 0/0", got, optionsPanel.DownAt(optionsPanel.Index("MODE")))
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
	if got := optionsPanel.StageAt(optionsPanel.Index("TRACKMODE")); got != shell.audioPrefs.CDMode-1 || optionsPanel.DownAt(optionsPanel.Index("TRACKMODE")) != 0 {
		t.Errorf("TRACKMODE stage/down=%d/%d, want %d/0", got, optionsPanel.DownAt(optionsPanel.Index("TRACKMODE")), shell.audioPrefs.CDMode-1)
	}
	if want := retailTrackCategory(optionsState.track); optionsPanel.StageAt(optionsPanel.Index("TRACKTYPE")) != want || optionsPanel.DownAt(optionsPanel.Index("TRACKTYPE")) != 0 {
		t.Errorf("TRACKTYPE stage/down=%d/%d, want %d/0", optionsPanel.StageAt(optionsPanel.Index("TRACKTYPE")), optionsPanel.DownAt(optionsPanel.Index("TRACKTYPE")), want)
	}
	if optionsState.tracks == 0 && optionsPanel.TextOf("TRACKNUM") != retailNoDiscText {
		t.Errorf("TRACKNUM with no tracks reads %q; want %q", optionsPanel.TextOf("TRACKNUM"), retailNoDiscText)
	}
	shell.activateGadget("NOTRAK")
	if shell.audioPrefs.MusicMode != 0 {
		t.Fatalf("NOTRAK left musicmode %d; want 0", shell.audioPrefs.MusicMode)
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
	fx := shell.retailOptionsSlider("FXVOL")
	if fx == nil {
		t.Fatal("the sound page installed no FXVOL slider")
	}
	shell.moveRetailSlider("FXVOL", fx, retailSliderKnob(3, fx.travel, fx.max))
	shell.activateGadget("SPEEDS")
	shell.activateGadget("LEFTCLICK")
	shell.activateGadget("CANCEL")

	if shell.audioPrefs != before.audio {
		t.Errorf("CANCEL left the audio block %+v; want the entry copy %+v", shell.audioPrefs, before.audio)
	}
	if shell.interfaceType != before.interfaceType {
		t.Errorf("CANCEL left Interface Type %d; want %d", shell.interfaceType, before.interfaceType)
	}
}

// Each page's `RESTORE` writes its own defaults and reopens the page
// [07 R-FE-01 §6][03 R-AUD-01 §2][03 R-AUD-01 §4][07 R-CAM-01 §7].
func TestRetailOptionsRestorePerPageDefaults(t *testing.T) {
	shell, _, _ := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")

	shell.activateGadget("SOUND")
	shell.audioPrefs.FXVol, shell.audioPrefs.SoundMode, shell.audioPrefs.UnitChat, shell.audioPrefs.AckFX = 3, settings.SoundModeOff, 0, 0
	shell.activateGadget("RESTORE")
	if shell.audioPrefs.FXVol != settings.DefaultFXVol || shell.audioPrefs.SoundMode != settings.SoundModeMono ||
		shell.audioPrefs.UnitChat != settings.MaxUnitChat || shell.audioPrefs.AckFX != 1 {
		t.Errorf("sound RESTORE left %+v", shell.audioPrefs)
	}

	shell.activateGadget("MUSIC")
	shell.audioPrefs.MusicVol, shell.audioPrefs.CDMode, shell.audioPrefs.MusicMode = 5, 1, 0
	shell.activateGadget("RESTORE")
	if shell.audioPrefs.MusicVol != settings.DefaultMusicVol || shell.audioPrefs.CDMode != settings.DefaultCDMode || shell.audioPrefs.MusicMode != 1 {
		t.Errorf("music RESTORE left musicvol %d cdmode %d musicmode %d", shell.audioPrefs.MusicVol, shell.audioPrefs.CDMode, shell.audioPrefs.MusicMode)
	}

	shell.activateGadget("SPEEDS")
	shell.scrollSpeed, shell.gameSpeed, shell.interfaceType = 3, 20, 1
	shell.messages.TextScroll, shell.messages.TextLines, shell.messages.UnitChatText = 1, 1, 0
	shell.activateGadget("RESTORE")
	if shell.scrollSpeed != settings.DefaultScrollSpeed || shell.gameSpeed != settings.DefaultGameSpeed ||
		shell.interfaceType != settings.DefaultInterfaceType {
		t.Errorf("interface RESTORE left scroll %d speed %d iface %d", shell.scrollSpeed, shell.gameSpeed, shell.interfaceType)
	}
	if shell.messages.TextScroll != settings.DefaultTextScroll || shell.messages.TextLines != settings.DefaultTextLines ||
		shell.messages.UnitChatText != settings.DefaultUnitChatText || shell.audioPrefs.UnitChat != settings.MaxUnitChat {
		t.Errorf("interface RESTORE left messages %+v unitchat %d", shell.messages, shell.audioPrefs.UnitChat)
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

// retailBattleOptionsShell composes a real battle with a frontend shell
// attached, so `ARMOPT`'s `PREFS` has both a session to write into and a
// mounted resource set to draw from.
func retailBattleOptionsShell(t *testing.T) (*gameShell, *battleSession, *client.Client) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Skipf("retail frontend unavailable: %v", err)
	}
	rng.SeedGlobal(7, 7)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Skipf("retail battle unavailable: %v", err)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.fs)
	b, err := composeBattleEntry(sess, cat, cs, cl, shell)
	if err != nil {
		t.Fatal(err)
	}
	shell.battle = b
	// The shell's own battle hand-off puts the frontend in battle mode; this
	// harness composes the battle directly, so it does the same here.
	shell.frontend.SetMode(modeBattle)
	t.Cleanup(func() {
		optionsPanel, optionsAssets, optionsState = nil, nil, nil
		b.teardown(cl)
	})
	for i := 0; i < 30; i++ {
		b.viewerStep(1.0/30.0, cl)
	}
	return shell, b, cl
}

// `ARMOPT`'s `PREFS` opens the options root as a child window over the paused
// battle; the root is `PREFS.GUI` widened by 150 with a synthesised `PANEL`,
// and each page button merges the `…RT.GUI` variant with no page bitmap
// [07 R-FE-01 §6][07 R-FE-01 §7].
//
// With NANOLATHE_OPTIONS_SHOT set to a directory the root and each merged page
// are written there as PNGs.
func TestBattlePrefsOpensTheInBattleOptionsRootAndMergesTheRTPages(t *testing.T) {
	shell, b, cl := retailBattleOptionsShell(t)
	shotDir := os.Getenv("NANOLATHE_OPTIONS_SHOT")

	b.openBattleMenu()
	if !b.battleState().Paused() {
		t.Fatal("ARMOPT did not pause the battle")
	}
	b.activateBattleMenuButton("PREFS", cl)
	if !b.battlePrefsActive() {
		t.Fatal("PREFS did not open the options root over the battle")
	}
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("PREFS left the modal chain at %v; ARMOPT must stay underneath", b.battleState().Modal())
	}
	if !b.battleState().Paused() {
		t.Fatal("opening PREFS resumed the battle")
	}
	// The root is the in-battle file, widened by 150 with the synthesised
	// PANEL over the new columns [07 R-FE-01 §6].
	root := optionsAssets.window
	if root.Rect.W != 128+retailBattleOptionsWiden {
		t.Fatalf("the in-battle root is %d columns wide; want the authored 128 plus %d", root.Rect.W, retailBattleOptionsWiden)
	}
	panelRect, _, ok := retailOptionsPanelRect(root)
	if !ok {
		t.Fatal("the in-battle arm synthesised no PANEL gadget")
	}
	if panelRect.W != retailBattleOptionsWiden || panelRect.X != 128 {
		t.Fatalf("the synthesised PANEL is %+v; want the %d new columns beside the plate", panelRect, retailBattleOptionsWiden)
	}
	// No page bitmap is installed at any step, so the battle stays visible.
	if optionsAssets.background != nil {
		t.Fatal("the in-battle root installed a background bitmap")
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/battle-prefs-root.png")
	}

	for _, page := range []struct{ button, key, gui string }{
		{"SOUND", "sound", "guis/soundsrt.gui"},
		{"MUSIC", "music", "guis/musicrt.gui"},
		{"SPEEDS", "speeds", "guis/speedsrt.gui"},
		{"VISUALS", "visuals", "guis/visualrt.gui"},
	} {
		shell.activateGadget(page.button)
		if optionsState.page != page.key {
			t.Fatalf("%s merged page %q; want %q", page.button, optionsState.page, page.key)
		}
		if got := retailOptionsPages[page.key].source(true); got != page.gui {
			t.Fatalf("%s in battle merges %q; want %q", page.button, got, page.gui)
		}
		if optionsAssets.background != nil {
			t.Fatalf("%s installed a page bitmap in battle", page.button)
		}
		// The centring arm places every stock page at its own authored header
		// origin, which is where the widened column is [07 R-FE-01 §6].
		merged := false
		for _, gad := range optionsPanel.Window.Gadgets {
			if !retailOptionsPageGadget(gad) {
				continue
			}
			merged = true
			if gad.Rect.X < 128 {
				t.Fatalf("%s placed %q at x=%d; the merged page belongs in the widened column", page.button, gad.Name, gad.Rect.X)
			}
		}
		if !merged {
			t.Fatalf("%s merged no gadgets", page.button)
		}
		if shotDir != "" {
			writeShellShot(t, cl, shotDir+"/battle-prefs-"+page.key+".png")
		}
	}

	// PREV ("OK") closes the options root and leaves ARMOPT open, with the
	// battle still paused [07 R-FE-01 §6].
	shell.activateGadget("PREV")
	if b.battlePrefsActive() {
		t.Fatal("PREV did not close the in-battle options root")
	}
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("PREV left the modal chain at %v; want ARMOPT", b.battleState().Modal())
	}
	if !b.battleState().Paused() {
		t.Fatal("PREV resumed the battle; only ARMOPT's own close does that")
	}
	if shotDir != "" {
		writeShellShot(t, cl, shotDir+"/battle-armopt.png")
	}
}

// The interface page's writes reach the live battle: `GAME` goes through the
// session's speed setter and `SCREEN` through the camera's cached scroll byte
// [07 R-FE-01 §6][07 R-CAM-01 §3][07 R-CAM-01 §7][07 §10]. `CANCEL` takes both
// back from the entry snapshot.
func TestBattlePrefsInterfacePageDrivesTheLiveSession(t *testing.T) {
	shell, b, cl := retailBattleOptionsShell(t)
	entrySpeed := int(b.sess.Clock.Requested)
	entryScroll := b.scrollSetting()

	b.openBattleMenu()
	b.activateBattleMenuButton("PREFS", cl)
	if shell.gameSpeed != entrySpeed {
		t.Fatalf("the in-battle root opened over stored speed %d; want the session's live %d", shell.gameSpeed, entrySpeed)
	}
	shell.activateGadget("SPEEDS")

	game := shell.retailOptionsSlider("GAME")
	if game == nil {
		t.Fatal("the merged interface page installed no GAME slider")
	}
	shell.moveRetailSlider("GAME", game, retailSliderKnob(settings.MaxGameSpeed, game.travel, game.max))
	if shell.gameSpeed != settings.MaxGameSpeed {
		t.Fatalf("GAME at the end of travel stored %d; want %d", shell.gameSpeed, settings.MaxGameSpeed)
	}
	if got := int(b.sess.Clock.Requested); got != settings.MaxGameSpeed {
		t.Fatalf("GAME left the session requesting speed %d; want %d", got, settings.MaxGameSpeed)
	}

	screen := shell.retailOptionsSlider("SCREEN")
	if screen == nil {
		t.Fatal("the merged interface page installed no SCREEN slider")
	}
	shell.moveRetailSlider("SCREEN", screen, retailSliderKnob(settings.ScrollSliderMax, screen.travel, screen.max))
	if b.scrollSetting() != byte(shell.scrollSpeed) {
		t.Fatalf("SCREEN left the camera reading %d; want the stored %d", b.scrollSetting(), shell.scrollSpeed)
	}
	if b.scrollSetting() == entryScroll {
		t.Fatal("SCREEN at the end of travel did not move the camera's scroll byte")
	}

	shell.activateGadget("CANCEL")
	if b.battlePrefsActive() {
		t.Fatal("CANCEL did not close the in-battle options root")
	}
	if got := int(b.sess.Clock.Requested); got != entrySpeed {
		t.Fatalf("CANCEL left the session at speed %d; want the entry copy %d", got, entrySpeed)
	}
	if b.scrollSetting() != entryScroll {
		t.Fatalf("CANCEL left the camera reading %d; want the entry copy %d", b.scrollSetting(), entryScroll)
	}
}

// The pointer capture goes to the first gadget in index order that has a press
// handler. `PREFS.GUI` authors its whole plate as a picture box at index 1, in
// front of every button, and a picture box takes no press [07 R-WGT-01 §1]
// [07 R-WGT-01 §8].
func TestBattlePrefsPressSkipsThePlatePictureBox(t *testing.T) {
	_, b, cl := retailBattleOptionsShell(t)
	b.openBattleMenu()
	b.activateBattleMenuButton("PREFS", cl)

	want := -1
	for i, gad := range optionsPanel.Window.Gadgets {
		if strings.EqualFold(gad.Name, "SOUND") {
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatal("the in-battle root authors no SOUND button")
	}
	r := optionsPanel.Window.PlacedRect(want)
	x, y := r.X+r.W/2, r.Y+r.H/2
	if got := optionsPanel.PressTest(x, y); got != want {
		name := "nothing"
		if got >= 0 {
			name = optionsPanel.Window.Gadgets[got].Name
		}
		t.Fatalf("a press on SOUND was captured by %q (index %d); want index %d", name, got, want)
	}
}

// The authored preferences page selector has the immediate-action attribute;
// it fires on press and rebuilding the page releases its old panel capture
// [07 R-WGT-01 §3]. Ordinary release buttons are covered by the shared service.
func TestBattlePrefsPumpActivatesAuthoredPressButton(t *testing.T) {
	_, b, cl := retailBattleOptionsShell(t)
	b.openBattleMenu()
	b.activateBattleMenuButton("PREFS", cl)

	index := -1
	for i, gad := range optionsPanel.Window.Gadgets {
		if strings.EqualFold(gad.Name, "VISUALS") {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("the in-battle root authors no VISUALS button")
	}
	if optionsPanel.Window.Gadgets[index].Attribs&0x10 == 0 {
		t.Fatal("VISUALS no longer authors an immediate-action button")
	}
	r := optionsPanel.Window.PlacedRect(index)
	x, y := float32(r.X+r.W/2), float32(r.Y+r.H/2)
	mouse := cl.Input().Mouse

	mouse.ResetEdges()
	mouse.SetPosition(x, y)
	mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleMenuInput(cl.Input(), cl)
	if optionsState == nil || optionsState.page != "visuals" {
		t.Fatal("the authored immediate-action button did not merge VISUALS on press")
	}
	opened := optionsPanel
	if owner, button := opened.Capture(); owner != -1 || button != 0 {
		t.Fatal("rebuilt page retained the previous panel's capture")
	}

	mouse.ResetEdges()
	mouse.SetButton(input.MouseButtonLeft, false)
	b.handleBattleMenuInput(cl.Input(), cl)
	if optionsPanel != opened || optionsState == nil || optionsState.page != "visuals" {
		t.Fatal("release activated the immediate-action selector again")
	}
	mouse.ResetEdges()
}

// `PREFS.GUI` authors `escdefault=PREV`, so Escape leaves through "OK" — the
// tab-close path — and lands back on `ARMOPT` with the battle still paused
// [07 R-FE-01 §6][07 R-FE-01 §12].
func TestBattlePrefsEscapeReturnsToArmopt(t *testing.T) {
	shell, b, cl := retailBattleOptionsShell(t)
	b.openBattleMenu()
	b.activateBattleMenuButton("PREFS", cl)
	if got := optionsPanel.Window.Header.EscDefault; !strings.EqualFold(got, "PREV") {
		t.Fatalf("the in-battle root authors escdefault %q; want PREV", got)
	}
	shell.activateEscape()
	if b.battlePrefsActive() {
		t.Fatal("Escape did not close the in-battle options root")
	}
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("Escape left the modal chain at %v; want ARMOPT", b.battleState().Modal())
	}
	if !b.battleState().Paused() {
		t.Fatal("Escape on the options root resumed the battle")
	}
	// A second Escape is ARMOPT's own, and that one does resume.
	in := input.NewState()
	in.Kbd.SetKey(input.KeyEscape, true)
	b.handleBattleMenuInput(in, cl)
	if b.battleState().Modal() != ui.BattleModalClosed || b.battleState().Paused() {
		t.Fatalf("Escape on ARMOPT left modal %v paused %v", b.battleState().Modal(), b.battleState().Paused())
	}
}
