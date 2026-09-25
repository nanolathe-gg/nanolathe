package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The strategic view's marker layer (docs/DESIGN_GPU_RENDERER.md §16.11).
//
// Below strategicMarkerOn visible units use generated role art (§18).
// Enhanced sensor-only contacts keep a generic square at every zoom.
// The layer is recorded after the world region closes, positioned through
// the LIVE zoom factor, because a fixed-pixel mark must not be scaled by the
// executor's world transform.
//
// Visible unit icons use the committed world-visibility predicate independently
// of minimap blink/status. Radar-only dots retain the minimap admission gate.
//
// A submerged enemy without sonar contact is never identified here: the world
// predicate rejects it below the sea plane [03 §3.2] step 3. Inside the
// viewer's line of sight it still gets a generic dot, because the retail
// line-of-sight probe sets the seen bit with no sea-level term and the minimap
// blips on that bit [03 R-VIS-01 §4] pass 5 [03 R-MM-01 §3]. Hiding that dot
// in the main view would be a new Enhanced presentation policy (§16.11 names
// the minimap gate as this layer's own), not a retail correction.

// SetStrategicTeamArt binds the same lobby-colour logo frames used by the HUD.
// Their dominant opaque shade supplies Enhanced icon ink (§18.4). The radar
// blip's dominant shade is its gray border, so it is unsuitable for team ink.
func (c *Client) SetStrategicTeamArt(entry *formats.GAFEntry) {
	if c == nil {
		return
	}
	c.strategicTeam = entry
	c.strategicTeamColors = c.strategicTeamColors[:0]
}

func (c *Client) strategicTeamColor(selector uint8) (uint8, bool) {
	return strategicArtColor(c.strategicTeam, &c.strategicTeamColors, selector)
}

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
	if c == nil {
		return 0, false
	}
	return strategicArtColor(c.strategicBlip, &c.strategicBlipColors, selector)
}

func strategicArtColor(entry *formats.GAFEntry, colors *[]strategicBlipColor, selector uint8) (uint8, bool) {
	if entry == nil {
		return 0, false
	}
	if int(selector) >= len(entry.Frames) {
		return 0, false
	}
	for len(*colors) <= int(selector) {
		*colors = append(*colors, strategicBlipColor{})
	}
	slot := &(*colors)[selector]
	if slot.resolved {
		return slot.index, slot.ok
	}
	slot.resolved = true
	f := entry.Frames[selector].Frame
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

// drawStrategicMarkers records strategic icons and Enhanced sensor dots.
// Dots share the foreground layer so they remain visible over fog at any zoom.
func (c *Client) drawStrategicMarkers(cur *frame.Frame) {
	if c == nil {
		return
	}
	// Identification must use committed visibility, even when the world models
	// are interpolated. Radar contact coordinates themselves are not blended.
	committed := cur
	if c.buffer != nil && c.committedFrame() != nil {
		committed = c.committedFrame()
	}
	viewer := uint8(0)
	if committed != nil {
		viewer = committed.ViewingPlayer
	}
	c.layoutStrategicMarkers(committed, viewer, &c.strategicDraw, false)
	c.markerArena = c.strategicDraw.marks
	if len(c.markerArena) != 0 {
		c.list.RecordMarkers(drawlist.Markers{Marks: c.markerArena})
	}
}
