package main

// The playback overlay (DESIGN_INTERFACE_HUD_INPUT "Replays"): a compact
// panel at the top left of the world view, in the side's console face like
// the online network overlay, that says what is playing and where, and holds
// the playback's controls — pause, slower, faster, skip ahead, the next
// perspective and exit — each also on a key. Everything here drives the
// playback's host controls (replay_playback.go); nothing reaches a tick.

import (
	"fmt"
	"image"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// replayControl is one control of the overlay.
type replayControl int

const (
	replayControlPause replayControl = iota
	replayControlSlower
	replayControlFaster
	replayControlSkip
	replayControlView
	replayControlExit
	replayControlCount
)

// replayKey is what a playback key asks for.
type replayKey int

const (
	replayKeyNone replayKey = iota
	replayKeyPause
	replayKeySlower
	replayKeyFaster
	replayKeySkip
	replayKeyStopSkip
	replayKeyView
	replayKeyExit
)

// replaySkipStep is how far one skip goes: a minute of game time.
const replaySkipStep = 30 * 60

// replayOverlayState is the overlay's host state: where its controls were
// last drawn, the one pressed, and an exit the shell step takes.
type replayOverlayState struct {
	buttons  [replayControlCount]image.Rectangle
	backdrop image.Rectangle
	// pressed is the control a left press began on, plus one; the release
	// over the same control activates it.
	pressed int
	exit    bool
	// mapName is the map as the map list spells it, when the Replays screen
	// knows it; the header's otherwise.
	mapName string
}

// replayOverlayView is what the overlay shows of a playback.
type replayOverlayView struct {
	Map           string
	Tick, Final   uint32
	Speed         int
	Skipping      bool
	Target        uint32
	Ended, Paused bool
	View          string
	Views         int
	// Message is the end or divergence line; Failed marks a stop that is
	// not the recording's end.
	Message string
	Failed  bool
}

// overlayView is the playback as the overlay shows it now.
func (p *replayPlayback) overlayView() replayOverlayView {
	v := replayOverlayView{Map: p.header.MapName, Tick: p.Tick(), Final: p.FinalTick(), Speed: p.speed, Ended: p.Ended(), Paused: p.paused, Views: len(p.perspectives), Message: p.Message()}
	v.Target, v.Skipping = p.Skipping()
	if p.overlay.mapName != "" {
		v.Map = p.overlay.mapName
	}
	if i := p.perspective; i >= 0 && i < len(p.perspectives) {
		v.View = p.perspectives[i].Label
	}
	if s := p.Status(); s.Mismatch != nil || s.Err != nil {
		v.Failed = true
	}
	return v
}

// replaySpeedText is a speed for the console face, which has no fraction
// glyphs.
func replaySpeedText(i int) string {
	q := replaySpeeds[max(0, min(len(replaySpeeds)-1, i))].Quarters
	if q < 4 {
		return fmt.Sprintf("1/%dx", 4/q)
	}
	return fmt.Sprintf("%dx", q/4)
}

// lines are the overlay's text lines: what is playing, where it is, and
// whose view it shows.
func (v replayOverlayView) lines() []string {
	place := replayGameTime(v.Tick) + " / " + replayGameTime(v.Final) + "   " + replaySpeedText(v.Speed)
	switch {
	case v.Skipping:
		place += "   Skipping to " + replayGameTime(v.Target)
	case v.Ended:
		place += "   Ended"
	case v.Paused:
		place += "   Paused"
	}
	return []string{"Replay: " + v.Map, place, "View: " + v.View}
}

// label is a control's caption now.
func (v replayOverlayView) label(c replayControl) string {
	switch c {
	case replayControlPause:
		if v.Paused || v.Ended {
			return "Play"
		}
		return "Pause"
	case replayControlSlower:
		return "Slower"
	case replayControlFaster:
		return "Faster"
	case replayControlSkip:
		if v.Skipping {
			return "Stop skip"
		}
		return "Skip 1:00"
	case replayControlView:
		return "View"
	case replayControlExit:
		return "Exit"
	}
	return ""
}

// enabled reports a control that does something now.
func (v replayOverlayView) enabled(c replayControl) bool {
	switch c {
	case replayControlPause, replayControlSkip:
		return !v.Ended
	case replayControlSlower:
		return v.Speed > 0
	case replayControlFaster:
		return v.Speed < len(replaySpeeds)-1
	case replayControlView:
		return v.Views > 1
	}
	return true
}

// replayControlHint names a control and its key, for the hovered control.
func replayControlHint(c replayControl) string {
	switch c {
	case replayControlPause:
		return "Pause or resume (Pause key)"
	case replayControlSlower:
		return "Slower (- key)"
	case replayControlFaster:
		return "Faster (+ key)"
	case replayControlSkip:
		return "Skip ahead a minute (]); stop a skip ([)"
	case replayControlView:
		return "Next player's view, then the full map (Home)"
	case replayControlExit:
		return "Back to the Replays screen (Backspace)"
	}
	return ""
}

// activate applies a control: Pause during a skip stops the skip there,
// paused, and the skip control stops a running skip.
func (p *replayPlayback) activate(c replayControl) {
	switch c {
	case replayControlPause:
		p.apply(replayKeyPause)
	case replayControlSlower:
		p.apply(replayKeySlower)
	case replayControlFaster:
		p.apply(replayKeyFaster)
	case replayControlSkip:
		if _, skipping := p.Skipping(); skipping {
			p.apply(replayKeyStopSkip)
		} else {
			p.apply(replayKeySkip)
		}
	case replayControlView:
		p.apply(replayKeyView)
	case replayControlExit:
		p.apply(replayKeyExit)
	}
}

// apply does what a key asks for. The skip key goes a minute ahead of where
// the playback is, or of a running skip's target.
func (p *replayPlayback) apply(k replayKey) {
	switch k {
	case replayKeyPause:
		if _, skipping := p.Skipping(); skipping {
			p.CancelSkip()
			p.paused = true
		} else {
			p.paused = !p.paused
		}
		p.menuHeld = false
	case replayKeySlower:
		p.SetSpeed(p.speed - 1)
	case replayKeyFaster:
		p.SetSpeed(p.speed + 1)
	case replayKeySkip:
		if p.Ended() {
			return
		}
		from := p.Tick()
		if target, skipping := p.Skipping(); skipping {
			from = target
		}
		p.SkipTo(from + replaySkipStep)
	case replayKeyStopSkip:
		p.CancelSkip()
	case replayKeyView:
		if n := len(p.perspectives); n > 0 {
			_ = p.SetPerspective((p.perspective + 1) % n)
		}
	case replayKeyExit:
		p.overlay.exit = true
	}
}

// applySchedule takes a battle schedule intent in a playback, which takes no
// pause or speed command: the in-battle menu holds the playback while it is
// open and restores what it was, as a single-player battle's menu pauses it;
// Tab's resume of a paused battle resumes it; a speed step steps its speed.
func (p *replayPlayback) applySchedule(intent ui.BattleScheduleIntent) {
	if intent.PauseSet {
		switch {
		case intent.Pause && !p.menuHeld:
			p.menuHeld, p.menuWasPaused, p.paused = true, p.paused, true
		case intent.Pause:
			p.paused = true
		case p.menuHeld:
			p.menuHeld, p.paused = false, p.menuWasPaused
		default:
			p.paused = false
		}
	}
	switch {
	case intent.SpeedDelta > 0:
		p.SetSpeed(p.speed + 1)
	case intent.SpeedDelta < 0:
		p.SetSpeed(p.speed - 1)
	}
}

// replayShortcutKey reads a playback key from the battle's residual token:
// Pause, - and + as in a single-player battle (the speed keys left to the
// developer film's table while it runs); ] and [ to skip ahead a minute and
// stop a skip; Home for the next view; Backspace to leave.
func replayShortcutKey(in *input.State, kbd *input.KeyboardState, film bool) replayKey {
	if kbd == nil || kbd.KeyHeld(input.KeyCtrl) {
		return replayKeyNone
	}
	switch {
	case kbd.KeyDown(input.KeyPause):
		return replayKeyPause
	case !film && (kbd.KeyDown(input.KeyMinus) || kbd.KeyDown(input.KeyNumpadSubtract)):
		return replayKeySlower
	case !film && (kbd.KeyDown(input.KeyEqual) || kbd.KeyDown(input.KeyNumpadAdd)):
		return replayKeyFaster
	case kbd.KeyDown(input.KeyHome):
		return replayKeyView
	case kbd.KeyDown(input.KeyBackspace):
		return replayKeyExit
	}
	if in != nil && in.ShortcutTokenMode && in.ShortcutToken.Kind == input.TokenText {
		switch in.ShortcutToken.Rune {
		case ']':
			return replayKeySkip
		case '[':
			return replayKeyStopSkip
		}
	}
	return replayKeyNone
}

// handleReplayShortcut gives a playback its keys. It reports a key it took.
func (b *battleSession) handleReplayShortcut(in *input.State, kbd *input.KeyboardState) bool {
	p := b.replayPlayback()
	if p == nil {
		return false
	}
	k := replayShortcutKey(in, kbd, b.developer.film)
	if k == replayKeyNone {
		return false
	}
	p.apply(k)
	return true
}

// serviceReplayOverlayPointer takes a press on the overlay: a left press on
// a control arms it and its release over the same control activates it, as
// the authored buttons do; any other press on the overlay is consumed so it
// never selects or orders through it. It reports the pointer taken.
func (b *battleSession) serviceReplayOverlayPointer(mouse *input.MouseState, x, y int32) bool {
	p := b.replayPlayback()
	if p == nil || mouse == nil {
		return false
	}
	o := &p.overlay
	at := image.Pt(int(x), int(y))
	if o.pressed != 0 {
		if !mouse.Held(input.MouseButtonLeft) {
			c := replayControl(o.pressed - 1)
			o.pressed = 0
			if at.In(o.buttons[c]) && p.overlayView().enabled(c) {
				p.activate(c)
			}
		}
		return true
	}
	if !at.In(o.backdrop) {
		return false
	}
	if mouse.Pressed(input.MouseButtonLeft) {
		for c, r := range o.buttons {
			if at.In(r) {
				o.pressed = c + 1
				break
			}
		}
		return true
	}
	return mouse.Pressed(input.MouseButtonRight) || mouse.Pressed(input.MouseButtonMiddle)
}

// replayOverlayLayout is the overlay measured in its font and placed.
type replayOverlayLayout struct {
	step     int
	x, y     int // the first line's pen
	backdrop image.Rectangle
	buttons  [replayControlCount]image.Rectangle
	buttonsY int
	message  []string
	hint     string
}

const (
	replayButtonPadX = 4
	replayButtonGap  = 3
)

// layoutReplayOverlay places the overlay where the online network overlay
// goes: the top left of the world view, below the resource strip and what
// else is shown there (above, the row the overlay keeps below), and below
// the +fps panel when it would reach it. The end message wraps to the
// overlay's width; a hint takes one more line.
func layoutReplayOverlay(measure func(string) int, height int, lines []string, labels [replayControlCount]string, message, hint string, screenW int, fps bool, above int) replayOverlayLayout {
	const clearance = 2
	l := replayOverlayLayout{step: height + 1, hint: hint}
	width, row := 0, 0
	for _, line := range lines {
		width = max(width, measure(line))
	}
	for i, label := range labels {
		if i > 0 {
			row += replayButtonGap
		}
		row += measure(label) + 2*replayButtonPadX
	}
	width = max(width, row)
	if message != "" {
		l.message = retailWrapLines(message, measure, width)
		for _, line := range l.message {
			width = max(width, measure(line))
		}
	}
	width = max(width, measure(hint))
	buttonH := l.step + 3
	rows := len(lines) + len(l.message)
	if hint != "" {
		rows++
	}
	left, top := hud.ChromeRailX+2, max(hud.ChromeStripHeight+4, above+clearance)
	w, h := width+6, rows*l.step+buttonH+7
	if fps && left+w+clearance > screenW-onlineFPSPanelW-onlineFPSPanelMargin {
		top = max(top, onlineFPSPanelMargin+onlineFPSPanelH+2*clearance)
	}
	l.backdrop = image.Rect(left, top, left+w, top+h)
	l.x, l.y = left+3, top+2
	l.buttonsY = l.y + len(lines)*l.step + 1
	x := l.x
	for i, label := range labels {
		bw := measure(label) + 2*replayButtonPadX
		l.buttons[i] = image.Rect(x, l.buttonsY, x+bw, l.buttonsY+buttonH)
		x += bw + replayButtonGap
	}
	return l
}

// replayWeatherBottom is the row under the optional Community weather
// report, which takes the top left of the world view on a display too narrow
// for it in the resource strip (drawCommunityWeather): two console lines
// from row 36 on a backdrop two rows deeper. The overlay keeps below it
// wherever the report is shown, so the two never meet; 0 without it.
func replayWeatherBottom(b *battleSession, fontHeight uint8) int {
	if b == nil || b.hostPreferences().WeatherReport == 0 || b.sess == nil || b.sess.Wind == nil {
		return 0
	}
	return 36 + 2*int(fontHeight) + 6
}

// drawReplayOverlay draws the playback overlay over the world view.
func (h *retailBattleHUD) drawReplayOverlay(c *client.Client, b *battleSession) {
	p := b.replayPlayback()
	if h == nil || c == nil || h.console == nil || p == nil {
		return
	}
	font := h.console
	measure := func(text string) int { return client.MeasureText(font, text) }
	v := p.overlayView()
	var labels [replayControlCount]string
	for i := range labels {
		labels[i] = v.label(replayControl(i))
	}
	o := &p.overlay
	hovered := -1
	if in := c.Input(); in != nil && in.Mouse != nil {
		mouse, _ := in.PointerSample()
		at := image.Pt(int(mouse.X), int(mouse.Y))
		for i, r := range o.buttons {
			if at.In(r) {
				hovered = i
			}
		}
	}
	hint := ""
	if hovered >= 0 {
		hint = replayControlHint(replayControl(hovered))
	}
	screenW, _ := c.Size()
	lines := v.lines()
	l := layoutReplayOverlay(measure, int(font.Height), lines, labels, v.Message, hint, screenW, b.fpsShown(), max(c.MessageColumnBottom(), replayWeatherBottom(b, font.Height)))
	o.buttons, o.backdrop = l.buttons, l.backdrop
	r := l.backdrop
	c.UIFillRect(r.Min.X, r.Min.Y, r.Dx(), r.Dy(), h.guiColor(0))
	for i, line := range lines {
		c.UIText(font, line, l.x, l.y+i*l.step, h.guiColor(15))
	}
	for i, br := range l.buttons {
		fill, text := h.guiColor(8), h.guiColor(15)
		switch {
		case !v.enabled(replayControl(i)):
			text = h.guiColor(7)
		case o.pressed == i+1:
			fill, text = h.guiColor(15), h.guiColor(0)
		case hovered == i:
			fill, text = h.guiColor(7), h.guiColor(0)
		}
		c.UIFillRect(br.Min.X, br.Min.Y, br.Dx(), br.Dy(), fill)
		c.UIText(font, labels[i], br.Min.X+replayButtonPadX, br.Min.Y+2, text)
	}
	y := l.buttons[0].Max.Y + 2
	color := h.guiColor(14)
	if v.Failed {
		color = h.guiColor(12)
	}
	for _, line := range l.message {
		c.UIText(font, line, l.x, y, color)
		y += l.step
	}
	if l.hint != "" {
		c.UIText(font, l.hint, l.x, y, h.guiColor(7))
	}
}
