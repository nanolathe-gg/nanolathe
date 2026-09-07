package main

// The battle modal windows — the in-battle menu, the front-end dialog and the
// paused title — and the authored-window painter they share [07 §11].

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// placeBattleModal applies the established 0x1000 modal placement at the
// negotiated display size. The battle rail occupies x=0..127; modal centering
// therefore uses the remaining width and adds 128 [07 "Tab options menu and
// manual exit"][07 R-HUD-05].
func placeBattleModal(window *gui.Window, screenW, screenH int) {
	if window == nil {
		return
	}
	px, py := hud.ModalPlacement(int32(screenW), int32(screenH), window.Rect.W, window.Rect.H)
	x, y := int(px), int(py)
	window.Rect.X, window.Rect.Y = int32(x), int32(y)
	window.OriginX, window.OriginY = int32(x), int32(y)
	if len(window.Gadgets) != 0 {
		window.Gadgets[0].Rect.X = int32(x)
		window.Gadgets[0].Rect.Y = int32(y)
	}
}

// pauseOverlayVisible synchronizes the UI state from a committed frame only
// before any UI-issued scheduling transition. Once a pause intent is applied,
// the canonical UI truth drives the overlay immediately even if pausing leaves
// the committed tick unchanged [01 §4.3][07 §11][I6].
func pauseOverlayVisible(b *battleSession, committed *frame.Frame) bool {
	if b == nil {
		return false
	}
	state := b.battleState()
	if committed != nil {
		state.SyncCommittedPause(committed.Paused)
	}
	return state.Paused()
}

func (h *retailBattleHUD) drawPausedTitle(c *client.Client) {
	if h == nil || c == nil || h.pausedFrame == nil {
		return
	}
	w, height := c.Size()
	// In-game titles use the view centre as their GAF hotspot. The anchored
	// blitter subtracts the authored offsets [07 R-HUD-05 "Centred in the view"].
	c.UIBlitAnchor(h.pausedFrame, (w+128)/2, height/2)
}

func (h *retailBattleHUD) drawBattleMenu(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || b.battleState() == nil || b.battleState().Modal() == ui.BattleModalClosed {
		return
	}
	state := b.battleState()
	if state.HasOptionsLayer() {
		h.drawGUIWindow(c, h.optionsWin, h.optionsGAF, "")
	}
	if state.HasExitLayer() {
		h.drawGUIWindow(c, h.exitWin, nil, "")
	}
	if state.Modal() == ui.BattleModalConfirmMain {
		h.drawGUIWindow(c, h.confirmWin, nil, state.ConfirmTitle())
	} else if state.Modal() == ui.BattleModalConfirmExit {
		h.drawGUIWindow(c, h.confirmWin, nil, state.ConfirmTitle())
	}
}

// drawFrontendDialog paints the frontend panel stack's open child window over
// the battle and over the results surface.
//
// `ARMOPT` opens the two `LOADGAME.GUI` modes as child windows over whatever
// surface raised them, and `ENDMSN`'s `SaveGame`/`LoadGame` do the same over
// the results screen [07 R-FE-01 §7][07 R-FE-01 §8]. The dialog is therefore
// the last layer of the battle composition: above the options window it was
// opened from and above the result overlay. Until this call the dialog was
// driven but never painted, because the shell's own draw returns early in
// battle mode; the seam is the one battle_menu.go's opener records.
//
// The shell's authored `MSGBOX` follows it: the load direction's "no saved
// games" refusal is raised from inside a battle and must be visible there
// [07 R-FE-01 §9].
func (h *retailBattleHUD) drawFrontendDialog(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || b.shell == nil {
		return
	}
	g := b.shell
	// `PREFS` opens the options root over `ARMOPT`, which the modal chain has
	// already drawn, and the four merged pages leave the battle visible around
	// it [07 R-FE-01 §6][07 R-FE-01 §7].
	h.drawBattleOptionsWindow(c, b)
	if g.saveLoadPanelActive() && saveLoadPanel != nil {
		g.drawRetailWindow(c, g.panelMode(saveLoadPanel), saveLoadPanel)
	}
	g.drawRetailModal(c)
}

