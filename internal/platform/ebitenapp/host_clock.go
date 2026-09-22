package ebitenapp

import "time"

// hostClock keeps the existing 30 Hz host work independent of Ebitengine's
// refresh-paced input snapshots. This is window scheduling policy, not the
// simulation clock (DESIGN_GPU_RENDERER §13.5).
type hostClock struct {
	last  time.Time
	carry time.Duration
}

func (c *hostClock) advance(now time.Time) int {
	if c.last.IsZero() {
		c.last = now
		return 1 // Initialize the client before its first Draw.
	}
	elapsed := max(now.Sub(c.last), 0)
	c.last = now
	const period = time.Second / presentationTPS
	// Match the window scheduler's previous five-step catch-up bound. A long
	// stall must not trigger an unbounded burst of camera/menu steps on resume.
	c.carry = min(c.carry+elapsed, 5*period)
	// Round to the closest step and retain its signed remainder, so display
	// jitter around a 30 Hz boundary does not cause alternating zero/two steps.
	steps := int((c.carry + period/2) / period)
	c.carry -= time.Duration(steps) * period
	return steps
}
