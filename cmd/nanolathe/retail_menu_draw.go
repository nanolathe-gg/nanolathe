package main

// Painting one authored front-end window: the panel background, and one
// drawer per control kind — art, button, text and surface [07 §5].

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func (g *gameShell) drawRetailPanel(c *client.Client) {
	if g != nil && g.briefing != nil && g.briefing.State() == BriefingOpen {
		g.drawBriefing(c)
		return
	}
	p := g.activePanel()
	if p == nil || p.Window == nil || g.assets == nil {
		return
	}
	if g.frontend.Mode == modeMenuSkirmish {
		g.updateHoverHelp(int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
	}
	// Back to front along the window chain. Modal entries are presented by
	// drawRetailModal after all saved-under screens; every entry is authored.
	entries := g.frontend.Panels.Entries()
	for i, entry := range entries {
		if entry.Modal || entry.Panel == nil || entry.Panel.Window == nil {
			continue
		}
		mode := g.panelMode(entry.Panel)
		if i == len(entries)-1 && g.frontend.Panels.Modal() == nil {
			mode = g.frontend.Mode
		}
		g.drawRetailWindow(c, mode, entry.Panel)
	}
}

func (g *gameShell) panelMode(panel *ui.Panel) shellMode {
	if g != nil && g.assets != nil && panel != nil {
		for mode := modeMenuMain; mode <= modeMenuSkirmish; mode++ {
			if asset := g.assets.panel[mode]; asset != nil && asset.window == panel.Window {
				return mode
			}
		}
	}
	return g.frontend.Mode
}

// drawRetailWindow composes one window of the chain. mode selects the resource
// set the gadget art is resolved against, so a window beneath the active one
// still draws with its own GUI GAF.
func (g *gameShell) drawRetailWindow(c *client.Client, mode shellMode, p *ui.Panel) {
	if p == nil || p.Window == nil {
		return
	}
	savedMode := g.frontend.Mode
	g.frontend.SetMode(mode)
	defer func() { g.frontend.SetMode(savedMode) }()

	background := g.panelBackground()
	var page, common *formats.GAF
	if g.assets != nil {
		common = g.assets.common
	}
	if asset := g.panelAssets(); asset != nil {
		page = asset.art
	}
	if optionsAssets != nil && p == optionsPanel {
		// The options root is a child window with its own full-screen
		// background, and the merged page swaps it [07 R-FE-01 §6].
		background = optionsAssets.background
		page = optionsAssets.art
	}
	if saveLoadAssets != nil && p == saveLoadPanel {
		// The save/load dialog is a child window with its own authored
		// backdrop; it must not borrow the surface it was opened over
		// [07 R-FE-01 §8].
		background = saveLoadAssets.background
		page = saveLoadAssets.art
	}
	if bg := background; bg != nil {
		// the retail implementation copies the window's background bitmap into the window's
		// own surface at (0,0), and that surface is the window rectangle. The
		// bitmap therefore lands at the window origin and anything past the
		// rectangle is not part of the window [07 §4].
		r := p.Window.Rect
		c.UIBlitPCXClipped(bg, int(r.X), int(r.Y), int(r.X), int(r.Y), int(r.W), int(r.H))
	} else {
		drawWindowPanel(c, p.Window, page, common, g.guiColor)
	}
	for i, gad := range p.Window.Gadgets {
		if i == 0 || !p.ActiveAt(i) {
			continue
		}
		r := p.Window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			g.drawRetailButton(c, p, i, gad, r)
		case gui.KindListBox:
			g.drawRetailList(c, p, i, gad, r)
		case gui.KindScrollBar:
			g.drawRetailScrollbar(c, p, i, gad, r)
		case gui.KindSurface:
			g.drawRetailSurface(c, p, i, gad, r)
		case gui.KindLabel, gui.KindPicture:
			g.drawRetailArt(c, p, i, gad, r)
			g.drawRetailText(c, p, i, gad, r)
		default:
			g.drawRetailArt(c, p, i, gad, r)
			g.drawRetailText(c, p, i, gad, r)
		}
	}
}

