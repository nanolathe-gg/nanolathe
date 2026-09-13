package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Building foam is an Enhanced footprint approximation, not a model waterline
// intersection. It is recorded beneath visible completed floating buildings,
// then clipped to ordinary water by the executor (GPU design §26).
func (c *Client) drawBuildingFoam(cur *frame.Frame) {
	if c == nil || cur == nil || !c.enhanced || c.cam == nil || c.terrain == nil || c.strategicView() {
		return
	}
	t := c.terrain
	if t.SeaLevel == 0 || t.LavaWorld || t.WaterDoesDamage != 0 && t.WaterDamage != 0 {
		return
	}
	c.waterFoam = c.waterFoam[:0]
	scale := float32(c.cam.EffectiveScale().Float())
	w, h := c.recordExtent()
	// The ring phase advances with the presentation fraction, as scorch and
	// blast ages do (§13.5), so the foam does not step at 30 Hz on a faster
	// display. The tick is still wrapped first, keeping the phase bounded.
	fraction := float32(0)
	if c.interpolation {
		fraction = c.TickFraction()
	}
	phase := (float32(cur.Tick%240) + fraction) / 240
	for _, u := range cur.Units {
		if !u.IsBuilding || u.BuildRemaining > 0 || isCarried(u) || !unitVisibleForFrame(cur, u, cur.ViewingPlayer) ||
			(u.Owner != cur.ViewingPlayer && fogUnexploredUnit(cur.Fog, u)) {
			continue
		}
		ground := t.HeightAt(u.X, u.Z)
		if ground < 0 || ground >= t.SeaLevelWorld() || u.Y+u.HullYExtent < t.SeaLevelWorld() {
			continue
		}
		// Water-yard buildings use sea minus authored draft at placement [05
		// "Geothermal requirement"]. Their FBI Floater flag need not be set.
		if u.Y+numeric.Fixed(u.Waterline)*numeric.FixedOne != t.SeaLevelWorld() {
			continue
		}
		sx, sy := c.cam.WorldToScreen(u.X, t.SeaLevelWorld(), u.Z)
		x, y := float32(sx-camera.OriginX), float32(sy-camera.OriginY)
		hx, hz := (float32(max(u.FootX, 1))*8+10)*scale, (float32(max(u.FootZ, 1))*8+10)*scale
		if x+hx < 0 || y+hz < 0 || x-hx >= float32(w) || y-hz >= float32(h) {
			continue
		}
		c.waterFoam = append(c.waterFoam, drawlist.SurfaceWake{X: x, Y: y, AxisX: hx, CrossY: hz, Alpha: .12, Age: phase, Foam: true})
		if len(c.waterFoam) == 1024 {
			break
		}
	}
	if len(c.waterFoam) > 0 {
		c.list.RecordSurfaceWakes(drawlist.SurfaceWakes{Marks: c.waterFoam})
	}
}
