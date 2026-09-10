package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// The strategic view's marker layer (docs/DESIGN_GPU_RENDERER.md §16.11).
//
// Below strategicMarkerOn each unit the local player may see becomes one filled
// square of a fixed SCREEN size, in the team colour its minimap dot is drawn
// in. The layer is recorded after the world region closes, positioned through
// the LIVE zoom factor, because a fixed-pixel mark must not be scaled by the
// executor's world transform.
//
// The visibility rule is not re-derived here. The markers come from the
// committed radar contact list and pass exactly render.MinimapBlipAdmitted —
// the same records and the same gate the minimap's own dots take — so a unit
// the minimap will not show cannot appear as a marker either.

// SetStrategicBlipArt installs the minimap blip art the marker colours are
// taken from: the `radlogo` GAF entry, one frame per player colour. The HUD
// resolves it at battle entry and hands it over here so the marker layer and
// the minimap dot name the same team palette entry (§16.11).
//
// A client without the art records no markers, which is the same thing the
// minimap does with no blip art: it draws no dot.
func (c *Client) SetStrategicBlipArt(entry *formats.GAFEntry) {
	if c == nil {
		return
	}
	c.strategicBlip = entry
	c.strategicBlipColors = c.strategicBlipColors[:0]
}

// SetRadarOptions records the battle's radar mode-flags word, whose bit 9 is
// the full-radar cheat the minimap blip gate reads [03 R-MM-01 §3]. The battle
// owner sets it beside the HUD's own copy so the marker layer and the minimap
// answer the gate identically.
func (c *Client) SetRadarOptions(options uint32) {
	if c == nil {
		return
	}
	c.radarOptions = options
}

// strategicBlipColor is the team palette entry for one player-colour selector:
// the most common opaque index of that selector's minimap blip frame, resolved
// once per selector and kept for the life of the client. Taking the frame's own
// dominant pixel rather than naming a colour is what makes the marker the same
// colour as the dot without a second table to keep in step.
func (c *Client) strategicBlipColor(selector uint8) (uint8, bool) {
	if c == nil || c.strategicBlip == nil {
		return 0, false
	}
	if int(selector) >= len(c.strategicBlip.Frames) {
		return 0, false
	}
	for len(c.strategicBlipColors) <= int(selector) {
		c.strategicBlipColors = append(c.strategicBlipColors, strategicBlipColor{})
	}
	slot := &c.strategicBlipColors[selector]
	if slot.resolved {
		return slot.index, slot.ok
	}
	slot.resolved = true
	f := c.strategicBlip.Frames[selector].Frame
	if f != nil {
		var counts [256]int32
		best, bestCount := uint8(0), int32(0)
		for y := 0; y < int(f.Height); y++ {
			for x := 0; x < int(f.Width); x++ {
				b, ok := f.At(x, y)
				if !ok {
					continue
				}
				counts[b]++
				if counts[b] > bestCount {
					best, bestCount = b, counts[b]
				}
			}
		}
		if bestCount > 0 {
			slot.index, slot.ok = best, true
		}
	}
	return slot.index, slot.ok
}

// strategicBlipColor caches one player colour's resolved marker index.
type strategicBlipColor struct {
	index    uint8
	resolved bool
	ok       bool
}

// drawStrategicMarkers records this frame's marker layer. It is a no-op above
// strategicMarkerOn, which is every ordinary frame, so the whole of the
// strategic view costs one comparison at a normal zoom.
func (c *Client) drawStrategicMarkers(cur *frame.Frame) {
	if c == nil || cur == nil || c.cam == nil {
		return
	}
	alpha := c.markerAlpha()
	if alpha == 0 {
		return
	}
	viewport := c.battleViewportRect()
	if viewport.W <= 0 || viewport.H <= 0 {
		return
	}
	z := c.liveZoom()
	camX, camZ := c.cam.X, c.cam.Z
	blink := render.BlinkState{Phase: cur.Radar.BlinkPhase}
	outline := c.paletteIndex(selectionQuadLogicalColor)
	half := int32(strategicMarkerSize) / 2
	marks := c.markerArena[:0]
	for i := range cur.Radar.Contacts {
		published := &cur.Radar.Contacts[i]
		if published.Kind != frame.RadarContactUnit || !published.PaletteKnown {
			continue
		}
		// The minimap's own admission, on the minimap's own records: a contact
		// that fails it draws no dot, and so has no marker either [03 §3.9].
		contact := render.MinimapContact{
			Owner:         published.Owner,
			Status:        published.Status,
			BlinkSuppress: published.BlinkSuppress,
			Visible:       published.Visible,
			LocalPlayer:   cur.ViewingPlayer,
			Options:       c.radarOptions,
			MinimapMode:   cur.Radar.MappingLOS,
		}
		if !render.MinimapBlipAdmitted(contact, blink) {
			continue
		}
		index, ok := c.strategicBlipColor(published.Palette)
		if !ok {
			continue
		}
		// The live factor, not the record step: a marker is a fixed number of
		// framebuffer pixels and is recorded outside the world transform.
		wx := int32(int64(published.X) >> 16)
		wy := int32(int64(published.Y) >> 16)
		wz := int32(int64(published.Z) >> 16)
		// The recorder's world origin is the FRAMEBUFFER's own top-left — the
		// camera origin is drawn there and the chrome is painted over it — so a
		// marker's framebuffer position carries no beam offset [03 §2.5].
		sx := z.Project(wx - camX)
		sy := z.Project(wz - (wy >> 1) - camZ)
		if sx+half < viewport.X || sx-half >= viewport.X+viewport.W ||
			sy+half < viewport.Y || sy-half >= viewport.Y+viewport.H {
			continue
		}
		marks = append(marks, drawlist.Marker{
			X: sx, Y: sy, Size: strategicMarkerSize,
			Index: index, Outline: outline, Selected: published.Selected,
			Alpha: alpha, Clip: viewport, HasClip: true,
		})
	}
	c.markerArena = marks
	if len(marks) == 0 {
		return
	}
	c.list.RecordMarkers(drawlist.Markers{Marks: marks})
}
