package main

// The unit information screen `UNITINFOx.GUI` [07 R-HUD-03 §8].
//
// F1 without Shift opens it for the hovered build button's product, or for
// the hovered world unit; the authored file carries the frame, the picture
// surface and the DONE button, and the runtime appends the eight label rows
// and their value column. Everything drawn here is either an authored gadget
// rectangle or one of the fixed positions the section lists; nothing about
// the layout is ours.
//
// Screen state is a process singleton for the same reason the save/load
// dialog's is (see loadgame.go): there is exactly one battle shell, and the
// battleSession struct is owned by another unit's file.

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// retailUnitInfoGUI is the authored file the screen is built from
// [07 R-HUD-03 §8]. The stock install spells it `Unitinfox.GUI`; the mount is
// case-insensitive.
const retailUnitInfoGUI = "guis/unitinfox.gui"

// unitInfoTicksPerSecond is the runtime's ticks-per-second word the three
// mobile statistics are scaled by [07 R-HUD-03 §8].
const unitInfoTicksPerSecond = 30.0

// unitInfoMetreScale is retail's world-unit-to-metre convention for this
// screen only; no other reader uses it [07 R-HUD-03 §8].
const unitInfoMetreScale = 0.4

// unitInfoDegreeScale is 360/65536, the literal double the turn rate is
// scaled by [07 R-HUD-03 §8].
const unitInfoDegreeScale = 0.0054931640625

// unitInfoNotApplicable is the value a building's three statistic rows carry
// [07 R-HUD-03 §8].
const unitInfoNotApplicable = "N/A"

// unitInfoHeaderValue is the value-column string the two header rows hold. It
// occupies its row without printing anything [07 R-HUD-03 §8].
const unitInfoHeaderValue = "\n"

// unitInfoLabel is one appended label gadget: its window-local position and
// its localized caption [07 R-HUD-03 §8].
type unitInfoLabel struct {
	X, Y int
	Text string
}

// unitInfoLabels are the eight appended labels, in the order the screen
// appends them, at the fixed positions [07 R-HUD-03 §8] gives.
var unitInfoLabels = [8]unitInfoLabel{
	{130, 32, "Cost"},
	{140, 47, "Energy"},
	{140, 62, "Metal"},
	{140, 77, "Build Time"},
	{130, 92, "Statistics"},
	{140, 107, "Max Velocity"},
	{140, 122, "Acceleration"},
	{140, 137, "Turn Rate"},
}

// unitInfoValueColumn is the value column's window-local x [07 R-HUD-03 §8].
const unitInfoValueColumn = 240

// unitInfoNameLimit is the 128-byte limit argument the NAME gadget's text is
// written with [07 R-HUD-03 §8].
const unitInfoNameLimit = 128

// unitInfoScreen is the open screen: the authored window, the subject
// definition, its picture and the eight value strings.
type unitInfoScreen struct {
	window *gui.Window
	panel  *ui.Panel
	def    *content.UnitDef
	pic    *formats.PCX
	values [8]string
}

// unitInfoUI is the one open screen, or nil.
var unitInfoUI *unitInfoScreen

// unitInfoOpen reports whether the screen is up.
func unitInfoOpen() bool { return unitInfoUI != nil }

// closeUnitInfo is what `DONE` does, and what the command-panel page close
// does to every window sitting above the command window [07 R-HUD-04 §3].
//
// It is **not** what Enter or Escape do. The authored file does name `DONE` as
// its `crdefault`, `escdefault` and `defaultfocus`, but those keys are read by
// the window key matrix, and the matrix never runs for a battle window: see
// the keyboard-ownership note below.
func closeUnitInfo() bool {
	if unitInfoUI == nil {
		return false
	}
	unitInfoUI = nil
	return true
}

