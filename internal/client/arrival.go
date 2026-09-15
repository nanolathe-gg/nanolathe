package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// arrivalPresentation owns only the displayed opening, never a mutable unit.
// All timing and displacement here are artistic prototype choices (GPU §36).
type arrivalPresentation struct {
	active     bool
	revealOnly bool
	cooling    bool
	landed     bool
	presented  bool
	seconds    float32
	unit       frame.UnitView
}

// StartArrival binds the already-published local commander to a fresh intro.
func (c *Client) StartArrival(unit frame.UnitView) {
	if c == nil {
		return
	}
	c.CancelPreRecord()
	// Retain identity and position only, not the publication's piece slices.
	c.arrival = arrivalPresentation{active: true, cooling: true, unit: frame.UnitView{
		Slot: unit.Slot, InstanceID: unit.InstanceID, X: unit.X, Y: unit.Y, Z: unit.Z,
	}}
	c.BumpPresentationEpoch()
}

// StartMapReveal fades and bounces the already-published scene around the
// current camera, preserving saved unit poses and camera placement (GPU §36).
func (c *Client) StartMapReveal() {
	if c == nil || c.cam == nil {
		return
	}
	c.CancelPreRecord()
	viewport := c.battleViewportRect()
	x, z := c.cam.ScreenToWorld(viewport.X+viewport.W/2+camera.OriginX, viewport.Y+viewport.H/2+camera.OriginY)
	c.arrival = arrivalPresentation{active: true, revealOnly: true, unit: frame.UnitView{X: x, Z: z}}
	c.BumpPresentationEpoch()
}

// ArrivalDuration bounds input holding for the selected opening.
func (c *Client) ArrivalDuration() float32 {
	if c != nil && c.arrival.revealOnly {
		return drawlist.ArrivalRevealSeconds
	}
	return drawlist.ArrivalDurationSeconds
}

// ArrivalHasDrop distinguishes landing cues from a scene-only reveal.
func (c *Client) ArrivalHasDrop() bool { return c != nil && !c.arrival.revealOnly }

func (c *Client) ArrivalActive() bool { return c != nil && c.arrival.active }

// MarkArrivalPresented starts the host's intro clock only after its first GPU
// frame is submitted. Window creation must not consume the reveal (GPU §36).
func (c *Client) MarkArrivalPresented() {
	if c.ArrivalActive() {
		c.arrival.presented = true
	}
}

func (c *Client) ArrivalPresented() bool { return c.ArrivalActive() && c.arrival.presented }

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
	if !c.arrival.revealOnly && seconds >= drawlist.ArrivalImpactSeconds && (c.arrival.active || c.arrival.cooling) {
		c.arrival.landed = true
	}
	if seconds >= c.ArrivalDuration() {
		c.arrival.active = false
	}
	if seconds >= drawlist.ArrivalCoolingEndSeconds {
		// Keep the world-space landing scar after the animation cools away.
		c.arrival = arrivalPresentation{landed: c.arrival.landed, unit: c.arrival.unit,
			seconds: drawlist.ArrivalCoolingEndSeconds}
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
	a := drawlist.Arrival{Active: true, RevealOnly: c.arrival.revealOnly, Seconds: c.arrival.seconds,
		X: float32(x - camera.OriginX), Y: float32(y - camera.OriginY),
		GridX: float32(gx - camera.OriginX), GridY: float32(gy - camera.OriginY),
		Scale: float32(c.cam.EffectiveScale().Project(32)) / 32,
	}
	a.DropHeight = c.arrivalDropHeight()
	a.RevealRadius = c.arrivalRevealRadius(a)
	return a
}

func (c *Client) arrivalMatches(v frame.UnitView) bool {
	return c.ArrivalActive() && !c.arrival.revealOnly && c.enhanced && v.Slot == c.arrival.unit.Slot && v.InstanceID == c.arrival.unit.InstanceID
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
		v.Y += numeric.Fixed(c.arrivalDropHeight() * (1 - t*t*t) * 65536)
		v.NoShadow = true
	}
	return v
}

// StepArrivalCooling keeps the hot model moving with gameplay after handoff.
// This is a short presentation clock, frozen on pause/focus loss (GPU §36).
func (c *Client) StepArrivalCooling(delta float64) {
	if c != nil && c.arrival.cooling && !c.arrival.active && c.IsFocused() && !c.PresentationPaused() && delta > 0 {
		c.SetArrivalSeconds(c.arrival.seconds + float32(min(delta, 0.05)))
	}
}

// Reuse the wreck's emission and rising air field on per-frame model packets.
// The retained model texture stays intact; no authoritative unit is changed.
func (c *Client) applyArrivalHeat(g *drawlist.ModelGeometry, v frame.UnitView) {
	if g == nil {
		return
	}
	g.WreckEmission = [3]float32{}
	g.WreckHeatStrength, g.WreckHeatTime, g.WreckHeatScale = 0, 0, 0
	if !c.enhanced || !c.effects.Distortion || !c.arrival.cooling || v.Slot != c.arrival.unit.Slot || v.InstanceID != c.arrival.unit.InstanceID || c.arrival.seconds < drawlist.ArrivalDropSeconds {
		return
	}
	age := max(0, c.arrival.seconds-drawlist.ArrivalImpactSeconds)
	cool := max(0, 1-age/4)
	red, amber := cool*cool, cool*cool*cool*cool
	flash := max(0, 1-age/0.3)
	g.WreckEmission = [3]float32{0.95*red + 0.15*flash, 0.30*amber + 0.55*flash, 0.025*amber + 0.42*flash}
	g.WreckHeatStrength = 0.85 * cool * cool
	g.WreckHeatScale = float32(c.viewScale().Float())
	g.WreckHeatTime = c.arrival.seconds * 30
}

