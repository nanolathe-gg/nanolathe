package presentation

import "time"

// Clock is the presentation clock domain. It deliberately keeps simulation
// ticks, frame serials, and wall time in separate fields so a caller cannot
// accidentally use a render-frame count as an authoritative tick [03 §1],
// [01 §4.2]. The clock has no reference to simulation state.
//
// A zero Clock is ready for use. BeginFrame marks the first observed tick as a
// new tick; subsequent calls mark only a changed SimTick. FrameSerial advances
// once for every drained presentation frame, including paused frames.
type Clock struct {
	SimTick     uint32
	FrameSerial uint32
	WallDelta   time.Duration
	NewSimTick  bool

	initialized bool
	consumed    bool
}

// BeginFrame records the snapshot tick and wall-clock delta for one complete
// presentation frame. WallDelta is retained exactly as supplied; scaling and
// cursor-specific interpretation belong to the consumer [03 §1].
func (c *Clock) BeginFrame(simTick uint32, wallDelta time.Duration) {
	if c == nil {
		return
	}
	c.FrameSerial++
	c.WallDelta = wallDelta
	c.NewSimTick = !c.initialized || simTick != c.SimTick
	c.SimTick = simTick
	c.initialized = true
	c.consumed = false
}

// Update is a concise alias for BeginFrame for clients whose frame pump uses
// an update naming convention.
func (c *Clock) Update(simTick uint32, wallDelta time.Duration) { c.BeginFrame(simTick, wallDelta) }

// ConsumeNewSimTick returns the one-shot tick boundary for the current frame.
// It is useful to services that must advance exactly once even when several
// draw passes read the same Clock. NewSimTick remains true for diagnostic
// inspection until the next BeginFrame.
func (c *Clock) ConsumeNewSimTick() bool {
	if c == nil || !c.NewSimTick || c.consumed {
		return false
	}
	c.consumed = true
	return true
}

// TakeNewSimTick is an alias for ConsumeNewSimTick.
func (c *Clock) TakeNewSimTick() bool { return c.ConsumeNewSimTick() }

// Reset clears the presentation-domain state. It does not affect any
// authoritative clock or random stream.
func (c *Clock) Reset() {
	if c == nil {
		return
	}
	*c = Clock{}
}
