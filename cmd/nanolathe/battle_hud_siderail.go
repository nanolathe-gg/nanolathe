package main

// The side rail's command and build pages: the product buttons, their
// captions and queue counts, and the command button state machine [07 §6].

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
)

func (h *retailBattleHUD) drawSidePage(c *client.Client, b *battleSession, f *frame.Frame) {
	if b == nil || b.cat == nil {
		return
	}
	window, pageGAF, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return
	}
	if window == nil {
		return
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail window and
		// no gadget rectangle [07 R-HUD-05] (WU-19-223).
		r := window.PlacedRect(i)
		down, stage := int(gad.Status), 0
		panel := h.palettePanels[window]
		if panel != nil {
			down, stage = panel.DownAt(i), panel.StageAt(i)
			gad.ColorF = panel.FlashRow(i)
		}
		command, isCommand := commandGadgetVerdict(gad, f, paged)
		if isCommand && command.hidden {
			// A hidden command button is one the switch deactivates outright —
			// LOAD without the transport bit, BLAST with it [07 R-HUD-03 §6].
			// An inactive gadget paints nothing [07 §3].
			continue
		}
		grey := gad.GrayedOut&1 != 0 || (isCommand && command.grey)
		var frameArt *formats.GAFFrame
		if isCommand {
			frameArt = commandButtonFrame(h.gadgetArtEntry(gad, pageGAF), gad, command.stage, grey, down != 0)
		} else {
			frameArt = h.gadgetButtonFrame(gad, pageGAF, down, stage, grey)
		}
		if frameArt != nil {
			// .GUI controls use the authored rectangle origin; unlike the PANEL
			// shell, their GAF offsets are not applied [07 §4].
			c.UIBlit(frameArt, int(r.X), int(r.Y))
		} else if gad.Kind == gui.KindButton {
			v := retailButtonVerdict(gad, 0, int(gad.ArtFrame), down, command.stage, grey)
			drawGUIBevel(c, r, h.guiColor(v.top), h.guiColor(v.bot), h.guiColor(v.fill))
		}
		// A greyed button's rectangle goes through the rectangle shader after
		// the frame blit, at level -20 — PALETTE.SHD darken row 12 — unless the
		// button carries attribute 0x80 [07 R-HUD-04 §4][03 R-COMP-02 §5]. That
		// settles what §6's "20 palette steps" means: the two are one operator,
		// and the level indexes the SHD rows as level + 32. The cycle branch
		// (attribute 0x100) is excluded because it has its own greyed frame,
		// frames-1, and §3 attaches the darken clause to the other three greyed
		// sub-branches only [07 R-WGT-01 §3].
		if frameArt != nil && retailButtonVerdict(gad, 1, int(gad.ArtFrame), down, command.stage, grey).shade {
			c.UIShadeRect(h.pal, int(r.X), int(r.Y), int(r.W), int(r.H), retailGreyedButtonShade)
		}
		// A command button never draws its caption: the painter reads the art
		// alone [07 R-HUD-03 §6]. Stock content authors these gadgets with an
		// empty label anyway.
		if !isCommand && gad.Kind == gui.KindButton {
			text := retailBattleButtonText(gad, panel, i, stage)
			// The count-label writer runs over the open page's toys after every
			// enqueue or cancel and writes the count **into the toy's own text
			// slot**; the ordinary window text pass then draws that slot with
			// the window's font and the toy's authored rectangle and GUI colour
			// fields [07 R-P0-11 §2]. The toy's `commonattribs` byte selects the
			// format, bit 0x04 tested before bit 0x08:
			//
			//   0x04 — resolve the toy's *name* to a unit definition and write
			//          "+%d" of that product's queued total; a zero total clears
			//          the slot.
			//   0x08 — the MAKENUKE/MAKEANTI stockpile toys: "%d" of the
			//          builder's stockpile count, with " +%d" of the pending
			//          build-weapon total appended.
			//
			// Stock content authors 0x04 on all 480 build-product buttons and
			// 0x08 on the eight stockpile buttons; every other side-panel button
			// authors 0 (asset census over the reference install's guis/*.gui).
			//
			// This replaces a gate of `gad.Text != ""` that appended " N" to an
			// authored caption. Every build-product button authors an empty
			// caption, so the count could never appear on the only toys that
			// carry one.
			if f != nil && gad.CommonAttribs&0x04 != 0 {
				text = productQueueCountLabel(f, gad.Name)
			} else if f != nil && gad.CommonAttribs&0x08 != 0 {
				text = stockpileCountLabel(f)
			}
			if text != "" {
				// The button painter first selects the FNT the toy's
				// `fontnumber` picks from the page's kind-7 records — number
				// 0, which every product button authors, is the page's first
				// record, `armbutt`/`corbutt` — and the common font when none
				// matches. That FNT is only reached on the GAF pen's null-slot
				// fallback below [07 R-WGT-01 §6][03 R-FONT-01 §5].
				h.drawProductButtonCaptionSelected(c, gad, r, window.Rect, text, window.Font(h.fs, gad.FontNumber))
			}
		}
	}
}

