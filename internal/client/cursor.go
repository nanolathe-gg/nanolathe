package client

// Software cursor [07 §8] C12.
//
// Retail draws the pointer itself rather than handing a shape to the window
// system: cursor artwork comes from a cursor GAF root, the handle array is
// resolved once at init from named entries, and the cursor is blitted after the
// offscreen battle/front-end surface is prepared, with the window pointer
// position translated into surface coordinates [07 §8].
//
// The index writer diffs and swaps shapes, so re-selecting the shape already
// shown keeps its playback position and only a real change restarts the
// sequence [07 §8].

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// CursorGAFPath is the cursor GAF root [07 §8][02 §6].
const CursorGAFPath = "anims/cursors.gaf"

// Cursors is the resolved cursor handle array plus the playback state of the
// shape currently shown [07 §8][03 §4.4].
//
// Slot 0 of the handle array is unused; entries 1..21 are resolved by name at
// load and stay resolved for the session, exactly as retail resolves them once
// at init [07 §8].
type Cursors struct {
	gaf     *formats.GAF
	entries [render.CursorCount]*formats.GAFEntry

	idx  int           // current index; 0 means nothing installed [07 §8]
	play render.Cursor // playback over the installed entry [03 §4.4]

	// Hidden suppresses drawing without losing the installed shape. The
	// front-end shell uses it while a video or a modal owns the screen.
	Hidden bool
}

// cursorProviders returns provider identities for diagnostics [AGENTS.md §Diagnostics].
func cursorProviders(fs vfs.FSOps) string {
	if fs == nil {
		return ""
	}
	if p, ok := fs.(interface{ Providers() []vfs.ProviderInfo }); ok {
		infos := p.Providers()
		ids := make([]string, 0, len(infos))
		for _, info := range infos {
			id := info.ID
			if id == "" {
				id = info.Type
			}
			ids = append(ids, id)
		}
		return strings.Join(ids, ", ")
	}
	return ""
}

// LoadCursors opens the mandatory cursor GAF root and resolves the complete
// handle array [07 §8]. The error includes a provider-aware diagnostic
// (logical path + providers searched) [AGENTS.md §Diagnostics].
func LoadCursors(fs vfs.FSOps) (*Cursors, error) {
	gaf, err := formats.LoadGAFFile(fs, CursorGAFPath)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: load retail cursor GAF: logical path %s, providers searched [%s], expected retail cursor GAF: %w", CursorGAFPath, cursorProviders(fs), err)
	}
	cs := &Cursors{gaf: gaf}
	for idx := 1; idx < render.CursorCount; idx++ {
		e, ok := render.ResolveCursorEntry(gaf, idx)
		if !ok || e == nil || len(e.Frames) == 0 {
			name := render.CursorName(idx)
			// Retail has no behaviour here to clone. A name the bank lookup
			// does not find leaves a null handle slot, and the sequence binder
			// reads the entry's frame count before it tests the pointer, so
			// selecting that index faults; an entry with no frames binds frame
			// 0 and reads a hold word past the end of its empty frame table.
			// Both are undefined in retail, which is why this validation is
			// mandatory rather than a relaxation waiting on a trace
			// [03 R-FX-01 §5][03 §4.4].
			cause := errors.New("cursor entry is missing or has no frames")
			return nil, fmt.Errorf("nanolathe: load retail cursor entry: logical path %s, providers searched [%s], expected cursor GAF entry %q: %w", CursorGAFPath, cursorProviders(fs), name, cause)
		}
		cs.entries[idx] = e
	}
	cs.SetIndex(render.CursorNormal)
	return cs, nil
}

// Index returns the cursor index currently installed [07 §8].
func (cs *Cursors) Index() int {
	if cs == nil {
		return 0
	}
	return cs.idx
}

// SetIndex installs a cursor shape, diffing against the shape already shown so
// an unchanged index does not restart the sequence [07 §8]. LoadCursors
// validates every named entry before a Cursors value can be installed, so an
// invalid index is ignored rather than selecting a different shape.
func (cs *Cursors) SetIndex(idx int) {
	if cs == nil || idx == cs.idx {
		return
	}
	if !render.IsValidCursorIndex(idx) || cs.entries[idx] == nil {
		// Retail's setter has no range check: it stores the byte and binds
		// whatever word sits at that offset of the handle array. Slot 0 is
		// the zero word before the first handle, so index 0 binds a null
		// sequence and faults in the binder; an index past 21 binds the
		// neighbouring globals as if they were sequences. No producer yields
		// either — the shape chooser starts at `cursornormal` and takes
		// minima over 1..20, the hourglass and reset sites force 20 and 19 —
		// so ignoring the write reproduces every reachable retail outcome
		// without the undefined ones [03 R-FX-01 §5][07 §8].
		return
	}
	cs.idx = idx
	// Cursor sequences loop: retail never lets the pointer go blank at the end
	// of an animation [03 §4.4].
	cs.play.Bind(cs.entries[idx], 0, true)
}

// Step advances cursor playback by n simulation ticks. GAF frame references
// carry their duration in whole ticks [03 §4.4] C8, so the cursor shares the
// clock that drives animated textures.
func (cs *Cursors) Step(n int) {
	if cs == nil || n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		cs.play.Step()
	}
}

// Frame returns the frame to blit, or nil when nothing is installed [03 §4.4].
func (cs *Cursors) Frame() *formats.GAFFrame {
	if cs == nil || cs.Hidden {
		return nil
	}
	f, ok := cs.play.CurrentFrame()
	if !ok {
		return nil
	}
	return f
}

// SetCursors installs the software cursor. Windowed clients keep the software
// pointer as their only pointer once it has been installed [07 §8].
func (c *Client) SetCursors(cs *Cursors) {
	c.cursors = cs
}

// Cursors returns the installed software cursor, or nil.
func (c *Client) Cursors() *Cursors { return c.cursors }

// PositionPresentationCursor applies a newer host position only to the joined
// frame's cursor command. Picking and command input keep their host-step sample.
// Capture release retains the saved restore position until the next step, when
// the platform has observed its warp [07 R-CAM-01 §11].
func (c *Client) PositionPresentationCursor(list *drawlist.List, x, y int) {
	if c == nil || list == nil || c.PointerCaptured() || c.cursorRestorePending {
		return
	}
	list.PositionCursor(x, y)
}

// drawCursor blits the installed cursor at the pointer position. It runs after
// the world, the HUD, and any modal overlay have been composed [07 §8].
//
// The frame's authored x_offset/y_offset is the hotspot: that pixel lands on
// the pointer, so the blit origin is the pointer minus the offset
// [07 §8][fmt gaf "Placement offsets"].
func (c *Client) drawCursor() {
	if c == nil || c.cursors == nil || c.PointerCaptured() {
		return
	}
	f := c.cursors.Frame()
	if f == nil {
		return
	}
	mouse, _ := c.in.PointerSample()
	if c.cursorRestorePending {
		mouse.X, mouse.Y = c.cursorRestoreX, c.cursorRestoreY
	}
	x, y := render.CursorHotspot(f, int(mouse.X), int(mouse.Y))
	// Record then execute inline: classicSink.Cursor runs the same UIBlit at the
	// hotspot-resolved origin this used to call directly [07 §8].
	c.emitCursor(drawlist.Cursor{Frame: f, HotX: int32(x), HotY: int32(y)})
}
