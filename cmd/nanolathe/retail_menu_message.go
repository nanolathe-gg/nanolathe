package main

// The authored message window: its wrap, its line step and the error a
// missing MSGBOX reports [07 §5] [07 R-FE-01 §2].

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// retailMessageWrapWidth is the wrap width the shell's own diagnostics open the
// message box with. `MSGBOX.GUI` authors no text control at all, so the width is
// an opener argument, never an authored one; retail's own call sites use 150,
// 200, 250, 320, 400, 480 and 500, and 500 is the width all 25 of the widest
// group use — the checksum and sound warnings, the longest strings retail puts
// in the box [07 R-FE-01 §9].
const retailMessageWrapWidth = 500

// showRetailMessage opens the authored message window over a message the box
// builds its own controls for.
//
// `MSGBOX.GUI` holds exactly two gadgets: the `HEADER` panel and the `OK`
// button. There is no authored `TEXT` or `LABEL` gadget, so binding the message
// to one could never succeed and every diagnostic was swallowed behind
// "retail message box cannot be constructed". Retail does not bind: the opener
// localises the text, wraps it to the caller's pixel width, splits it at `\n`
// and **appends one centred `TEXT` label per line** to the window, then resizes
// and re-centres the window and moves `OK` into its bottom-right corner
// [07 R-FE-01 §9]. The authored file is the frame; the controls are runtime.
func (g *gameShell) showRetailMessage(message string) error {
	const logical = "guis/msgbox.gui"
	if g == nil || g.assets == nil || g.assets.message == nil || g.assets.message.window == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window")
	}
	if !g.hasRetailTextFont() {
		return g.retailMessageError(logical, "a loaded frontend text font to measure the message with")
	}
	built := g.buildRetailMessageWindow(g.assets.message.window, message)
	if built == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window with a panel record")
	}
	m := ui.NewPanel(built)
	if m == nil {
		return g.retailMessageError(logical, "an authored MSGBOX window")
	}
	m.SetMessage(message)
	g.frontend.Panels.PushModal(m)
	return nil
}

// buildRetailMessageWindow reproduces the `MSGBOX` opener's layout
// [07 R-FE-01 §9]. The authored window is left untouched; a copy carrying the
// appended labels and the recomputed geometry is returned.
//
// Every constant below is the opener's:
//   - the first label sits at local y = 20 and each next one advances by
//     `fontHeight + 5`, where `fontHeight` is the active FNT's height byte —
//     the opener takes the line step from the FNT even where the widths come
//     from the GUI's GAF font;
//   - each label is created at local x = 0 with height 15 and the centring
//     attribute, and is then widened to the finished panel width;
//   - the panel width is `max(label text widths) + 20` (the `autoWidth`
//     argument every retail call site but one passes), the wrap width otherwise;
//   - the panel height is `lines × 25 + 40` plus the height field of the
//     window's **second gadget record** — for `MSGBOX.GUI` that is the `OK`
//     button's 42, not a title bar. §9's "titleHeight" names that field; the
//     opener reads it at a fixed offset that lands on gadget 1;
//   - the window is centred at `((W − w) / 2, (H − h) / 2)`;
//   - `OK` moves to `(w − okW − 15, h − okH − 15)` and becomes the Enter and
//     Escape default. `MSGBOX.GUI` already authors both defaults as `OK`, so
//     that write is a no-op on the stock file and is not repeated here.
func (g *gameShell) buildRetailMessageWindow(authored *gui.Window, message string) *gui.Window {
	if authored == nil || len(authored.Gadgets) == 0 {
		return nil
	}
	lines := retailMessageWrap(message, g.retailTextWidth, retailMessageWrapWidth)
	step := g.retailMessageLineStep()

	built := &gui.Window{
		Name:   authored.Name,
		Rect:   authored.Rect,
		Focus:  authored.Focus,
		Header: authored.Header,
	}
	built.Gadgets = append(built.Gadgets, authored.Gadgets...)
	for i, line := range lines {
		built.Gadgets = append(built.Gadgets, gui.Gadget{
			Kind:       gui.KindLabel,
			Name:       "TEXT",
			SourceName: fmt.Sprintf("GADGET%d", len(authored.Gadgets)+i),
			Rect:       gui.Rect{X: 0, Y: int32(20 + i*step), W: 0, H: 15},
			Attribs:    2,
			ColorF:     15,
			Active:     1,
			Text:       line,
		})
	}

	// Every retail call site but one passes `autoWidth` — the panel is sized to
	// its widest line plus 20 and the wrap width only bounds the lines
	// [07 R-FE-01 §9]. The shell's diagnostics do the same.
	widest := 0
	for _, line := range lines {
		if w := g.retailTextWidth(line); w > widest {
			widest = w
		}
	}
	width := int32(widest + 20)
	height := int32(len(lines)*25 + 40)
	if len(authored.Gadgets) > 1 {
		height += authored.Gadgets[1].Rect.H
	}
	built.Rect.W, built.Rect.H = width, height
	built.Rect.X = (retailScreenW - width) / 2
	built.Rect.Y = (retailScreenH - height) / 2
	built.OriginX, built.OriginY = built.Rect.X, built.Rect.Y
	built.Gadgets[0].Rect = built.Rect

	for i := range built.Gadgets {
		gad := &built.Gadgets[i]
		switch {
		case gad.Kind == gui.KindLabel && gad.Name == "TEXT":
			gad.Rect.W = width
		case i != 0 && strings.EqualFold(gad.Name, "OK"):
			gad.Rect.X = width - gad.Rect.W - 15
			gad.Rect.Y = height - gad.Rect.H - 15
		}
	}
	return built
}

