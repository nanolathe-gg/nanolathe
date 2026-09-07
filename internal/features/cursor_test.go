package features

import "testing"

// TestEventCursorWalksTheAuthoredCadence locks the advance rule of
// [05 R-FEAT-01 §10] pass 1 on a sequence with nonuniform delays: a frame
// whose delay word is d holds for max(d, 1) visits, the delay is reloaded
// from each new frame's word, and the visit that steps past the last frame
// clears the sequence pointer — so the lifetime is the sum of the holds and
// the record finishes on exactly that visit, never earlier or later.
func TestEventCursorWalksTheAuthoredCadence(t *testing.T) {
	var c eventCursor
	c.start([]int32{3, 0, 1, 2})
	if c.frame != 0 || c.delay != 3 || !c.running() {
		t.Fatalf("start left the cursor at frame %d delay %d running=%v, want frame 0 with frame 0's delay 3", c.frame, c.delay, c.running())
	}
	// Frame after each visit: 3 visits on frame 0, one on frame 1 (delay 0
	// still holds one visit), one on frame 2, two on frame 3, then finished.
	wantFrames := []int32{0, 0, 1, 2, 3, 3}
	for visit, want := range wantFrames {
		if finished := c.advance(); finished {
			t.Fatalf("visit %d finished the sequence early", visit+1)
		}
		if c.frame != want {
			t.Fatalf("after visit %d the cursor is on frame %d, want %d", visit+1, c.frame, want)
		}
		if got := c.visitIndex(); got > int32(visit+1) {
			t.Fatalf("after visit %d visitIndex reports %d, which is beyond the visits taken", visit+1, got)
		}
	}
	if finished := c.advance(); !finished || c.running() {
		t.Fatalf("visit 7 did not finish a 3+1+1+2 = 7 visit sequence (running=%v)", c.running())
	}
	if c.advance() {
		t.Fatal("a finished cursor advanced again")
	}
}

// TestEventCursorVisitIndexNamesTheFrame locks the conversion the presentation
// boundary and the frame-geometry seam rely on: visitIndex is the FIRST visit
// of the cursor's frame under the max(delay, 1) cadence, so a cadence walk over
// that index lands on the same frame — for every frame, including a zero-delay
// one.
func TestEventCursorVisitIndexNamesTheFrame(t *testing.T) {
	delays := []int32{4, 0, 2}
	prefix := []int32{0, 4, 5}
	for frame := int32(0); frame < 3; frame++ {
		c := eventCursor{delays: delays, frame: frame, delay: delays[frame]}
		if got := c.visitIndex(); got != prefix[frame] {
			t.Fatalf("frame %d reports visit %d, want %d", frame, got, prefix[frame])
		}
		// Walk the cadence back from that visit, as content.SimArt and the
		// client do, and land on the frame.
		elapsed := int32(0)
		landed := int32(-1)
		for i, d := range delays {
			if d < 1 {
				d = 1
			}
			elapsed += d
			if prefix[frame] < elapsed {
				landed = int32(i)
				break
			}
		}
		if landed != frame {
			t.Fatalf("visit %d walks back to frame %d, want %d", prefix[frame], landed, frame)
		}
	}
}

// TestEventCursorRestoredFrameKeepsFrameZeroDelay locks the documented save
// loss of [08 R-SAVE-FEATURE-01]: the reload restarts the sequence at frame 0
// (frame 0's delay) and writes the saved frame byte over the frame index
// only, so the restored frame holds for frame 0's delay and the sequence then
// continues at its own cadence. A saved frame past the sequence's end
// finishes on the first step rather than running on.
func TestEventCursorRestoredFrameKeepsFrameZeroDelay(t *testing.T) {
	var c eventCursor
	c.start([]int32{5, 1})
	c.frame = 1 // the saved byte
	if c.delay != 5 {
		t.Fatalf("delay %d after the frame overwrite, want frame 0's 5", c.delay)
	}
	visits := 0
	for !c.advance() {
		visits++
		if visits > 10 {
			t.Fatal("restored cursor never finished")
		}
	}
	if visits != 4 { // 5 → 4 → 3 → 2 → step (delay 1 < 2 is reached after four decrements)
		t.Fatalf("restored frame 1 ran %d visits before the finishing one, want 4 (frame 0's delay, not frame 1's)", visits)
	}
	c.start([]int32{2, 2})
	c.frame = 9
	c.delay = 1
	if !c.advance() || c.running() {
		t.Fatal("a saved frame past the sequence did not finish on its first step")
	}
}
