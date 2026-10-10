//go:build retail

package main

import (
	"errors"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// recordHeadlessReplay records a displayless battle of ticks to path.
func recordHeadlessReplay(t *testing.T, path string, ticks string) {
	t.Helper()
	opts := replayTestOptions(t, "--map", "ashap plateau", "--seed", "3", "--headless", "--ticks", ticks, "--record-replay", path, "--report", filepath.Join(t.TempDir(), "report.json"))
	cs := replayTestContent(t, opts)
	if err := runHeadless(opts, cs, io.Discard); !errors.Is(err, errHeadlessTickLimit) {
		t.Fatalf("headless run: %v", err)
	}
}

// replaysRetailShell is a window's shell on the retail install, bound to a
// client that is never shown, on the main menu.
func replaysRetailShell(t *testing.T, args ...string) (*gameShell, *client.Client) {
	t.Helper()
	opts := replayTestOptions(t, args...)
	cs := replayTestContent(t, opts)
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	var cl *client.Client
	cl, err = client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH,
		Step: func(delta float64) { shell.step(delta, cl) }})
	if err != nil {
		t.Fatal(err)
	}
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	t.Cleanup(func() { shell.teardownBattle(cl) })
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	if err := bindShellContent(shell, cl); err != nil {
		t.Fatal(err)
	}
	shell.openMenu(modeMenuMain)
	return shell, cl
}

// awaitReplayPlayback steps the shell until Watch has entered the playback.
func awaitReplayPlayback(t *testing.T, shell *gameShell, cl *client.Client) *replayPlayback {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for shell.battle == nil {
		if time.Now().After(deadline) || shell.replays == nil || shell.replays.load == nil {
			status := ""
			if shell.replays != nil {
				status = shell.replays.status
			}
			t.Fatalf("the playback did not start: %q", status)
		}
		cl.Step(1.0 / 30)
		time.Sleep(5 * time.Millisecond)
	}
	p := shell.battle.replayPlayback()
	if p == nil {
		t.Fatal("Watch entered a battle that is not a playback")
	}
	return p
}

// pressKey queues one key the way the window's producer does and runs one
// host step.
func pressKey(shell *gameShell, cl *client.Client, token input.Token) {
	cl.Input().EnqueueToken(token)
	cl.Step(1.0 / 30)
}

