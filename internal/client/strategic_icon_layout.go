package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// Strategic icons are an Enhanced presentation policy, not retail behavior
// (DESIGN_GPU_RENDERER §18). All scratch is rebuilt from this publication and
// projection; a reused slot can never inherit an old unit's identification.
// Fixed framebuffer pixels at every camera zoom, including the 0.25× overview.
const strategicIconSize = 24

type strategicLayoutScratch struct {
	marks   []drawlist.Marker
	targets []int // UnitView index + 1; zero is an unidentified contact
	slots   []int
}

// A successful GPU submission freezes the camera used by that list. The
// prerecorder may be accepted within a fraction tolerance; input must follow
// the accepted projection, not the subsequently measured fraction (§13.10).
type strategicProjectionKey struct {
	frame                    *frame.Frame
	tick                     uint32
	viewer                   uint8
	zoom                     camera.Zoom
	viewport                 drawlist.Rect
	icons                    *StrategicIconCatalog
	blended                  bool
	x, z, prevX, prevZ       int32
	viewW, viewH, mapW, mapH int32
}
type strategicProjection struct {
	key   strategicProjectionKey
	x, z  int32
	valid bool
}

func (c *Client) strategicProjectionKey(f *frame.Frame, viewer uint8) strategicProjectionKey {
	k := strategicProjectionKey{frame: f, tick: f.Tick, viewer: viewer, zoom: c.liveZoom(), viewport: c.battleViewportRect(), icons: c.strategicIcons, blended: c.hasCameraBlend(), x: c.cam.X, z: c.cam.Z, viewW: c.cam.ViewW, viewH: c.cam.ViewH, mapW: c.cam.MapW, mapH: c.cam.MapH}
	if k.blended {
		k.x, k.z, k.prevX, k.prevZ = c.camCurX, c.camCurZ, c.camPrevX, c.camPrevZ
	}
	return k
}

// CommitStrategicPresentation is called only after the recorded foreground
// was submitted successfully, never for a speculative or discarded list.
func (c *Client) CommitStrategicPresentation() {
	if c != nil {
		c.strategicPresented = c.strategicRecorded
	}
}

func (c *Client) SetStrategicIconCatalog(icons *StrategicIconCatalog) {
	if c == nil {
		return
	}
	c.strategicIcons = icons
	c.strategicDraw = strategicLayoutScratch{}
	c.strategicPick = strategicLayoutScratch{}
	c.strategicHover = 0
	c.strategicPresented = strategicProjection{}
	c.strategicRecorded = strategicProjection{}
}

// SetStrategicHover stores a publication instance identity, never a reusable
// pool slot. A halo is presentation only and cannot change draw/pick priority.
func (c *Client) SetStrategicHover(instance uint64) {
	if c != nil && c.strategicHover != instance {
		c.strategicHover = instance
	}
}

// strategicOrigin is shared by the recorder and input. The recorder temporarily
// blends c.cam; deriving the origin from the same sample pair also works when
// input observes the restored camera (GPU design §13.5, §18.4).
func (c *Client) strategicOrigin() (int32, int32) {
	if c.hasCameraBlend() {
		w, h := c.cam.EffectiveView()
		return lerpOrigin(c.camPrevX, c.camCurX, int64(c.cameraFraction16), w), lerpOrigin(c.camPrevZ, c.camCurZ, int64(c.cameraFraction16), h)
	}
	return c.cam.X, c.cam.Z
}