// productQueueCountLabel is the bit-0x04 format of the count-label writer
// [07 R-P0-11 §2]: the toy's name resolves to a product definition, and the
// label is "+%d" of the counts summed over **both** the selected builder's
// primary and secondary order lists for that product. A zero total clears the
// label; there is no clamp and no display cap.
//
// It is a local copy of hud.QueueCountLabel rather than a call to it: the two
// now agree (WU-19-135 corrected hud.QueueCountLabel's stale "two separate
// numbers" reading to this same one-sum "+%d" shape), but hud.QueueCountLabel
// takes a []frame.OrderQueueView slice rather than the committed frame this
// composer already holds, and internal/hud is not this unit's package to
// re-plumb a caller into.
func productQueueCountLabel(f *frame.Frame, product string) string {
	key := content.CanonicalKey(product)
	if f == nil || key == "" || f.CommandPage.Builder == 0 {
		return ""
	}
	// The writer is handed the single selected builder, so only that unit's
	// queues are counted [07 R-P0-11 §1]. Retail additionally admits only nodes
	// carrying the counted-production flag; internal/orders models no such flag
	// yet, and only build nodes carry a product key at all, so matching on the
	// product is equivalent over the queues nanolathe produces today.
	total := uint32(0)
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != f.CommandPage.Builder {
			continue
		}
		for _, o := range q.Primary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
		for _, o := range q.Secondary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("+%d", total)
}

// stockpileCountLabel is the bit-0x08 format of the count-label writer
// [07 R-P0-11 §2], the one the eight MAKENUKE/MAKEANTI toys author. Its two
// numbers are two different quantities, not one total split in two: "%d" of a
// byte on the builder unit — the held stockpile count, published on the
// committed command page — and then " +%d" of the count query run with id 0,
// which is the pending BUILDWEAPON total the stockpile button itself enqueues.
// So the toy reads "held +pending".
//
// A quantity of zero clears its own half, which is the writer's stated rule for
// a zero total and is what §2 says explicitly of the held byte ("printing
// nothing when that byte is zero"); a toy with nothing held and nothing pending
// therefore draws no caption at all, like an empty product slot.
func stockpileCountLabel(f *frame.Frame) string {
	if f == nil || f.CommandPage.Builder == 0 {
		return ""
	}
	held := f.CommandPage.Stockpile
	// The second number is the BUILDWEAPON queue, not the secondary order list
	// as a whole [07 R-P0-11 §2]. Stockpile nodes live in the page unit's
	// secondary segment [06 §11.1], and the node's own count is what the query
	// sums.
	pending := uint32(0)
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != f.CommandPage.Builder {
			continue
		}
		for _, o := range q.Secondary {
			if o.Kind == "BuildWeapon" {
				pending += o.BuildCount
			}
		}
	}
	label := ""
	if held != 0 {
		label = fmt.Sprintf("%d", held)
	}
	if pending != 0 {
		label += fmt.Sprintf(" +%d", pending)
	}
	return strings.TrimSpace(label)
}

