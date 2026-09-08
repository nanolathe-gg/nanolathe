package main

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/pool"
	"slices"
)

// flushWindowTokens runs at an actual window open, before its next service
// pass. Cached asset lookups and redraws must not discard new input
// [07 R-WGT-02 §5].
func flushWindowTokens(cl *client.Client) {
	if cl != nil && cl.Input() != nil {
		cl.Input().DrainTokens()
	}
}

// Command-window selection is an open boundary even for a cached GUI. The
// selection-change path closes/reopens the page; repeated draw and hit-test
// lookups of the same selection/page are not opens [07 §6][07 R-HUD-04 §3].
type commandWindowInputState struct {
	window    *gui.Window
	selection []pool.Handle
}

func (s *commandWindowInputState) open(b *battleSession, f *frame.Frame, window *gui.Window) {
	if window == nil || f == nil {
		s.window = nil
		s.selection = s.selection[:0]
		return
	}
	if s.window == window && slices.Equal(s.selection, f.Selection.Handles) {
		return
	}
	s.window = window
	s.selection = append(s.selection[:0], f.Selection.Handles...)
	if b != nil {
		flushWindowTokens(b.cl)
	}
}
