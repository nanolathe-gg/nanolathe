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
	"fmt"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/vfs"
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

// LoadCursors opens the cursor GAF root and resolves the handle array [07 §8].
// A missing entry leaves its slot nil; the caller falls back to cursornormal.
func LoadCursors(fs *vfs.FS) (*Cursors, error) {
	gaf, err := formats.LoadGAFFile(fs, CursorGAFPath)
	if err != nil {
		return nil, fmt.Errorf("client: cursors: %w", err)
	}
	cs := &Cursors{gaf: gaf}
	for idx := 1; idx < render.CursorCount; idx++ {
		if e, ok := render.ResolveCursorEntry(gaf, idx); ok {
			cs.entries[idx] = e
		}
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
// an unchanged index does not restart the sequence [07 §8]. An index with no
// resolved entry falls back to cursornormal; if that is missing too the cursor
// is left uninstalled and draws nothing.
func (cs *Cursors) SetIndex(idx int) {
	if cs == nil || idx == cs.idx {
		return
	}
	if !render.IsValidCursorIndex(idx) || cs.entries[idx] == nil {
		if idx == render.CursorNormal || cs.entries[render.CursorNormal] == nil {
			cs.idx = 0
			cs.play.Clear()
			return
		}
		idx = render.CursorNormal
		if idx == cs.idx {
			return
		}
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

// SetCursors installs the software cursor. Passing nil removes it and the
// window system's own pointer is shown again.
func (c *Client) SetCursors(cs *Cursors) {
	c.cursors = cs
	c.applyCursorMode()
}

// Cursors returns the installed software cursor, or nil.
func (c *Client) Cursors() *Cursors { return c.cursors }

// drawCursor blits the installed cursor at the pointer position. It runs after
// the world, the HUD, and any modal overlay have been composed [07 §8].
//
// The frame's authored x_offset/y_offset is the hotspot: that pixel lands on
// the pointer, so the blit origin is the pointer minus the offset
// [07 §8][fmt gaf "Placement offsets"].
func (c *Client) drawCursor() {
	f := c.cursors.Frame()
	if f == nil {
		return
	}
	x, y := render.CursorHotspot(f, int(c.in.Mouse.X), int(c.in.Mouse.Y))
	c.UIBlit(f, x, y)
}
