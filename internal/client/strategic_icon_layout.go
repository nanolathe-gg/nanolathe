package client

import (
	"math"

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
	x, z                     int32
	viewW, viewH, mapW, mapH int32
	prevView, curView        camera.PresentationView
}
type strategicProjection struct {
	key   strategicProjectionKey
	view  camera.PresentationView
	valid bool
}

func (c *Client) strategicProjectionKey(f *frame.Frame, viewer uint8) strategicProjectionKey {
	k := strategicProjectionKey{frame: f, tick: f.Tick, viewer: viewer, zoom: c.liveZoom(), viewport: c.battleViewportRect(), icons: c.strategicIcons, blended: c.hasCameraBlend(), x: c.cam.X, z: c.cam.Z, viewW: c.cam.ViewW, viewH: c.cam.ViewH, mapW: c.cam.MapW, mapH: c.cam.MapH}
	if c.camBlending {
		k.zoom = c.camSave.EffectiveZoom()
	} else {
		k.zoom = c.cam.EffectiveZoom()
	}
	k.prevView, k.curView = c.camPrevView, c.camCurView
	if k.blended {
		k.x, k.z = int32(c.camCurView.X), int32(c.camCurView.Z)
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

// presentationPoint rounds only after applying the complete view transform.
// Fixed-size markers and guides share the world's subpixel camera placement.
func presentationPoint(v camera.PresentationView, x, z int64) (int64, int64) {
	return int64(math.Ceil((float64(x) - v.X) * v.Factor)), int64(math.Ceil((float64(z) - v.Z) * v.Factor))
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
	viewport := c.battleViewportRect()
	if viewport.W <= 0 || viewport.H <= 0 {
		return
	}
	view := c.presentationCameraView()
	key := c.strategicProjectionKey(f, viewer)
	if picking && c.strategicPresented.valid && c.strategicPresented.key == key {
		view = c.strategicPresented.view
	}
	if !picking {
		c.strategicRecorded = strategicProjection{key: key, view: view, valid: true}
	}
	alpha := c.markerAlphaAtZoom(camera.Zoom(math.Round(view.Factor * float64(camera.ZoomUnit))))
	if alpha == 0 && !c.enhanced {
		return
	}
	blink := render.BlinkState{Phase: f.Radar.BlinkPhase}
	outline := c.paletteIndex(selectionQuadLogicalColor)
	typed := c.enhanced && c.strategicIcons != nil
	neutral := uint8(0)
	if typed {
		neutral = c.nearestIndex(255, 255, 255)
	}
	if c.enhanced {
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
			visible := false
			if c.enhanced && p.Handle != 0 && int(p.Handle) < len(dst.slots) {
				if n := dst.slots[int(p.Handle)]; n != 0 {
					u := f.Units[n-1]
					if u.Owner == p.Owner && (strategicUnitVisible(f, u, viewer, dst.slots) || (isCarried(u) && unitVisibleForFrame(f, u, viewer))) {
						visible = true
					}
				}
			}
			if visible && (typed || alpha == 0) {
				continue // the visible model/icon lane owns this contact, including hidden cargo
			}
			// Enhanced sensor dots stay legible above fog at every zoom (§18.4).
			// Only identified units take the strategic fade; radar cannot grant identity.
			contactAlpha := alpha
			if c.enhanced && !visible {
				contactAlpha = 255
			}
			contact := render.MinimapContact{Owner: p.Owner, Status: p.Status, BlinkSuppress: p.BlinkSuppress, Visible: p.Visible, LocalPlayer: viewer, Options: c.radarOptions, MinimapMode: f.Radar.MappingLOS}
			if !render.MinimapBlipAdmitted(contact, blink) {
				continue
			}
			index, ok := c.strategicBlipColor(p.Palette)
			if !ok {
				continue
			}
			if c.enhanced {
				if team, known := c.strategicTeamColor(p.Palette); known {
					index = team
				}
			}
			x, y := presentationPoint(view, int64(p.X)>>16, (int64(p.Z)>>16)-(int64(p.Y)>>17))
			appendMark(drawlist.Marker{X: int32(x), Y: int32(y), Size: strategicMarkerSize, Index: index, Outline: outline, Selected: p.Selected, Alpha: contactAlpha, Clip: viewport, HasClip: true}, 0)
		}
		if !typed || alpha == 0 {
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
			x, y := presentationPoint(view, int64(u.X)>>16, (int64(u.Z)>>16)-(int64(u.Y)>>17))
			hovered := u.InstanceID != 0 && u.InstanceID == c.strategicHover
			highlighted := selected || hovered
			iconOutline := outline
			iconRect := icon.Rect
			if icon.communityConfigured {
				if selected {
					iconOutline = icon.communitySelected
				} else if hovered {
					iconOutline = icon.communityHover
					if icon.communityCircle && icon.communityHoverRect.W > 0 && icon.communityHoverRect.H > 0 {
						iconRect = icon.communityHoverRect
					}
				}
			}
			appendMark(drawlist.Marker{X: int32(x), Y: int32(y), Size: strategicIconSize, Index: index, Outline: iconOutline, Selected: highlighted, Alpha: alpha, Clip: viewport, HasClip: true, IconAtlas: icon.Atlas, IconRect: iconRect}, i+1)
		}
	}
}

// strategicUnitVisible mirrors the painter's hull visibility gate
// [03 §3.2][03 R-RAST-01 §7], including the owner's bypass.
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
	return unitVisibleForFrame(f, u, viewer)
}

// StrategicIconsActive reports whether typed icons replace model hulls in the
// current presentation. The battle shell retains its ordinary camera picker
// for every other mode, including clients not yet bound to a battle camera.
func (c *Client) StrategicIconsActive() bool {
	return c != nil && c.enhanced && c.strategicIcons != nil && c.cam != nil && c.strategicView()
}

// PickPresentedUnit keeps the retail hull picker at normal zoom and during the
// fade. In the strategic view it uses the same admission, projection, ordering
// and bounds as the icon recorder. Generic contacts never expose a UnitView.
func (c *Client) PickPresentedUnit(f *frame.Frame, x, y int32, viewer uint8) (pool.Handle, frame.UnitView, bool) {
	if c == nil {
		return 0, frame.UnitView{}, false
	}
	if f == nil || c.cam == nil {
		return 0, frame.UnitView{}, false
	}
	view := c.presentationCameraView()
	if c.strategicPresented.valid && c.strategicPresented.key == c.strategicProjectionKey(f, viewer) {
		view = c.strategicPresented.view
	}
	// The accepted view decides both the projection and the model/icon cut.
	// A later predicted fraction must not switch the picker before the display.
	strategic := c.enhanced && c.strategicIcons != nil && c.strategicViewAtZoom(camera.Zoom(math.Round(view.Factor*float64(camera.ZoomUnit))))
	if !strategic {
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
