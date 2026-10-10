package main

import (
	"image"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// testPlayback is a playback of nothing, at its start: its controls work on
// its host state without a session.
func testPlayback() *replayPlayback {
	return &replayPlayback{player: &replay.Player{}, speed: replayNormalSpeed, header: replay.Header{MapName: "ashap plateau"},
		perspectives: []replayPerspective{{Label: "Ann"}, {Label: "Bob", Seat: 1}, {Label: "Full map", FullMap: true}}}
}

// The overlay says what plays, where, at what speed and from whose view, and
// its controls say what they do now.
func TestReplayOverlayText(t *testing.T) {
	var speeds []string
	for i := range replaySpeeds {
		speeds = append(speeds, replaySpeedText(i))
	}
	if !slices.Equal(speeds, []string{"1/4x", "1/2x", "1x", "2x", "4x", "8x"}) {
		t.Fatalf("speeds %v", speeds)
	}
	v := replayOverlayView{Map: "Ashap Plateau", Tick: 30 * 192, Final: 30 * 940, Speed: replayNormalSpeed, View: "Ann", Views: 3}
	if got := v.lines(); !slices.Equal(got, []string{"Replay: Ashap Plateau", "3:12 / 15:40   1x", "View: Ann"}) {
		t.Fatalf("playing: %q", got)
	}
	paused := v
	paused.Paused, paused.Speed = true, 0
	if got := paused.lines()[1]; got != "3:12 / 15:40   1/4x   Paused" || paused.label(replayControlPause) != "Play" || paused.enabled(replayControlSlower) {
		t.Fatalf("paused: %q, %q", got, paused.label(replayControlPause))
	}
	skipping := v
	skipping.Skipping, skipping.Target, skipping.Paused = true, 30*252, true
	if got := skipping.lines()[1]; got != "3:12 / 15:40   1x   Skipping to 4:12" || skipping.label(replayControlSkip) != "Stop skip" {
		t.Fatalf("skipping: %q, %q", got, skipping.label(replayControlSkip))
	}
	ended := v
	ended.Ended, ended.Tick, ended.Speed, ended.View = true, ended.Final, len(replaySpeeds)-1, "Full map"
	if got := ended.lines(); got[1] != "15:40 / 15:40   8x   Ended" || got[2] != "View: Full map" ||
		ended.enabled(replayControlPause) || ended.enabled(replayControlSkip) || ended.enabled(replayControlFaster) || !ended.enabled(replayControlExit) {
		t.Fatalf("ended: %q", got)
	}
	var labels []string
	for c := range replayControlCount {
		labels = append(labels, v.label(c))
		if replayControlHint(c) == "" {
			t.Fatalf("control %d has no hint", c)
		}
	}
	if !slices.Equal(labels, []string{"Pause", "Slower", "Faster", "Skip 1:00", "View", "Exit"}) {
		t.Fatalf("labels %v", labels)
	}
	if got := testPlayback().overlayView(); got.Map != "ashap plateau" || got.View != "Ann" || got.Views != 3 || got.Ended || got.Message != "" {
		t.Fatalf("view %+v", got)
	}
}

// The playback's keys reach its controls through the battle's residual
// token: Pause and the speed keys as in a single-player battle, ] and [ to
// skip and stop a skip, Home for the next view and Backspace to leave.
func TestReplayKeysDispatchToThePlayback(t *testing.T) {
	text := func(r rune) input.Token { return input.Token{Kind: input.TokenText, Rune: r} }
	edit := func(k input.Key) input.Token { return input.Token{Kind: input.TokenEdit, Key: k} }
	key := func(token input.Token, film bool) replayKey {
		in := &input.State{ShortcutTokenMode: true, ShortcutToken: token}
		return replayShortcutKey(in, battleShortcutKeyboard(in), film)
	}
	for _, c := range []struct {
		token input.Token
		want  replayKey
	}{
		{edit(input.KeyPause), replayKeyPause},
		{text('+'), replayKeyFaster}, {text('='), replayKeyFaster},
		{text('-'), replayKeySlower}, {text('_'), replayKeySlower},
		{text(']'), replayKeySkip}, {text('['), replayKeyStopSkip},
		{edit(input.KeyHome), replayKeyView}, {edit(input.KeyBackspace), replayKeyExit},
		{text('p'), replayKeyNone}, {edit(input.KeyEscape), replayKeyNone}, {edit(input.KeyTab), replayKeyNone},
		{input.Token{Kind: input.TokenEdit, Key: input.KeyA, Ctrl: true}, replayKeyNone},
	} {
		if got := key(c.token, false); got != c.want {
			t.Errorf("%+v: %d, want %d", c.token, got, c.want)
		}
	}
	if key(text('+'), true) != replayKeyNone || key(edit(input.KeyPause), true) != replayKeyPause {
		t.Fatal("the developer film keeps the speed keys")
	}

	p := testPlayback()
	b := &battleSession{playback: p}
	press := func(token input.Token) bool {
		in := &input.State{ShortcutTokenMode: true, ShortcutToken: token}
		return b.handleReplayShortcut(in, battleShortcutKeyboard(in))
	}
	if !press(edit(input.KeyPause)) || !p.Paused() {
		t.Fatal("Pause did not pause")
	}
	press(edit(input.KeyPause))
	if p.Paused() {
		t.Fatal("Pause did not resume")
	}
	press(text('+'))
	press(text('+'))
	if p.Speed() != replayNormalSpeed+2 {
		t.Fatalf("two + keys: speed %d", p.Speed())
	}
	for range 9 {
		press(text('+'))
	}
	if p.Speed() != len(replaySpeeds)-1 {
		t.Fatalf("+ ran past the fastest speed: %d", p.Speed())
	}
	press(text('-'))
	if p.Speed() != len(replaySpeeds)-2 {
		t.Fatalf("- key: speed %d", p.Speed())
	}
	for _, want := range []int{1, 2, 0} {
		press(edit(input.KeyHome))
		if p.Perspective() != want || !p.viewChanged {
			t.Fatalf("Home: perspective %d", p.Perspective())
		}
	}
	// A recording of nothing has nowhere to skip to.
	press(text(']'))
	if _, skipping := p.Skipping(); skipping {
		t.Fatal("skipped past the recording's end")
	}
	if p.overlay.exit || !press(edit(input.KeyBackspace)) || !p.overlay.exit {
		t.Fatal("Backspace did not ask to leave")
	}
	if press(text('p')) || (&battleSession{}).handleReplayShortcut(&input.State{ShortcutTokenMode: true, ShortcutToken: edit(input.KeyPause)}, &input.KeyboardState{}) {
		t.Fatal("a key that is not the playback's, or a battle without one, was taken")
	}
}

// The in-battle menu holds a playback while it is open and restores what it
// was; Tab's resume of a paused battle resumes it; a speed step steps it.
func TestReplayPlaybackFollowsTheMenu(t *testing.T) {
	p := testPlayback()
	p.applySchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: true})
	if !p.Paused() || !p.menuHeld {
		t.Fatal("opening the menu did not hold the playback")
	}
	p.applySchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: false})
	if p.Paused() || p.menuHeld {
		t.Fatal("closing the menu did not resume the playback")
	}
	p.apply(replayKeyPause)
	p.applySchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: true})
	p.applySchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: false})
	if !p.Paused() {
		t.Fatal("closing the menu resumed a playback paused before it opened")
	}
	p.applySchedule(ui.PauseIntent(false))
	if p.Paused() {
		t.Fatal("Tab did not resume a paused playback")
	}
	p.applySchedule(ui.SpeedIntent(1))
	if p.Speed() != replayNormalSpeed+1 {
		t.Fatalf("a speed step: %d", p.Speed())
	}
}