// queueCountLabelPen is the retail button painter's pen arithmetic
// [03 R-FONT-01 §6] for a button caption, applied to the queue-count text
// written into a build-product toy's own text slot [07 R-P0-11 §2]. `s` is 1
// when the gadget's `stages` field is non-zero. The vertical pen is
// `gy + trunc((h-1-metric)/2) + s` for the left/right/centre attributes, but
// the build-attribute variant (attribute bit 0x20) keeps the centred
// horizontal pen and instead anchors near the bottom edge:
// `bottom - 4 - metric + s`. `metric` is the line metric of the family the
// caption is drawn with — the capital-I frame height plus two for a GAF font
// (the case here; see drawProductButtonCaption), or the FNT header height
// field on the GAF pen's null-slot fallback.
//
// Build-product buttons author attribute 0x20 and no left/right/centre bit
// (asset census over the reference install's guis/*.gui files, the same
// census that backs [07 R-P0-11 §2]'s refinement: all 480 author
// `attribs = 32` alongside `commonattribs = 4`), so the count lands at the
// bottom-centre of the button, not the vertically-centred left inset a
// left-aligned button would use.
func queueCountLabelPen(gad gui.Gadget, r gui.Rect, textWidth, metric int) (x, y int) {
	x, y, _, _ = retailButtonCaptionPen(gad, r, textWidth, metric)
	return x, y
}

// productButtonCaptionLayout picks the family and pen the retail button
// painter would use for a side-page button caption — in practice the queue
// count the count-label writer left in the toy's own text slot
// [07 R-P0-11 §2].
//
// Family. Every text call in the button painter goes through the GAF-font pen
// with mode 0, so a button caption is drawn with the window's *current GAF
// font*; the pen reaches the FNT drawer only when that slot is null, and then
// with the width limit dropped (maxW = -1) [03 R-FONT-01 §6]. This is the
// correction WU-19-221 makes: the count was drawn with the side font's FNT,
// the wrong family.
//
// Which GAF slot. Startup hands the GUI window slot 0 = anims/hattfont12.gaf
// and slot 1 = anims/hattfont11.gaf; the button painter switches the current
// slot to 1 only for a button carrying the small-font attribute bit 0x8000,
// and restores slot 0 when it is done, so slot 0 is what a button without
// that bit draws with [03 R-FONT-01 §5]. No count-bearing product button
// carries it: an asset census over the reference install's guis/*.gui files —
// the same census that backs [07 R-P0-11 §2]'s refinement — finds all 488
// count-bearing buttons (kind 1 with `commonattribs` 4 or 8) authoring
// `attribs = 32` and a 64x64 rectangle, and none of them 0x8000. The count is
// therefore hattfont12, the GAF font this HUD already loads for its other
// retail text.
//
// (The sentence in [03 R-FONT-01 §5] that has "the build-card count label"
// switching to slot 1 for its duration describes the routine RWU-19-34
// re-identified as the kind-13 score-bar painter — the same mis-subject that
// correction fixed in [03 R-FONT-01 §6], where it also settles that the
// side-page build count "is a button caption and never passes through this
// routine". Reported for a doc correction; the button rule above is what the
// count follows.)
//
// Colour. The GAF pen colours from the frame bytes with mode 0 and never
// reads the foreground the painter installs, so the button rule's map entry
// `colorf` (map entry 0 unless mid-flash, and nothing on this page ever
// writes the flash word) only reaches pixels on the FNT fallback
// [03 R-FONT-01 §6].
//
// `selected` is the FNT the button's `fontnumber` picked from the page's own
// kind-7 records — nil when none matched, which leaves the common font active.
// It is the active FNT the null-slot fallback draws with; with a GAF font in
// the slot it is never consulted [07 R-WGT-01 §6][03 R-FONT-01 §5].
func (h *retailBattleHUD) productButtonCaptionLayout(gad gui.Gadget, r gui.Rect, text string) (x, y int, font *formats.GAFEntry) {
	x, y, font, _ = h.productButtonCaptionLayoutSelected(gad, r, text, nil)
	return x, y, font
}

// productButtonCaptionLayoutSelected is productButtonCaptionLayout with the
// button's selected FNT; the fourth result is the FNT the null-slot fallback
// draws with, nil when a GAF font is in the slot or no FNT is available.
func (h *retailBattleHUD) productButtonCaptionLayoutSelected(gad gui.Gadget, r gui.Rect, text string, selected *formats.FNT) (x, y int, font *formats.GAFEntry, fallback *formats.FNT) {
	if font = h.buttonCaptionGAFFont(); font != nil {
		x, y, _, _ = retailButtonCaptionPen(gad, r, retailGAFTextWidth(font, text), retailGAFTextHeight(font))
		return x, y, font, nil
	}
	fallback = selected
	if fallback == nil {
		fallback = h.guiFont
	}
	if fallback == nil {
		return 0, 0, nil, nil
	}
	x, y, _, _ = retailButtonCaptionPen(gad, r, client.MeasureText(fallback, text), int(fallback.Height))
	return x, y, nil, fallback
}

