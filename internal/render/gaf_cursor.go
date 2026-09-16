package render

// GAF cursor playback [03 §4.4][07 §8] (C8).

import (
	"github.com/nanolathe-gg/nanolathe/formats"
)

// Cursor index constants name the handle-array slots [07 §8]. Slot 0 is
// unused/gray overflow; 1..21 resolve named `cursors.gaf` entries at init.
// Lower indices win when several selected units disagree, so the numbering is
// also the shape priority order (see hud.ChooseCursor) [07 §8].
const (
	CursorAttack    = 1
	CursorAirstrike = 2
	CursorTooFar    = 3
	CursorCapture   = 4
	CursorDefend    = 5
	CursorRepair    = 6
	CursorPatrol    = 7
	CursorPickup    = 8
	CursorTeleport  = 9
	CursorRevive    = 10
	CursorReclamate = 11
	CursorLoad      = 12
	CursorUnload    = 13
	CursorMove      = 14
	CursorSelect    = 15
	CursorFindSite  = 16
	CursorRed       = 17
	CursorGrn       = 18
	CursorNormal    = 19
	CursorHourglass = 20
	CursorPathIcon  = 21
)

// cursorIndexToName is the hardware-style cursor index table [07 §8].
// Slot 0 is unused/gray overflow; indices 1..21 map to named GAF entries [07 §8].
// The index writer diffs and swaps shapes; the armed-order latch decides
// authorization while the index decides shape [07 §8].
var cursorIndexToName = [CursorCount]string{
	0:               "", // unused/gray overflow [07 §8]
	CursorAttack:    "cursorattack",
	CursorAirstrike: "cursorairstrike",
	CursorTooFar:    "cursortoofar",
	CursorCapture:   "cursorcapture",
	CursorDefend:    "cursordefend",
	CursorRepair:    "cursorrepair",
	CursorPatrol:    "cursorpatrol",
	CursorPickup:    "cursorpickup",
	CursorTeleport:  "cursorteleport",
	CursorRevive:    "cursorrevive",
	CursorReclamate: "cursorreclamate",
	CursorLoad:      "cursorload",
	CursorUnload:    "cursorunload",
	CursorMove:      "cursormove",
	CursorSelect:    "cursorselect",
	CursorFindSite:  "cursorfindsite",
	CursorRed:       "cursorred",
	CursorGrn:       "cursorgrn",
	CursorNormal:    "cursornormal",
	CursorHourglass: "cursorhourglass",
	CursorPathIcon:  "pathicon",
}

// CursorCount is the size of the cursor index table including slot 0 [07 §8].
const CursorCount = 22 // 0..21 inclusive [07 §8]

// CursorName returns the GAF entry name for a cursor index [07 §8].
// Slot 0 returns "" (unused) [07 §8]; out-of-range also returns "".
func CursorName(idx int) string {
	if idx < 0 || idx >= len(cursorIndexToName) {
		return ""
	}
	return cursorIndexToName[idx]
}

// IsValidCursorIndex reports whether idx is a usable cursor index [07 §8].
// Slot 0 is unused and returns false [07 §8].
func IsValidCursorIndex(idx int) bool {
	return idx >= 1 && idx <= CursorCount-1
}

// ResolveCursorEntry resolves a cursor index to its GAF entry through formats.GAF [fmt gaf][07 §8].
// It uses the index table's entry name and the GAF's case-insensitive lookup [fmt gaf].
func ResolveCursorEntry(gaf *formats.GAF, idx int) (*formats.GAFEntry, bool) {
	if gaf == nil {
		return nil, false
	}
	name := CursorName(idx)
	if name == "" {
		return nil, false
	}
	e, ok := gaf.Find(name)
	return e, ok
}

// ResolveCursorFrame resolves the current frame for a cursor's playback state [03 §4.4][07 §8].
// It returns the GAFFrame at the cursor's current index through the cursor's bound entry.
func ResolveCursorFrame(cursor *Cursor) (*formats.GAFFrame, bool) {
	if cursor == nil || !cursor.active || cursor.entry == nil {
		return nil, false
	}
	if cursor.Idx < 0 || cursor.Idx >= len(cursor.entry.Frames) {
		return nil, false
	}
	ref := cursor.entry.Frames[cursor.Idx]
	if ref.Frame == nil {
		return nil, false
	}
	return ref.Frame, true
}

