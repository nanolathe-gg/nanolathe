package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Exercise the windowed --map owner, including its installed Step callback.
// Calling dialog actions directly would miss both a missing shell and a stale
// callback that keeps advancing the retired battle after load [08 R-SAVE-02 §11].
func TestDirectBattleSaveLoadThroughWindowInput(t *testing.T) {
	resetSaveLoadScreenState(t)
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	previous := clPtr
	defer func() { clPtr = previous }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	shell.opts.Root = t.TempDir()
	if shell.menuBGMPending {
		t.Fatal("direct battle retained the pending main-menu music cue")
	}
	b := shell.battle
	if b.shell != shell {
		t.Fatal("direct battle has no dialog owner")
	}
	millis := &shotMillisSource{}
	step := func() {
		if shell.battle != nil {
			shell.battle.millisSource = millis
		}
		millis.step++
		cl.Step(1.0 / 30)
		cl.Input().Mouse.ResetEdges()
		cl.Input().Kbd.ResetEdges()
	}
	click := func(w *gui.Window, name string) {
		t.Helper()
		i := w.GadgetIndex(name)
		if i < 0 {
			t.Fatalf("missing gadget %s", name)
		}
		r := w.PlacedRect(i)
		cl.Input().Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
		cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
		step()
		cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
		step()
	}
	openOptions := func() {
		cl.Input().Kbd.SetKey(input.KeyF2, true)
		step()
		cl.Input().Kbd.SetKey(input.KeyF2, false)
		step()
	}
	// An empty load list raises a modal without ever constructing LOADGAME.
	// That message must own input ahead of ARMOPT, including its F2 toggle.
	openOptions()
	click(b.hud.optionsWin, "LOADGAME")
	if m := shell.frontend.Panels.Modal(); m == nil || m.Message() != retailNoSavedGamesMessage {
		t.Fatal("missing empty-save-list message")
	}
	cl.Input().Kbd.SetKey(input.KeyF2, true)
	step()
	cl.Input().Kbd.SetKey(input.KeyF2, false)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	step()
	if shell.frontend.Panels.Modal() != nil {
		t.Fatal("battle did not dismiss the empty-list message")
	}
	click(b.hud.optionsWin, "OK")

	// A populated play-test scene uses real definitions and the ordinary unit
	// creation path. Compare live identities and saved positions, not pool capacity.
	def, ok := b.cat.Unit("armflash")
	if !ok {
		t.Fatal("missing armflash")
	}
	var handles []pool.Handle
	for owner := 0; owner < 2; owner++ {
		for i := 0; i < 100; i++ {
			x := numeric.Fixed((int64(512 + owner*1024 + (i%10)*40)) << 16)
			z := numeric.Fixed((int64(512 + (i/10)*40)) << 16)
			h, err := b.sess.Units.Create(def, uint8(owner), x, b.sess.World.HeightAt(x, z), z)
			if err != nil {
				t.Fatal(err)
			}
			b.sess.Movement.EnsureUnit(b.sess.Units.Unit(h))
			handles = append(handles, h)
		}
	}
	for i := 0; i < 30; i++ {
		step()
	}
	openOptions()
	tick := b.sess.Clock.GlobalTick
	positions := make([][3]numeric.Fixed, len(handles))
	for i, h := range handles {
		u := b.sess.Units.Unit(h)
		positions[i] = [3]numeric.Fixed{u.X, u.Y, u.Z}
	}
	click(b.hud.optionsWin, "SAVEGAME")
	if !shell.saveLoadPanelActive() {
		t.Fatal("SAVEGAME click did not open a dialog")
	}
	for _, r := range "army" {
		cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
	step()
	if saveLoadUI.Name() != "army" {
		t.Fatalf("save name = %q", saveLoadUI.Name())
	}
	captureSaveLoadUI(t, cl, "save")
	click(saveLoadPanel.Window, "LOAD")
	path := session.RetailSavePath(shell.saveLoadDir(), "army")
	if _, err := save.Open(path); err != nil {
		t.Fatalf("UI save failed: %v (modal %v)", err, shell.frontend.Panels.Modal())
	}
	click(saveLoadPanel.Window, "CANCEL")
	click(b.hud.optionsWin, "LOADGAME")
	if !shell.saveLoadPanelActive() {
		t.Fatal("LOADGAME click did not open a dialog")
	}
	captureSaveLoadUI(t, cl, "load-before-selection")
	// There is one row; click its first line rather than the list's midpoint.
	r := saveLoadPanel.Window.PlacedRect(saveLoadPanel.Window.GadgetIndex("GAMES"))
	cl.Input().Mouse.SetPosition(float32(r.X+8), float32(r.Y+8))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	step()
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	step()
	if saveLoadUI.Selected() != 0 {
		t.Fatal("save row was not selected")
	}
	captureSaveLoadUI(t, cl, "load")
	click(saveLoadPanel.Window, "LOAD")
	restored := shell.battle
	if restored == b || restored == nil || restored.sess.Clock.GlobalTick != tick {
		t.Fatal("load did not replace the battle at its saved tick")
	}
	for i, h := range handles {
		u := restored.sess.Units.Unit(h)
		if u == nil || !u.Alive || [3]numeric.Fixed{u.X, u.Y, u.Z} != positions[i] {
			t.Fatalf("unit %d did not retain its saved position", h)
		}
	}
	// The scheduler account retains the options-menu pause. Resume through
	// the newly restored battle's own options controls [01 §4.3].
	openOptions()
	click(restored.hud.optionsWin, "OK")
	for i := 0; i < 4; i++ {
		step()
	}
	if restored.sess.Clock.GlobalTick <= tick {
		t.Fatalf("installed client callback did not advance the restored battle: clock=%+v modal=%v mode=%v", restored.sess.Clock, restored.battleState().Modal(), shell.frontend.Mode)
	}
	// A restored session must remain saveable through the same controls.
	openOptions()
	click(restored.hud.optionsWin, "SAVEGAME")
	for range saveLoadUI.Name() {
		cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyBackspace})
	}
	for _, r := range "again" {
		cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
	step()
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	step()
	if _, err := save.Open(session.RetailSavePath(shell.saveLoadDir(), "again")); err != nil {
		t.Fatalf("save after load: %v", err)
	}
	t.Logf("saved and restored %d added units at tick %d; resumed at %d", len(handles), tick, restored.sess.Clock.GlobalTick)
}

func captureSaveLoadUI(t *testing.T, cl *client.Client, name string) {
	t.Helper()
	if dir := os.Getenv("NANOLATHE_SAVE_UI_CAPTURE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, cl.ComposeFrame())
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}