// The Replays screen lists a recorded battle, Watch plays it as the recorded
// seat through the battle's own key path — the speed keys up to 8x, Pause
// and the skip key — to its end, where it holds with the end line, and
// Backspace returns to the screen with the replay selected.
func TestReplaysScreenWatchesARecordingRetail(t *testing.T) {
	replayTestSettings(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-10-09_14-00-00_ashap-plateau_skirmish.nlreplay")
	recordHeadlessReplay(t, path, "2100")
	shell, cl := replaysRetailShell(t, "--replay-dir", dir)
	shell.activateGadget(replaysButton)
	s := shell.replays
	if s == nil || len(s.list) != 1 || s.selected != 0 || s.list[0].Path != path {
		t.Fatalf("the screen lists %+v", s)
	}
	if row := replayRowText(s.list[0], "Ashap Plateau"); row == "" || s.panel.TextOf("SIDE") != "Ashap Plateau" || s.panel.TextOf("DIFF") != "1:10" {
		t.Fatalf("row %q, map %q, length %q", row, s.panel.TextOf("SIDE"), s.panel.TextOf("DIFF"))
	}
	shell.activateGadget("LOAD")
	p := awaitReplayPlayback(t, shell, cl)
	if shell.replays != nil || p.Path() != path || p.Tick() > 30 {
		t.Fatalf("the screen stayed open over the playback of %s at %d", p.Path(), p.Tick())
	}
	// Orders are refused quietly: nothing reaches the message column.
	commander := localCommanderOf(t, shell.battle.sess)
	if _, err := shell.battle.submitHumanCommand(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{commander.Handle}}}); !errors.Is(err, errReplayPlaybackCommand) || len(cl.MessageLines()) != 0 {
		t.Fatalf("an order in a playback: %v, %d message lines", err, len(cl.MessageLines()))
	}
	for range 3 {
		pressKey(shell, cl, input.Token{Kind: input.TokenText, Rune: '+'})
	}
	if p.Speed() != len(replaySpeeds)-1 {
		t.Fatalf("three + keys reached speed %d", p.Speed())
	}
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyPause})
	held := p.Tick()
	for range 5 {
		cl.Step(1.0 / 30)
	}
	if !p.Paused() || p.Tick() != held {
		t.Fatal("Pause did not hold the playback")
	}
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyPause})
	if p.Paused() {
		t.Fatal("Pause did not resume the playback")
	}
	before := p.Tick()
	pressKey(shell, cl, input.Token{Kind: input.TokenText, Rune: ']'})
	if target, skipping := p.Skipping(); (!skipping || target < before+replaySkipStep) && p.Tick() < before+replaySkipStep {
		t.Fatalf("the skip key did not skip a minute from %d: at %d, target %d", before, p.Tick(), target)
	}
	for i := 0; !p.Ended(); i++ {
		if i > 2000 {
			t.Fatalf("the playback did not reach its end: at %d of %d", p.Tick(), p.FinalTick())
		}
		cl.Step(1.0 / 30)
	}
	st := p.Status()
	if st.Mismatch != nil || st.Err != nil || st.End != replay.EndLeft || p.Tick() != p.FinalTick() || p.Message() == "" {
		t.Fatalf("playback ended %+v at %d: %q", st, p.Tick(), p.Message())
	}
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyBackspace})
	cl.Step(1.0 / 30)
	if shell.battle != nil || shell.replays == nil || !shell.replaysPanelActive() || shell.replays.list[shell.replays.selected].Path != path {
		t.Fatal("leaving the playback did not return to the Replays screen")
	}
	// The in-battle menu holds the playback and leaves it for the screen too.
	shell.activateGadget("LOAD")
	p = awaitReplayPlayback(t, shell, cl)
	b := shell.battle
	b.openBattleMenu()
	if !p.Paused() {
		t.Fatal("the in-battle menu did not hold the playback")
	}
	for _, name := range []string{"EXIT", "MAINMENU", "CHOICE1"} {
		b.activateBattleMenuButton(name, cl)
	}
	if shell.battle != nil || !shell.replaysPanelActive() {
		t.Fatal("the in-battle menu's main menu did not return to the Replays screen")
	}
}