// Cursor is a playback cursor over a GAF sequence [03 §4.4] (C8) (I13).
// Retail identity is 12 bytes: current index, countdown, loop flag, and entry
// pointer [03 §4.4]; Go uses named fields (I13). It does not free sequence
// storage on termination [03 §4.4].
// Retail GAF frame references have 8-byte identity with an int32 duration in
// whole ticks — not milliseconds [03 §4.4] (C8).
type Cursor struct {
	Idx       int   // current frame index [03 §4.4]
	Countdown int32 // ticks remaining for current frame; countdown <2 advances [03 §4.4]
	Loop      bool  // loop-or-hold flag [03 §4.4]

	entry  *formats.GAFEntry // non-owning view over shared sequence data [03 §4.4]
	active bool              // entry pointer present; false == cleared/terminated [03 §4.4]
}

// IsActive reports whether the cursor has a bound entry [03 §4.4].
func (c *Cursor) IsActive() bool {
	if c == nil {
		return false
	}
	return c.active && c.entry != nil
}

// Entry returns the bound GAF entry, if any [03 §4.4].
func (c *Cursor) Entry() *formats.GAFEntry {
	if c == nil {
		return nil
	}
	return c.entry
}

// FrameCount returns the frame count of the bound sequence [03 §4.4].
func (c *Cursor) FrameCount() int {
	if c == nil || !c.active || c.entry == nil {
		return 0
	}
	return int(c.entry.FrameCount)
}

// Bind binds the cursor to a GAF entry [03 §4.4].
// It clamps an out-of-range start index to zero, loads that frame's authored
// duration into the countdown, and copies the loop flag [03 §4.4].
// A nil entry or zero-frame entry clears the cursor [03 §4.4].
func (c *Cursor) Bind(entry *formats.GAFEntry, startIdx int, loop bool) {
	if c == nil {
		return
	}
	if entry == nil || entry.FrameCount == 0 || len(entry.Frames) == 0 {
		c.entry = nil
		c.active = false
		c.Idx = 0
		c.Countdown = 0
		c.Loop = loop
		return
	}
	c.entry = entry
	c.active = true
	c.Loop = loop
	// clamp out-of-range start index to zero [03 §4.4]
	if startIdx < 0 || startIdx >= len(entry.Frames) || startIdx >= int(entry.FrameCount) {
		startIdx = 0
	}
	c.Idx = startIdx
	dur := int32(entry.Frames[c.Idx].Value)
	if dur == 0 {
		// The authored duration is 1-10 across all retail data and the cursor
		// loads it as a whole-tick countdown [fmt gaf "Frame entry"]; a zero is
		// never authored. Treating one as a single tick is a bounds check, the
		// INVARIANTS I11 exception, not a traced behavior.
		dur = 1
	}
	c.Countdown = dur
}

// BindIndex resolves a cursor index through a GAF and binds the cursor [07 §8][03 §4.4].
// It returns false when the index or GAF entry cannot be resolved.
func (c *Cursor) BindIndex(gaf *formats.GAF, idx int, startIdx int, loop bool) bool {
	entry, ok := ResolveCursorEntry(gaf, idx)
	if !ok {
		if c != nil {
			c.entry = nil
			c.active = false
			c.Idx = 0
			c.Countdown = 0
		}
		return false
	}
	c.Bind(entry, startIdx, loop)
	return true
}

// Clear clears the cursor's entry pointer [03 §4.4].
// It does not free sequence storage [03 §4.4].
func (c *Cursor) Clear() {
	if c == nil {
		return
	}
	c.entry = nil
	c.active = false
	c.Idx = 0
	c.Countdown = 0
}