func (c *Client) layoutStrategicMarkers(f *frame.Frame, viewer uint8, dst *strategicLayoutScratch, picking bool) {
	dst.marks = dst.marks[:0]
	dst.targets = dst.targets[:0]
	if !picking && c != nil {
		c.strategicRecorded.valid = false
	}
	if c == nil || f == nil || c.cam == nil || viewer >= 10 {
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
	camX, camZ := c.strategicOrigin()
	key := c.strategicProjectionKey(f, viewer)
	if picking && c.strategicPresented.valid && c.strategicPresented.key == key {
		camX, camZ = c.strategicPresented.x, c.strategicPresented.z
	}
	if !picking {
		c.strategicRecorded = strategicProjection{key: key, x: camX, z: camZ, valid: true}
	}
	blink := render.BlinkState{Phase: f.Radar.BlinkPhase}
	outline := c.paletteIndex(selectionQuadLogicalColor)
	typed := c.enhanced && c.strategicIcons != nil
	neutral := uint8(0)
	if typed {
		neutral = c.nearestIndex(255, 255, 255)
	}
	if typed {
		dst.indexUnits(f)
	}
	// Visible world units are independent of the minimap contact latch and its
	// damage blink. This is Enhanced presentation policy (§18.4); the minimap
	// and unidentified sensor contacts retain their retail gates [03 §3.9].
	passes := 1
	if typed {
		passes = 2
	}
	appendMark := func(m drawlist.Marker, target int) {
		x, y := m.X-m.Size/2, m.Y-m.Size/2
		if x+m.Size <= viewport.X || x >= viewport.X+viewport.W || y+m.Size <= viewport.Y || y >= viewport.Y+viewport.H {
			return
		}
		dst.marks = append(dst.marks, m)
		dst.targets = append(dst.targets, target)
	}
	for pass := 0; pass < passes; pass++ {
		for i := range f.Radar.Contacts {
			p := &f.Radar.Contacts[i]
			if p.Kind != frame.RadarContactUnit || !p.PaletteKnown || (typed && p.Selected != (pass == 1)) {
				continue
			}
			if typed && p.Handle != 0 && int(p.Handle) < len(dst.slots) {
				if n := dst.slots[int(p.Handle)]; n != 0 {
					u := f.Units[n-1]
					if u.Owner == p.Owner && (strategicUnitVisible(f, u, viewer, dst.slots) || (isCarried(u) && unitVisibleForFrame(f, u, viewer))) {
						continue // the unit lane below owns this contact, including hidden cargo
					}
				}
			}
			contact := render.MinimapContact{Owner: p.Owner, Status: p.Status, BlinkSuppress: p.BlinkSuppress, Visible: p.Visible, LocalPlayer: viewer, Options: c.radarOptions, MinimapMode: f.Radar.MappingLOS}
			if !render.MinimapBlipAdmitted(contact, blink) {
				continue
			}
			index, ok := c.strategicBlipColor(p.Palette)
			if !ok {
				continue
			}
			if typed {
				if team, known := c.strategicTeamColor(p.Palette); known {
					index = team
				}
			}
			appendMark(drawlist.Marker{X: z.Project(int32(int64(p.X)>>16) - camX), Y: z.Project(int32(int64(p.Z)>>16) - (int32(int64(p.Y)>>16) >> 1) - camZ), Size: strategicMarkerSize, Index: index, Outline: outline, Selected: p.Selected, Alpha: alpha, Clip: viewport, HasClip: true}, 0)
		}
		if !typed {
			continue
		}
		for i := range f.Units {
			u := f.Units[i]
			selected := u.Owner == viewer && u.Flags&0x10 != 0
			if u.Slot == 0 || u.BuildRemaining > 0 || selected != (pass == 1) || !strategicUnitVisible(f, u, viewer, dst.slots) || (isCarried(u) && u.CarriedPiece < 0) {
				continue
			}
			// Only this world-visibility gate permits definition art and a unit
			// hit target. Neither radar metadata nor the blink phase can grant it.
			icon, _ := c.strategicIcons.Lookup(u.DefName, u.DefID)
			index := neutral // missing owner colour must not suppress a visible unit
			if u.OwnerColorKnown {
				if team, known := c.strategicTeamColor(u.OwnerColor); known {
					index = team
				}
			}
			appendMark(drawlist.Marker{X: z.Project(int32(int64(u.X)>>16) - camX), Y: z.Project(int32(int64(u.Z)>>16) - (int32(int64(u.Y)>>16) >> 1) - camZ), Size: strategicIconSize, Index: index, Outline: outline, Selected: selected || (u.InstanceID != 0 && u.InstanceID == c.strategicHover), Alpha: alpha, Clip: viewport, HasClip: true, IconAtlas: icon.Atlas, IconRect: icon.Rect}, i+1)
		}
	}
}

// strategicUnitVisible mirrors the painter's independent fog-anchor and hull
// visibility gates [03 §3.2][03 §3.3], including the owner's bypass.
func strategicUnitVisible(f *frame.Frame, u frame.UnitView, viewer uint8, slots []int) bool {
	// Attached models inherit their carrier's admission [03 R-RAST-01 §7].
	// Only direct children of an uncarried model are composed; nested cargo
	// or malformed links cannot manufacture an independently visible child.
	if isCarried(u) {
		if u.CarriedPiece < 0 || int(u.Carrier) >= len(slots) || slots[int(u.Carrier)] == 0 {
			return false
		}
		carrier := f.Units[slots[int(u.Carrier)]-1]
		if isCarried(carrier) {
			return false
		}
		linked := false
		for _, child := range carrier.Cargo {
			if child == u.Slot {
				linked = true
				break
			}
		}
		if !linked {
			return false
		}
		u = carrier
	}
	return unitVisibleForFrame(f, u, viewer) && (u.Owner == viewer || !fogUnexploredUnit(f.Fog, u))
}

// StrategicIconsActive reports whether typed icons replace model hulls in the
// current presentation. The battle shell retains its ordinary camera picker
// for every other mode, including clients not yet bound to a battle camera.
func (c *Client) StrategicIconsActive() bool {
	return c != nil && c.enhanced && c.strategicIcons != nil && c.cam != nil && c.liveZoom() <= strategicModelCut
}

// PickPresentedUnit keeps the retail hull picker at normal zoom and during the
// fade. In the strategic view it uses the same admission, projection, ordering
// and bounds as the icon recorder. Generic contacts never expose a UnitView.
func (c *Client) PickPresentedUnit(f *frame.Frame, x, y int32, viewer uint8) (pool.Handle, frame.UnitView, bool) {
	if c == nil {
		return 0, frame.UnitView{}, false
	}
	if !c.StrategicIconsActive() {
		return PickSnapshotUnit(f, x, y, c.cam, viewer)
	}
	c.layoutStrategicMarkers(f, viewer, &c.strategicPick, true)
	for i := len(c.strategicPick.marks) - 1; i >= 0; i-- {
		m := c.strategicPick.marks[i]
		left, top := m.X-m.Size/2, m.Y-m.Size/2
		if x < left || x >= left+m.Size || y < top || y >= top+m.Size || x < m.Clip.X || x >= m.Clip.X+m.Clip.W || y < m.Clip.Y || y >= m.Clip.Y+m.Clip.H {
			continue
		}
		n := c.strategicPick.targets[i]
		if n == 0 {
			return 0, frame.UnitView{}, false
		}
		u := f.Units[n-1]
		return u.Slot, u, true
	}
	return 0, frame.UnitView{}, false
}

// indexUnits shares committed carrier lookup between icons and tactical guides.
func (dst *strategicLayoutScratch) indexUnits(f *frame.Frame) {
	largest := 0
	for i := range f.Units {
		if n := int(f.Units[i].Slot); n > largest {
			largest = n
		}
	}
	if cap(dst.slots) < largest+1 {
		dst.slots = make([]int, largest+1)
	} else {
		dst.slots = dst.slots[:largest+1]
		clear(dst.slots)
	}
	for i := range f.Units {
		if f.Units[i].Slot != 0 {
			dst.slots[int(f.Units[i].Slot)] = i + 1
		}
	}
}