// This battle child window owns no keyboard at all, which is why there is no
// consume-keys seam here any more [07 §3][07 R-WGT-01 §1 step 3][07 R-WGT-01
// §2].
//
// The GUI pass branches on the **window's token-mode word**. Non-zero: it pops
// one queued token, and the window has it. Zero: it *peeks* — the token stays
// in the queue for the battle hotkey dispatcher that runs after the GUI pass —
// and tokens `0xE2..0xEB` (F1..F10) are additionally replaced by zero for that
// pass alone, so the window's own gadgets never see a function key. Only the
// front-end shell and the in-battle options root set the word; every other
// battle window, this one included, leaves it zero [07 R-WGT-02 §2].
//
// The window key matrix — the Enter/Escape/Tab/Space rows that would fire
// `crdefault` and `escdefault` — is gated on the same non-zero token mode, so
// it never runs for a battle window either: "battle windows leave it clear, so
// in battle none of this applies and tokens reach the hotkey dispatcher after
// the gadget loop" [07 R-WGT-01 §2].
//
// **Correction (WU-19-174).** This file used to swallow every token while the
// screen was open and fire `DONE` on Escape and on Enter, citing the matrix.
// That inverted the contract twice: no battle hotkey ran underneath the screen
// (F1, F2, Tab, the group keys, the speed keys), and Enter closed the screen
// instead of opening chat [07 R-CAM-01 §2]. What actually closes the screen is
// the `DONE` click and the command-panel page close, which every selection
// change runs and which pops each window above the command window
// [07 R-HUD-04 §3] — so Escape still closes it, by deselecting first.

// openUnitInfo is F1's whole behavior. Retail opens the screen when the
// options window is not open; the subject is the hovered gadget's product
// when a gadget is hovered, otherwise the hovered world unit when it is alive
// and passes the visibility predicate, otherwise nothing opens
// [07 R-HUD-03 §8][07 §2].
//
// **Correction (WU-19-174).** This was a *toggle*: F1 with the screen open
// closed it. No section gives F1 a close arm — [R-CAM-01 §2]'s F1 row and
// [R-HUD-03 §8] are both "open", with a three-way subject resolution whose
// third branch opens nothing. The toggle only looked harmless while the window
// swallowed F1; with the token reaching the dispatcher as retail's does, the
// close arm would have made F1 a close. F1 over the open screen now resolves
// no subject — the modal covering the pointer gates world picking out
// [07 §3] — so the screen simply stands, which is retail's outcome.
func (b *battleSession) openUnitInfo() {
	b.openUnitInfoScreen()
}

// openUnitInfoScreen resolves the subject and builds the screen. A subject
// that does not resolve opens nothing, which is what retail's third branch
// does [07 R-HUD-03 §8].
func (b *battleSession) openUnitInfoScreen() {
	if b == nil || b.cat == nil || b.fs == nil {
		return
	}
	// "F1 opens the screen when the options window is not open"
	// [07 R-HUD-03 §8].
	if state := b.battleState(); state != nil && state.Modal() != ui.BattleModalClosed {
		return
	}
	def, ok := b.unitInfoSubject()
	if !ok || def == nil {
		return
	}
	window, err := gui.LoadWithTranslation(b.fs, retailUnitInfoGUI, hudCaptionTranslator(b.hud))
	if err != nil || window == nil {
		if b.hud != nil {
			b.hud.assetErr = hudAssetError(b.fs, retailUnitInfoGUI, "the authored unit information window", err)
		}
		return
	}
	if b.hud != nil {
		b.hud.installWindow(window, nil)
	}
	screen := &unitInfoScreen{window: window, panel: ui.NewPanel(window), def: def, values: unitInfoValues(def)}
	// The `HOTR` gadget receives the picture `unitpics/<internal name>.PCX`
	// [07 R-HUD-03 §8]. Missing art leaves the surface empty rather than
	// refusing the screen.
	logical := "unitpics/" + strings.ToLower(strings.TrimSpace(def.UnitName)) + ".pcx"
	if pic, err := formats.LoadPCXFile(b.fs, logical); err == nil {
		screen.pic = pic
	} else {
		hudAssetWarning(b.fs, logical, "the unit information picture", err)
	}
	unitInfoUI = screen
	flushWindowTokens(b.cl)
}

