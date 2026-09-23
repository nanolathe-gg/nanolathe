package ebitenapp

import (
	"testing"
	"time"
)

func TestFPSCounterCountsPresentedFrames(t *testing.T) {
	var counter fpsCounter
	start := time.Unix(1, 0)
	for frame := 0; frame <= 60; frame++ {
		counter.observe(start.Add(time.Duration(frame) * time.Second / 60))
	}
	if !counter.ready || counter.value < 59.9 || counter.value > 60.1 {
		t.Fatalf("completed presentations measured %.2f FPS, ready=%v", counter.value, counter.ready)
	}
}