// buttonCaptionGAFFont is the window's current GAF-font slot for a button
// caption: slot 0, anims/hattfont12.gaf, which battle entry already loads
// [03 R-FONT-01 §5]. A missing font file leaves the slot null rather than
// failing battle entry, which is the pen's FNT-fallback case.
func (h *retailBattleHUD) buttonCaptionGAFFont() *formats.GAFEntry {
	if h == nil || h.modalFont == nil || len(h.modalFont.Frames) == 0 {
		return nil
	}
	return h.modalFont
}

// drawProductButtonCaption draws that caption where productButtonCaptionLayout
// puts it. The GAF pen blits each glyph at `penX - XOffset, penY -
// normalizedYOffset` (the load-time baseline normalization of [07 §4]) and
// stops on the first glyph wider than the remaining width, the painter's
// `maxW = w` [03 R-FONT-01 §6]. On the null-slot fallback the FNT drawer is
// called with the width limit dropped, which is what maxWidth < 0 means to
// UITextWidth.
func (h *retailBattleHUD) drawProductButtonCaption(c *client.Client, gad gui.Gadget, r gui.Rect, text string) {
	w, height := c.Size()
	h.drawProductButtonCaptionSelected(c, gad, r, gui.Rect{W: int32(w), H: int32(height)}, text, nil)
}

// drawProductButtonCaptionSelected is drawProductButtonCaption with the FNT
// the button's `fontnumber` selected from the page's kind-7 records (nil when
// none matched): the active FNT the null-slot fallback draws with
// [07 R-WGT-01 §6][03 R-FONT-01 §5].
func (h *retailBattleHUD) drawProductButtonCaptionSelected(c *client.Client, gad gui.Gadget, r, clip gui.Rect, text string, selected *formats.FNT) {
	_, _, font, fallback := h.productButtonCaptionLayoutSelected(gad, r, text, selected)
	var width, metric int
	if font != nil {
		width, metric = retailGAFTextWidth(font, text), retailGAFTextHeight(font)
	} else if fallback != nil {
		width, metric = client.MeasureText(fallback, text), int(fallback.Height)
	} else {
		return
	}
	h.drawBattleButtonCaption(c, clip, gad, r, text, selected, width, metric, gad.ColorF)
}

// commandPageIsPaged reports the selected builder's page-shown bit (status bit
// 22) off the committed frame [07 §9]. BUILD stages from that bit and ORDERS
// from its inverse [07 R-HUD-03 §6].
//
// Every caller derives it again from the frame it is acting on rather than
// keeping a copy, so the painter, the pointer pass and the click path cannot
// drift apart and nothing about a gadget is latched [I6].
func commandPageIsPaged(f *frame.Frame) bool {
	if f == nil || f.CommandPage.Builder == 0 {
		return false
	}
	builder, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found {
		return false
	}
	return hud.IsPaged(builder.Flags)
}

// buildButtonPage is the build page a BUILD click selects. Selecting page 0
// clears the page-shown bit and leaves the page field alone [07 §9], so that
// field still names the build page the builder was last on and setting the bit
// again brings that page back.
//
// No click writes the page field: it is seeded at **unit creation**, page 1
// with the paged bit set when the definition's page-count byte is at least 2
// and both cleared otherwise [07 R-HUD-04 §4 "First build page"]. The seed
// lives in internal/units' allocator initializer, which is why the zero-field
// case this function used to guess about no longer arises for a multi-page
// builder. The clamp below stays as a bounds guard for a single-page or
// malformed record, not as a stand-in for the missing producer.
func buildButtonPage(f *frame.Frame) int {
	if f == nil || f.CommandPage.Builder == 0 {
		return 0
	}
	remembered := 0
	if builder, found := snapshotUnitByHandle(f, f.CommandPage.Builder); found {
		remembered = hud.RememberedPage(builder.Flags)
	}
	if remembered <= 0 || remembered >= int(f.CommandPage.PageCount) {
		return 1
	}
	return remembered
}