// serviceUnitInfoKeyboard performs the battle-child peek pass before battle
// hotkeys. Its token mode stays zero: only an admitted accelerator pops a
// token, while Enter and Escape never reach the key matrix [07 R-WGT-01 §§1-3].
func (b *battleSession) serviceUnitInfoKeyboard(in *input.State) {
	if b == nil || in == nil || unitInfoUI == nil || unitInfoUI.panel == nil {
		return
	}
	frame := ui.WidgetFrame{Tokens: in.PeekTokens(), DisableQuickKeys: b.developer.quickkeysDisabled}
	if in.Kbd != nil {
		frame.AltHeld = in.Kbd.KeyHeld(input.KeyAlt)
	}
	result := unitInfoUI.panel.ServiceFrame(frame, ui.WidgetHooks{})
	in.DiscardTokens(result.ConsumedTokens)
	if result.Fired && result.FiredIndex == unitInfoUI.doneIndex() {
		b.developer.quickkeysDisabled = false
		closeUnitInfo()
	}
}

// unitInfoSubject is the section's two-branch subject resolution: the hovered
// gadget's product when the hovered-gadget index is not -1, resolved by the
// same name lookup as the build card; otherwise the hovered world unit when
// it is alive and passes the visibility predicate [07 R-HUD-03 §8].
func (b *battleSession) unitInfoSubject() (*content.UnitDef, bool) {
	if b == nil || b.cat == nil {
		return nil, false
	}
	if b.hud != nil {
		if index, name := b.hud.hoveredGadgetSource(); index != hud.NoGadget && name != "" {
			def, ok := b.cat.Unit(content.CanonicalKey(name))
			return def, ok && def != nil
		}
	}
	f, ok := b.currentSnapshot()
	if !ok || b.footerHoverUnit == 0 {
		return nil, false
	}
	view, found := snapshotUnitByHandle(f, b.footerHoverUnit)
	if !found || view.Slot == 0 || view.DefName == "" {
		return nil, false
	}
	if b.sess != nil && !client.SnapshotVisible(f, view, f.ViewingPlayer) {
		return nil, false
	}
	def, ok := b.cat.Unit(view.DefName)
	return def, ok && def != nil
}

// unitInfoValues builds the value column: the three costs, then three
// localized `N/A` for a building (`bmcode` 0) or the three scaled statistics
// for a mobile unit. The two header rows hold the `"\n"` string
// [07 R-HUD-03 §8].
//
// The scale arithmetic is presentation text formatting only; no authoritative
// value is produced here [I2].
func unitInfoValues(def *content.UnitDef) [8]string {
	var out [8]string
	out[0] = unitInfoHeaderValue
	out[4] = unitInfoHeaderValue
	if def == nil {
		return out
	}
	out[1] = fmt.Sprintf("%d", numeric.TruncateFloat64ToLow32(float64(def.BuildCostEnergy)))
	out[2] = fmt.Sprintf("%d", numeric.TruncateFloat64ToLow32(float64(def.BuildCostMetal)))
	out[3] = fmt.Sprintf("%d", def.BuildTime)
	if def.BMCode == 0 {
		out[5], out[6], out[7] = unitInfoNotApplicable, unitInfoNotApplicable, unitInfoNotApplicable
		return out
	}
	// The first two products narrow to single after the 2^-16 scale and are
	// then widened; the unit words are localized [07 R-HUD-03 §8].
	velocity := float64(float32(def.MaxVelocity)/65536) * unitInfoTicksPerSecond * unitInfoMetreScale
	accel := float64(float32(def.Acceleration)/65536) * unitInfoTicksPerSecond * unitInfoMetreScale
	turn := float64(def.TurnRate) * unitInfoTicksPerSecond * unitInfoDegreeScale
	out[5] = fmt.Sprintf("%.1f m/s", velocity)
	out[6] = fmt.Sprintf("%.2f m/s/s", accel)
	out[7] = fmt.Sprintf("%.0f deg/s", turn)
	return out
}

// unitInfoDoneIndex returns the authored `DONE` gadget's index, or -1.
func (s *unitInfoScreen) doneIndex() int {
	if s == nil || s.window == nil {
		return -1
	}
	i := s.window.GadgetIndex("DONE")
	if i >= 0 && s.window.Gadgets[i].Kind == gui.KindButton {
		return i
	}
	return -1
}

// unitInfoCovers reports whether the open screen covers a pointer position.
// A modal that covers the pointer gates world picking out [07 §3].
func unitInfoCovers(x, y int32) bool {
	if unitInfoUI == nil || unitInfoUI.window == nil {
		return false
	}
	return guiRectContains(unitInfoUI.window.Rect, x, y)
}

