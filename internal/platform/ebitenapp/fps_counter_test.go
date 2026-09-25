package ebitenapp

import (
	"testing"
	"time"
)

func TestFPSLiveMedianSmoothsShortSpikes(t *testing.T) {
	var counter fpsCounter
	start := time.Unix(1, 0)
	counter.observe(start, 0, 0, 0, 0, 0)
	for frame := 1; frame <= 20; frame++ {
		interval := 16 * time.Millisecond
		if frame == 11 {
			interval = 40 * time.Millisecond
		}
		counter.observe(counter.completed.Add(interval), 4*time.Millisecond, time.Millisecond, time.Millisecond, 2*time.Millisecond, time.Millisecond)
	}
	live := counter.live(counter.completed)
	if live.frames != 20 || live.interval != 16*time.Millisecond || live.draw != 4*time.Millisecond {
		t.Fatalf("500 ms medians = %+v", live)
	}
	var columns [10]fpsGraphColumn
	if peak := counter.graph(counter.completed, 16*time.Millisecond, columns[:]).peakInterval; peak != 40*time.Millisecond {
		t.Fatalf("smoothing hid graph peak: %s", peak)
	}
}

func TestFPSLiveMedianIgnoresSkippedPhaseSamples(t *testing.T) {
	var counter fpsCounter
	start := time.Unix(1, 0)
	counter.observe(start, 0, 0, 0, 0, 0)
	counter.observe(start.Add(16*time.Millisecond), 4*time.Millisecond, 3*time.Millisecond, 0, 2*time.Millisecond, time.Millisecond)
	counter.observe(start.Add(32*time.Millisecond), 6*time.Millisecond, 0, time.Millisecond, 3*time.Millisecond, 2*time.Millisecond)
	counter.observe(start.Add(48*time.Millisecond), 8*time.Millisecond, 5*time.Millisecond, 0, 4*time.Millisecond, 3*time.Millisecond)
	counter.observe(start.Add(64*time.Millisecond), 10*time.Millisecond, 0, 3*time.Millisecond, 5*time.Millisecond, 4*time.Millisecond)
	live := counter.live(start.Add(64 * time.Millisecond))
	if live.sim != 4*time.Millisecond || live.blend != 2*time.Millisecond || live.draw != 7*time.Millisecond || live.record != 3500*time.Microsecond {
		t.Fatalf("phase medians = %+v", live)
	}
	if expired := counter.live(start.Add(time.Second)); expired.frames != 0 || expired.interval != 0 || expired.sim != 0 {
		t.Fatalf("old samples remained live: %+v", expired)
	}
}

func TestFPSGraphRetainsRecentStallsAndBoundedHistory(t *testing.T) {
	var counter fpsCounter
	start := time.Unix(1, 0)
	counter.observe(start, 0, 0, 0, 0, 0)
	for frame := 1; frame <= fpsHistoryCapacity+1; frame++ {
		counter.observe(start.Add(time.Duration(frame)*time.Millisecond), 4*time.Millisecond, time.Millisecond, time.Millisecond, 2*time.Millisecond, time.Millisecond)
	}
	if counter.count != fpsHistoryCapacity || len(counter.history) != fpsHistoryCapacity {
		t.Fatalf("history has %d of %d samples", counter.count, len(counter.history))
	}
	var columns [32]fpsGraphColumn
	summary := counter.graph(start.Add(time.Duration(fpsHistoryCapacity+1)*time.Millisecond), 16*time.Millisecond, columns[:])
	if summary.frames != fpsHistoryCapacity || summary.peakInterval != time.Millisecond || summary.latest.draw != 4*time.Millisecond {
		t.Fatalf("bounded graph summary = %+v", summary)
	}
	stall := start.Add(40 * time.Second)
	counter.observe(stall, 12*time.Millisecond, 7*time.Millisecond, 3*time.Millisecond, 9*time.Millisecond, 5*time.Millisecond)
	columns = [32]fpsGraphColumn{}
	summary = counter.graph(stall, 16*time.Millisecond, columns[:])
	if summary.frames != 1 || summary.peakInterval != stall.Sub(start.Add(time.Duration(fpsHistoryCapacity+1)*time.Millisecond)) || summary.late != 1 {
		t.Fatalf("stale samples leaked into graph: %+v", summary)
	}
	if columns[len(columns)-1].interval != summary.peakInterval || columns[len(columns)-1].draw != 12*time.Millisecond || columns[len(columns)-1].sim != 7*time.Millisecond || columns[len(columns)-1].blend != 3*time.Millisecond || columns[len(columns)-1].record != 9*time.Millisecond || columns[len(columns)-1].submit != 5*time.Millisecond {
		t.Fatal("newest stall is not visible in the graph's right edge")
	}
	if summary.peakDraw != 12*time.Millisecond || summary.peakSim != 7*time.Millisecond || summary.peakBlend != 3*time.Millisecond || summary.peakRecord != 9*time.Millisecond || summary.peakSubmit != 5*time.Millisecond {
		t.Fatalf("phase peaks lost: %+v", summary)
	}
}

func TestFPSGraphCountsLateFramesWithoutLosingShortSpikes(t *testing.T) {
	var counter fpsCounter
	start := time.Unix(1, 0)
	counter.observe(start, 0, 0, 0, 0, 0)
	counter.observe(start.Add(17*time.Millisecond), 3*time.Millisecond, 0, 0, 0, 0)
	counter.observe(start.Add(35*time.Millisecond), 4*time.Millisecond, 0, 0, 0, 0)
	counter.observe(start.Add(65*time.Millisecond), 9*time.Millisecond, 0, 0, 0, 0)
	var columns [10]fpsGraphColumn
	summary := counter.graph(start.Add(65*time.Millisecond), 16*time.Millisecond, columns[:])
	if summary.frames != 3 || summary.late != 2 || summary.peakInterval != 30*time.Millisecond {
		t.Fatalf("late count and spike peak = %+v", summary)
	}
	if columns[len(columns)-1].interval != 30*time.Millisecond || columns[len(columns)-1].draw != 9*time.Millisecond {
		t.Fatalf("same-column spike was lost: %+v", columns[len(columns)-1])
	}
}