// retailMessageLineStep is the message box's line advance: the active FNT's
// height plus five [07 R-FE-01 §9].
func (g *gameShell) retailMessageLineStep() int {
	height := 0
	if g != nil && g.font != nil {
		height = int(g.font.Height)
	}
	if height <= 0 {
		height = g.retailTextHeight()
	}
	return height + 5
}

// retailMessageWrap is the message box's word wrapper [07 R-FE-01 §9]. It is
// not the label painter's wrapper: this one measures the line only when the
// **next** character is a space, a newline or a hyphen, breaks when the line has
// reached the wrap width, and rewinds to that separator, replacing it with a
// CR/LF pair. Splitting the result on newlines therefore leaves a trailing CR on
// every broken line, which is retail's own buffer content; control bytes have no
// glyph and measure zero, so the carriage return neither draws nor moves the pen
// [07 §4].
func retailMessageWrap(text string, measure func(string) int, limit int) []string {
	if measure == nil || limit <= 0 {
		return splitRetailMessageLines(text)
	}
	out := make([]byte, 0, len(text)*2+2)
	lineStart := 0
	for i := 0; i < len(text); i++ {
		out = append(out, text[i])
		if i+1 < len(text) {
			switch text[i+1] {
			case ' ', '\n', '-':
			default:
				continue
			}
		} else {
			continue
		}
		if measure(string(out[lineStart:])) < limit {
			if text[i+1] == '\n' {
				lineStart = len(out) + 1
			}
			continue
		}
		// Rewind over the word just completed, in the output and the source
		// together, until the separator that precedes it. Retail walks off the
		// front of the buffer when a single word is wider than the wrap width;
		// the line is kept whole here instead, which is the only deviation.
		back := i
		for back > 0 && text[back] != ' ' && text[back] != '-' {
			back--
		}
		if back == 0 || len(out)-(i-back) <= lineStart {
			continue
		}
		out = out[:len(out)-(i-back)]
		out[len(out)-1] = '\r'
		out = append(out, '\n')
		lineStart = len(out)
		i = back
	}
	return splitRetailMessageLines(string(out))
}

func splitRetailMessageLines(text string) []string {
	// The opener walks the wrapped buffer with strtok on "\n", so empty runs
	// between separators produce no label at all.
	lines := make([]string, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

func (g *gameShell) retailMessageError(logical, expected string) error {
	var providers []string
	if g != nil && g.cs != nil && g.cs.fs != nil {
		providers = providerNames(g.cs.fs)
	}
	return &missingProductError{what: "retail message box cannot be constructed", logical: logical, providers: providers, expected: expected}
}
