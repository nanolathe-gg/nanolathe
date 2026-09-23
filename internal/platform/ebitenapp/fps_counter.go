package ebitenapp

import "time"

// fpsCounter counts completed modern presentations over elapsed host time.
// A one-second window avoids reporting Ebitengine's faster, cap-skipped Draw
// callbacks as presented frames (DESIGN_GPU_RENDERER §13.5).
type fpsCounter struct {
	started time.Time
	frames  int
	value   float64
	ready   bool
}

func (c *fpsCounter) observe(now time.Time) {
	if c.started.IsZero() {
		c.started = now
		return
	}
	c.frames++
	if elapsed := now.Sub(c.started); elapsed >= time.Second {
		c.value = float64(c.frames) / elapsed.Seconds()
		c.ready = true
		c.started = now
		c.frames = 0
	}
}
