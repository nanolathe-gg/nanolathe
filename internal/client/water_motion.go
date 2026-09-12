package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Enhanced water follows wind through a slow velocity response, not by rotating
// world coordinates. Its integrated drift therefore cannot jump at a wind change.
// These are presentation coefficients, never simulation inputs (GPU design §26).
type waterMotionState struct {
	tick                       uint32
	valid                      bool
	x, z, prevX, prevZ         float32
	vx, vz, energy, prevEnergy float32
}

func (c *Client) observeWaterMotion(cur *frame.Frame) {
	if c == nil || cur == nil || !c.enhanced {
		return
	}
	st := &c.waterMotion
	if st.valid && st.tick == cur.Tick {
		return
	}
	energy := min(max(float32(cur.Wind.Strength)/5000, 0), 1)
	a := numeric.Angle(cur.Wind.Heading)
	// Existing wind uses negative X/Z components [R-WIND-01].
	vx := -float32(numeric.Sin(a)) / 8192 * (0.4 + 1.6*energy)
	vz := -float32(numeric.Cos(a)) / 8192 * (0.4 + 1.6*energy)
	if !st.valid || cur.Tick < st.tick || cur.Tick-st.tick > 300 {
		*st = waterMotionState{valid: true, tick: cur.Tick, vx: vx, vz: vz, energy: energy, prevEnergy: energy}
		return
	}
	for st.tick < cur.Tick {
		st.prevX, st.prevZ, st.prevEnergy = st.x, st.z, st.energy
		// About three seconds of response; no angle-wrap special case is needed.
		st.vx += (vx - st.vx) / 90
		st.vz += (vz - st.vz) / 90
		st.energy += (energy - st.energy) / 90
		st.x += st.vx / 30
		st.z += st.vz / 30
		st.tick++
	}
}
