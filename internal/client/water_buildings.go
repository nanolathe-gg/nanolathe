package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
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
	if t.SeaLevel == 0 || t.LavaWorld {
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
		if !u.IsBuilding || u.BuildRemaining > 0 || u.Cloaked || isCarried(u) || !unitVisibleForFrame(cur, u, cur.ViewingPlayer) {
			continue
		}
		ground := t.HeightAt(u.X, u.Z)
		if ground < 0 || ground >= t.SeaLevelWorld() {
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
		if !c.buildingTouchesSurface(u, t.SeaLevelWorld()) {
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

// A definition hull includes unused vertices and hidden/retracted pieces. Foam
// follows the visible committed pose instead, using the canonical transforms and
// hierarchy visibility [03 §2.4]. A visible face must straddle the sea plane;
// detached geometry entirely above it does not displace water (§26.3).
func (c *Client) buildingTouchesSurface(u frame.UnitView, sea numeric.Fixed) bool {
	m := c.modelForUnit(u)
	if m == nil || m.compiled == nil {
		return false
	}
	states := c.modelStates(m, u.Pieces)
	draw := presentationrender.BuildUnitDrawInto(m.compiled, states, u.Heading, u.Pitch, u.Bank, u, nil, c.borrowDrawScratch())
	for _, piece := range draw.Pieces {
		for i, face := range piece.Primitives {
			if m.compiled.Pieces[piece.Index].Selection && i == 0 || len(face.VertexIndices) < 3 || modelPrimitiveDispatch(&face, true) == modelPrimitiveSkip {
				continue
			}
			below, above, valid := false, false, true
			for _, index := range face.VertexIndices {
				if int(index) >= len(piece.WorldVertices) {
					valid = false
					break
				}
				y := piece.WorldVertices[index][1]
				below = below || y <= sea
				above = above || y >= sea
			}
			if valid && below && above {
				return true
			}
		}
	}
	return false
}