// commandGadgetVerdict resolves one authored gadget against the command-button
// stage and grey table [07 R-HUD-03 §6]. It is the single decision the painter,
// the pointer pass and the click path all consult, so what a button looks like
// and whether it responds can never disagree. The second result is false for a
// gadget the table does not name — a product slot, NEXT/PREV, a label.
func commandGadgetVerdict(gad gui.Gadget, f *frame.Frame, paged bool) (commandButtonVerdict, bool) {
	return commandButtonState(commandButtonName(gad.Name), f, paged)
}

// commandButtonNames are the gadget names of the command-button stage and grey
// table [07 R-HUD-03 §6]. Every one of them is authored with the side's
// nameprefix in stock content — ARMONOFF, CORMOVEORD, ARMUNLOAD — the same
// shape §6 spells out for "%sPREV"/"%sNEXT", so a gadget is matched by the
// table name that is a suffix of its own.
var commandButtonNames = [...]string{
	"BUILD", "ORDERS", "CLOAK", "ONOFF", "MOVEORD", "FIREORD",
	"MOVE", "STOP", "ATTACK", "DEFEND", "PATROL", "RECLAIM", "CAPTURE", "REPAIR",
	"LOAD", "UNLOAD", "BLAST",
}

// ordersButtonCue and buildButtonCue are the two stage buttons' own cues,
// named by the click handler itself rather than by the page-switch routine
// [07 R-HUD-04 §5]. They are `allsound.tdf` aliases like every other interface
// cue; a mount that does not author one leaves the click silent.
const (
	ordersButtonCue = "ordersbutton"
	buildButtonCue  = "buildbutton"
)

// commandButtonName returns the table row a gadget belongs to, or "" when it is
// not a command button. The longest matching suffix wins, which is what keeps
// ARMUNLOAD out of the LOAD row and ARMMOVEORD out of the MOVE row.
func commandButtonName(gadget string) string {
	upper := strings.ToUpper(gadget)
	best := ""
	for _, name := range commandButtonNames {
		if len(name) > len(best) && strings.HasSuffix(upper, name) {
			best = name
		}
	}
	return best
}

// commandButtonVerdict is one row of the stage and grey table, evaluated
// against the committed frame [07 R-HUD-03 §6]. It is derived per draw and
// never written back to the gadget, so nothing latches [I6].
type commandButtonVerdict struct {
	stage  int
	grey   bool
	hidden bool
}

// commandButtonState evaluates the stage and grey table of [07 R-HUD-03 §6] for
// one command button. paged is the selected builder's page-shown bit. The
// second result is false when the name is not a command button at all.
//
// The stance and pair values come from the committed selection aggregate the
// command-window switch computes [07 §9][07 R-HUD-03 §13]. A stance field greys
// at 4 and a cloak/on-off pair at 3: both are the not-applicable value their
// fold starts from, so a selection carrying nothing that accepts the command
// greys its button. A disagreeing selection folds to one below that instead — 3
// and 2 — which stages the generic orders plate rather than greying.
//
// The capability folds are a disjunction, so a button greys only when no
// selected unit can perform its command [07 R-HUD-03 §13].
func commandButtonState(name string, f *frame.Frame, paged bool) (commandButtonVerdict, bool) {
	if name == "" || f == nil {
		return commandButtonVerdict{}, false
	}
	page := f.CommandPage
	// BUILD and ORDERS are the two halves of the page-shown bit; both grey when
	// there is no builder or the builder has no pages.
	noPages := page.Builder == 0 || page.PageCount == 0
	switch name {
	case "BUILD":
		return commandButtonVerdict{stage: boolStage(paged), grey: noPages}, true
	case "ORDERS":
		return commandButtonVerdict{stage: boolStage(!paged), grey: noPages}, true
	case "CLOAK":
		return commandButtonVerdict{stage: int(page.CloakState), grey: page.CloakState == 3}, true
	case "ONOFF":
		return commandButtonVerdict{stage: int(page.OnOffState), grey: page.OnOffState == 3}, true
	case "MOVEORD":
		return commandButtonVerdict{stage: int(page.MoveStance), grey: page.MoveStance == 4}, true
	case "FIREORD":
		return commandButtonVerdict{stage: int(page.FireStance), grey: page.FireStance == 4}, true
	// The eight capability buttons carry no stage; each greys when the
	// selection's aggregate bit for its command is clear.
	case "MOVE":
		return commandButtonVerdict{grey: !page.CanMove}, true
	case "STOP":
		return commandButtonVerdict{grey: !page.CanStop}, true
	case "ATTACK":
		return commandButtonVerdict{grey: !page.CanAttack}, true
	case "DEFEND":
		return commandButtonVerdict{grey: !page.CanDefend}, true
	case "PATROL":
		return commandButtonVerdict{grey: !page.CanPatrol}, true
	case "RECLAIM":
		return commandButtonVerdict{grey: !page.CanReclaim}, true
	case "CAPTURE":
		return commandButtonVerdict{grey: !page.CanCapture}, true
	case "REPAIR":
		return commandButtonVerdict{grey: !page.CanRepair}, true
	// The transport trio is the one row that hides rather than greys: without
	// the transport bit LOAD disappears and UNLOAD greys, with it BLAST
	// disappears. BLAST otherwise greys unless the selection carries the blast
	// bit.
	case "LOAD":
		return commandButtonVerdict{hidden: !page.IsTransport}, true
	case "UNLOAD":
		return commandButtonVerdict{grey: !page.IsTransport}, true
	case "BLAST":
		if page.IsTransport {
			return commandButtonVerdict{hidden: true}, true
		}
		return commandButtonVerdict{grey: !page.CanBlast}, true
	}
	return commandButtonVerdict{}, false
}