// battleSessionKind is doc 08's session-kind word for the running battle: 1
// for a campaign mission, otherwise 2 (skirmish). Multiplayer (3) is out of
// scope for this build, so no path produces it [08 "Session kinds"].
func battleSessionKind(b *battleSession) uint8 {
	if b != nil && b.sess != nil && b.sess.Mission != nil && b.sess.Mission.Type == mission.TypeCampaign {
		return 1
	}
	return 2
}

// commanderDeathOption is the session's commander-death option word. Value 2
// is Deathmatch, which swaps the panel's counters for the commander pair. It
// is immutable session setup, chosen in the lobby before the battle starts
// [07 R-HUD-04 §1][07 R-FE-01 §7][08 R-SKIR-01 §3].
func commanderDeathOption(b *battleSession) int {
	if b == nil || b.sess == nil {
		return 0
	}
	return b.sess.Skirmish.CommanderDeath
}

// drawGUIWindow composes an authored modal into its retail private surface.
// Every write is clipped to the window rectangle before that surface is
// presented, and buttons take the runtime dimensions of their selected art.
func (h *retailBattleHUD) drawGUIWindow(c *client.Client, window *gui.Window, page *formats.GAF, title string) {
	if window == nil {
		return
	}
	clip := window.Rect
	h.drawWindowBackground(c, window, page)
	for i, gad := range window.Gadgets {
		active := gad.Active != 0
		if window == h.resultWin && h.resultPanel != nil {
			active = h.resultPanel.ActiveOf(gad.Name)
		}
		if i == 0 || !active || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		r := h.modalGadgetRect(window, i, page)
		pressed := false
		if c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft) {
			pressed = guiRectContains(r, int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
		}
		frame := h.modalGadgetFrame(gad, page, pressed, gad.GrayedOut != 0)
		if frame != nil {
			if modalArtResampled(gad.Kind, frame, r) {
				c.UIBlitFrameScaledClipped(frame, int(r.X), int(r.Y), int(r.W), int(r.H), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			} else {
				c.UIBlitClipped(frame, int(r.X), int(r.Y), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			}
		}
		text := gad.Text
		if strings.EqualFold(gad.Name, "TITLE") && title != "" {
			text = title
		} else if gad.Kind == gui.KindButton && len(gad.Labels) != 0 {
			text = gad.Labels[0]
		}
		if text == "" || (gad.Kind != gui.KindButton && gad.Kind != gui.KindLabel) {
			continue
		}
		// The painter first selects the FNT the gadget's `fontnumber` picks
		// from the window's own kind-7 records (number 0 is the first record;
		// the common font when none matches). A label whose number matched
		// draws through the FNT drawer directly; a button's caption, and a
		// label that matched nothing, go through the GAF pen, which reaches
		// the selected FNT only when the GAF slot is null
		// [07 R-WGT-01 §6][03 R-FONT-01 §5][03 R-FONT-01 §6]. No stock modal
		// window authors a font record, so the FNT branches below are the
		// rule's no-record and null-slot cases made explicit.
		selected := window.Font(h.fs, gad.FontNumber)
		if gad.Kind == gui.KindLabel && selected != nil {
			h.drawModalLabelFNT(c, window, gad, r, text, selected)
			continue
		}
		// The GAF pen's family: the slot's GAF font, else — the null-slot
		// fallback — the active FNT, which is the selected record or the
		// common font, drawn with the width limit dropped [03 R-FONT-01 §6].
		fallback := selected
		if fallback == nil {
			fallback = h.guiFont
		}
		var textWidth, metric int
		switch {
		case h.modalFont != nil:
			textWidth, metric = retailGAFTextWidth(h.modalFont, text), retailGAFTextHeight(h.modalFont)
		case fallback != nil:
			textWidth, metric = client.MeasureText(fallback, text), int(fallback.Height)
		default:
			continue
		}
		x := int(r.X)
		switch {
		case gad.Attribs&1 != 0:
			x += 3
		case gad.Attribs&4 != 0:
			x = int(r.X+r.W) - textWidth - 3
			if x < int(r.X) {
				x = int(r.X)
			}
		case gad.Attribs&2 != 0:
			x += (int(r.W)-1-textWidth)/2 + 1
		default:
			x += 3
		}
		if pressed {
			x += boolInt(gad.Attribs&1 != 0 || gad.Attribs&2 != 0)
		}
		y := retailTextPenY(gad, r, metric)
		if h.modalFont != nil {
			drawRetailGAFTextClipped(c, h.modalFont, text, x, y, int(r.W), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			continue
		}
		// A button installs map entry `colorf`, which the builder zeroed at
		// open; a label installs its colour word raw, likewise zero because no
		// battle modal writes it [03 R-FONT-01 §6].
		color := byte(0)
		if gad.Kind == gui.KindButton {
			color = h.guiColor(0)
		}
		c.UITextWidth(fallback, text, x, y, -1, color)
	}
}

// drawModalLabelFNT is the label painter's FNT path inside a modal window,
// taken when the label's `fontnumber` matched one of the window's kind-7
// records [03 R-FONT-01 §6]: an authored x of -1 centres the text on the
// window width; attribute bit 4 puts the pen at `gx + w - tw`, bit 2 at
// `gx + trunc(w/2) - trunc(tw/2)`, else `gx`; the pen Y is `gy`; bit 8 draws
// the shadow first, one pixel right and three down, in map entry 0; the text
// then goes through the FNT drawer with no width limit in the label's colour
// word, which is raw palette index 0 here because the builder zeroes it and
// no battle modal writes it. The FNT drawer writes unclipped: retail's private
// window surface bounds it, and no stock modal window authors a font record,
// so nothing reaches this path with a caption wider than its window.
func (h *retailBattleHUD) drawModalLabelFNT(c *client.Client, window *gui.Window, gad gui.Gadget, r gui.Rect, text string, font *formats.FNT) {
	if c == nil || window == nil || font == nil || text == "" {
		return
	}
	tw := client.MeasureText(font, text)
	gx, gy, w := int(r.X), int(r.Y), int(r.W)
	if gad.Rect.RawX == -1 {
		gx = int(window.Rect.X) + (int(window.Rect.W)-tw)/2
	}
	penX := gx
	switch {
	case gad.Attribs&4 != 0:
		penX = gx + w - tw
	case gad.Attribs&2 != 0:
		penX = gx + w/2 - tw/2
	}
	if gad.Attribs&8 != 0 {
		c.UITextWidth(font, text, penX+1, gy+3, -1, h.guiColor(0))
	}
	c.UITextWidth(font, text, penX, gy, -1, 0)
}

// modalArtResampled reports whether a modal gadget's selected frame is
// texture-mapped onto its authored rectangle rather than stamped at the
// translated gadget origin.
//
// Retail resamples in exactly one place: a blank surface (kind 6) whose frame
// is raw. That renderer branches on the frame's `Compressed` byte — an RLE
// frame is stamped once at the gadget origin, a raw frame is texture-mapped
// through the four-corner blitter with the destination spanning
// `(x,y)..(x+w-1,y+h-1)` and the source spanning `(0,0)..(frameW-1,frameH-1)`,
// which is what makes skirmish's raw 32x32 `logos.gaf` team colours fit their
// authored 20x20 records. Every other control stamps: a button's runtime
// dimensions simply become its selected frame's, and a picture box (kind 12)
// "blits its frame" [07 "Retail frontend control activation and raster rules"]
// [07 R-WGT-01 §8].
//
// This used to resample every non-button gadget whose art did not match its
// authored rectangle. That was invented, and it was the in-battle pause menu's
// visible defect: `ARMOPT.GUI`'s `OPTBG` picture box is authored 128x362 while
// the RLE frame behind it is 128x354, so the panel plate was stretched eight
// rows taller than the art and each recess drifted progressively down the rail
// away from the button meant to sit in it — up to seven pixels by `Resume`.
// The drift is authored-size business and has nothing to do with the display
// mode; it was equally wrong at 640x480.
func modalArtResampled(kind gui.Kind, frame *formats.GAFFrame, r gui.Rect) bool {
	if frame == nil || kind != gui.KindSurface || frame.Compressed != 0 {
		return false
	}
	return int32(frame.Width) != r.W || int32(frame.Height) != r.H
}

// drawWindowBackground delegates panel resolution and child-surface painting to
// the common frontend/battle path [07 R-FE-02 §4][07 R-WGT-01 §12].
func (h *retailBattleHUD) drawWindowBackground(c *client.Client, window *gui.Window, page *formats.GAF) {
	if window == nil || window.Rect.W <= 0 || window.Rect.H <= 0 {
		return
	}
	// A window whose background bitmap the cache installed uses that bitmap
	// instead of its authored `panel` entry [07 R-WGT-01 §12][08 R-CAMP-01 §8].
	if window == h.resultWin && h.shell != nil && h.shell.resultBackground != nil {
		return
	}
	drawWindowPanel(c, window, page, h.common, h.guiColor)
}

// modalGadgetRect returns the runtime rectangle installed by the modal
// initializer.
// Stock button selection replaces the authored width/height with the chosen
// frame dimensions (YESORNO's 95x20 choices therefore become 96x20).
func (h *retailBattleHUD) modalGadgetRect(window *gui.Window, index int, page *formats.GAF) gui.Rect {
	if window == nil || index < 0 || index >= len(window.Gadgets) {
		return gui.Rect{}
	}
	r := window.PlacedRect(index)
	gad := window.Gadgets[index]
	if gad.Kind == gui.KindButton {
		if frame := h.modalGadgetFrame(gad, page, false, gad.GrayedOut != 0); frame != nil {
			r.W = int32(frame.Width)
			r.H = int32(frame.Height)
		}
	}
	return r
}

func (h *retailBattleHUD) modalPage(window *gui.Window) *formats.GAF {
	if h != nil && window == h.optionsWin {
		return h.optionsGAF
	}
	return nil
}

func (h *retailBattleHUD) modalButtonAt(window *gui.Window, x, y int32) int {
	if h == nil || window == nil {
		return -1
	}
	page := h.modalPage(window)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 || gad.GrayedOut != 0 {
			continue
		}
		if guiRectContains(h.modalGadgetRect(window, i, page), x, y) {
			return i
		}
	}
	return -1
}

// modalGadgetFrame resolves modal control art through [07 §4]'s three-link
// chain: the gadget's own named entry in the window's own GAF, then the
// side-specific interface GAF, then the built-in fallback (the common GUI
// stock controls, and BUTTONS0 for a button).
//
// The middle link used to be missing here — the one art chain in the shell
// that skipped it, while the window background [07 §4] and the side page
// [07 §6] both already walked `page → intGAF → … → common`. The side GAF is
// resolved through `content.SideDef.IntGAF` [02 §6] and is the same handle the
// three battle panel frames come from, so a modal control the side authors
// (rather than the stock common set) now resolves instead of falling through
// to the generic button plate.
//
// Side-*page* GAFs are still not consulted: they carry unrelated entries with
// colliding names (notably EXIT) and are not part of ARMOPT's retail binding.
func (h *retailBattleHUD) modalGadgetFrame(gad gui.Gadget, page *formats.GAF, pressed, disabled bool) *formats.GAFFrame {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	for _, gaf := range []*formats.GAF{page, h.intGAF, h.common} {
		if gaf == nil {
			continue
		}
		if entry, ok := gaf.Find(name); ok {
			return selectGadgetFrame(entry, gad, pressed, disabled, false)
		}
	}
	if gad.Kind == gui.KindButton && h.common != nil {
		if entry, ok := h.common.Find("BUTTONS0"); ok {
			return selectGadgetFrame(entry, gad, pressed, disabled, true)
		}
	}
	return nil
}
