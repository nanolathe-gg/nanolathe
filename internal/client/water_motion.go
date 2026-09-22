package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Enhanced water follows wind through a slow velocity response, not by rotating
// world coordinates. Its integrated drift therefore cannot jump at a wind change.
// These are presentation coefficients, never simulation inputs (GPU design §26).
type waterMotionState struct {
	tidalX, tidalZ, prevTidalX, prevTidalZ float32
	tidalHeading                           float32
	tick                                   uint32
	valid                                  bool
	x, z, prevX, prevZ                     float32
	vx, vz, energy, prevEnergy             float32
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
	// Visual current: map tidal / 20 sets speed; only the wind heading sets
	// direction. Twenty is the selected presentation calibration,
	// not a retail water rule. Smooth heading separately from speed so a wind
	// reversal bends the current instead of stopping it (GPU design §26).
	dx, dz := -float32(numeric.Sin(a)), -float32(numeric.Cos(a))
	heading := float32(math.Atan2(float64(dx), float64(dz)))
	tidal := float32(0)
	if c.terrain != nil && !math.IsNaN(float64(c.terrain.Tidal)) && !math.IsInf(float64(c.terrain.Tidal), 0) {
		tidal = max(0, c.terrain.Tidal) / 20
	}
	// Existing wind uses negative X/Z components [R-WIND-01].
	vx := -float32(numeric.Sin(a)) / 8192 * (0.4 + 1.6*energy)
	vz := -float32(numeric.Cos(a)) / 8192 * (0.4 + 1.6*energy)
	if !st.valid || cur.Tick < st.tick || cur.Tick-st.tick > 300 {
		*st = waterMotionState{valid: true, tick: cur.Tick, vx: vx, vz: vz, energy: energy, prevEnergy: energy, tidalHeading: heading}
		return
	}
	for st.tick < cur.Tick {
		st.prevX, st.prevZ, st.prevEnergy = st.x, st.z, st.energy
		st.prevTidalX, st.prevTidalZ = st.tidalX, st.tidalZ
		// Ease along the shortest arc, with the same three-second response as
		// the original wind treatment. Unit direction preserves tidal speed even
		// through a reversal; wind strength never scales this current.
		delta := heading - st.tidalHeading
		if delta > math.Pi {
			delta -= 2 * math.Pi
		} else if delta < -math.Pi {
			delta += 2 * math.Pi
		}
		st.tidalHeading += delta / 90
		if st.tidalHeading > math.Pi {
			st.tidalHeading -= 2 * math.Pi
		} else if st.tidalHeading < -math.Pi {
			st.tidalHeading += 2 * math.Pi
		}
		sx, sz := math.Sincos(float64(st.tidalHeading))
		st.tidalX += float32(sx) * tidal / 30
		st.tidalZ += float32(sz) * tidal / 30
		// About three seconds of response; no angle-wrap special case is needed.
		st.vx += (vx - st.vx) / 90
		st.vz += (vz - st.vz) / 90
		st.energy += (energy - st.energy) / 90
		st.x += st.vx / 30
		st.z += st.vz / 30
		st.tick++
	}
}
