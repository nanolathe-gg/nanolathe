package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
)

// replayForTest executes the recorded committed-frame list once through the
// classic sink. Before WU-1.8 the emit* helpers wrote c.indexed inline, so a
// white-box byte-writer test could call a draw method and read c.indexed
// straight away; the flip made drawing record-only, so those tests record their
// draws and call this to run the deferred replay the production Frame path runs.
// It does not clear c.indexed, so a resetListForTest / draw / replayForTest batch
// composes over whatever the previous batch already replayed, exactly as one
// production recording pass composes within a frame.
func (c *Client) replayForTest() { c.list.Replay(c.classicSink()) }

// resetListForTest truncates the recorded list, the point arena and the
// model-commit table the way composeIndexed does at the top of a frame, so a
// test can record a fresh batch without replaying a previous one twice.
func (c *Client) resetListForTest() {
	c.list.Reset()
	c.pointArena = c.pointArena[:0]
	c.modelCommits = c.modelCommits[:0]
}

// drawUnitModelReplay records one unit model and replays the list once — the
// deferred-replay equivalent of the pre-WU-1.8 inline drawUnitModel the piece
// tests were written against. It resets the list first so each call composes
// over the surface as clearIndexed left it, not over the previous call's record.
func (c *Client) drawUnitModelReplay(v frame.UnitView, sx, sy int32) bool {
	c.resetListForTest()
	ok := c.drawUnitModel(v, sx, sy)
	c.replayForTest()
	return ok
}

// TestRecordingPassWritesNothingUntilReplay is the structural proof of WU-1.8:
// no committed-frame draw reaches c.indexed except through the classic sink
// during Replay. It composes one frame's recording pass — composeIndexed, which
// runs drawCommittedFrame, and drawCursor — over an indexed surface pre-filled
// with a sentinel, and asserts the sentinel is untouched: the recording pass
// records commands and writes no pixels. Only when the list is replayed does the
// surface change (the Clear zeroes it and the cursor paints), which proves every
// write goes through the sink [C-G1].
//
// If any draw wrote to c.indexed directly during recording — a byte writer not
// moved behind the list, a model shadow or trace still committing inline — the
// sentinel would be gone before Replay and this test would fail.
func TestRecordingPassWritesNothingUntilReplay(t *testing.T) {
	buf := frame.NewBuffer()
	buf.BeginWrite()
	if err := buf.Publish(3); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Buffer: buf, Width: 4, Height: 3})
	if err != nil {
		t.Fatal(err)
	}
	// A palette so the cursor blit (and any lit op) has tables to read; a cursor
	// so Replay has something to paint after the clear.
	p := &palette.Tables{}
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
	}
	c.SetPalette(p)
	c.cursors = snapshotCursor(200)
	c.in.Mouse.SetPosition(1, 1)

	// Pre-fill the whole indexed surface with a sentinel that no draw in this
	// scene produces, so "still the sentinel" means "no pixel was written".
	const sentinel = 0x5A
	for i := range c.indexed {
		c.indexed[i] = sentinel
	}

	cur := buf.Current()
	c.composeIndexed(cur, cur != nil) // records; must write no pixels
	c.drawCursor()                    // records the cursor; must write no pixels
	for i, v := range c.indexed {
		if v != sentinel {
			t.Fatalf("recording pass wrote pixel %d = %d; a draw bypassed the list [C-G1]", i, v)
		}
	}

	c.list.RecordExpand()
	c.list.Replay(c.classicSink())
	changed := false
	for _, v := range c.indexed {
		if v != sentinel {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("replay left the surface untouched; the list did not carry the frame's draws")
	}
	// The clear ran first, so the cursor's own pixel is present and the rest is
	// the cleared index, never the sentinel.
	if c.indexed[1*4+1] != 200 {
		t.Fatalf("cursor pixel after replay = %d, want 200 (clear then cursor)", c.indexed[1*4+1])
	}
}