func boolStage(v bool) int {
	if v {
		return 1
	}
	return 0
}

// guiAttribCycle is the cycle-button attribute bit [fmt gui][07 R-WGT-01 §3].
// Every staged command button in stock content carries it: ARMONOFF, ARMCLOAK,
// ARMMOVEORD and ARMFIREORD are all authored with it.
const guiAttribCycle = 0x100

// guiAttribCheckbox is attribute 0x80, the checkbox fallback bit. It is also
// the one exemption from the greyed-button darkening [07 R-WGT-01 §3]
// [07 R-HUD-04 §4].
const guiAttribCheckbox = 0x80

// retailGreyedButtonShade is the rectangle-shader level a greyed button's
// rectangle is darkened at: -20, which indexes PALETTE.SHD row 12 as
// level + 32 [07 R-HUD-04 §4][03 R-COMP-02 §5].
const retailGreyedButtonShade = -20

func cycleButton(gad gui.Gadget) bool { return gad.Attribs&guiAttribCycle != 0 }

// commandButtonFrame is the button painter's frame choice
// [07 R-WGT-01 §3 "the painter's frame choice"], which completes and corrects
// [07 R-HUD-03 §6]. The authored `status` field is the button's **down-state
// word**, not the frame a stage counts from; the frame *base* comes from art
// resolution and is frame 0 on the named-art path every command button takes.
// §6's reading of `status` as a base is superseded [07 R-HUD-04 §4].
//
// With `down` the down-state word and `stage` the current-stage byte:
//
//   - greyed and a cycle button (0x100) -> frames - 1;
//   - greyed otherwise                  -> base + min(state + 2, frames - 1);
//   - a cycle button                     -> base + its state, with no held look;
//   - down set on staged art             -> frames - 2, the pressed look;
//   - down set otherwise                 -> base + down;
//   - otherwise                          -> base + the state.
//
// `state` is the runtime index the command-button table supplies: the
// down-state word for a cycle button and the current-stage byte for staged
// art, which is the one value [07 R-HUD-03 §6] calls the stage.
//
// A press sets the down-state word to 1 while the button is captured — except
// on a cycle button, which has no held look at all: a press advances its state
// and fires immediately, so it keeps showing its stage while the mouse is down.
// The greyed darkening is applied by the caller, not folded into the frame.
func commandButtonFrame(entry *formats.GAFEntry, gad gui.Gadget, stage int, grey, pressed bool) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	if cycleButton(gad) && !grey {
		// Command-page cycle state is supplied by the committed aggregate, not
		// the generic gadget's down word. It retains that state while captured.
		idx := max(0, min(stage, len(entry.Frames)-1))
		return entry.Frames[idx].Frame
	}
	down := int(gad.Status)
	if pressed && !cycleButton(gad) {
		down = 1
	}
	frame, _ := retailButtonFrameFromEntry(entry, gad, 0, down, stage, grey)
	return frame
}