// drawRetailModal draws the authored MSGBOX.GUI panel. Message text is bound
// only to an authored text control; no runtime fallback gadgets are emitted.
func (g *gameShell) drawRetailModal(c *client.Client) {
	if g == nil || g.assets == nil {
		return
	}
	m := g.frontend.Panels.Modal()
	if m == nil || m.Window == nil {
		return
	}
	var page *formats.GAF
	if g.assets.message != nil {
		page = g.assets.message.art
	}
	drawWindowPanel(c, m.Window, page, g.assets.common, g.guiColor)
	for i, gad := range m.Window.Gadgets {
		if i == 0 || !m.ActiveAt(i) {
			continue
		}
		r := m.Window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindButton:
			if frame := g.retailButtonArt(gad, m.DownAt(i), m.StageAt(i), gad.GrayedOut&1 != 0); frame != nil {
				blitRetailFrame(c, frame, int(r.X), int(r.Y))
			}
			g.drawRetailTextState(c, m, i, gad, r)
		case gui.KindLabel:
			g.drawRetailTextState(c, m, i, gad, r)
		}
	}
}

// blitRetailFrame is the GUI gadget path. GAF offsets are animation-anchor
// metadata; retail's interface renderer places GUI art by the authored
// gadget rectangle and does not apply those offsets (the common GUI frames
// deliberately carry offsets from their animation canvases) [fmt gaf].
func blitRetailFrame(c *client.Client, f *formats.GAFFrame, x, y int) {
	if f == nil {
		return
	}
	c.UIBlit(f, x, y)
}

