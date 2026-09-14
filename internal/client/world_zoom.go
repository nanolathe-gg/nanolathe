package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The recorder's half of smooth zoom and the strategic view
// (docs/DESIGN_GPU_RENDERER.md §16).
//
// The recorder still emits every world command at an INTEGER record step — 2x
// above 1x, 1x at or below it — and never at the live factor. What changes with
// the factor is how much world one frame records (the record extent below), and
// which layers are recorded at all (the strategic gates below). The modern
// executor scales the recorded region by factor/step on the way in; the classic
// executor's factor is always its step, so none of this changes a classic
// pixel.

// Strategic-view thresholds (§16.10, §16.11). Like the feel knobs in
// internal/camera/zoomfeel.go these are tuning values, not retail findings.
const (
	// strategicModelCut is the factor at and below which unit models,
	// projectiles, effects, trails and unit labels stop being recorded. Above
	// it the world is drawn in full. Features are NOT gated on it: they record at
	// every factor (§16.10).
	strategicModelCut = camera.ZoomUnit / 2 // 0.5x
	// strategicMarkerOn is the factor at which the marker layer starts to fade
	// in. Between it and strategicModelCut both layers are recorded, the markers
	// growing from nothing to full.
	strategicMarkerOn = camera.ZoomUnit*5/8 + 0 // 0.625x
	// strategicMarkerSize is one marker's side in FRAMEBUFFER pixels. Markers do
	// not scale with the world; that is the whole point of them.
	strategicMarkerSize = 4
)

// refreshRecordExtent recomputes the record-space extent one frame is recorded
// against (§16.3). It runs once per recorded frame, before any world site reads
// c.recordW/c.recordH.
//
// At a rest factor the extent IS the framebuffer, exactly, so a 1x or 2x frame
// records the same commands it recorded before §16 and the parity gate holds
// unchanged. Below the step the recorded world has to cover framebuffer/factor
// world pixels, which is Project_step of that many; a two-step pad on each axis
// absorbs the rounding of the two conversions, and over-recording only ever
// costs a command whose transformed rectangle falls off the surface.
func (c *Client) refreshRecordExtent() {
	if c == nil {
		return
	}
	c.recordW, c.recordH = c.width, c.height
	if c.cam == nil || c.cam.AtRestStep() {
		return
	}
	z := c.cam.EffectiveZoom()
	s := c.cam.EffectiveScale()
	pad := 2 * int(s.Px(1))
	if w := int(s.Project(z.Inverse(int32(c.width)))) + pad; w > c.recordW {
		c.recordW = w
	}
	if h := int(s.Project(z.Inverse(int32(c.height)))) + pad; h > c.recordH {
		c.recordH = h
	}
}

// recordExtent is the record-space extent every world site clips against. It
// heals a client whose recording has not begun — a focused test that composes
// one layer directly — by falling back to the framebuffer, which is what the
// extent is at every rest factor anyway.
func (c *Client) recordExtent() (int, int) {
	if c == nil {
		return 0, 0
	}
	if c.recordW <= 0 || c.recordH <= 0 {
		return c.width, c.height
	}
	return c.recordW, c.recordH
}

// worldSpace builds the boundary marker for this frame's world region.
func (c *Client) worldSpace(begin bool) drawlist.WorldSpace {
	recW, recH := c.recordExtent()
	w := drawlist.WorldSpace{
		Begin:   begin,
		Zoom:    camera.ZoomUnit,
		Step:    camera.ViewScaleNative,
		RecordW: int32(recW),
		RecordH: int32(recH),
	}
	if c.cam != nil {
		w.Zoom = c.cam.EffectiveZoom()
		w.Step = c.cam.EffectiveScale()
	}
	w.Viewport = c.battleViewportRect()
	return w
}