// TestReplaysScreenCapture writes the screen and the playback overlay to
// $NANOLATHE_REPLAY_CAPTURE for visual review: the main menu, the list with
// an incomplete and a damaged file, the delete confirmation, the empty list,
// and a playback at 1x, paused, skipping, on the full map and at its end.
func TestReplaysScreenCapture(t *testing.T) {
	out := os.Getenv("NANOLATHE_REPLAY_CAPTURE")
	if out == "" {
		t.Skip("NANOLATHE_REPLAY_CAPTURE is unset")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	replayTestSettings(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	// A window battle with orders to watch: the scripted battle of the
	// recording tests.
	opts := replayTestOptions(t, "--map", "ashap plateau", "--seed", "7", "--replay-dir", dir)
	w := openReplayWindow(t, opts, replayTestContent(t, opts))
	playScriptedWindowBattle(t, w, 3600)
	w.shell.teardownBattle(w.cl)
	_, data := onlyReplay(t, dir)
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	real := r.Header()
	fake := func(kind replay.Kind, mapName string, ago time.Duration) replay.Header {
		h := real
		h.Kind, h.MapName, h.Started = kind, mapName, time.Now().Add(-ago).UnixMilli()
		return h
	}
	recordTestReplay(t, replayTarget{dir: dir}, fake(replay.KindSurvival, "Lava Run", 26*time.Hour), 30*60*14+30*7, replay.EndFinished)
	recordTestReplay(t, replayTarget{dir: dir}, fake(replay.KindOnlineSkirmish, "The Pass", 50*time.Hour), 30*60*31+30*42, replay.EndLeft)
	// An incomplete one: the recording a crash cut short after its first
	// flush.
	rec, err := startReplayRecording(replayTarget{dir: dir}, fake(replay.KindSkirmish, "Great Divide", 3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint32(1); tick <= 30*60*6+30*12; tick++ {
		_ = rec.w.Pump(tick, 1)
	}
	if err := rec.flush(); err != nil {
		t.Fatal(err)
	}
	close(rec.stop)
	<-rec.done
	_ = rec.file.Close()
	markReplayActive(rec.part, false)
	rec.setLive(false)
	damaged := filepath.Join(dir, "2026-10-05_20-11-40_lusch-puppy_skirmish.nlreplay")
	if err := os.WriteFile(damaged, []byte("NLRPL damaged beyond reading"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-100 * time.Hour)
	_ = os.Chtimes(damaged, stamp, stamp)

	shell, cl := replaysRetailShell(t, "--replay-dir", dir)
	// The window's draw begins each presented frame, which latches the
	// resource readouts.
	step := func() {
		cl.Step(1.0 / 30)
		cl.BeginPresentationFrame()
	}
	capture := func(name string) {
		t.Helper()
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, cl.ComposeFrame()); err != nil {
			t.Fatal(err)
		}
	}
	capture("mainmenu")
	shell.activateGadget(replaysButton)
	s := shell.replays
	if s == nil || len(s.list) != 5 {
		t.Fatalf("the screen lists %+v", s)
	}
	capture("replays")
	for i, l := range s.list {
		switch {
		case l.Err != nil:
			shell.selectReplayRow(i)
			shell.refreshReplaysPanel()
			capture("replays-damaged")
		case l.Incomplete:
			shell.selectReplayRow(i)
			shell.refreshReplaysPanel()
			capture("replays-incomplete")
		}
	}
	shell.selectReplayRow(1)
	shell.activateGadget("DELETE")
	if s.confirm == nil {
		t.Fatal("Delete did not ask")
	}
	capture("delete-confirm")
	m := shell.frontend.Panels.Modal()
	shell.frontend.Panels.CloseModal()
	shell.finishReplayDeleteConfirmation(m, "OK")
	shell.activateGadget("CANCEL")
	empty := t.TempDir()
	shell.opts.ReplayDir = empty
	shell.activateGadget(replaysButton)
	capture("replays-empty")
	shell.activateGadget("CANCEL")
	shell.opts.ReplayDir = dir
	shell.activateGadget(replaysButton)
	shell.selectReplayRow(0)
	shell.activateGadget("LOAD")
	capture("replays-loading")
	p := awaitReplayPlayback(t, shell, cl)
	for range 300 {
		step()
	}
	capture("playback-1x")
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyPause})
	step()
	capture("playback-paused")
	if b := p.overlay.buttons[replayControlSkip]; !b.Empty() {
		cl.Input().Mouse.SetPosition(float32(b.Min.X+3), float32(b.Min.Y+3))
	}
	step()
	capture("playback-paused-hover")
	cl.Input().Mouse.SetPosition(400, 300)
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyPause})
	pressKey(shell, cl, input.Token{Kind: input.TokenText, Rune: ']'})
	if _, skipping := p.Skipping(); !skipping {
		t.Fatal("the skip finished in one step; nothing to capture")
	}
	capture("playback-skip")
	for _, skipping := p.Skipping(); skipping; _, skipping = p.Skipping() {
		step()
	}
	for !p.Perspectives()[p.Perspective()].FullMap {
		pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyHome})
	}
	for range 30 {
		step()
	}
	capture("playback-fullmap")
	p.SkipTo(p.FinalTick())
	for i := 0; !p.Ended(); i++ {
		if i > 2000 {
			t.Fatal("the playback did not end")
		}
		step()
	}
	step()
	capture("playback-end")
	pressKey(shell, cl, input.Token{Kind: input.TokenEdit, Key: input.KeyBackspace})
	step()
	capture("replays-after")
}