func (g *gameShell) drawRetailArt(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	if f := g.gadgetArt(gad, p.StatusAt(index)); f != nil {
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
}

func (g *gameShell) drawRetailButton(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	grey := gad.GrayedOut&1 != 0
	art := g.retailButtonArt(gad, p.DownAt(index), p.StageAt(index), grey)
	drawn := art
	if art != nil {
		blitRetailFrame(c, art, int(r.X), int(r.Y))
	}
	// A greyed button's rectangle goes through the rectangle shader after the
	// frame blit, at level -20 — PALETTE.SHD darken row 12 — which is what the
	// painter's "darkened by 20 palette steps" means. The cycle branch
	// (attribute 0x100) is excluded because it has its own greyed frame, and
	// the checkbox branch (attribute 0x80) is the painter's one exemption
	// [07 R-WGT-01 §3][07 R-HUD-04 §4][03 R-COMP-02 §5].
	if drawn != nil && grey && gad.Attribs&guiAttribCheckbox == 0 && !cycleButton(gad) && g.assets != nil {
		c.UIShadeRect(g.assets.pal, int(r.X), int(r.Y), int(r.W), int(r.H), retailGreyedButtonShade)
	}
	g.drawRetailText(c, p, index, gad, r)
}

func (g *gameShell) retailButtonFrame(gad gui.Gadget, status int, pressed bool) *formats.GAFFrame {
	// The compatibility callers predate the separate stage word. Resolve first
	// so their staged special cases use the builder's effective stages.
	resolved := g.resolveRetailButtonArt(gad)
	gad = resolved.gadget
	down, stage := status, 0
	if gad.Stages != 0 {
		down, stage = 0, status
	}
	if pressed {
		down = 1
	}
	return g.retailButtonArt(gad, down, stage, false)
}

func (g *gameShell) drawRetailText(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	g.drawRetailTextState(c, p, index, gad, r)
}

// guiColor resolves a GUI file's semantic color field through the retail
// GUIPAL→PALETTE nearest-RGB table. This is a color-field operation, not an
// image-pixel conversion: GAF/PCX/TNT bytes are copied directly to the
// indexed surface and use PALETTE.PAL at presentation.
func (g *gameShell) guiColor(source byte) byte {
	if g != nil && g.assets != nil && g.assets.pal != nil {
		return g.assets.pal.GUIColor(source)
	}
	return source
}

func (g *gameShell) drawRetailTextState(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p == nil {
		return
	}
	if gad.Kind == gui.KindPicture {
		// The picture-box painter blits frame 0 and darkens the rectangle; it
		// installs no colour and draws no text at all. A caption on a picture
		// box has no retail counterpart [03 R-FONT-01 §6].
		return
	}
	text := retailGadgetText(p, index, gad)
	if text == "" && !(gad.Kind == gui.KindTextBox && p.EditorCaptured() && p.EditorIndex() == index) {
		return
	}
	// Every text painter first selects the font the gadget's `fontnumber`
	// picks from the window's own kind-7 records — font number 0 is the first
	// record — and the common font when none matches. Only the label painter
	// keeps the result: a label whose number matched draws through the FNT
	// drawer directly, while a button's caption always goes through the GAF
	// pen, where the selected FNT is reached only when the window's GAF slot
	// is null [03 R-FONT-01 §5][03 R-FONT-01 §6].
	selected := g.windowGadgetFont(p, gad)
	if gad.Kind == gui.KindLabel && selected != nil {
		color, _ := g.retailTextPen(p, index, gad)
		g.drawRetailLabelFNT(c, p, gad, r, text, selected, color)
		return
	}
	if !g.hasRetailTextFont() && selected == nil {
		return
	}
	measure, lineStep := g.retailTextMetrics(selected)
	if gad.Kind == gui.KindTextBox {
		// A filled kind-3 input owns its plain background; otherwise the
		// window background already restored by the panel draw remains visible
		// [07 R-WGT-01 §6].
		if gad.Attribs&1 != 0 {
			c.UIFillRect(int(r.X), int(r.Y), int(r.W), int(r.H), 0)
		}
		x, y := int(r.X), int(r.Y)+3
		color, shade := g.retailTextPen(p, index, gad)
		if selected != nil && g.retailGAFTextFont() == nil {
			c.UITextWidth(selected, text, x, y, int(r.W), color)
		} else {
			g.drawRetailStringLit(c, text, x, y, int(r.W), color, shade)
		}
		if p.EditorCaptured() && p.EditorIndex() == index {
			caret := p.EditorCaret()
			if caret > len(text) {
				caret = len(text)
			}
			c.UIFillRect(x+measure(text[:caret]), y, 1, lineStep, g.guiColor(9))
		}
		return
	}
	width := measure(text)
	pressed := retailButtonPressed(c, p, index)
	x := int(r.X)
	// the retail implementation tests the left-aligned attribute before the right/center
	// attributes. These are the actual authored GUI conventions: bit 0 uses a
	// three-pixel inset, bit 2 centers, and bit 4 right-aligns with a
	// three-pixel inset. A held button adds the one-pixel armed offset.
	switch {
	case gad.Attribs&1 != 0:
		x += 3
	case gad.Attribs&4 != 0:
		x = int(r.X+r.W) - width - 3
		if x < int(r.X) {
			x = int(r.X)
		}
	case gad.Attribs&2 != 0:
		x += (int(r.W)-1-width)/2 + 1
	default:
		x += 3
	}
	if pressed {
		x += boolInt(gad.Attribs&1 != 0 || gad.Attribs&2 != 0)
	}
	y := retailTextPenY(gad, r, lineStep)
	color, shade := g.retailTextPen(p, index, gad)
	maxWidth := int(r.W)
	if maxWidth <= 0 {
		width, _ := c.Size()
		maxWidth = width - x
	}
	// the retail implementation measures the gadget against two lines of the active font
	// before it picks a renderer: it compares the rectangle's inclusive height
	// (y1-y0) with twice the capital-I frame height plus two, and sends the
	// taller case to the wrapping renderer the retail implementation and everything else to
	// the single-line the retail implementation. SELMAP.GUI authors DESCRIPTION 235x31 for
	// the wrapped case and SIZE 235x18 for the single-line one [07 §4].
	if int(r.H)-1 > 2*lineStep {
		lines := retailWrapLines(text, measure, maxWidth)
		top := int(r.Y) + (int(r.H)-1-len(lines)*lineStep)/2
		if top < int(r.Y) {
			top = int(r.Y)
		}
		for i, line := range lines {
			g.drawRetailStringSelected(c, line, x, top+i*lineStep, maxWidth, color, shade, selected)
		}
		return
	}
	g.drawRetailStringSelected(c, text, x, y, maxWidth, color, shade, selected)
}

// retailGadgetText selects staged captions from the current-stage byte. The
// down-state word only controls an armed frame, so a released selection keeps
// both its art and its caption [07 R-WGT-01 §3].
func retailGadgetText(p *ui.Panel, index int, gad gui.Gadget) string {
	if p == nil {
		return ""
	}
	text := p.TextAt(index)
	if len(gad.Labels) == 0 {
		return text
	}
	state := p.DownAt(index)
	if gad.Stages != 0 {
		state = p.StageAt(index)
	}
	return gad.Labels[clampMenuStage(state, len(gad.Labels))]
}

// windowGadgetFont is the FNT a gadget's `fontnumber` selects from its own
// window's kind-7 font records — the n-th record counting from zero, so font
// number 0 is the window's first record — or nil when the window has no such
// record (the painters then keep the common font) or the record's file did
// not load [07 R-WGT-01 §6][07 R-WGT-01 §12][03 R-FONT-01 §5].
func (g *gameShell) windowGadgetFont(p *ui.Panel, gad gui.Gadget) *formats.FNT {
	if g == nil || g.cs == nil || g.cs.fs == nil || p == nil || p.Window == nil {
		return nil
	}
	return p.Window.Font(g.cs.fs, gad.FontNumber)
}

// retailTextMetrics is the width measurer and line metric of the family the
// GAF pen draws with: the window's GAF font when the slot holds one, else the
// active FNT — the gadget's selected record, or the common font
// [03 R-FONT-01 §6].
func (g *gameShell) retailTextMetrics(selected *formats.FNT) (measure func(string) int, lineStep int) {
	if g.retailGAFTextFont() != nil || selected == nil {
		return g.retailTextWidth, g.retailTextHeight()
	}
	return func(text string) int { return client.MeasureText(selected, text) }, int(selected.Height)
}

// drawRetailStringSelected is the GAF pen with the FNT the gadget selected as
// its null-slot fallback: with a GAF font in the slot the glyphs come from it
// and the FNT is never consulted; with a null slot the FNT drawer is called
// with the width limit dropped (`maxW = -1`) and the selected record's FNT —
// or the common font when the gadget selected none [03 R-FONT-01 §6].
func (g *gameShell) drawRetailStringSelected(c *client.Client, text string, x, y, maxWidth int, color byte, shade int, selected *formats.FNT) {
	if selected == nil || g.retailGAFTextFont() != nil {
		g.drawRetailStringLit(c, text, x, y, maxWidth, color, shade)
		return
	}
	c.UITextWidth(selected, text, x, y, -1, color)
}

// drawRetailLabelFNT is the label painter's FNT path, taken when the label's
// `fontnumber` matched one of the window's kind-7 records [03 R-FONT-01 §6]:
//
//   - an authored x of -1 centres the text on the panel width once;
//   - attribute bit 4 (right) puts the pen at `gx + w - tw`, else bit 2
//     (centre) at `gx + trunc(w/2) - trunc(tw/2)` — two separate truncations —
//     else at `gx`; the pen Y is `gy` in every case;
//   - attribute bit 8 first draws the shadow one pixel right and three down in
//     map entry 0 [03 R-FONT-01 §4];
//   - the FNT drawer then draws at the pen with no width limit, in the raw
//     colour word the label painter installs.
//
// The GAF pen's wrap-or-single-line choice belongs to the other branch and
// never runs here.
func (g *gameShell) drawRetailLabelFNT(c *client.Client, p *ui.Panel, gad gui.Gadget, r gui.Rect, text string, font *formats.FNT, color byte) {
	if c == nil || font == nil || text == "" {
		return
	}
	tw := client.MeasureText(font, text)
	gx, gy, w := int(r.X), int(r.Y), int(r.W)
	if gad.Rect.RawX == -1 && p != nil && p.Window != nil {
		gx = int(p.Window.Rect.X) + (int(p.Window.Rect.W)-tw)/2
	}
	penX := gx
	switch {
	case gad.Attribs&4 != 0:
		penX = gx + w - tw
	case gad.Attribs&2 != 0:
		penX = gx + w/2 - tw/2
	}
	if gad.Attribs&8 != 0 {
		c.UITextWidth(font, text, penX+1, gy+3, -1, g.guiColor(0))
	}
	c.UITextWidth(font, text, penX, gy, -1, color)
}

// retailTextPen resolves the two things a gadget's text pen needs: the
// light-table row the keyed GAF blitter remaps every glyph byte through, and
// the foreground byte the FNT fallback installs ahead of its glyph mask.
//
// Neither is the authored `colorf` field read directly. `colorf` is a
// light-table row that lives on the Panel instance as the gadget's live
// flash word (`Panel.FlashRow`): the builder zeroes it for buttons and
// labels at open, and the service pass decays whatever a screen sets
// [07 R-WGT-01 §1][07 R-WGT-01 §12]. The FNT painter each kind installs
// ahead of its text call is closed by "The FNT foreground each painter
// installs" [03 R-FONT-01 §6]:
//
//   - Button (kind 1): foreground = GUIPAL map entry `row` (the live flash
//     word) when `stages == 0`, map entry 0 when `stages != 0` — the
//     authored `colorf` is never read. The GAF pen's `mode` is always 0 for
//     a button, whatever the flash row, so `shade` stays 0 here.
//   - Label (kind 5): foreground = the live flash word RAW, as a physical
//     palette index — no GUIPAL lookup. The GAF pen's `mode` is the same
//     word, so `shade` still carries it for the GAF path.
//   - Picture box (kind 12): draws no caption at all; drawRetailTextState
//     returns before reaching here, so this function is never called for one.
//
// Every other kind keeps `colorf` as a colour read through the GUIPAL map: a
// listbox draws its rows in "the window colour-table entry the gadget's
// `colorf` selects" [07 R-WGT-01 §4], and focusing a text input "sets the
// drawing colour from the gadget's `colorf`" [07 R-WGT-01 §8].
func (g *gameShell) retailTextPen(p *ui.Panel, index int, gad gui.Gadget) (color byte, shade int) {
	row := p.FlashRow(index)
	switch gad.Kind {
	case gui.KindButton:
		if gad.Stages != 0 {
			return g.guiColor(0), 0
		}
		return g.guiColor(byte(row & 0xff)), 0
	case gui.KindLabel:
		return byte(row & 0xff), int(row)
	default:
		return g.guiColor(byte(gad.ColorF & 0xff)), shade
	}
}

// retailWrapLines breaks a label at spaces so it fits maxWidth, keeping each
// line verbatim. the retail implementation wraps the authored string in place rather than
// re-joining words, so the double space the OTA missiondescription carries
// after the map size survives into the drawn line.
func retailWrapLines(text string, measure func(string) int, maxWidth int) []string {
	if text == "" || measure == nil || maxWidth <= 0 {
		return []string{text}
	}
	var lines []string
	for text != "" {
		if measure(text) <= maxWidth {
			lines = append(lines, text)
			break
		}
		cut := -1
		for i := 0; i < len(text); i++ {
			if text[i] != ' ' {
				continue
			}
			if measure(text[:i]) > maxWidth {
				break
			}
			cut = i
		}
		if cut <= 0 {
			// A single run wider than the box: emit what fits and continue,
			// which is what the renderer's per-glyph width test amounts to.
			cut = len(text)
			for cut > 1 && measure(text[:cut]) > maxWidth {
				cut--
			}
			lines = append(lines, text[:cut])
			text = text[cut:]
			continue
		}
		lines = append(lines, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return lines
}

// retailTextPenY mirrors the retail implementation. The inclusive gadget bottom makes the
// centering span H-1, and staged controls add one to the pen coordinate. The
// pressed state changes the selected art frame but does not move the text pen.
func retailTextPenY(gad gui.Gadget, r gui.Rect, textHeight int) int {
	y := int(r.Y)
	if r.H > 0 {
		y += (int(r.H)-1-textHeight)/2 + boolInt(gad.Stages != 0)
	}
	return y
}

func retailButtonPressed(c *client.Client, p *ui.Panel, index int) bool {
	if c == nil || c.Input() == nil || c.Input().Mouse == nil || p == nil {
		return false
	}
	owner, button := p.Capture()
	if owner != index || p.DownAt(index) == 0 {
		return false
	}
	switch button {
	case 1:
		return c.Input().Mouse.Held(input.MouseButtonLeft)
	case 2:
		return c.Input().Mouse.Held(input.MouseButtonRight)
	}
	return false
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (g *gameShell) drawRetailSurface(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect) {
	if p != nil && p.Window != nil && p.Window.GadgetIndex("MAPPIC") == index && len(g.maps) != 0 && g.mapIdx >= 0 && g.mapIdx < len(g.maps) {
		if d := g.mapDataFor(g.maps[g.mapIdx]); d != nil && d.tnt != nil {
			// the retail implementation writes the selected RADARPIC into the authored
			// MAPPIC canvas using the map's aspect, leaving the surrounding
			// canvas intact. The TNT minimap is the same indexed source for
			// this frontend path; preserve that retail letterbox instead of
			// stretching rectangular maps into the 125×125 square.
			// The source passed to the retail implementation is RADARPIC. Its aspect is
			// the minimap raster, not the terrain cell dimensions in TNT's
			// header.
			previewW, previewH := int(d.tnt.MinimapWidth), int(d.tnt.MinimapHeight)
			if previewW <= 0 || previewH <= 0 {
				return
			}
			drawW, drawH := int(r.W), int(r.H)
			if previewW < previewH {
				drawW = drawH * previewW / previewH
			} else {
				drawH = drawW * previewH / previewW
			}
			if drawW < 1 {
				drawW = 1
			}
			if drawH < 1 {
				drawH = 1
			}
			x := int(r.X) + (int(r.W)-drawW)/2
			y := int(r.Y) + (int(r.H)-drawH)/2
			c.UIBlitIndexed(d.tnt.Minimap, int(d.tnt.MinimapWidth), int(d.tnt.MinimapHeight), x, y, drawW, drawH)
		}
		return
	}
	// the retail implementation is the retail surface renderer, and it is not the ordinary
	// gadget-art blit. An RLE frame (Compressed != 0) is stamped at the gadget
	// origin; a raw frame is texture-mapped across the whole gadget rectangle.
	// SKIRMISH's Color%d surface is the visible case: textures/logos.gaf holds
	// authored 32x32 frames that retail resamples into the authored 20x20 record,
	// while anims/skirmish.gaf's RLE ally icons are stamped 1:1 [07 §4].
	if p == nil {
		return
	}
	if f := g.gadgetArt(gad, p.StatusAt(index)); f != nil {
		if f.Compressed == 0 {
			c.UIBlitFrameScaled(f, int(r.X), int(r.Y), int(r.W), int(r.H))
			return
		}
		blitRetailFrame(c, f, int(r.X), int(r.Y))
	}
}

func pointInRect(x, y int32, r gui.Rect) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
