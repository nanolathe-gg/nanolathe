package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// arrivalPresentation owns only the displayed opening, never a mutable unit.
// All timing and displacement here are artistic prototype choices (GPU §36).
type arrivalPresentation struct {
	active  bool
	seconds float32
	unit    frame.UnitView
}

// StartArrival binds the already-published local commander to a fresh intro.
func (c *Client) StartArrival(unit frame.UnitView) {
	if c == nil {
		return
	}
	c.CancelPreRecord()
	// Retain identity and position only, not the publication's piece slices.
	c.arrival = arrivalPresentation{active: true, unit: frame.UnitView{
		Slot: unit.Slot, InstanceID: unit.InstanceID, X: unit.X, Y: unit.Y, Z: unit.Z,
	}}
	c.BumpPresentationEpoch()
}

func (c *Client) ArrivalActive() bool { return c != nil && c.arrival.active }

func (c *Client) ArrivalSeconds() float32 {
	if c == nil {
		return 0
	}
	return c.arrival.seconds
}

// SetArrivalSeconds also supports reproducible --shot stages. The host joins
// speculative recording before changing presentation state (GPU §13.10).
func (c *Client) SetArrivalSeconds(seconds float32) {
	if c == nil {
		return
	}
	c.CancelPreRecord()
	if seconds < 0 {
		seconds = 0
	}
	c.arrival.seconds = seconds
	if seconds >= drawlist.ArrivalDurationSeconds {
		c.arrival = arrivalPresentation{}
	}
	c.BumpPresentationEpoch()
}

func (c *Client) arrivalPacket() drawlist.Arrival {
	if !c.ArrivalActive() || !c.enhanced || c.cam == nil {
		return drawlist.Arrival{}
	}
	u := c.arrival.unit
	x, y := c.cam.WorldToScreen(u.X, u.Y, u.Z)
	gx, gy := c.cam.WorldToScreen(0, 0, 0)
	return drawlist.Arrival{Active: true, Seconds: c.arrival.seconds,
		X: float32(x - camera.OriginX), Y: float32(y - camera.OriginY),
		GridX: float32(gx - camera.OriginX), GridY: float32(gy - camera.OriginY),
		Scale: float32(c.cam.EffectiveScale().Project(32)) / 32,
	}
}

func (c *Client) arrivalMatches(v frame.UnitView) bool {
	return c.ArrivalActive() && c.enhanced && v.Slot == c.arrival.unit.Slot && v.InstanceID == c.arrival.unit.InstanceID
}

func (c *Client) arrivalHidesUnit(v frame.UnitView) bool {
	return c.arrivalMatches(v) && c.arrival.seconds < drawlist.ArrivalDropSeconds
}

func (c *Client) arrivalUnit(v frame.UnitView) frame.UnitView {
	if !c.arrivalMatches(v) {
		return v
	}
	t := max(0, (c.arrival.seconds-drawlist.ArrivalDropSeconds)/(drawlist.ArrivalImpactSeconds-drawlist.ArrivalDropSeconds))
	if t < 1 {
		// Accelerate into contact rather than braking at the ground. Height
		// shear halves the displayed lift; only a value copy changes [I6].
		v.Y += numeric.Fixed(640 * (1 - t*t*t) * 65536)
		v.NoShadow = true
	}
	return v
}