// Step single-steps the cursor by one simulation tick [03 §4.4] (C8).
// Countdown <2 advances to the next frame, wrapping to zero for looping
// sequences or clearing the entry pointer for non-looping sequences, and loads
// the new frame's duration [03 §4.4]. Single-frame entries never advance [03 §4.4].
func (c *Cursor) Step() {
	if c == nil || !c.active || c.entry == nil {
		return
	}
	n := int(c.entry.FrameCount)
	if n <= 0 {
		n = len(c.entry.Frames)
	}
	if n <= 0 {
		return
	}
	if n == 1 {
		// single-frame entries never advance [03 §4.4]
		if c.Countdown >= 2 {
			c.Countdown--
		}
		return
	}
	if c.Countdown < 2 {
		next := c.Idx + 1
		if next >= n || next >= len(c.entry.Frames) {
			if c.Loop {
				c.Idx = 0
				dur := int32(c.entry.Frames[0].Value)
				if dur == 0 {
					dur = 1
				}
				c.Countdown = dur
			} else {
				// clears the entry pointer for non-looping sequences [03 §4.4]
				c.active = false
				c.entry = nil
				c.Idx = 0
				c.Countdown = 0
			}
			return
		}
		c.Idx = next
		dur := int32(c.entry.Frames[c.Idx].Value)
		if dur == 0 {
			dur = 1
		}
		c.Countdown = dur
		return
	}
	c.Countdown--
}

// StepDelta subtracts a signed 16-bit tick delta and can cross multiple frames
// in one call while accumulating each newly selected frame's duration [03 §4.4] (C8).
// Negative deltas can cross multiple frames [PLAN_13 C8].
// Single-frame entries never advance [03 §4.4].
func (c *Cursor) StepDelta(d int16) {
	if c == nil || !c.active || c.entry == nil {
		return
	}
	n := int(c.entry.FrameCount)
	if n <= 0 {
		n = len(c.entry.Frames)
	}
	if n <= 0 || n == 1 {
		// single-frame entries never advance [03 §4.4]
		return
	}
	// subtract signed 16-bit delta; negative d reduces countdown and can cross frames [03 §4.4][PLAN_13 C8]
	c.Countdown += int32(d)
	// Can cross multiple frames while accumulating each newly selected frame's duration [03 §4.4]
	for c.Countdown < 2 && c.active && c.entry != nil {
		next := c.Idx + 1
		if next >= n || next >= len(c.entry.Frames) {
			if c.Loop {
				next = 0
			} else {
				c.active = false
				c.entry = nil
				c.Idx = 0
				c.Countdown = 0
				return
			}
		}
		c.Idx = next
		dur := int32(c.entry.Frames[c.Idx].Value)
		if dur == 0 {
			dur = 1
		}
		// accumulating each newly selected frame's duration [03 §4.4]
		c.Countdown += dur
		// guard against zero-duration infinite loop
		if dur == 0 {
			break
		}
		// If loop and durations are tiny and delta very negative, this loops correctly
		// across many frames in one call [03 §4.4].
	}
}

// CurrentFrame returns the current GAFFrame for the cursor's index [03 §4.4][fmt gaf].
func (c *Cursor) CurrentFrame() (*formats.GAFFrame, bool) {
	return ResolveCursorFrame(c)
}

// CursorHotspot returns the blit origin for a cursor frame drawn with its
// anchor at (x, y) [07 §8][fmt gaf]. The frame's authored x_offset/y_offset is
// the hotspot: that pixel lands on the pointer, so the top-left corner is the
// pointer position minus the offset [fmt gaf "Placement offsets"].
func CursorHotspot(f *formats.GAFFrame, x, y int) (int, int) {
	if f == nil {
		return x, y
	}
	return x - int(f.XOffset), y - int(f.YOffset)
}

// There is one cursor driver and one subframe lifetime, so there is no second
// family to describe. This file previously ended with an open-question marker,
// "cursor subframe lifetime and animation speed for families not shown to use
// the authored countdown cursor remain unknown"; the premise is false. Every
// interface cursor sequence is stepped by the same wall-clock delta path — a
// 30-unit-per-second scaled delta fed to the multi-frame countdown stepper
// above — and a subframe's lifetime is that frame's own authored 32-bit
// duration expressed in those scaled units, exactly as this cursor's Bind and
// StepDelta already implement it [03 §4.4]. Model-texture players are the only
// sequences on a different driver (the per-tick phase-7 walker), and they are
// not cursors [03 §4.4 "CRD-005 closure"]. The twenty-two cursor slots are one
// homogeneous family of GAF entries [07 §8], and re-selecting the shape already
// shown does not restart its animation, because the index writer diffs before
// it swaps [07 §8] — that swap rule, not a second lifetime rule, is what makes
// two cursor families look like they animate differently.