// battleViewportRect is the battle viewport in FRAMEBUFFER pixels — the
// rectangle `(128,32)..(W-1,H-33)` the chrome leaves for the world [03 §4.1].
// It does not move with the zoom: the chrome is drawn in framebuffer pixels.
func (c *Client) battleViewportRect() drawlist.Rect {
	w := int32(c.width) - camera.OriginX
	h := int32(c.height) - 2*camera.OriginY
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return drawlist.Rect{X: camera.OriginX, Y: camera.OriginY, W: w, H: h}
}

// emitWorldBegin and emitWorldEnd bracket the world region of one recording.
// They are cheap value records; an executor without free zoom never sees them.
func (c *Client) emitWorldBegin() { c.list.RecordWorld(c.worldSpace(true)) }
func (c *Client) emitWorldEnd()   { c.list.RecordWorld(c.worldSpace(false)) }

// BeginWorldOverlay and EndWorldOverlay open and close a SECOND world region,
// for the UI stage's world-positioned draws (§16.3).
//
// The rule the pair exists to enforce is that a draw positioned from WORLD
// coordinates belongs inside a world region and a draw positioned from POINTER
// or framebuffer coordinates belongs outside one. The build ghost and the
// order-queue overlay are world-positioned — both project through the camera at
// the RECORD step and subtract the view origin — but they are composed in the
// UI stage, after the frame's own region has closed, so off a rest step they
// were drawn at record coordinates and received no transform: at half scale the
// ghost sat twice as far from the framebuffer origin as the site it marked.
//
// A frame has at most one overlay region, and the executor treats each boundary
// as the barrier the frame's own region already is, so the cost is two extra
// schedule submissions on the frames that open one.
//
// While the region is open the UI helpers that bake a framebuffer bound into
// the recorded command take the record extent instead, so a world overlay near
// the right or bottom edge is not clipped away before the executor has shrunk
// it. At a rest factor the two extents are equal and nothing changes.
func (c *Client) BeginWorldOverlay() {
	if c == nil {
		return
	}
	c.worldOverlay = true
	c.emitWorldBegin()
}

func (c *Client) EndWorldOverlay() {
	if c == nil {
		return
	}
	c.emitWorldEnd()
	c.worldOverlay = false
}

// uiExtent is the bound the UI helpers clip and truncate against: the
// framebuffer for the chrome, the record extent inside a world overlay.
func (c *Client) uiExtent() (int, int) {
	if c == nil {
		return 0, 0
	}
	if c.worldOverlay {
		return c.recordExtent()
	}
	return c.width, c.height
}

// liveZoom is this frame's live presentation factor.
func (c *Client) liveZoom() camera.Zoom {
	if c == nil || c.cam == nil {
		return camera.ZoomUnit
	}
	return c.cam.EffectiveZoom()
}

// strategicView reports whether this frame is below the model cut, which is
// where unit models, projectiles, effects, trails and unit labels stop being
// recorded and the marker layer replaces the units (§16.10). Terrain, fog,
// features and the drag rectangle are not gated on it. Generated icons replace
// ground selection quads when StrategicIconsActive (§18.4).
func (c *Client) strategicView() bool {
	// The inclusive cut and the clamped tactical stop share one gate so models,
	// icon picking and selection outlines switch together (§16.10).
	return c.liveZoom() <= strategicModelCut || (c.enhanced && c.cam.TacticalAtFloor())
}

// markerAlpha is the strategic layer's fade: zero at and above
// strategicMarkerOn, full at and below strategicModelCut, linear between
// (§16.11).
//
// TODO(question): models are hard-cut at strategicModelCut rather than
// cross-faded against the marker layer, because a model fade needs an alpha
// lane on the model commit — the sceneOpModelCommit path of §13.3 writes an
// opaque fragment. A cross-fade is a follow-up on that lane.
func (c *Client) markerAlpha() uint8 {
	if c.strategicView() {
		return 255
	}
	z := c.liveZoom()
	if z >= strategicMarkerOn {
		return 0
	}
	if z <= strategicModelCut {
		return 255
	}
	span := int32(strategicMarkerOn - strategicModelCut)
	into := int32(strategicMarkerOn - z)
	return uint8((into*255 + span/2) / span)
}
