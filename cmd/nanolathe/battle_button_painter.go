package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// retailButtonRenderVerdict is the button painter's state reduction. Art is
// resolved by the window builder; the mutable down and stage words only choose
// within that installed entry [07 R-WGT-01 §3].
type retailButtonRenderVerdict struct {
	frame int
	top   byte
	bot   byte
	fill  byte
	shade bool
}

func retailButtonVerdict(gad gui.Gadget, frames, base, down, stage int, grey bool) retailButtonRenderVerdict {
	v := retailButtonRenderVerdict{frame: -1, top: 17, bot: 0, fill: 20}
	if grey {
		v.top, v.bot, v.fill = 0, 19, 19
	}
	if !grey && down != 0 {
		v.top, v.bot = 0, 17
	}
	if frames <= 0 {
		return v
	}
	last := frames - 1
	switch {
	case grey && cycleButton(gad):
		v.frame = last
	case grey && gad.Attribs&0x1800 != 0:
		v.frame = base
	case grey && gad.Stages != 0:
		v.frame = stage
	case grey:
		v.frame = base + min(down+2, last)
	case gad.Stages != 0 && down != 0 && int(gad.Stages) < frames:
		v.frame = last - 1
	case gad.Stages != 0:
		v.frame = stage
	case down != 0:
		v.frame = base + down
	default:
		v.frame = base
	}
	if v.frame < 0 {
		v.frame = 0
	}
	if v.frame > last {
		v.frame = last
	}
	// Checkbox and cycle controls have their dedicated grey-art branches; the
	// remaining art buttons darken after the chosen frame is blitted.
	v.shade = grey && gad.Attribs&guiAttribCheckbox == 0 && !cycleButton(gad)
	return v
}

func retailButtonFrameFromEntry(entry *formats.GAFEntry, gad gui.Gadget, base, down, stage int, grey bool) (*formats.GAFFrame, retailButtonRenderVerdict) {
	v := retailButtonVerdict(gad, 0, base, down, stage, grey)
	if entry == nil {
		return nil, v
	}
	v = retailButtonVerdict(gad, len(entry.Frames), base, down, stage, grey)
	if v.frame < 0 || v.frame >= len(entry.Frames) {
		return nil, v
	}
	return entry.Frames[v.frame].Frame, v
}

// stockButtonBase is the builder's closest four-frame BUTTONS0 group. Ties
// retain the first group and a score of 1000 is not replaced [07 R-WGT-01 §3].
func stockButtonBase(entry *formats.GAFEntry, gad gui.Gadget) int {
	if entry == nil {
		return 0
	}
	best, bestScore := 0, 1000
	for i := 0; i < len(entry.Frames); i += 4 {
		frame := entry.Frames[i].Frame
		if frame == nil {
			continue
		}
		score := numeric.Abs(int(frame.Width)-int(gad.Rect.W)) + numeric.Abs(int(frame.Height)-int(gad.Rect.H))
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// retailButtonCaptionPen is shared by frontend and battle button captions.
// The held down word deliberately is absent: stages move the pen, presses do
// not [03 R-FONT-01 §6].
//
// It is the retail button painter's pen arithmetic [03 R-FONT-01 §6], and the
// same arithmetic the queue-count text written into a build-product toy's own
// text slot [07 R-P0-11 §2] is laid out with. `s` is 1 when the gadget's
// `stages` field is non-zero. The vertical pen is
// `gy + trunc((h-1-metric)/2) + s` for the left/right/centre attributes, but
// the build-attribute variant (attribute bit 0x20) keeps the centred
// horizontal pen and instead anchors near the bottom edge:
// `bottom - 4 - metric + s`. `metric` is the line metric of the family the
// caption is drawn with — the capital-I frame height plus two for a GAF font
// (the build-product case; see drawProductButtonCaptionSelected), or the FNT
// header height field on the GAF pen's null-slot fallback.
//
// Build-product buttons author attribute 0x20 and no left/right/centre bit
// (asset census over the reference install's guis/*.gui files, the same
// census that backs [07 R-P0-11 §2]'s refinement: all 480 author
// `attribs = 32` alongside `commonattribs = 4`), so the count lands at the
// bottom-centre of the button, not the vertically-centred left inset a
// left-aligned button would use.
func retailButtonCaptionPen(gad gui.Gadget, r gui.Rect, textWidth, metric int) (x, y int, build, centred bool) {
	s := boolInt(gad.Stages != 0)
	right, bottom := int(r.X+r.W-1), int(r.Y+r.H-1)
	x = int(r.X) + (right-textWidth-int(r.X))/2 + s + 1
	y = retailTextPenY(gad, r, metric)
	switch {
	case gad.Attribs&1 != 0:
		x = int(r.X) + 3 + s
	case gad.Attribs&4 != 0:
		x = max(int(r.X), right-3-textWidth)
	case gad.Attribs&2 != 0:
		centred = true
	case gad.Attribs&0x20 != 0:
		build = true
		y = bottom - 4 - metric + s
	}
	return x, y, build, centred
}