// unitInfoDoneCaptures reports whether a pointer position lies in the open
// screen's `DONE` button. Exactly one gadget holds the pointer capture: a press
// inside a gadget takes it, and only a release inside that same gadget fires
// the control [07 R-WGT-01 §1 "Capture"]. Both endpoints of a click are tested
// with this, so a drag that starts on the unit picture and ends on `DONE`
// identifies two different gadgets and fires neither.
func (h *retailBattleHUD) unitInfoDoneCaptures(x, y int32) bool {
	if h == nil || unitInfoUI == nil || unitInfoUI.window == nil {
		return false
	}
	index := unitInfoUI.doneIndex()
	if index < 0 {
		return false
	}
	return guiRectContains(h.modalGadgetRect(unitInfoUI.window, index, nil), x, y)
}

// unitInfoConsumeClick services one release inside the open screen: a release
// on `DONE` closes it, and any other release inside the window is consumed by
// the window rather than reaching the world [07 §3][07 R-WGT-01 §1].
//
// The press that paired with this release is identified by `sameButton`, which
// refuses a pair whose two endpoints are not the same gadget, so reaching the
// `DONE` arm here means the press was captured by `DONE` as well.
func (h *retailBattleHUD) unitInfoConsumeClick(x, y int32) bool {
	if unitInfoUI == nil || unitInfoUI.window == nil {
		return false
	}
	if !unitInfoCovers(x, y) {
		return false
	}
	if h.unitInfoDoneCaptures(x, y) {
		closeUnitInfo()
		return true
	}
	return true
}

// drawUnitInfo composes the authored window, the picture, the name and the
// eight appended label rows with their value column [07 R-HUD-03 §8].
func (h *retailBattleHUD) drawUnitInfo(c *client.Client) {
	if h == nil || c == nil || unitInfoUI == nil || unitInfoUI.window == nil {
		return
	}
	screen := unitInfoUI
	window := screen.window
	// The `NAME` gadget's text is the definition's display name, written with
	// a 128-byte limit argument [07 R-HUD-03 §8]. The authored label carries
	// no text of its own, so the runtime binds it before the window draws.
	if i := window.GadgetIndex("NAME"); i >= 0 {
		name := screen.def.Name
		if len(name) > unitInfoNameLimit {
			name = name[:unitInfoNameLimit]
		}
		window.Gadgets[i].Text = name
	}
	h.drawGUIWindow(c, window, nil, "")
	h.drawUnitInfoPicture(c, screen)
	h.drawUnitInfoRows(c, screen)
}

// drawUnitInfoPicture stamps `unitpics/<internal name>.PCX` into the authored
// `HOTR` surface rectangle [07 R-HUD-03 §8].
func (h *retailBattleHUD) drawUnitInfoPicture(c *client.Client, screen *unitInfoScreen) {
	if screen.pic == nil {
		return
	}
	if i := screen.window.GadgetIndex("HOTR"); i >= 0 {
		r := screen.window.PlacedRect(i)
		// The F1 loader discards the PCX trailer palette; the picture is copied
		// as indices into the active display palette [07 R-HUD-03 §8][fmt pcx].
		c.UIBlitPCXClipped(screen.pic, int(r.X), int(r.Y), int(r.X), int(r.Y), int(r.W), int(r.H))
	}
}

// drawUnitInfoRows writes the eight labels and the value column. The value
// column's `"\n"` header entries occupy their row and print nothing
// [07 R-HUD-03 §8].
func (h *retailBattleHUD) drawUnitInfoRows(c *client.Client, screen *unitInfoScreen) {
	if h.modalFont == nil {
		return
	}
	clip := screen.window.Rect
	originX, originY := int(screen.window.Rect.X), int(screen.window.Rect.Y)
	for i, label := range unitInfoLabels {
		drawRetailGAFTextClipped(c, h.modalFont, label.Text,
			originX+label.X, originY+label.Y, -1,
			int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
		value := screen.values[i]
		if value == unitInfoHeaderValue || value == "" {
			continue
		}
		drawRetailGAFTextClipped(c, h.modalFont, value,
			originX+unitInfoValueColumn, originY+label.Y, -1,
			int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
	}
}