// Measure only the chunks the player can actually see. Using the viewport's
// black corners made a small starting island finish long before the drop.
// Fog Ch0 == 15 is wholly unexplored; partial edges and explored gray terrain
// still contain image pixels and participate [03 §3.3]. No GPU readback needed.
func (c *Client) arrivalRevealRadius(a drawlist.Arrival) float32 {
	if c.buffer == nil || c.buffer.Current() == nil || c.cam == nil || a.Scale <= 0 {
		return 0
	}
	fog := c.buffer.Current().Fog
	if !fog.Valid {
		return 0
	}
	if _, ok := visibilityGridSize(fog.W, fog.H, len(fog.Ch0)); !ok {
		return 0
	}
	viewport := c.battleViewportRect()
	factor := float32(c.cam.EffectiveZoom().Float()) / a.Scale
	ox, oy := float32(0), float32(0)
	if c.camBlending {
		factor = float32(c.camDrawView.Factor) / a.Scale
		ox = float32((float64(c.cam.X) - c.camDrawView.X) * c.camDrawView.Factor)
		oy = float32((float64(c.cam.Z) - c.camDrawView.Z) * c.camDrawView.Factor)
	}
	left, top := (float32(viewport.X)-ox)/factor, (float32(viewport.Y)-oy)/factor
	right, bottom := (float32(viewport.X+viewport.W)-ox)/factor, (float32(viewport.Y+viewport.H)-oy)/factor
	tile := 32 * a.Scale
	farSquared := float32(0)
	for z := int32(0); z < fog.H; z++ {
		for x := int32(0); x < fog.W; x++ {
			if fog.Ch0[z*fog.W+x] == 15 {
				continue
			}
			x0, y0, x1, y1 := render.FogScreenRect(c.cam, x+fog.OriginX, z+fog.OriginZ)
			l, t := max(left, float32(x0-camera.OriginX)), max(top, float32(y0-camera.OriginY))
			r, b := min(right, float32(x1-camera.OriginX)), min(bottom, float32(y1-camera.OriginY))
			if l >= r || t >= b {
				continue
			}
			// The reveal animates grid-aligned chunks, not individual fog pixels.
			// Match the shader's cell centres, including partially exposed chunks.
			for _, p := range [4][2]float32{{l, t}, {r - 0.001, t}, {l, b - 0.001}, {r - 0.001, b - 0.001}} {
				cx := a.GridX + (float32(math.Floor(float64((p[0]-a.GridX)/tile)))+0.5)*tile
				cy := a.GridY + (float32(math.Floor(float64((p[1]-a.GridY)/tile)))+0.5)*tile
				dx, dy := (cx-a.X)/a.Scale, (cy-a.Y)/a.Scale
				farSquared = max(farSquared, dx*dx+dy*dy)
			}
		}
	}
	return max(32, float32(math.Sqrt(float64(farSquared))))
}

// Start in view at the upper edge, so the authored descent interval is visible
// rather than spent crossing empty sky above the window (GPU §36).
func (c *Client) arrivalDropHeight() float32 {
	if c.cam == nil {
		return 640
	}
	u := c.arrival.unit
	_, sy := c.cam.WorldToScreen(u.X, u.Y, u.Z)
	scale := float32(c.cam.EffectiveScale().Float())
	factor := float32(c.cam.EffectiveZoom().Float()) / scale
	oy := float32(0)
	if c.camBlending {
		factor = float32(c.camDrawView.Factor) / scale
		oy = float32((float64(c.cam.Z) - c.camDrawView.Z) * c.camDrawView.Factor)
	}
	top := (float32(c.battleViewportRect().Y) - oy) / factor
	return 2 * max(32, (float32(sy-camera.OriginY)-top)/scale-16)
}

// A single match-long dry-ground scar, independent of the fading blast FIFO.
// The initial location stays fixed when the commander walks away (GPU §36).
func (c *Client) arrivalScorchMark() (drawlist.ScorchMark, bool) {
	if !c.arrival.landed {
		return drawlist.ScorchMark{}, false
	}
	u := c.arrival.unit
	ground, ok := c.scorchSurface(u.X, u.Z)
	if !ok {
		return drawlist.ScorchMark{}, false
	}
	sx, sy := c.cam.WorldToScreen(u.X, ground, u.Z)
	scale := float32(c.cam.EffectiveScale().Float())
	return drawlist.ScorchMark{X: float32(sx - camera.OriginX), Y: float32(sy - camera.OriginY),
		Radius: 38 * scale, Age: max(0, c.arrival.seconds-drawlist.ArrivalImpactSeconds) * 30,
		Variant: 17, Landing: true}, true
}