// A left press on a control arms it and its release over the same control
// activates it; a press elsewhere on the overlay is taken without effect,
// and one off it is not taken.
func TestReplayOverlayPointer(t *testing.T) {
	p := testPlayback()
	b := &battleSession{playback: p}
	p.overlay.backdrop = image.Rect(130, 36, 400, 100)
	for i := range p.overlay.buttons {
		x := 133 + 40*i
		p.overlay.buttons[i] = image.Rect(x, 70, x+36, 84)
	}
	mouse := &input.MouseState{}
	click := func(x, y int32) (pressed, released bool) {
		mouse.SetPosition(float32(x), float32(y))
		mouse.SetButton(input.MouseButtonLeft, true)
		pressed = b.serviceReplayOverlayPointer(mouse, x, y)
		mouse.SetButton(input.MouseButtonLeft, false)
		released = b.serviceReplayOverlayPointer(mouse, x, y)
		return
	}
	if pressed, released := click(140, 75); !pressed || !released || !p.Paused() {
		t.Fatal("a click on Pause did not pause")
	}
	if pressed, _ := click(300, 50); !pressed || !p.Paused() {
		t.Fatal("a click on the overlay's text was not taken, or did something")
	}
	// Pressed on Faster, released on Exit: nothing.
	mouse.SetButton(input.MouseButtonLeft, true)
	b.serviceReplayOverlayPointer(mouse, 213, 75)
	mouse.SetButton(input.MouseButtonLeft, false)
	b.serviceReplayOverlayPointer(mouse, 333, 75)
	if p.Speed() != replayNormalSpeed || p.overlay.exit {
		t.Fatal("a release off the pressed control activated a control")
	}
	if pressed, _ := click(500, 300); pressed {
		t.Fatal("a click off the overlay was taken")
	}
	if pressed, released := click(333, 75); !pressed || !released || !p.overlay.exit {
		t.Fatal("a click on Exit did not ask to leave")
	}
}
